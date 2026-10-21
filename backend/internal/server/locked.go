package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"file-converter/backend/internal/pdfops"
	"file-converter/backend/internal/thumbs"
)

// lockedSession holds what one connection needs to show a password-protected
// document: a single unlocked copy and a thumbnail cache of its own. Both live
// in a private directory that is removed when the connection ends, so a locked
// document's pages are never left in the persistent cache.
//
// The directory is made on first use, because most connections never open a
// locked document.
type lockedSession struct {
	base string
	// fork builds the connection's own cache; nil means thumbnails are off.
	fork func(root string) *thumbs.Cache

	mu     sync.Mutex
	dir    string
	cache  *thumbs.Cache
	copies map[string]string // document identity -> unlocked copy
}

func newLockedSession(base string, fork func(root string) *thumbs.Cache) *lockedSession {
	return &lockedSession{base: base, fork: fork, copies: map[string]string{}}
}

// ensure makes the session directory, 0700.
func (l *lockedSession) ensure() error {
	if l.dir != "" {
		return nil
	}
	if l.base != "" {
		if err := os.MkdirAll(l.base, 0o700); err != nil {
			return err
		}
	}
	dir, err := os.MkdirTemp(l.base, "locked-")
	if err != nil {
		return fmt.Errorf("make a private directory for the unlocked document: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	l.dir = dir
	return nil
}

// Close removes everything the session wrote. It is safe to call twice.
func (l *lockedSession) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dir != "" {
		_ = os.RemoveAll(l.dir)
		l.dir, l.cache, l.copies = "", nil, map[string]string{}
	}
}

// identity names a document and password pair, including the file's size and
// modification time so an edited document is unlocked again. The password is
// hashed into the name, never written to it.
func identity(file, password string) (string, error) {
	info, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(file + "\x00" + password + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)))
	return hex.EncodeToString(sum[:8]), nil
}

// render returns thumbnails of req.File, unlocking it with password first when
// it is encrypted. The second result is false when the document is not
// encrypted and the caller should use the persistent cache instead.
func (l *lockedSession) render(ctx context.Context, req thumbs.Request, password string) (pages []thumbs.Page, handled bool, err error) {
	// Without a password the document is either plain or locked, and in both
	// cases the persistent cache path reports it the way it always has.
	if password == "" || l.fork == nil {
		return nil, false, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	id, err := identity(req.File, password)
	if err != nil {
		return nil, false, err
	}
	unlocked, known := l.copies[id]
	if !known {
		if err := l.ensure(); err != nil {
			return nil, false, err
		}
		docDir := filepath.Join(l.dir, id)
		if err := os.MkdirAll(docDir, 0o700); err != nil {
			return nil, false, err
		}
		path, copied, err := pdfops.UnlockInto(ctx, req.File, docDir, password)
		if err != nil {
			_ = os.RemoveAll(docDir)
			return nil, false, err
		}
		if !copied {
			_ = os.RemoveAll(docDir)
			return nil, false, nil
		}
		unlocked = path
		l.copies[id] = unlocked
	}
	if l.cache == nil {
		l.cache = l.fork(filepath.Join(l.dir, "thumbs"))
	}
	req.File = unlocked
	pages, err = l.cache.Render(ctx, req)
	return pages, true, err
}
