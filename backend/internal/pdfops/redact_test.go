package pdfops

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactPNGPaintsOnlyMatchingPageAreas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page-001.png")
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.White)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if err := redactPNG(context.Background(), path, []RedactArea{{Page: 1, X: 0.2, Y: 0.3, W: 0.4, H: 0.2}}); err != nil {
		t.Fatal(err)
	}
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := png.Decode(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got.At(3, 4) != (color.RGBA{0, 0, 0, 255}) {
		t.Fatalf("redacted pixel = %v, want opaque black", got.At(3, 4))
	}
	if got.At(0, 0) != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("unredacted pixel = %v, want white", got.At(0, 0))
	}
}

func TestRedactPNGRejectsPageWithoutAnArea(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page-001.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	if err := redactPNG(context.Background(), path, []RedactArea{{Page: 2, X: 0, Y: 0, W: 1, H: 1}}); err == nil {
		t.Fatal("redactPNG accepted an area for a different page")
	}
}

func TestRedactPDFRemovesTextFromRedactedPage(t *testing.T) {
	requireTool(t, "pdftocairo")
	requireTool(t, "magick")
	requireTool(t, "pdftotext")
	input := buildTextPDF(t, 2)
	output := filepath.Join(t.TempDir(), "redacted.pdf")
	if err := redactPDF(context.Background(), input, output, RedactParams{Areas: []RedactArea{{Page: 1, X: 0, Y: 0, W: 1, H: 1}}, DPI: 150}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stampPageText(t, output, 1)); got != "" {
		t.Fatalf("redacted page text = %q, want empty", got)
	}
	if got := stampPageText(t, output, 2); got == "" {
		t.Fatal("untouched page lost its text")
	}
}
