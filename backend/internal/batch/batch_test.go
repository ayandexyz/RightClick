package batch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func paths(items []Item) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.Path)
	}
	return out
}

func TestExpandExplicitFileIsKept(t *testing.T) {
	file := write(t, filepath.Join(t.TempDir(), "a.png"))
	items, err := ExpandInputs([]string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []Item{{Path: file, Explicit: true}}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("got %#v, want %#v", items, want)
	}
}

func TestExpandTopLevelFolderIsNotRecursive(t *testing.T) {
	root := t.TempDir()
	a := write(t, filepath.Join(root, "b.png"))
	b := write(t, filepath.Join(root, "a.png"))
	write(t, filepath.Join(root, "sub", "c.png"))
	items, err := ExpandInputs([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths(items), []string{b, a}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, item := range items {
		if item.Explicit || item.Root != root {
			t.Fatalf("bad item %#v", item)
		}
	}
}

func TestExpandRecursiveWalksSubfoldersInStableOrder(t *testing.T) {
	root := t.TempDir()
	top := write(t, filepath.Join(root, "z.png"))
	deep := write(t, filepath.Join(root, "b", "c", "d.png"))
	mid := write(t, filepath.Join(root, "b", "m.png"))
	first := write(t, filepath.Join(root, "a.png"))
	for i := 0; i < 3; i++ {
		items, err := ExpandInputs([]string{root}, true)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := paths(items), []string{first, deep, mid, top}; !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: got %v, want %v", i, got, want)
		}
	}
}

func TestExpandSkipsHiddenFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	visible := write(t, filepath.Join(root, "ok.png"))
	write(t, filepath.Join(root, ".hidden.png"))
	write(t, filepath.Join(root, ".cache", "x.png"))
	write(t, filepath.Join(root, "sub", ".tmp.png"))
	items, err := ExpandInputs([]string{root}, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths(items), []string{visible}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.png"))
	real := write(t, filepath.Join(root, "real.png"))
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(root, "link.png")); err != nil {
		t.Fatal(err)
	}
	items, err := ExpandInputs([]string{root}, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths(items), []string{real}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandKeepsExplicitHiddenFileAndRoot(t *testing.T) {
	base := t.TempDir()
	hidden := write(t, filepath.Join(base, ".named.png"))
	root := filepath.Join(base, ".album")
	inner := write(t, filepath.Join(root, "p.png"))
	items, err := ExpandInputs([]string{hidden, root}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths(items), []string{hidden, inner}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDeduplicatesAndExplicitWins(t *testing.T) {
	root := t.TempDir()
	file := write(t, filepath.Join(root, "a.png"))
	items, err := ExpandInputs([]string{root, file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].Explicit || items[0].Root != root {
		t.Fatalf("got %#v", items)
	}
}

func TestExpandRejectsRelativeMissingAndSpecialInputs(t *testing.T) {
	root := t.TempDir()
	if _, err := ExpandInputs([]string{"relative.png"}, false); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := ExpandInputs([]string{filepath.Join(root, "missing.png")}, false); err == nil {
		t.Fatal("missing path accepted")
	}
	fifo := filepath.Join(root, "pipe")
	if err := mkfifo(fifo); err == nil {
		if _, err := ExpandInputs([]string{fifo}, false); err == nil {
			t.Fatal("fifo accepted")
		}
	}
}

func TestExpandEmptyFolderYieldsNoItems(t *testing.T) {
	items, err := ExpandInputs([]string{t.TempDir()}, true)
	if err != nil || len(items) != 0 {
		t.Fatalf("got %#v, %v", items, err)
	}
}

func TestOutputDirMirrorsRelativeFolderUnderOutDir(t *testing.T) {
	root := "/photos"
	out := "/dest"
	cases := []struct {
		item Item
		out  string
		want string
	}{
		{Item{Path: "/photos/trip/a.png", Root: root}, out, "/dest/trip"},
		{Item{Path: "/photos/a.png", Root: root}, out, "/dest"},
		{Item{Path: "/photos/trip/deep/a.png", Root: root}, out, "/dest/trip/deep"},
		{Item{Path: "/elsewhere/a.png", Explicit: true}, out, "/dest"},
		{Item{Path: "/photos/trip/a.png", Root: root}, "", ""},
		{Item{Path: "/elsewhere/a.png", Explicit: true}, "", ""},
	}
	for _, c := range cases {
		if got := c.item.OutputDir(c.out); got != c.want {
			t.Errorf("%+v with %q: got %q, want %q", c.item, c.out, got, c.want)
		}
	}
}

func TestExpandKeepsInputOrderAndSortsWithinFolders(t *testing.T) {
	base := t.TempDir()
	late := write(t, filepath.Join(base, "z-explicit.png"))
	root := filepath.Join(base, "folder")
	b := write(t, filepath.Join(root, "b.png"))
	a := write(t, filepath.Join(root, "a.png"))
	early := write(t, filepath.Join(base, "a-explicit.png"))
	items, err := ExpandInputs([]string{late, root, early}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths(items), []string{late, a, b, early}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
