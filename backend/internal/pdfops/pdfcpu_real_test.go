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

// The job layer creates an empty placeholder at the output path before calling
// the backend. pdfcpu appends to an existing file, so the import must still
// produce a valid PDF with one page per image.
func TestPDFCPUImportImagesOverEmptyPlaceholder(t *testing.T) {
	tmp := t.TempDir()
	a, b := filepath.Join(tmp, "a.png"), filepath.Join(tmp, "b.png")
	makePNG(t, a)
	makePNG(t, b)
	out := filepath.Join(tmp, "out.pdf")
	if err := os.WriteFile(out, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	be := NewPDFCPUBackend()
	if err := be.ImportImages(context.Background(), []string{a, b}, out); err != nil {
		t.Fatalf("ImportImages over a placeholder = %v", err)
	}
	pages, err := be.PageCount(context.Background(), out)
	if err != nil || pages != 2 {
		t.Fatalf("pages = %d, err = %v, want 2 pages", pages, err)
	}
}
