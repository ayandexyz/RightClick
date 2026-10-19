package transport

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListenReplacesStaleSocketAndSetsPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "fcd.sock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}

	listener, cleanup, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %o, want 600", info.Mode().Perm())
	}
	if _, err := net.Dial("unix", path); err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
}

func TestListenRejectsOverlongSocketPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), strings.Repeat("a", 100), "fcd.sock")
	if _, _, err := Listen(path); err == nil || !strings.Contains(err.Error(), "unix socket path is too long") {
		t.Fatalf("expected clear path length error, got %v", err)
	}
}

func TestListenRejectsActiveSocket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fcd.sock")
	first, firstCleanup, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer firstCleanup()
	defer first.Close()
	if _, _, err := Listen(path); err == nil {
		t.Fatal("expected active daemon error")
	}
}
