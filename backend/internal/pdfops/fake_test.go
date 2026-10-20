package pdfops

import (
	"context"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// fakeBackend records the calls an Operation makes so tests can assert the
// mapping from parameters to backend work without touching a real PDF.
type fakeBackend struct {
	pages          map[string]int // input path to page count
	pageCount      int            // used for paths not in pages
	calls          []string
	merged         [][]string
	collected      [][]int
	rotated        []rotation
	importedImages []int
	// assembled records Assemble calls and insertedBlanks records
	// InsertBlankPages calls, so the edit pipeline's exact steps can be
	// asserted without a real PDF.
	assembled      [][]PageItem
	insertedBlanks []inserted
	err            error
	available      error
}

type rotation struct {
	pages []int
	angle int
}

func newFakeBackend(pageCount int, paths ...string) *fakeBackend {
	fake := &fakeBackend{pageCount: pageCount, pages: map[string]int{}}
	for _, path := range paths {
		fake.pages[path] = pageCount
	}
	return fake
}

func (f *fakeBackend) ID() string       { return "fake" }
func (f *fakeBackend) Available() error { return f.available }
func (f *fakeBackend) PageCount(_ context.Context, path string) (int, error) {
	if err := f.err; err != nil {
		return 0, err
	}
	if count, ok := f.pages[path]; ok {
		return count, nil
	}
	return f.pageCount, nil
}

func (f *fakeBackend) Merge(_ context.Context, inputs []string, _ string) error {
	if err := f.err; err != nil {
		return err
	}
	f.calls = append(f.calls, "merge")
	f.merged = append(f.merged, inputs)
	return nil
}

func (f *fakeBackend) Collect(_ context.Context, _, _ string, pages []int) error {
	if err := f.err; err != nil {
		return err
	}
	f.calls = append(f.calls, "collect")
	f.collected = append(f.collected, pages)
	return nil
}

func (f *fakeBackend) Rotate(_ context.Context, _, _ string, pages []int, angle int) error {
	if err := f.err; err != nil {
		return err
	}
	f.calls = append(f.calls, "rotate")
	f.rotated = append(f.rotated, rotation{pages: pages, angle: angle})
	return nil
}

func (f *fakeBackend) ImportImages(_ context.Context, inputs []string, _ string) error {
	f.calls = append(f.calls, "import_images")
	f.importedImages = append(f.importedImages, len(inputs))
	return f.err
}

// inserted records one InsertBlankPagesFile call: the final page numbers a
// blank was inserted beside, and whether the blank goes before them.
type inserted struct {
	after  []int
	before bool
}

// Assemble records the single Assemble call the operation makes, so a test can
// assert the exact items it passed rather than the pdfcpu calls behind them.
func (f *fakeBackend) Assemble(_ context.Context, _, _ string, items []PageItem) error {
	if err := f.err; err != nil {
		return err
	}
	f.calls = append(f.calls, "assemble")
	f.assembled = append(f.assembled, append([]PageItem(nil), items...))
	return nil
}

func (f *fakeBackend) InsertBlankPages(_ context.Context, _, _ string, after []int, _ bool, _ *types.Dim) error {
	if err := f.err; err != nil {
		return err
	}
	f.calls = append(f.calls, "insert_blank")
	f.insertedBlanks = append(f.insertedBlanks, inserted{after: append([]int(nil), after...)})
	return nil
}
