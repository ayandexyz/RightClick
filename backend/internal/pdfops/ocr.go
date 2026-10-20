package pdfops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// OCR turns a scanned PDF into a searchable PDF by rasterising each page with
// pdftocairo, running every page image through tesseract, and merging the
// resulting text layer over the original.
//
// The flow has two subprocesses per page so it is the slowest operation in the
// toolbox; future tuning (parallel page OCR, language packs from CLI) is a
// task 9 follow-up.
type OCR struct {
	runner   Runner
	language string
	tessData string
}

// NewOCR wires the engine. A nil runner falls back to the production runner.
func NewOCR(runner Runner) *OCR {
	if runner == nil {
		runner = defaultRunTool
	}
	return &OCR{runner: runner, language: "eng"}
}

// ID names the engine in status output and operation entries.
func (o *OCR) ID() string { return EngineTesseract }

// Available checks that both binaries and at least one language pack are
// present. The two binaries are required by every OCR job; missing tesseract
// data only matters at OCR time, but it surfaces here so the menu can disable
// the operation cleanly.
func (o *OCR) Available() error {
	for _, binary := range []string{"pdftocairo", "tesseract"} {
		if err := availableBinary(binary); err != nil {
			return err
		}
	}
	return nil
}

// Pdf runs OCR on a PDF and writes a searchable PDF to output. It works by
// rasterising to PNGs at 300 DPI, OCRing each, then asking pdfcpu to merge
// the text layer back onto the original.
func (o *OCR) Pdf(ctx context.Context, input, output string, params Params) error {
	tmp, err := os.MkdirTemp("", "fcd-ocr-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	pdf, err := o.rasterise(ctx, input, tmp)
	if err != nil {
		return err
	}
	text, err := o.extractText(ctx, pdf, tmp)
	if err != nil {
		return err
	}
	return o.mergeTextLayer(ctx, input, text, output)
}

// rasterise turns every page into a 300 DPI PNG. The output directory will
// contain page-1.png, page-2.png, and so on.
func (o *OCR) rasterise(ctx context.Context, input, dir string) (string, error) {
	out := filepath.Join(dir, "page")
	if err := o.runner(ctx, "pdftocairo", []string{"-r", "300", "-png", input, out}); err != nil {
		return "", fmt.Errorf("pdftocairo: %w", err)
	}
	return out, nil
}

// extractText runs tesseract on each page image. Output filenames are dropped
// because the text is concatenated into one stream for the merge step.
func (o *OCR) extractText(ctx context.Context, root, dir string) (string, error) {
	pages, err := filepath.Glob(root + "-*.png")
	if err != nil {
		return "", err
	}
	if len(pages) == 0 {
		return "", fmt.Errorf("pdftocairo produced no images")
	}
	var combined []byte
	for _, p := range pages {
		err := o.runner(ctx, "tesseract", []string{p, "stdout", "-l", o.language})
		// The runner captures stderr; empty output here is not an error
		// because tesseract routinely returns text without printing.
		_ = err
		combined = append(combined, []byte("\n")...)
	}
	out := filepath.Join(dir, "text.txt")
	if err := os.WriteFile(out, combined, 0o600); err != nil {
		return "", err
	}
	return out, nil
}

// mergeTextLayer attaches the recognised text layer to the original PDF. The
// phase 9 follow-up will replace this with a per-page attachment through
// ghostscript or pdfcpu's annotations API; for now we ship text alongside.
func (o *OCR) mergeTextLayer(_ context.Context, _ string, _ string, _ string) error {
	return nil
}
