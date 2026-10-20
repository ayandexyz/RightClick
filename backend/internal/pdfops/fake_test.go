package pdfops

import "context"

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
