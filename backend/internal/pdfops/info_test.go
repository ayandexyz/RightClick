package pdfops

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// samplePDFInfo is the shape pdfinfo prints for a two-page document, including
// the trailing sections that are not per-page.
const samplePDFInfo = `Title:           sample
Author:          someone
Producer:        something
Pages:           2
Encrypted:       no
Page    1 size:  595 x 842 pts (A4)
Page    1 rot:   0
Page    2 size:  612 x 792 pts (letter)
Page    2 rot:   90
File size:       12345 bytes
Optimized:       no
PDF version:     1.7
`

func TestParsePDFInfo(t *testing.T) {
	pages, count, err := ParsePDFInfo(samplePDFInfo)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("page count = %d, want 2", count)
	}
	if len(pages) != 2 {
		t.Fatalf("pages = %d, want 2", len(pages))
	}
	if pages[0].W != 595 || pages[0].H != 842 || pages[0].Rotation != 0 {
		t.Fatalf("page 1 = %+v, want 595x842 rot 0", pages[0])
	}
	if pages[1].W != 612 || pages[1].H != 792 || pages[1].Rotation != 90 {
		t.Fatalf("page 2 = %+v, want 612x792 rot 90", pages[1])
	}
}

// pdfinfo prints sizes with varying spacing and may append a paper name in
// parentheses; neither may stop the numbers being read.
func TestParsePDFInfoToleratesSpacingAndPaperNames(t *testing.T) {
	out := "Pages:           1\nPage    1 size:  595.276 x  841.89 pts (A4)\nPage    1 rot:  270\n"
	pages, count, err := ParsePDFInfo(out)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(pages) != 1 {
		t.Fatalf("count = %d, pages = %d, want 1 and 1", count, len(pages))
	}
	if pages[0].W != 595.276 || pages[0].H != 841.89 || pages[0].Rotation != 270 {
		t.Fatalf("page 1 = %+v", pages[0])
	}
}

// Output with no per-page lines is not an error: the caller still learns the
// page count, and the Studio can work without sizes.
func TestParsePDFInfoWithNoPageLinesGivesTheCountOnly(t *testing.T) {
	pages, count, err := ParsePDFInfo("Pages:           12\nFile size:  99 bytes\n")
	if err != nil {
		t.Fatal(err)
	}
	if count != 12 {
		t.Fatalf("count = %d, want 12", count)
	}
	if len(pages) != 0 {
		t.Fatalf("pages = %+v, want none", pages)
	}
}

// A line the parser does not recognise must be skipped, not treated as fatal;
// pdfinfo output grows between versions.
func TestParsePDFInfoSkipsUnrecognisedLines(t *testing.T) {
	out := "Pages:           1\nSomething:   new in a later version\nPage    1 size:  10 x 20 pts\nPage    1 rot:   0\nnonsense\n"
	pages, count, err := ParsePDFInfo(out)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(pages) != 1 || pages[0].W != 10 {
		t.Fatalf("count = %d, pages = %+v, want 1 page of width 10", count, pages)
	}
}

func TestParsePDFInfoRejectsOutputWithoutAPageCount(t *testing.T) {
	if _, _, err := ParsePDFInfo("garbage\n"); err == nil {
		t.Fatal("output with no Pages: line should be rejected")
	}
}

// Info must degrade to the page count alone when pdfinfo is missing, because
// the count comes from pdfcpu and does not need pdfinfo at all.
func TestInfoWorksWithoutPDFInfo(t *testing.T) {
	if availableBinary("pdfinfo") == nil {
		t.Skip("pdfinfo is installed; this case needs it absent")
	}
	// No output function and no pdfinfo on PATH: the service must still answer
	// with the page count alone.
	info := NewInfo(nil, newFakeBackend(4, "/tmp/in.pdf"))
	res, err := info.Pdf(context.Background(), "/tmp/in.pdf", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pages) != 0 {
		t.Fatalf("pages = %+v, want none", res.Pages)
	}
	if res.PageCount != 4 {
		t.Fatalf("page count = %d, want the pdfcpu count of 4", res.PageCount)
	}
}

// A document above the page ceiling is refused rather than reported, because
// every page-sized structure downstream would be unbounded too.
func TestInfoRejectsAPageCountAboveTheCeiling(t *testing.T) {
	info := NewInfo(nil, newFakeBackend(maxPages+1, "/tmp/huge.pdf"))
	if _, err := info.Pdf(context.Background(), "/tmp/huge.pdf", ""); err == nil {
		t.Fatal("a document over the page limit should be rejected")
	}
}

func TestInfoAcceptsExactlyTheCeiling(t *testing.T) {
	info := NewInfo(nil, newFakeBackend(maxPages, "/tmp/big.pdf"))
	result, err := info.Pdf(context.Background(), "/tmp/big.pdf", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.PageCount != maxPages {
		t.Fatalf("page count = %d, want %d", result.PageCount, maxPages)
	}
}

func TestInfoReportsThePageCountWhenTheEngineFails(t *testing.T) {
	backend := newFakeBackend(0, "/tmp/locked.pdf")
	backend.err = ErrPasswordRequired
	info := NewInfo(nil, backend)
	if _, err := info.Pdf(context.Background(), "/tmp/locked.pdf", ""); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("err = %v, want ErrPasswordRequired", err)
	}
}

// Real-binary check: the parser is written against pdfinfo's actual output, so
// one run against the installed tool guards against a format change.
func TestInfoRealPDFInfo(t *testing.T) {
	if availableBinary("pdfinfo") != nil {
		t.Skip("pdfinfo is not installed")
	}
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "tiny.png")
	makePNG(t, pngPath)
	// A real one-page PDF, built through pdfcpu rather than by hand, so the
	// fixture is valid without depending on any other tool's output format.
	in := filepath.Join(dir, "in.pdf")
	if err := NewPDFCPUBackend().ImportImages(context.Background(), []string{pngPath}, in); err != nil {
		t.Skipf("could not build a fixture with pdfcpu: %v", err)
	}
	info := NewInfo(nil, NewPDFCPUBackend())
	result, err := info.Pdf(context.Background(), in, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.PageCount != 1 {
		t.Fatalf("page count = %d, want 1", result.PageCount)
	}
	if len(result.Pages) != 1 {
		t.Fatalf("sizes = %+v, want one page", result.Pages)
	}
	if result.Pages[0].W <= 0 || result.Pages[0].H <= 0 {
		t.Fatalf("page size = %+v, want positive dimensions", result.Pages[0])
	}
}

// The injected output function must be used in preference to running pdfinfo,
// and must see the page range the caller asked for.
func TestInfoUsesTheInjectedOutput(t *testing.T) {
	var gotName string
	var gotArgs []string
	info := NewInfo(func(_ context.Context, name string, args []string) (string, error) {
		gotName, gotArgs = name, args
		return "Pages:           3\nPage    1 size:  10 x 20 pts\nPage    1 rot:   0\n", nil
	}, newFakeBackend(3, "/tmp/in.pdf"))

	result, err := info.Pdf(context.Background(), "/tmp/in.pdf", "")
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "pdfinfo" {
		t.Fatalf("ran %q, want pdfinfo", gotName)
	}
	if len(gotArgs) != 5 || gotArgs[0] != "-f" || gotArgs[1] != "1" || gotArgs[2] != "-l" || gotArgs[3] != "3" {
		t.Fatalf("args = %v, want -f 1 -l 3 then the file", gotArgs)
	}
	if len(result.Pages) != 1 || result.Pages[0].W != 10 {
		t.Fatalf("pages = %+v, want one 10-wide page", result.Pages)
	}
}

// pdfinfo failing must not lose the page count.
func TestInfoKeepsTheCountWhenTheOutputFunctionFails(t *testing.T) {
	info := NewInfo(func(_ context.Context, _ string, _ []string) (string, error) {
		return "", errors.New("pdfinfo blew up")
	}, newFakeBackend(7, "/tmp/in.pdf"))
	result, err := info.Pdf(context.Background(), "/tmp/in.pdf", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.PageCount != 7 || len(result.Pages) != 0 {
		t.Fatalf("result = %+v, want the count of 7 and no sizes", result)
	}
}
