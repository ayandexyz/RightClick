package pdfops

import (
	"context"
	"errors"
	"os"
)

// ErrOCRUnsupported reports that this build cannot produce a searchable PDF.
// It is a distinct error so the caller can say why instead of reporting a
// generic failure, and so the operation can be disabled rather than offered
// and then failing.
var ErrOCRUnsupported = errors.New("making a searchable PDF is not supported by this build")

// OCR shells out to pdftocairo and tesseract to build a searchable PDF.
//
// A searchable PDF needs an invisible text layer drawn over each page image.
// Assembling one means matching every recognised word to its position on the
// page and writing a content stream per page, which no single command-line tool
// in this toolbox does. Rather than emit a PDF with an image layer and no text
// — which looks successful and is not searchable — this engine reports
// ErrOCRUnsupported until that step exists. The availability and rasterisation
// helpers below are the parts that are already correct and are reused by any
// future implementation.
type OCR struct {
	runner   Runner
	language string
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

// Available checks that both binaries the operation needs are present. A missing
// binary is reported here so the menu disables the operation with a reason
// rather than offering it and failing on activation.
func (o *OCR) Available() error {
	for _, binary := range []string{"pdftocairo", "tesseract"} {
		if err := availableBinary(binary); err != nil {
			return err
		}
	}
	return nil
}

// Pdf is not implemented: see the type comment. It always returns
// ErrOCRUnsupported and writes nothing, so no caller can publish an empty
// output.
func (o *OCR) Pdf(_ context.Context, _, output string, _ Params) error {
	// Refuse before creating anything, so an unsupported run never leaves a
	// partial file for the job layer to clean up.
	_ = os.Remove(output)
	return ErrOCRUnsupported
}
