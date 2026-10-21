package pdfops

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var pageLabel = regexp.MustCompile(`PAGE\d+`)

// labelsOnPage returns the sorted PAGEn labels drawn on one page. Order within
// a sheet is not asserted: pdftotext reports content-stream order, which is
// not where the pages sit.
func labelsOnPage(t *testing.T, path string, page int) []string {
	t.Helper()
	labels := pageLabel.FindAllString(strings.Join(strings.Fields(stampPageText(t, path, page)), ""), -1)
	sort.Strings(labels)
	return labels
}

func TestNUpPageCountAndSheetSize(t *testing.T) {
	requireTool(t, "pdfinfo")
	requireTool(t, "pdftotext")
	in := buildTextPDF(t, 5)
	be := NewPDFCPUBackend()
	for _, tc := range []struct {
		perSheet  int
		sheets    int
		landscape bool
	}{{2, 3, true}, {4, 2, false}, {6, 1, true}, {9, 1, false}} {
		out := filepath.Join(t.TempDir(), "out.pdf")
		if err := be.NUp(context.Background(), in, out, NUpParams{PerSheet: tc.perSheet}); err != nil {
			t.Fatalf("%d-up: %v", tc.perSheet, err)
		}
		count, err := be.PageCount(context.Background(), out)
		if err != nil || count != tc.sheets {
			t.Fatalf("%d-up of 5 pages = %d sheets (%v), want %d", tc.perSheet, count, err, tc.sheets)
		}
		size := pageDims(t, out, 1)[1]
		if (size[0] > size[1]) != tc.landscape || !near(size[0]*size[1], 612*792) {
			t.Fatalf("%d-up sheet = %v, want Letter paper, landscape=%v", tc.perSheet, size, tc.landscape)
		}
	}
}

func TestNUpPutsTheRightPagesOnEachSheetAndHonoursOrientation(t *testing.T) {
	requireTool(t, "pdfinfo")
	requireTool(t, "pdftotext")
	in := buildTextPDF(t, 5)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if err := NewPDFCPUBackend().NUp(context.Background(), in, out, NUpParams{PerSheet: 4, Orientation: "landscape", Margin: 10, Border: true}); err != nil {
		t.Fatal(err)
	}
	if size := pageDims(t, out, 1)[1]; !near(size[0], 792) || !near(size[1], 612) {
		t.Fatalf("sheet = %v, want landscape Letter", size)
	}
	if got := labelsOnPage(t, out, 1); strings.Join(got, ",") != "PAGE1,PAGE2,PAGE3,PAGE4" {
		t.Fatalf("sheet 1 = %v", got)
	}
	if got := labelsOnPage(t, out, 2); strings.Join(got, ",") != "PAGE5" {
		t.Fatalf("sheet 2 = %v, want only the leftover page", got)
	}
}

// pdfcpu pads a page count that does not fill the last sheet with blanks, so a
// 5-page document makes the same 4 sheets as an 8-page one.
func TestBookletImposesAndPads(t *testing.T) {
	requireTool(t, "pdfinfo")
	requireTool(t, "pdftotext")
	be := NewPDFCPUBackend()
	for _, tc := range []struct {
		pages, perSheet int
		sheets          []string // pages on each output sheet
	}{
		{8, 2, []string{"PAGE1,PAGE8", "PAGE2,PAGE7", "PAGE3,PAGE6", "PAGE4,PAGE5"}},
		{5, 2, []string{"PAGE1", "PAGE2", "PAGE3", "PAGE4,PAGE5"}},
		{8, 4, []string{"PAGE1,PAGE3,PAGE6,PAGE8", "PAGE2,PAGE4,PAGE5,PAGE7"}},
		{7, 4, []string{"PAGE1,PAGE3,PAGE6", "PAGE2,PAGE4,PAGE5,PAGE7"}},
	} {
		out := filepath.Join(t.TempDir(), "out.pdf")
		if err := be.Booklet(context.Background(), buildTextPDF(t, tc.pages), out, BookletParams{PerSheet: tc.perSheet}); err != nil {
			t.Fatalf("%d pages, %d up: %v", tc.pages, tc.perSheet, err)
		}
		count, _ := be.PageCount(context.Background(), out)
		if count != len(tc.sheets) {
			t.Fatalf("%d pages, %d up: %d sheets, want %d", tc.pages, tc.perSheet, count, len(tc.sheets))
		}
		for i, want := range tc.sheets {
			if got := strings.Join(labelsOnPage(t, out, i+1), ","); got != want {
				t.Errorf("%d pages, %d up, sheet %d = %s, want %s", tc.pages, tc.perSheet, i+1, got, want)
			}
		}
	}
}

func TestLayoutRejectsUnsupportedCounts(t *testing.T) {
	in := buildTextPDF(t, 2)
	be := NewPDFCPUBackend()
	for _, n := range []int{0, 1, 3, 5, 16} {
		if err := be.NUp(context.Background(), in, filepath.Join(t.TempDir(), "o.pdf"), NUpParams{PerSheet: n}); err == nil {
			t.Errorf("%d-up was accepted", n)
		}
	}
	for _, n := range []int{0, 3, 6} {
		if err := be.Booklet(context.Background(), in, filepath.Join(t.TempDir(), "o.pdf"), BookletParams{PerSheet: n}); err == nil {
			t.Errorf("%d-up booklet was accepted", n)
		}
	}
}
