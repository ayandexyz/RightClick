package pdfops

import (
	"context"
	"fmt"
)

// runExternal dispatches an operation to the external engine that owns it.
// Only the structural operations ship in phase 2 task 2; each engine file adds
// its own cases as it lands.
func (o Operation) runExternal(ctx context.Context, tb Toolbox, inputs []string, output string, p Params) error {
	if len(inputs) != 1 && !o.Merges {
		return fmt.Errorf("%s works on one file at a time", o.ID)
	}
	switch o.EngineID() {
	case EngineQPDF:
		switch o.ID {
		case "pdf-repair":
			return tb.QPDF.Repair(ctx, inputs[0], output)
		case "pdf-linearize":
			return tb.QPDF.Linearize(ctx, inputs[0], output)
		case "pdf-flatten":
			return tb.QPDF.Flatten(ctx, inputs[0], output)
		}
	case EngineGhostscript:
		switch o.ID {
		case "pdf-grayscale":
			return tb.GS.Grayscale(ctx, inputs[0], output)
		case "pdf-compress-light", "pdf-compress-balanced", "pdf-compress-strong":
			return tb.GS.Compress(ctx, inputs[0], output, o.Preset)
		case "pdf-to-pdfa":
			level := p.PDFA
			if level == "" {
				level = "2b"
			}
			return tb.GS.PDFA(ctx, inputs[0], output, level)
		}
	case EngineLibreOffice:
		if len(inputs) != 1 {
			return fmt.Errorf("%s works on one file at a time", o.ID)
		}
		return tb.LibreOffice.Convert(ctx, inputs[0], output)
	case EngineTesseract:
		switch o.ID {
		case "pdf-ocr":
			return tb.OCR.Pdf(ctx, inputs[0], output, p)
		}
	case EnginePoppler:
		switch o.ID {
		case "pdf-to-text":
			return tb.Poppler.Text(ctx, inputs[0], output)
		case "pdf-compare":
			if len(inputs) != 2 {
				return fmt.Errorf("%s needs two inputs", o.ID)
			}
			return tb.Poppler.Compare(ctx, inputs[0], inputs[1], output)
		case "pdf-extract-images":
			// Numbered operations do not write their own output; they fill the
			// directory the job layer made and the job layer publishes each file.
			_, err := tb.Poppler.Images(ctx, inputs[0], output)
			return err
		}
	}
	return fmt.Errorf("%s has no implementation", o.ID)
}
