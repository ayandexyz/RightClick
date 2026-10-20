package thumbs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRenderer stands in for pdftocairo. It writes a minimal but valid PNG so
// the header parsing and the cache plumbing are exercised without the tool
// being installed.
type fakeRenderer struct {
	mu    sync.Mutex
	calls [][]int
	delay time.Duration
	fail  error
}

func (f *fakeRenderer) Render(_ context.Context, file string, pages []int, _ int, dir string) ([]string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]int(nil), pages...))
	delay, fail := f.delay, f.fail
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if fail != nil {
		return nil, fail
	}
	// One PNG per page, named the way pdftocairo names its own output so the
	// Cache's real matching logic is exercised.
	out := make([]string, 0, len(pages))
	for _, page := range pages {
		path := filepath.Join(dir, fmt.Sprintf("page-%d.png", page))
		if err := os.WriteFile(path, onePixelPNG(), 0o600); err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	return out, nil
}

func (f *fakeRenderer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func onePixelPNG() []byte {
	return []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0, 0, 0, 13, 'I', 'H', 'D', 'R',
		0, 0, 0, 1, 0, 0, 0, 1,
		8, 6, 0, 0, 0,
		0x1f, 0x15, 0xc4, 0x89,
	}
}

func testPDF(t *testing.T, dir, name string, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("%PDF-1.7\n"+content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestCache(t *testing.T, r Renderer) (*Cache, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "thumbs")
	cache := NewCache(root, Limits{MaxBytes: 1 << 20, MaxAge: time.Hour}, r)
	return cache, root
}

func TestCacheCreatesPrivateDirectories(t *testing.T) {
	cache, root := newTestCache(t, &fakeRenderer{})
	if err := cache.Ensure(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	// A thumbnail of a sensitive document must not be readable by other users.
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("cache root mode = %o, want 700", info.Mode().Perm())
	}
}

func TestCacheHitAvoidsTheRenderer(t *testing.T) {
	renderer := &fakeRenderer{}
	cache, _ := newTestCache(t, renderer)
	dir := t.TempDir()
	pdf := testPDF(t, dir, "a.pdf", "body")

	req := Request{File: pdf, Pages: []int{1, 2}, Width: 200}
	if _, err := cache.Render(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	first := renderer.callCount()
	if first == 0 {
		t.Fatal("the first request should have rendered")
	}
	if _, err := cache.Render(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if renderer.callCount() != first {
		t.Fatalf("renderer called %d times, want %d: the second request should hit the cache", renderer.callCount(), first)
	}
}

// The key must follow content, not the path: a renamed or moved file reuses
// its thumbnails, and an edited one does not.
func TestCacheKeyFollowsContentNotPath(t *testing.T) {
	renderer := &fakeRenderer{}
	cache, _ := newTestCache(t, renderer)
	dir := t.TempDir()
	original := testPDF(t, dir, "a.pdf", "body")
	elsewhere := filepath.Join(t.TempDir(), "renamed.pdf")
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(elsewhere, data, 0o600); err != nil {
		t.Fatal(err)
	}
	edited := testPDF(t, t.TempDir(), "edited.pdf", "body with one more byte")

	req := Request{Width: 200, Pages: []int{1}}
	if _, err := cache.Render(context.Background(), withFile(req, original)); err != nil {
		t.Fatal(err)
	}
	after := renderer.callCount()
	if _, err := cache.Render(context.Background(), withFile(req, elsewhere)); err != nil {
		t.Fatal(err)
	}
	if renderer.callCount() != after {
		t.Fatal("a copy of the same bytes should reuse the cached thumbnails")
	}
	if _, err := cache.Render(context.Background(), withFile(req, edited)); err != nil {
		t.Fatal(err)
	}
	if renderer.callCount() == after {
		t.Fatal("an edited file should render again")
	}
}

func TestCachedFilesArePrivate(t *testing.T) {
	cache, root := newTestCache(t, &fakeRenderer{})
	dir := t.TempDir()
	pdf := testPDF(t, dir, "a.pdf", "body")
	page, err := cache.Render(context.Background(), Request{File: pdf, Pages: []int{1}, Width: 200})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(page[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("thumbnail mode = %o, want 600", info.Mode().Perm())
	}
	keyDir := filepath.Dir(page[0].Path)
	keyInfo, err := os.Stat(keyDir)
	if err != nil {
		t.Fatal(err)
	}
	if keyInfo.Mode().Perm() != 0o700 {
		t.Fatalf("key directory mode = %o, want 700", keyInfo.Mode().Perm())
	}
	_ = root
}

// Pruning must never take a file a concurrent request is about to use, so
// anything younger than a minute survives even when the cache is over its limit.
func TestPruneKeepsYoungFiles(t *testing.T) {
	renderer := &fakeRenderer{}
	cache, _ := newTestCache(t, renderer)
	dir := t.TempDir()
	pdf := testPDF(t, dir, "a.pdf", "body")
	pages, err := cache.Render(context.Background(), Request{File: pdf, Pages: []int{1}, Width: 200})
	if err != nil {
		t.Fatal(err)
	}
	// A limit of zero bytes should still leave the fresh thumbnail alone.
	zeroed := NewCache(cache.root, Limits{MaxBytes: 0, MaxAge: time.Hour}, renderer)
	if err := zeroed.Prune(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pages[0].Path); err != nil {
		t.Fatalf("a thumbnail younger than a minute was pruned: %v", err)
	}
}

func TestPruneBySizeRemovesTheOldestFirst(t *testing.T) {
	renderer := &fakeRenderer{}
	_, root := newTestCache(t, renderer)
	old := filepath.Join(root, "old")
	fresh := filepath.Join(root, "fresh")
	for _, dir := range []string{old, fresh} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "p1.png"), make([]byte, 4096), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(filepath.Join(old, "p1.png"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(fresh, "p1.png"), newTime, newTime); err != nil {
		t.Fatal(err)
	}

	// A tiny budget that is below the fresh file, but both are over a minute
	// old so both are prunable; the older one must go first.
	pruner := NewCache(root, Limits{MaxBytes: 5000, MaxAge: time.Hour}, renderer)
	if err := pruner.Prune(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(old, "p1.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the oldest thumbnail should have been pruned")
	}
	if _, err := os.Stat(filepath.Join(fresh, "p1.png")); err != nil {
		t.Fatalf("the newer thumbnail should have survived: %v", err)
	}
}

func TestPruneByAge(t *testing.T) {
	renderer := &fakeRenderer{}
	_, root := newTestCache(t, renderer)
	dir := filepath.Join(root, "stale")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "p1.png")
	if err := os.WriteFile(stale, make([]byte, 16), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	pruner := NewCache(root, Limits{MaxBytes: 1 << 30, MaxAge: 7 * 24 * time.Hour}, renderer)
	if err := pruner.Prune(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a thumbnail older than the age limit should have been pruned")
	}
}

// Two identical requests arriving together must render once, not twice.
func TestConcurrentIdenticalRequestsRenderOnce(t *testing.T) {
	renderer := &fakeRenderer{delay: 20 * time.Millisecond}
	cache, _ := newTestCache(t, renderer)
	dir := t.TempDir()
	pdf := testPDF(t, dir, "a.pdf", "body")
	req := Request{File: pdf, Pages: []int{1, 2}, Width: 200}

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = cache.Render(context.Background(), req)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if got := renderer.callCount(); got != 1 {
		t.Fatalf("renderer called %d times, want 1", got)
	}
}

func TestWidthBounds(t *testing.T) {
	cache, _ := newTestCache(t, &fakeRenderer{})
	dir := t.TempDir()
	pdf := testPDF(t, dir, "a.pdf", "body")
	// Zero means "no width asked for" and takes the default; anything else
	// outside the range is a client mistake worth reporting.
	for _, width := range []int{63, 801, -1, 1000} {
		if _, err := cache.Render(context.Background(), Request{File: pdf, Pages: []int{1}, Width: width}); err == nil {
			t.Fatalf("width %d should have been rejected", width)
		}
	}
	for _, width := range []int{0, 64, 200, 800} {
		if _, err := cache.Render(context.Background(), Request{File: pdf, Pages: []int{1}, Width: width}); err != nil {
			t.Fatalf("width %d should be accepted: %v", width, err)
		}
	}
}

func TestPageCountBounds(t *testing.T) {
	cache, _ := newTestCache(t, &fakeRenderer{})
	dir := t.TempDir()
	pdf := testPDF(t, dir, "a.pdf", "body")
	many := make([]int, maxPagesPerRequest+1)
	for i := range many {
		many[i] = i + 1
	}
	if _, err := cache.Render(context.Background(), Request{File: pdf, Pages: many, Width: 200}); err == nil {
		t.Fatalf("more than %d pages should be rejected", maxPagesPerRequest)
	}
	if _, err := cache.Render(context.Background(), Request{File: pdf, Pages: []int{0}, Width: 200}); err == nil {
		t.Fatal("page 0 should be rejected")
	}
	if _, err := cache.Render(context.Background(), Request{File: pdf, Pages: []int{-1}, Width: 200}); err == nil {
		t.Fatal("a negative page should be rejected")
	}
}

// A cancelled or failed render must not leave a partial file where a later
// request would treat it as a valid thumbnail.
func TestCancellationLeavesNoPartialFile(t *testing.T) {
	renderer := &fakeRenderer{fail: errors.New("pdftocairo exploded")}
	cache, root := newTestCache(t, renderer)
	dir := t.TempDir()
	pdf := testPDF(t, dir, "a.pdf", "body")
	if _, err := cache.Render(context.Background(), Request{File: pdf, Pages: []int{1}, Width: 200}); err == nil {
		t.Fatal("a renderer failure should surface as an error")
	}
	if err := walkPngs(root); err != nil {
		t.Fatalf("a failed render left files behind: %v", err)
	}
}

func TestCancellationStopsRendering(t *testing.T) {
	renderer := &fakeRenderer{delay: time.Second}
	cache, _ := newTestCache(t, renderer)
	dir := t.TempDir()
	pdf := testPDF(t, dir, "a.pdf", "body")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.Render(ctx, Request{File: pdf, Pages: []int{1}, Width: 200}); err == nil {
		t.Fatal("a cancelled context should stop the render")
	}
}

// Thumbnail dimensions come from the PNG header, so the response tells the
// caller how large the image really is rather than what it asked for.
func TestResponseCarriesTheRenderedSize(t *testing.T) {
	cache, _ := newTestCache(t, &fakeRenderer{})
	dir := t.TempDir()
	pdf := testPDF(t, dir, "a.pdf", "body")
	pages, err := cache.Render(context.Background(), Request{File: pdf, Pages: []int{1}, Width: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(pages))
	}
	if pages[0].W != 1 || pages[0].H != 1 {
		t.Fatalf("size = %dx%d, want the header's 1x1", pages[0].W, pages[0].H)
	}
	if pages[0].Page != 1 {
		t.Fatalf("page = %d, want 1", pages[0].Page)
	}
}

// A damaged or unreadable file must fail before any rendering happens.
func TestUnreadableFileIsRejected(t *testing.T) {
	renderer := &fakeRenderer{}
	cache, _ := newTestCache(t, renderer)
	dir := t.TempDir()
	missing := filepath.Join(dir, "gone.pdf")
	if _, err := cache.Render(context.Background(), Request{File: missing, Pages: []int{1}, Width: 200}); err == nil {
		t.Fatal("a missing file should be rejected")
	}
	if renderer.callCount() != 0 {
		t.Fatal("nothing should be rendered for a missing file")
	}
}

func withFile(r Request, file string) Request {
	r.File = file
	return r
}

func walkPngs(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if err := walkPngs(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
			continue
		}
		if filepath.Ext(entry.Name()) == ".png" {
			return errors.New("found " + filepath.Join(root, entry.Name()))
		}
	}
	return nil
}

// TestPDFToolRealRenders exercises the installed pdftocairo end to end: three
// pages of a real PDF rendered at a requested width, each decodable and the
// right size. Skipped where pdftocairo is absent, which is the case this task
// is designed to degrade through rather than fail.
func TestPDFToolRealRenders(t *testing.T) {
	tool := NewPDFTool(nil)
	if err := tool.Available(); err != nil {
		t.Skip("pdftocairo is not installed")
	}
	pdf := buildRealPDF(t, 3)

	dir := t.TempDir()
	out, err := tool.Render(context.Background(), pdf, []int{1, 2, 3}, 120, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("rendered %d files, want 3", len(out))
	}
	for i, path := range out {
		w, h, err := pngSize(path)
		if err != nil {
			t.Fatalf("page %d: %v", i+1, err)
		}
		// -scale-to caps the longest side, so a portrait page comes back
		// narrower than the requested width. What matters is that it is the
		// cap and not exceeded, and that the aspect ratio survived.
		if w > 120 || h > 120 {
			t.Fatalf("page %d is %dx%d, want both sides within the requested 120", i+1, w, h)
		}
		if w <= 0 || h <= 0 {
			t.Fatalf("page %d is %dx%d, want positive dimensions", i+1, w, h)
		}
		if max(w, h) != 120 {
			t.Fatalf("page %d longest side = %d, want it filled to the requested 120", i+1, max(w, h))
		}
	}
}

// The real tool zero-pads its output names to the document's digit width. A
// 12-page document writes page-09.png, so matching must parse the number rather
// than build the name.
func TestPDFToolRealMatchesZeroPaddedNames(t *testing.T) {
	tool := NewPDFTool(nil)
	if err := tool.Available(); err != nil {
		t.Skip("pdftocairo is not installed")
	}
	pdf := buildRealPDF(t, 12)

	dir := t.TempDir()
	out, err := tool.Render(context.Background(), pdf, []int{9, 10, 11, 12}, 100, dir)
	if err != nil {
		t.Fatal(err)
	}
	for i, page := range []int{9, 10, 11, 12} {
		if got := cairoPageNumber(out[i]); got != page {
			t.Fatalf("file %d is page %d, want %d", i, got, page)
		}
	}
}

// buildRealPDF makes a real document with the given page count, using the same
// tools the daemon depends on. It skips the test when they are absent, so this
// never fails for want of a fixture.
func buildRealPDF(t *testing.T, pages int) string {
	t.Helper()
	if _, err := exec.LookPath("qpdf"); err != nil {
		t.Skip("qpdf is not installed")
	}
	dir := t.TempDir()
	// One page per image, assembled into a PDF by ImageMagick, which is the
	// same route the daemon's images-to-PDF operation takes.
	images := make([]string, 0, pages)
	for i := 1; i <= pages; i++ {
		image := filepath.Join(dir, fmt.Sprintf("p%d.png", i))
		if err := runTool(t, "magick", []string{"-size", "120x160", fmt.Sprintf("xc:gray%d", i%100), image}); err != nil {
			t.Skipf("could not build a page image: %v", err)
		}
		images = append(images, image)
	}
	pdf := filepath.Join(dir, "fixture.pdf")
	if err := runTool(t, "magick", append(append([]string{}, images...), pdf)); err != nil {
		t.Skipf("could not build a PDF fixture: %v", err)
	}
	if out, err := exec.Command("pdfinfo", pdf).CombinedOutput(); err == nil {
		if !strings.Contains(string(out), "Pages:           "+strconv.Itoa(pages)) {
			t.Fatalf("fixture has the wrong page count:\n%s", out)
		}
	}
	return pdf
}

func runTool(t *testing.T, name string, args []string) error {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		if name == "magick" {
			return runTool(t, "convert", args)
		}
		return err
	}
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, out)
	}
	return nil
}
