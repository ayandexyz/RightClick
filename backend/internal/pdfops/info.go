package pdfops

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// PageSize is one page's dimensions in points and its rotation in degrees.
// Rotation is the page's own /Rotate value, not a cumulative rotation applied by
// a previous edit.
type PageSize struct {
	W        float64 `json:"w"`
	H        float64 `json:"h"`
	Rotation int     `json:"rot"`
}

// PDFInfoResult is what pdf_info returns. Pages is empty when the sizes could
// not be read; PageCount is always populated, because it comes from pdfcpu and
// does not depend on pdfinfo being installed.
type PDFInfoResult struct {
	PageCount int        `json:"pages"`
	Pages     []PageSize `json:"sizes,omitempty"`
}

// Info answers structural questions about a PDF: how many pages it has, and
// how big and how rotated each one is. The page count comes from the pdfcpu
// backend, which also maps encrypted and damaged files onto the error codes the
// protocol already uses; sizes and rotations come from pdfinfo, which is
// optional.
// InfoBackend is the surface pdf_info depends on; it lets the server take a
// stub in tests without depending on pdfcpu or pdfinfo.
type InfoBackend interface {
	Pdf(ctx context.Context, file string) (PDFInfoResult, error)
}

type Info struct {
	output  OutputFunc
	backend Backend
}

var _ InfoBackend = (*Info)(nil)

// NewInfo wires the service. A nil outFunc runs the real pdfinfo, which is the
// production path; tests inject one to drive the parsing.
func NewInfo(outFunc OutputFunc, backend Backend) *Info {
	return &Info{output: outFunc, backend: backend}
}

// Pdf reports the page count, and per-page sizes when pdfinfo is available.
func (i *Info) Pdf(ctx context.Context, file string) (PDFInfoResult, error) {
	if i.backend == nil {
		return PDFInfoResult{}, fmt.Errorf("no PDF engine is available")
	}
	count, err := i.backend.PageCount(ctx, file)
	if err != nil {
		return PDFInfoResult{}, err
	}
	if count > maxPages {
		return PDFInfoResult{}, fmt.Errorf("document has %d pages, more than the %d page limit", count, maxPages)
	}
	result := PDFInfoResult{PageCount: count}
	sizes, err := i.pageSizes(ctx, file, count)
	if err != nil || len(sizes) == 0 {
		// Sizes are a convenience: the Studio can work without them, so a
		// missing or unparsable pdfinfo degrades to the page count alone.
		return result, nil
	}
	result.Pages = sizes
	return result, nil
}

// pageSizes runs pdfinfo over the whole document and parses the per-page lines.
func (i *Info) pageSizes(ctx context.Context, file string, count int) ([]PageSize, error) {
	out, err := runForOutput(ctx, i.output, "pdfinfo", "-f", "1", "-l", strconv.Itoa(count), file)
	if err != nil {
		return nil, err
	}
	sizes, _, err := ParsePDFInfo(out)
	return sizes, err
}

// parsePDFInfo reads the page count and the per-page size and rotation lines
// from pdfinfo output. Lines it does not recognise are skipped, because
// pdfinfo gains fields between versions and an unknown field is not a reason to
// fail the request.
// ParsePDFInfo is exported so the server package and tests can drive it
// without having to produce a real pdfinfo binary. See package-internal
// callers for the semantics.
func ParsePDFInfo(output string) ([]PageSize, int, error) {
	count := 0
	var sizes []PageSize
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if rest, ok := cutPrefix(line, "Pages:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil {
				return nil, 0, fmt.Errorf("cannot read the page count from pdfinfo output")
			}
			count = n
			continue
		}
		page, rest, ok := cutPageLine(line)
		if !ok {
			continue
		}
		if len(sizes) < page {
			sizes = append(sizes, make([]PageSize, page-len(sizes))...)
		}
		if value, ok := cutPrefix(rest, "size:"); ok {
			if w, h, ok := parseSize(value); ok {
				sizes[page-1].W, sizes[page-1].H = w, h
			}
			continue
		}
		if value, ok := cutPrefix(rest, "rot:"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				sizes[page-1].Rotation = n
			}
		}
	}
	if count == 0 {
		return nil, 0, fmt.Errorf("cannot read the page count from pdfinfo output")
	}
	return sizes, count, nil
}

// cutPageLine splits "Page    2 size:  612 x 792 pts" into the page number and
// the rest of the line.
func cutPageLine(line string) (int, string, bool) {
	if !strings.HasPrefix(line, "Page") {
		return 0, "", false
	}
	rest := strings.TrimSpace(line[len("Page"):])
	space := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if space < 0 {
		return 0, "", false
	}
	page, err := strconv.Atoi(rest[:space])
	if err != nil || page < 1 {
		return 0, "", false
	}
	return page, strings.TrimSpace(rest[space:]), true
}

// parseSize reads "595.276 x  841.89 pts (A4)" and ignores the paper name.
func parseSize(value string) (float64, float64, bool) {
	fields := strings.Fields(value)
	if len(fields) < 3 || fields[1] != "x" {
		return 0, 0, false
	}
	w, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, 0, false
	}
	h, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return 0, 0, false
	}
	return w, h, true
}

func cutPrefix(s, prefix string) (string, bool) {
	if !strings.HasPrefix(s, prefix) {
		return "", false
	}
	return strings.TrimSpace(s[len(prefix):]), true
}
