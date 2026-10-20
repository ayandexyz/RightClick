// Package batch expands the inputs of a conversion request into a flat,
// deterministic list of files, descending into folders when asked.
package batch

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Item is one file to convert.
type Item struct {
	Path     string // absolute path of the file
	Explicit bool   // named directly by the caller rather than found in a folder
	Root     string // folder the file was found under; empty for files not found by a scan
}

// OutputDir returns where this item's outputs go. With no outDir, outputs sit
// next to the source (reported as ""). With an outDir, files found under a
// folder keep their path relative to that folder.
func (i Item) OutputDir(outDir string) string {
	if outDir == "" {
		return ""
	}
	if i.Root == "" {
		return outDir
	}
	rel, err := filepath.Rel(i.Root, filepath.Dir(i.Path))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return outDir
	}
	return filepath.Join(outDir, rel)
}

// ExpandInputs turns files and folders into a flat list of files in input
// order; the files a folder contributes are sorted lexically by path. Folders
// contribute their top-level files, or every file below them when recursive.
// Hidden entries and symbolic links found while scanning are skipped, so a scan
// never leaves the folder it was pointed at. Explicit files and folders are
// kept even when hidden. A path repeated in the input appears once, as explicit
// if any occurrence was.
func ExpandInputs(paths []string, recursive bool) ([]Item, error) {
	byPath := map[string]*Item{}
	var order []string
	add := func(item Item) {
		if existing, ok := byPath[item.Path]; ok {
			existing.Explicit = existing.Explicit || item.Explicit
			if existing.Root == "" {
				existing.Root = item.Root
			}
			return
		}
		byPath[item.Path] = &item
		order = append(order, item.Path)
	}
	for _, input := range paths {
		if !filepath.IsAbs(input) {
			return nil, fmt.Errorf("path must be absolute: %s", input)
		}
		clean := filepath.Clean(input)
		info, err := os.Stat(clean)
		if err != nil {
			return nil, err
		}
		switch {
		case info.IsDir():
			var found []Item
			if err := scan(clean, clean, recursive, func(item Item) { found = append(found, item) }); err != nil {
				return nil, err
			}
			sort.Slice(found, func(a, b int) bool { return found[a].Path < found[b].Path })
			for _, item := range found {
				add(item)
			}
		case info.Mode().IsRegular():
			add(Item{Path: clean, Explicit: true})
		default:
			return nil, fmt.Errorf("not a regular file or folder: %s", input)
		}
	}
	items := make([]Item, 0, len(order))
	for _, path := range order {
		items = append(items, *byPath[path])
	}
	return items, nil
}

func scan(root, dir string, recursive bool, add func(Item)) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			continue
		case entry.IsDir():
			if recursive {
				if err := scan(root, path, recursive, add); err != nil {
					return err
				}
			}
		case entry.Type().IsRegular():
			add(Item{Path: path, Root: root})
		}
	}
	return nil
}
