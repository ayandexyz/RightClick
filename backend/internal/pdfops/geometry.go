package pdfops

import (
	"context"
	"fmt"
	"os"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// CropParams holds parameters for pdf-crop operation.
type CropParams struct {
	Unit   string  // "mm", "pt", or "percent"
	Top    float64 // margin from top
	Right  float64 // margin from right
	Bottom float64 // margin from bottom
	Left   float64 // margin from left
	Pages  string  // page selection
}

// ResizeParams holds parameters for pdf-resize operation.
type ResizeParams struct {
	To    string // target page size: "A4", "A3", "A5", "Letter", "Legal"
	Fit   string // "" or "scale": fit the page inside the target, keeping its proportions
	Pages string // page selection
}

// Validate checks that CropParams are valid.
func (p CropParams) Validate() error {
	switch p.Unit {
	case "mm", "pt", "percent":
	default:
		return fmt.Errorf("crop unit must be mm, pt, or percent")
	}
	for _, margin := range []float64{p.Top, p.Right, p.Bottom, p.Left} {
		if margin < 0 {
			return fmt.Errorf("crop margins cannot be negative")
		}
		if margin != margin || margin > 1e6 {
			return fmt.Errorf("crop margins must be ordinary numbers")
		}
	}
	if p.Top+p.Right+p.Bottom+p.Left == 0 {
		return fmt.Errorf("crop needs at least one margin")
	}
	if p.Unit == "percent" {
		if p.Top+p.Bottom >= 100 {
			return fmt.Errorf("top and bottom margins would leave no page height")
		}
		if p.Left+p.Right >= 100 {
			return fmt.Errorf("left and right margins would leave no page width")
		}
	}
	return nil
}

// Validate checks that ResizeParams are valid.
func (p ResizeParams) Validate() error {
	switch p.To {
	case "A4", "A3", "A5", "Letter", "Legal":
	default:
		return fmt.Errorf("resize target must be A4, A3, A5, Letter, or Legal")
	}
	switch p.Fit {
	case "", "scale":
	default:
		return fmt.Errorf("resize fit must be scale: pdfcpu can only fit a page inside the target, keeping its proportions")
	}
	return nil
}

// points converts a margin to points on a page dimension, for the check that a
// crop leaves something of the page.
func (p CropParams) points(margin, dimension float64) float64 {
	switch p.Unit {
	case "mm":
		return margin * 72 / 25.4
	case "percent":
		return dimension * margin / 100
	}
	return margin
}

// Crop sets the crop box of each selected page to the page less the given
// margins. Margins are measured on the page as the reader sees it: a page that
// carries /Rotate is made upright first.
func (b *PDFCPU) Crop(ctx context.Context, input, output string, params CropParams) error {
	if err := params.Validate(); err != nil {
		return err
	}
	count, err := b.PageCount(ctx, input)
	if err != nil {
		return err
	}
	pages, numbers, err := selectedPages(params.Pages, count)
	if err != nil {
		return err
	}
	sizes := pdfPageSizes(ctx, input, count)
	// A margin that leaves no page is refused naming the page, so a document
	// with one small page among large ones is explained and not just rejected.
	if numbers == nil {
		numbers = make([]int, count)
		for i := range numbers {
			numbers[i] = i + 1
		}
	}
	for _, page := range numbers {
		if page > len(sizes) || sizes[page-1].W <= 0 || sizes[page-1].H <= 0 {
			continue
		}
		w, h := sizes[page-1].W, sizes[page-1].H
		if params.points(params.Top, h)+params.points(params.Bottom, h) >= h {
			return fmt.Errorf("page %d is %.0f points tall, and the top and bottom margins leave none of it", page, h)
		}
		if params.points(params.Left, w)+params.points(params.Right, w) >= w {
			return fmt.Errorf("page %d is %.0f points wide, and the left and right margins leave none of it", page, w)
		}
	}

	work, err := os.MkdirTemp("", "fcd-crop-")
	if err != nil {
		return fmt.Errorf("make a work directory: %w", err)
	}
	defer os.RemoveAll(work)
	source, err := uprightCopy(ctx, input, work, sizes)
	if err != nil {
		return fmt.Errorf("cropping a rotated page needs qpdf: %w", err)
	}

	// pdfcpu reads four bare numbers as top, right, bottom, left margins. The
	// relative form takes fractions of the page.
	unit := types.POINTS
	spec := fmt.Sprintf("%g %g %g %g", params.Top, params.Right, params.Bottom, params.Left)
	switch params.Unit {
	case "mm":
		unit = types.MILLIMETRES
	case "percent":
		spec = fmt.Sprintf("%g %g %g %g rel", params.Top/100, params.Right/100, params.Bottom/100, params.Left/100)
	}
	box, err := model.ParseBox(spec, unit)
	if err != nil {
		return fmt.Errorf("crop margins: %w", err)
	}
	return runPDFCPU(ctx, func() error {
		return api.CropFile(source, output, pages, box, model.NewDefaultConfiguration())
	})
}

// Resize fits each selected page inside the target size, keeping its
// proportions and centring it. A landscape page stays landscape: the target's
// long side follows the page's.
func (b *PDFCPU) Resize(ctx context.Context, input, output string, params ResizeParams) error {
	if err := params.Validate(); err != nil {
		return err
	}
	count, err := b.PageCount(ctx, input)
	if err != nil {
		return err
	}
	pages, _, err := selectedPages(params.Pages, count)
	if err != nil {
		return err
	}
	resize, err := pdfcpu.ParseResizeConfig("formsize:"+params.To, types.POINTS)
	if err != nil {
		return fmt.Errorf("resize target: %w", err)
	}
	return runPDFCPU(ctx, func() error {
		return api.ResizeFile(input, output, pages, resize, model.NewDefaultConfiguration())
	})
}
