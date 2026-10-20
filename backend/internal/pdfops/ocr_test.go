package pdfops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// OCR is not implemented in this phase, and the important property is that it
// says so rather than reporting success: a PDF that looks processed but has no
// text layer is worse than one the user was told could not be made.
func TestOCRRefusesRatherThanWritingAnEmptyPDF(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.pdf")
	err := NewOCR(nil).Pdf(context.Background(), "in.pdf", out, Params{})
	if !errors.Is(err, ErrOCRUnsupported) {
		t.Fatalf("err = %v, want ErrOCRUnsupported", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("OCR wrote %s despite refusing; it must write nothing", out)
	}
}

func TestOCRAvailabilityNeedsBothBinaries(t *testing.T) {
	if availableBinary("pdftocairo") != nil || availableBinary("tesseract") != nil {
		t.Skip("poppler-utils or tesseract not installed")
	}
	if err := NewOCR(nil).Available(); err != nil {
		t.Fatalf("Available() = %v, want nil", err)
	}
}

// The menu must not offer an operation that cannot run, whatever the engine's
// own availability says.
func TestOCRIsOfferedDisabledWithAReason(t *testing.T) {
	op, ok := Lookup("pdf-ocr")
	if !ok {
		t.Fatal("pdf-ocr is not in the catalogue")
	}
	tb := Toolbox{PDFCPU: &fakeBackend{}, OCR: NewOCR(nil)}
	if reason := op.UnavailableReason(tb, []string{"a.pdf"}); reason == "" {
		t.Fatal("pdf-ocr has no reason, so the menu would offer it")
	} else if reason != ErrOCRUnsupported.Error() {
		t.Fatalf("reason = %q, want %q", reason, ErrOCRUnsupported.Error())
	}
	if err := op.Run(context.Background(), tb, []string{"a.pdf"}, "out.pdf", Params{}); !errors.Is(err, ErrOCRUnsupported) {
		t.Fatalf("Run = %v, want ErrOCRUnsupported", err)
	}
}
