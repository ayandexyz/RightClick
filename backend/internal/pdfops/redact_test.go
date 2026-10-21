package pdfops

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
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

// buildSecretPDF makes three pages: only page 2 carries SECRETWORD in its
// text, and the document properties repeat it, as real documents often do.
func buildSecretPDF(t *testing.T) string {
	t.Helper()
	requireTool(t, "ps2pdf")
	dir := t.TempDir()
	script := "%!PS-Adobe-3.0\n%%Pages: 3\n%%EndComments\n" +
		"[ /Title (SECRETWORD report) /Author (SECRETWORD author) /Subject (SECRETWORD) /Keywords (SECRETWORD) /DOCINFO pdfmark\n" +
		"/Helvetica findfont 48 scalefont setfont\n" +
		"72 700 moveto (PAGE1 plain) show showpage\n" +
		"72 700 moveto (PAGE2 SECRETWORD) show showpage\n" +
		"72 700 moveto (PAGE3 plain) show showpage\n%%EOF\n"
	source := filepath.Join(dir, "secret.ps")
	if err := os.WriteFile(source, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	pdf := filepath.Join(dir, "secret.pdf")
	runTool(t, "ps2pdf", source, pdf)
	return pdf
}

// TestRedactPDFLeavesNoTraceOfTheSecret is the property the whole tool rests
// on: after redaction the word must not be recoverable from the file by any
// route, not merely hidden from a text extractor. It reads the file the way an
// attacker would: every object decoded by qpdf, plus the raw bytes.
func TestRedactPDFLeavesNoTraceOfTheSecret(t *testing.T) {
	requireTool(t, "pdftocairo")
	requireTool(t, "magick")
	requireTool(t, "pdftotext")
	requireTool(t, "qpdf")
	input := buildSecretPDF(t)

	// Self-check: the fixture really does contain the word before redaction,
	// so an empty result below means something.
	if !strings.Contains(decodedPDF(t, input), "SECRETWORD") {
		t.Skip("fixture does not carry the secret in a form this test can see")
	}

	output := filepath.Join(t.TempDir(), "redacted.pdf")
	area := RedactArea{Page: 2, X: 0.05, Y: 0.05, W: 0.9, H: 0.2}
	if err := redactPDF(context.Background(), input, output, RedactParams{Areas: []RedactArea{area}, DPI: 150}); err != nil {
		t.Fatal(err)
	}

	for name, haystack := range map[string]string{
		"decoded objects": decodedPDF(t, output),
		"raw bytes":       readFileString(t, output),
	} {
		if strings.Contains(strings.ToLower(haystack), "secretword") {
			t.Errorf("the secret survives in the %s of the redacted file", name)
		}
	}
	if got := wholeDocText(t, output); strings.Contains(strings.ToLower(got), "secretword") {
		t.Errorf("the secret survives as extractable text: %q", got)
	}
	// Pages that were not touched keep their text; the redaction is not a
	// blanket rasterisation of the document.
	if !strings.Contains(stampPageText(t, output, 1), "PAGE1") || !strings.Contains(stampPageText(t, output, 3), "PAGE3") {
		t.Error("untouched pages lost their text")
	}
	if n, err := pageCountViaPDFInfo(t, output); err != nil || n != 3 {
		t.Errorf("redacted file has %d pages (%v), want 3", n, err)
	}
}

func decodedPDF(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("qpdf", "--qdf", "--object-streams=disable", "--decode-level=all", path, "-").Output()
	if err != nil {
		t.Fatalf("qpdf --qdf %s: %v", path, err)
	}
	return string(out)
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func wholeDocText(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("pdftotext", "-raw", path, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	return string(out)
}
