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
)

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
