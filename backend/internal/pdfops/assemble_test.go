package pdfops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assembleOn drives the pdf-edit operation against the fake backend and returns
// the fake so a test can assert the steps it took.
func assembleOn(t *testing.T, input string, pageCount int, items []PageItem) *fakeBackend {
	t.Helper()
	fake := newFakeBackend(pageCount, input)
	op, ok := Lookup("pdf-edit")
	if !ok {
		t.Fatal("pdf-edit is not in the catalogue")
	}
	toolbox := Toolbox{PDFCPU: fake}
	if err := op.Run(context.Background(), toolbox, []string{input}, filepath.Join(t.TempDir(), "out.pdf"), Params{Edit: items}); err != nil {
		t.Fatalf("pdf-edit: %v", err)
	}
	return fake
}

// A plain reorder is one Collect of the sources in the order given.
func TestAssembleReorder(t *testing.T) {
	input := filepath.Join(t.TempDir(), "a.pdf")
	fake := assembleOn(t, input, 4, []PageItem{{Src: 3}, {Src: 1}, {Src: 2}, {Src: 4}})
	if len(fake.assembled) != 1 {
		t.Fatalf("assemble calls = %d, want 1", len(fake.assembled))
	}
	want := []PageItem{{Src: 3}, {Src: 1}, {Src: 2}, {Src: 4}}
	got := fake.assembled[0]
	if len(got) != len(want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Omitting a page deletes it; the operation never sees a "delete" flag.
func TestAssembleDeleteIsOmission(t *testing.T) {
	input := filepath.Join(t.TempDir(), "a.pdf")
	fake := assembleOn(t, input, 5, []PageItem{{Src: 1}, {Src: 3}, {Src: 5}})
	got := fake.assembled[0]
	if len(got) != 3 {
		t.Fatalf("items = %v, want the three kept pages", got)
	}
	for i, want := range []int{1, 3, 5} {
		if got[i].Src != want {
			t.Fatalf("item %d src = %d, want %d", i, got[i].Src, want)
		}
	}
}

func TestAssembleDuplicate(t *testing.T) {
	input := filepath.Join(t.TempDir(), "a.pdf")
	fake := assembleOn(t, input, 3, []PageItem{{Src: 2}, {Src: 2}, {Src: 1}})
	got := fake.assembled[0]
	if len(got) != 3 {
		t.Fatalf("items = %v, want three entries", got)
	}
	if got[0].Src != 2 || got[1].Src != 2 {
		t.Fatalf("items = %v, want the page duplicated in place", got)
	}
}

// A blank carries no source page, which is what distinguishes it from page 1.
func TestAssembleBlankAtStartMiddleAndEnd(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []PageItem
	}{
		{"start", []PageItem{{Src: 0}, {Src: 1}, {Src: 2}}},
		{"middle", []PageItem{{Src: 1}, {Src: 0}, {Src: 2}}},
		{"end", []PageItem{{Src: 1}, {Src: 2}, {Src: 0}}},
		{"only one real page", []PageItem{{Src: 0}, {Src: 0}, {Src: 1}, {Src: 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "a.pdf")
			fake := assembleOn(t, input, 2, tc.items)
			got := fake.assembled[0]
			if len(got) != len(tc.items) {
				t.Fatalf("items = %v, want %d entries", got, len(tc.items))
			}
			for i := range tc.items {
				if got[i].Src != tc.items[i].Src {
					t.Fatalf("item %d = %+v, want %+v", i, got[i], tc.items[i])
				}
			}
		})
	}
}

// A document of only blank pages is not an edit of this document, so it is
// rejected rather than silently producing an empty PDF.
func TestAssembleAllBlankRejected(t *testing.T) {
	input := filepath.Join(t.TempDir(), "a.pdf")
	fake := newFakeBackend(3, input)
	op, _ := Lookup("pdf-edit")
	toolbox := Toolbox{PDFCPU: fake}
	err := op.Run(context.Background(), toolbox, []string{input}, filepath.Join(t.TempDir(), "out.pdf"), Params{Edit: []PageItem{{Src: 0}, {Src: 0}}})
	if err == nil {
		t.Fatal("an all-blank edit should be rejected")
	}
	if len(fake.assembled) != 0 {
		t.Fatal("a rejected edit must not reach the backend")
	}
}

func TestAssembleOutOfRangeSourceRejected(t *testing.T) {
	input := filepath.Join(t.TempDir(), "a.pdf")
	fake := newFakeBackend(10, input)
	op, _ := Lookup("pdf-edit")
	toolbox := Toolbox{PDFCPU: fake}
	err := op.Run(context.Background(), toolbox, []string{input}, filepath.Join(t.TempDir(), "out.pdf"),
		Params{Edit: []PageItem{{Src: 1}, {Src: 12}}})
	if err == nil {
		t.Fatal("a source page past the end should be rejected")
	}
	// The message must say which page and how many there are, because a grid
	// that raced a document edit leaves the user with no other way to tell.
	if !strings.Contains(err.Error(), "12") || !strings.Contains(err.Error(), "10") {
		t.Fatalf("error %q should name the page and the page count", err)
	}
	if len(fake.assembled) != 0 {
		t.Fatal("a rejected edit must not reach the backend")
	}
}

// The edit is bounded so one request cannot ask for an unbounded document.
func TestAssembleTooManyItemsRejected(t *testing.T) {
	input := filepath.Join(t.TempDir(), "a.pdf")
	fake := newFakeBackend(100000, input)
	op, _ := Lookup("pdf-edit")
	items := make([]PageItem, maxPages+1)
	for i := range items {
		items[i] = PageItem{Src: 1}
	}
	toolbox := Toolbox{PDFCPU: fake}
	err := op.Run(context.Background(), toolbox, []string{input}, filepath.Join(t.TempDir(), "out.pdf"), Params{Edit: items})
	if err == nil {
		t.Fatal("an edit beyond the page ceiling should be rejected")
	}
}

// Rotations are carried per item so a duplicated page can be turned one way
// while its original stays put.
func TestAssembleMixedRotations(t *testing.T) {
	input := filepath.Join(t.TempDir(), "a.pdf")
	fake := assembleOn(t, input, 3, []PageItem{
		{Src: 1, Rotate: 90}, {Src: 2}, {Src: 3, Rotate: 180}, {Src: 2, Rotate: 270},
	})
	got := fake.assembled[0]
	for i, want := range []int{90, 0, 180, 270} {
		if got[i].Rotate != want {
			t.Fatalf("item %d rotate = %d, want %d", i, got[i].Rotate, want)
		}
	}
}

func TestAssembleRejectsAnInvalidRotation(t *testing.T) {
	input := filepath.Join(t.TempDir(), "a.pdf")
	fake := newFakeBackend(3, input)
	op, _ := Lookup("pdf-edit")
	toolbox := Toolbox{PDFCPU: fake}
	err := op.Run(context.Background(), toolbox, []string{input}, filepath.Join(t.TempDir(), "out.pdf"),
		Params{Edit: []PageItem{{Src: 1, Rotate: 45}}})
	if err == nil {
		t.Fatal("a 45 degree rotation should be rejected")
	}
}

func TestAssembleEmptyEditRejected(t *testing.T) {
	input := filepath.Join(t.TempDir(), "a.pdf")
	fake := newFakeBackend(3, input)
	op, _ := Lookup("pdf-edit")
	toolbox := Toolbox{PDFCPU: fake}
	err := op.Run(context.Background(), toolbox, []string{input}, filepath.Join(t.TempDir(), "out.pdf"), Params{})
	if err == nil {
		t.Fatal("an edit with no items should be rejected")
	}
}

// pdf-edit is a studio operation: it takes no pages, angle, ranges or every,
// because the edit list already says exactly what happens to each page.
func TestAssembleTakesNoOtherSelection(t *testing.T) {
	op, ok := Lookup("pdf-edit")
	if !ok {
		t.Fatal("pdf-edit is not in the catalogue")
	}
	if op.Mode != "studio" {
		t.Fatalf("mode = %q, want studio", op.Mode)
	}
	if op.NeedsPages || op.NeedsAngle || op.OptionalPages {
		t.Fatal("pdf-edit must not ask for a page selection or an angle")
	}
	if op.Suffix != "-edited" {
		t.Fatalf("suffix = %q, want -edited", op.Suffix)
	}
}

// The real pipeline, driven by pdfcpu. It asserts the outcome a user cares
// about: the pages come out in the requested order, a blank is really blank,
// and only the pages that asked to be turned are turned.
func TestAssembleRealBackend(t *testing.T) {
	backend := NewPDFCPUBackend()
	if err := backend.Available(); err != nil {
		t.Skip("pdfcpu is not available")
	}
	input := buildTextPDF(t, 4)
	output := filepath.Join(t.TempDir(), "edited.pdf")

	err := backend.Assemble(context.Background(), input, output, []PageItem{
		{Src: 3}, {Src: 1, Rotate: 90}, {Src: 0}, {Src: 3}, {Src: 4},
	})
	if err != nil {
		t.Fatal(err)
	}

	count, err := backend.PageCount(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("page count = %d, want 5", count)
	}
	if text := documentText(t, output); text != "PAGE3 PAGE1 PAGE3 PAGE4" {
		t.Fatalf("page order = %q", text)
	}
	rotations := pageRotations(t, output)
	if rotations[1] != 90 {
		t.Fatalf("page 2 rotation = %d, want 90", rotations[1])
	}
	for i, want := range map[int]int{0: 0, 2: 0, 3: 0, 4: 0} {
		if rotations[i] != want {
			t.Fatalf("page %d rotation = %d, want %d", i+1, rotations[i], want)
		}
	}
}

// A blank page takes the size of the page it sits next to, so inserting one
// into a document does not change the size of the pages around it.
func TestAssembleRealBlankMatchesNeighbourSize(t *testing.T) {
	backend := NewPDFCPUBackend()
	if err := backend.Available(); err != nil {
		t.Skip("pdfcpu is not available")
	}
	input := buildTextPDF(t, 2)
	output := filepath.Join(t.TempDir(), "edited.pdf")
	if err := backend.Assemble(context.Background(), input, output, []PageItem{{Src: 1}, {Src: 0}, {Src: 2}}); err != nil {
		t.Fatal(err)
	}
	sizes := pageSizes(t, output)
	if len(sizes) != 3 {
		t.Fatalf("page count = %d, want 3, sizes=%v", len(sizes), sizes)
	}
	if sizes[1] != sizes[0] || sizes[1] != sizes[2] {
		t.Fatalf("blank page is %v but its neighbours are %v and %v", sizes[1], sizes[0], sizes[2])
	}
}

// Nothing temporary may survive: the intermediate files hold the user's pages
// in a predictable path and must not outlive the operation.
func TestAssembleRemovesIntermediates(t *testing.T) {
	backend := NewPDFCPUBackend()
	if err := backend.Available(); err != nil {
		t.Skip("pdfcpu is not available")
	}
	input := buildTextPDF(t, 3)
	dir := t.TempDir()
	output := filepath.Join(dir, "edited.pdf")
	if err := backend.Assemble(context.Background(), input, output, []PageItem{
		{Src: 3}, {Src: 0}, {Src: 1, Rotate: 90},
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "edited.pdf" {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("directory holds %v, want only the output", names)
	}
}

// A cancelled context stops the pipeline instead of finishing the work for a
// caller that has gone.
func TestAssembleCancellationStops(t *testing.T) {
	backend := NewPDFCPUBackend()
	if err := backend.Available(); err != nil {
		t.Skip("pdfcpu is not available")
	}
	input := buildTextPDF(t, 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := backend.Assemble(ctx, input, filepath.Join(t.TempDir(), "edited.pdf"),
		[]PageItem{{Src: 1}, {Src: 2}})
	if err == nil {
		t.Fatal("a cancelled context should stop the assembly")
	}
}
