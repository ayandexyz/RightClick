package pdfops

import (
	"context"
	"path/filepath"
	"strconv"
)

// selectedPages expands a page spec against a document's page count into the
// two forms the callers need: pdfcpu's string list and the numbers. An empty
// spec means every page and returns nil for both, which pdfcpu reads the same.
func selectedPages(spec string, count int) ([]string, []int, error) {
	if spec == "" {
		return nil, nil, nil
	}
	pages, err := ParsePages(spec, count)
	if err != nil {
		return nil, nil, err
	}
	out := make([]string, len(pages))
	for i, page := range pages {
		out[i] = strconv.Itoa(page)
	}
	return out, pages, nil
}

// pageSizes returns the size and rotation pdfinfo reports for pages 1..count,
// or nil when pdfinfo is missing or cannot read the document. Sizes are as the
// reader sees the page, after /Rotate.
func pdfPageSizes(ctx context.Context, path string, count int) []PageSize {
	out, err := runForOutput(ctx, nil, "pdfinfo", "-f", "1", "-l", strconv.Itoa(count), path)
	if err != nil {
		return nil
	}
	sizes, _, err := ParsePDFInfo(out)
	if err != nil {
		return nil
	}
	return sizes
}

// uprightCopy returns a path to a copy of input whose pages carry no /Rotate,
// written under work, or input itself when no page is rotated. The pages look
// the same afterwards. Tools that place things in page coordinates, such as
// pdfcpu's stamps and crop boxes, otherwise act on the page as stored and not
// as the reader sees it. Rotated documents need qpdf for this.
func uprightCopy(ctx context.Context, input, work string, sizes []PageSize) (string, error) {
	rotated := false
	for _, size := range sizes {
		if size.Rotation != 0 {
			rotated = true
			break
		}
	}
	if !rotated {
		return input, nil
	}
	upright := filepath.Join(work, "upright.pdf")
	if err := NewQPDF(nil).FlattenRotation(ctx, input, upright); err != nil {
		return "", err
	}
	return upright, nil
}
