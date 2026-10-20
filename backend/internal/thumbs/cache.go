// Package thumbs renders and caches PDF page thumbnails.
//
// Thumbnails exist so a document browser can show a page grid without opening a
// document itself. Two properties drive the design. The cache is keyed by file
// content, so moving or renaming a document reuses its thumbnails and editing
// one does not. And the cache is private and bounded, because a thumbnail of a
// sensitive page must not outlive its usefulness or be readable by other users.
package thumbs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// MinWidth and MaxWidth bound a thumbnail request. Below the minimum a
	// thumbnail is not worth a render; above the maximum it stops being a
	// thumbnail.
	MinWidth = 64
	MaxWidth = 800

	// DefaultWidth is used when a request does not choose one.
	DefaultWidth = 200

	// maxPagesPerRequest bounds how much work one request can ask for, so a
	// single call cannot pin the renderer for minutes.
	maxPagesPerRequest = 50

	// maxConcurrentRenders caps how many pdftocairo processes run at once.
	// Each is CPU-hungry, and more would slow the daemon rather than speed it.
	maxConcurrentRenders = 2

	// renderTimeout stops a request that hangs. The runner kills the child
	// process when the context is cancelled.
	renderTimeout = 60 * time.Second

	// youngFileGrace protects a render that is in flight right now: pruning
	// runs after each batch, and must not delete the very files that batch is
	// still writing.
	youngFileGrace = 60 * time.Second

	// DefaultMaxBytes and DefaultMaxAge are the daemon's defaults. Tests and
	// callers can lower them; there is no way to raise them without a
	// deliberate change here.
	DefaultMaxBytes = 256 << 20
	DefaultMaxAge   = 7 * 24 * time.Hour
)

// ErrTooManyPages and ErrBadWidth are request errors, distinct from a document
// that cannot be read.
var (
	ErrTooManyPages = errors.New("too many pages requested at once")
	ErrBadWidth     = errors.New("width is out of range")
	ErrNoPages      = errors.New("no pages were requested")
	ErrBadPage      = errors.New("page number is out of range")
)

// Limits bounds the cache. Both limits are checked by Prune.
type Limits struct {
	MaxBytes int64
	MaxAge   time.Duration
}

// Renderer draws one page into a file at the given output prefix and returns
// the path of the PNG it produced. pdftocairo zero-pads its output names to the
// document's page-count width, so a renderer must report the name it actually
// wrote rather than assume one.
type Renderer interface {
	Render(ctx context.Context, file string, pages []int, width int, dir string) ([]string, error)
}

// Request is one batch of pages to thumbnail.
type Request struct {
	File  string
	Pages []int
	Width int
}

// Page is one thumbnail in a response.
type Page struct {
	Page int    `json:"page"`
	Path string `json:"path"`
	W    int    `json:"w"`
	H    int    `json:"h"`
}

// Cache renders thumbnails through a Renderer and keeps them on disk.
//
// It is safe for concurrent use. Identical concurrent requests share one render
// rather than racing to produce the same file twice.
type Cache struct {
	root     string
	limits   Limits
	renderer Renderer

	// sem bounds concurrent renders.
	sem chan struct{}

	// mu and calls collapse concurrent renders of the same key, so a UI that
	// scrolls a grid does not start the same render twice. The work is coarse
	// (a batch of pages) and concurrent requests are few, so a mutex and a map
	// are enough; no need for a dependency.
	mu    sync.Mutex
	calls map[string]*call
}

// call is one render in progress. The leader writes pages and err and then
// closes done; followers wait on done before reading them, which is what makes
// the hand-off race-free.
type call struct {
	done  chan struct{}
	pages []Page
	err   error
}

func (c *call) finish() { close(c.done) }

func (c *call) wait() { <-c.done }

// NewCache returns a Cache rooted at root. A zero Limits means the defaults.
func NewCache(root string, limits Limits, renderer Renderer) *Cache {
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = DefaultMaxBytes
	}
	if limits.MaxAge <= 0 {
		limits.MaxAge = DefaultMaxAge
	}
	return &Cache{
		root:     root,
		limits:   limits,
		renderer: renderer,
		sem:      make(chan struct{}, maxConcurrentRenders),
		calls:    map[string]*call{},
	}
}

// Ensure creates the cache root if it does not exist, with private permissions.
// It is safe to call repeatedly and on an existing directory.
func (c *Cache) Ensure() error {
	if err := os.MkdirAll(c.root, 0o700); err != nil {
		return fmt.Errorf("create the thumbnail cache: %w", err)
	}
	// MkdirAll leaves an existing directory's mode alone, and a cache created
	// by an older version may be world-readable. Fix it either way.
	return os.Chmod(c.root, 0o700)
}

// Root returns the cache directory, for the daemon's start-up prune and for the
// cache clear command.
func (c *Cache) Root() string { return c.root }

// Render returns a thumbnail for every requested page, rendering only the ones
// that are not already cached.
func (c *Cache) Render(ctx context.Context, req Request) ([]Page, error) {
	pages, width, err := validate(req)
	if err != nil {
		return nil, err
	}
	// An already-cancelled caller must not start a render. A context cancelled
	// part-way through is handled by the render's own timeout.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.Ensure(); err != nil {
		return nil, err
	}
	key, err := c.key(req.File, width)
	if err != nil {
		return nil, err
	}
	keyDir := filepath.Join(c.root, key)

	// Collapse concurrent identical requests: followers wait for the leader's
	// render instead of starting a duplicate one.
	work, leader := c.join(key)
	if leader {
		c.mu.Lock()
		c.calls[key] = work
		c.mu.Unlock()
		defer func() {
			c.mu.Lock()
			delete(c.calls, key)
			c.mu.Unlock()
		}()
		work.pages, work.err = c.renderBatch(ctx, req.File, keyDir, pages, width)
		work.finish()
	} else {
		work.wait()
	}
	if work.err != nil {
		return nil, work.err
	}
	return work.pages, nil
}

// join registers interest in key and reports whether the caller is the leader,
// meaning the one that must do the work.
func (c *Cache) join(key string) (*call, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.calls[key]; ok {
		return existing, false
	}
	return &call{done: make(chan struct{})}, true
}

// renderBatch renders what is missing and then prunes, because a batch is the
// moment the cache is most likely to have grown past its limit.
func (c *Cache) renderBatch(ctx context.Context, file, keyDir string, pages []int, width int) ([]Page, error) {
	var missing []int
	result := make(map[int]Page, len(pages))
	for _, page := range pages {
		path := filepath.Join(keyDir, fmt.Sprintf("p%d.png", page))
		if w, h, err := pngSize(path); err == nil {
			result[page] = Page{Page: page, Path: path, W: w, H: h}
			continue
		}
		missing = append(missing, page)
	}
	if len(missing) > 0 {
		rendered, err := c.renderMissing(ctx, file, keyDir, missing, width)
		if err != nil {
			return nil, err
		}
		for page, path := range rendered {
			w, h, err := pngSize(path)
			if err != nil {
				return nil, fmt.Errorf("read the thumbnail for page %d: %w", page, err)
			}
			result[page] = Page{Page: page, Path: path, W: w, H: h}
		}
	}
	if err := c.Prune(time.Now()); err != nil {
		// A prune failure is not the caller's problem: the thumbnails they
		// asked for exist. It is worth surfacing in the daemon's log instead.
		_ = err
	}
	out := make([]Page, 0, len(pages))
	for _, page := range pages {
		if p, ok := result[page]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// renderMissing renders the given pages into a temporary directory and moves
// each finished PNG into place. A PNG is only ever visible under its final name
// once it is complete, so a concurrent reader never sees a half-written file.
func (c *Cache) renderMissing(ctx context.Context, file, keyDir string, pages []int, width int) (map[int]string, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// A temp directory inside the cache root keeps the rename on the same
	// filesystem, so promoting a file is atomic rather than a copy.
	tmpDir, err := os.MkdirTemp(c.root, "render-")
	if err != nil {
		return nil, fmt.Errorf("make a thumbnail work directory: %w", err)
	}
	// The work directory holds partially written images and is removed even if
	// the render fails.
	defer os.RemoveAll(tmpDir)

	ctx, cancel := context.WithTimeout(ctx, renderTimeout)
	defer cancel()

	produced, err := c.renderer.Render(ctx, file, pages, width, tmpDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return nil, err
	}
	out := make(map[int]string, len(pages))
	for i, produced := range produced {
		if i >= len(pages) {
			break
		}
		page := pages[i]
		final := filepath.Join(keyDir, fmt.Sprintf("p%d.png", page))
		if err := os.Rename(produced, final); err != nil {
			return nil, fmt.Errorf("store the thumbnail for page %d: %w", page, err)
		}
		if err := os.Chmod(final, 0o600); err != nil {
			return nil, err
		}
		out[page] = final
	}
	if len(out) != len(pages) {
		return nil, fmt.Errorf("rendered %d of %d pages", len(out), len(pages))
	}
	return out, nil
}

// key derives the cache directory name from the file's content and the width.
// Content, not path: a renamed or moved document keeps its thumbnails, and an
// edited one does not.
func (c *Cache) key(file string, width int) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", fmt.Errorf("read the document: %w", err)
	}
	defer f.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		return "", fmt.Errorf("read the document: %w", err)
	}
	sum := hex.EncodeToString(digest.Sum(nil))
	// The width is part of the key because the same page at 64px and at 800px
	// are different files. The first bytes are enough to keep directory names
	// short and still spread across a filesystem.
	return sum[:16] + "-" + strconv.Itoa(width), nil
}

// Prune removes thumbnails until the cache is inside both limits, oldest first.
// Files younger than youngFileGrace are never removed, so a render that is
// still in flight cannot lose its output.
func (c *Cache) Prune(now time.Time) error {
	type entry struct {
		path    string
		modTime time.Time
		size    int64
	}
	var entries []entry
	var total int64
	err := filepath.WalkDir(c.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// A directory that vanished under us is not a prune failure.
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".png" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		entries = append(entries, entry{path: path, modTime: info.ModTime(), size: info.Size()})
		total += info.Size()
		return nil
	})
	if err != nil {
		return err
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].modTime.Before(entries[j].modTime) })
	cutoff := now.Add(-c.limits.MaxAge)

	// Age first: a file past its limit is stale no matter how much room is left.
	for _, e := range entries {
		if e.modTime.Before(cutoff) && now.Sub(e.modTime) >= youngFileGrace {
			if err := remove(e.path, e.size, &total); err != nil {
				return err
			}
		}
	}
	// Then size, oldest first, until the total is back inside the limit.
	for _, e := range entries {
		if total <= c.limits.MaxBytes {
			break
		}
		if now.Sub(e.modTime) < youngFileGrace {
			continue
		}
		if err := remove(e.path, e.size, &total); err != nil {
			return err
		}
	}
	return nil
}

// remove deletes a thumbnail and subtracts its size from the running total. The
// size is passed in because the file is gone by the time the total is fixed up,
// so it cannot be re-read from disk.
func remove(path string, size int64, total *int64) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	*total -= size
	return nil
}

// Clear removes every cached thumbnail. It reports how many files went, which
// is what `fcctl cache clear` prints.
func (c *Cache) Clear() (int, error) {
	var removed int
	err := filepath.WalkDir(c.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".png" {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		removed++
		return nil
	})
	return removed, err
}

// validate checks a request and returns its pages in order with the width to
// use, applying the default width when none was asked for.
func validate(req Request) ([]int, int, error) {
	width := req.Width
	if width == 0 {
		width = DefaultWidth
	}
	if width < MinWidth || width > MaxWidth {
		return nil, 0, fmt.Errorf("%w: %d is not between %d and %d", ErrBadWidth, width, MinWidth, MaxWidth)
	}
	if len(req.Pages) == 0 {
		return nil, 0, ErrNoPages
	}
	if len(req.Pages) > maxPagesPerRequest {
		return nil, 0, fmt.Errorf("%w: %d, the limit is %d", ErrTooManyPages, len(req.Pages), maxPagesPerRequest)
	}
	seen := map[int]bool{}
	pages := make([]int, 0, len(req.Pages))
	for _, page := range req.Pages {
		if page < 1 {
			return nil, 0, fmt.Errorf("%w: %d", ErrBadPage, page)
		}
		if seen[page] {
			continue
		}
		seen[page] = true
		pages = append(pages, page)
	}
	sort.Ints(pages)
	return pages, width, nil
}

// pngSize reads a PNG's dimensions from its header. Only the fixed part of the
// header is needed: the signature, then the IHDR chunk's width and height.
func pngSize(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	var header [24]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return 0, 0, err
	}
	if !strings.HasPrefix(string(header[:8]), "\x89PNG\r\n\x1a\n") {
		return 0, 0, errors.New("not a PNG")
	}
	width := int(header[16])<<24 | int(header[17])<<16 | int(header[18])<<8 | int(header[19])
	height := int(header[20])<<24 | int(header[21])<<16 | int(header[22])<<8 | int(header[23])
	if width <= 0 || height <= 0 {
		return 0, 0, errors.New("the PNG header reports no size")
	}
	return width, height, nil
}

// CacheRoot returns the private cache directory: $XDG_CACHE_HOME/file-converter/
// thumbs, falling back to ~/.cache/file-converter/thumbs. Thumbnails of a
// sensitive document should not be written somewhere another user's processes
// would expect to find them, so the root is never shared.
func CacheRoot() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locate the user cache directory: %w", err)
		}
		base = home
	}
	return filepath.Join(base, "file-converter", "thumbs"), nil
}
