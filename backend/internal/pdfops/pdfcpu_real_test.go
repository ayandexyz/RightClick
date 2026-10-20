package pdfops

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// makePDFWithMetadata produces a valid one-page PDF whose Info dictionary holds
// an Author and a Title, so stripping metadata has something to remove. A blank
// PDF would make pdfcpu report "no property removed" and pass vacuously.
func makePDFWithMetadata(t *testing.T, path string) {
	t.Helper()
	if availableBinary("qpdf") != nil {
		t.Skip("qpdf not installed; cannot synthesise a valid PDF for this test")
	}
	if err := defaultRunTool(context.Background(), "qpdf", []string{"--empty", path}); err != nil {
		t.Fatalf("qpdf --empty failed: %v", err)
	}
	if err := runPDFCPU(context.Background(), func() error {
		return api.AddPropertiesFile(path, "", map[string]string{"author": "Ada Lovelace", "title": "Analytical Engine"}, model.NewDefaultConfiguration())
	}); err != nil {
		t.Fatalf("seeding metadata failed: %v", err)
	}
}

// makePNG writes a 1×1 grayscale PNG to path so pdfcpu has something to
// import. PNG is the one format whose bytes are easy to assemble; the
// alternative is go's encoder, and either is fine.
func makePNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.Gray{Y: 128})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write png: %v", err)
	}
}

func TestPDFCPUStripMetadataReal(t *testing.T) {
	tmp := t.TempDir()
	in := filepath.Join(tmp, "in.pdf")
	makePDFWithMetadata(t, in)
	out := filepath.Join(tmp, "out.pdf")
	be := NewPDFCPUBackend()
	if err := be.StripMetadata(context.Background(), in, out); err != nil {
		t.Fatalf("StripMetadata = %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output not written: %v", err)
	}
}

func TestPDFCPUImportImagesReal(t *testing.T) {
	tmp := t.TempDir()
	pngPath := filepath.Join(tmp, "tiny.png")
	makePNG(t, pngPath)
	out := filepath.Join(tmp, "out.pdf")
	be := NewPDFCPUBackend()
	if err := be.ImportImages(context.Background(), []string{pngPath}, out); err != nil {
		t.Fatalf("ImportImages = %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output not written: %v", err)
	}
}
