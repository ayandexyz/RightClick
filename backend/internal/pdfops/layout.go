package pdfops

import (
	"context"
	"fmt"
	"math"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// NUpParams holds parameters for pdf-nup operation.
type NUpParams struct {
	PerSheet    int    // pages per sheet: 2, 4, 6, 9
	Orientation string // "portrait" or "landscape"
	Margin      int    // margin in points
	Border      bool   // draw border around each page
}

// BookletParams holds parameters for pdf-booklet operation.
type BookletParams struct {
	PerSheet int // pages per sheet: 2 or 4
}

// LayoutParams is the wire-level layout form shared by n-up and booklet.
type LayoutParams struct {
	PerSheet    int
	Orientation string
	Margin      int
	Border      bool
}

// Validate checks that NUpParams are valid.
func (p NUpParams) Validate() error {
	switch p.PerSheet {
	case 2, 4, 6, 9:
	default:
		return fmt.Errorf("n-up per_sheet must be 2, 4, 6, or 9")
	}

	switch p.Orientation {
	case "portrait", "landscape", "":
	default:
		return fmt.Errorf("n-up orientation must be portrait or landscape")
	}

	if p.Margin < 0 {
		return fmt.Errorf("n-up margin cannot be negative")
	}

	return nil
}

// Validate checks that BookletParams are valid.
func (p BookletParams) Validate() error {
	switch p.PerSheet {
	case 2, 4:
	default:
		return fmt.Errorf("booklet per_sheet must be 2 or 4")
	}

	return nil
}

// firstPageDims returns the first page's width and height in points as the
// reader sees it, or a Letter page when pdfinfo cannot say. Sheets are sized
// from it so imposing a document does not change the paper it is printed on.
func (b *PDFCPU) firstPageDims(ctx context.Context, path string) (w, h float64) {
	if sizes := pdfPageSizes(ctx, path, 1); len(sizes) > 0 && sizes[0].W > 0 && sizes[0].H > 0 {
		return sizes[0].W, sizes[0].H
	}
	return 612, 792
}

// sheetDescription builds pdfcpu's description for a sheet of the given
// orientation, the size of the document's first page.
func sheetDescription(w, h float64, landscape bool) string {
	long, short := math.Max(w, h), math.Min(w, h)
	if landscape {
		return fmt.Sprintf("dimensions:%.0f %.0f", long, short)
	}
	return fmt.Sprintf("dimensions:%.0f %.0f", short, long)
}

// NUp arranges several pages on each sheet, left to right and top to bottom.
// The sheet is the size of the document's first page, turned to landscape when
// asked; a final sheet that is not full is left partly empty.
func (b *PDFCPU) NUp(ctx context.Context, input, output string, params NUpParams) error {
	if err := params.Validate(); err != nil {
		return err
	}
	w, h := b.firstPageDims(ctx, input)
	landscape := params.Orientation == "landscape"
	if params.Orientation == "" {
		// 2-up on a portrait page sits side by side, so it needs a landscape
		// sheet to keep the pages a readable size.
		landscape = params.PerSheet == 2 || params.PerSheet == 6
	}
	desc := sheetDescription(w, h, landscape)
	if params.Margin > 0 {
		desc += fmt.Sprintf(", margin:%d", params.Margin)
	}
	if params.Border {
		desc += ", border:on"
	}
	nup, err := api.PDFNUpConfig(params.PerSheet, desc, model.NewDefaultConfiguration())
	if err != nil {
		return fmt.Errorf("n-up layout: %w", err)
	}
	return runPDFCPU(ctx, func() error {
		return api.NUpFile([]string{input}, output, nil, nup, model.NewDefaultConfiguration())
	})
}

// Booklet imposes the document for printing double-sided and folding: with 2
// pages per sheet, the sheets are landscape and fold in the middle; with 4, a
// portrait sheet holds four pages. A page count that does not fill the last
// sheet is padded with blank pages by pdfcpu.
func (b *PDFCPU) Booklet(ctx context.Context, input, output string, params BookletParams) error {
	if err := params.Validate(); err != nil {
		return err
	}
	w, h := b.firstPageDims(ctx, input)
	nup, err := api.PDFBookletConfig(params.PerSheet, sheetDescription(w, h, params.PerSheet == 2), model.NewDefaultConfiguration())
	if err != nil {
		return fmt.Errorf("booklet layout: %w", err)
	}
	return runPDFCPU(ctx, func() error {
		return api.BookletFile([]string{input}, output, nil, nup, model.NewDefaultConfiguration())
	})
}
