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

func TestRedactPDFRejectsUnsafeValidationInputs(t *testing.T) {
	input := buildTextPDF(t, 2)
	for name, params := range map[string]RedactParams{
		"no areas or terms": {},
		"zero width":        {Areas: []RedactArea{{Page: 1, W: 0, H: 0.1}}},
		"outside page":      {Areas: []RedactArea{{Page: 1, X: 0.9, W: 0.2, H: 0.1}}},
		"missing page":      {Areas: []RedactArea{{Page: 3, W: 0.1, H: 0.1}}},
		"low dpi":           {Areas: []RedactArea{{Page: 1, W: 0.1, H: 0.1}}, DPI: 149},
		"high dpi":          {Areas: []RedactArea{{Page: 1, W: 0.1, H: 0.1}}, DPI: 301},
	} {
		t.Run(name, func(t *testing.T) {
			if err := redactPDF(context.Background(), input, filepath.Join(t.TempDir(), "out.pdf"), params); err == nil {
				t.Fatal("unsafe redaction was accepted")
			}
		})
	}
}

func TestRedactPDFReportsMissingTextLayer(t *testing.T) {
	requireTool(t, "pdftocairo")
	requireTool(t, "qpdf")
	scan := filepath.Join(t.TempDir(), "scan.pdf")
	pngPath := filepath.Join(t.TempDir(), "scan.png")
	if err := os.WriteFile(pngPath, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A valid one-page image-only PDF is created by ImageMagick.
	if _, err := exec.LookPath("magick"); err != nil {
		t.Skip("magick is not installed")
	}
	if out, err := exec.Command("magick", "-size", "40x40", "xc:white", pngPath).CombinedOutput(); err != nil {
		t.Skipf("magick fixture failed: %v: %s", err, out)
	}
	if out, err := exec.Command("magick", pngPath, scan).CombinedOutput(); err != nil {
		t.Skipf("magick PDF fixture failed: %v: %s", err, out)
	}
	err := redactPDF(context.Background(), scan, filepath.Join(t.TempDir(), "out.pdf"), RedactParams{Terms: []string{"missing"}})
	if err == nil || !strings.Contains(err.Error(), "this page has no text layer; draw a box instead") {
		t.Fatalf("err = %v, want no-text-layer guidance", err)
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

// buildResiduePDF repeats SECRETWORD in every place besides page content that
// could carry it: Info, XMP, a bookmark, and link and text annotations.
func buildResiduePDF(t *testing.T) string {
	t.Helper()
	requireTool(t, "ps2pdf")
	dir := t.TempDir()
	script := "%!PS-Adobe-3.0\n%%Pages: 3\n%%EndComments\n" +
		"[ /Title (SECRETWORD report) /Author (SECRETWORD author) /Keywords (SECRETWORD) /DOCINFO pdfmark\n" +
		"[ /_objdef {meta} /type /stream /OBJ pdfmark\n" +
		"[ {meta} << /Type /Metadata /Subtype /XML >> /PUT pdfmark\n" +
		"[ {meta} (<?xpacket begin='' id='W5M0MpCehiHzreSzNTczkc9d'?><x:xmpmeta xmlns:x='adobe:ns:meta/'><rdf:RDF xmlns:rdf='http://www.w3.org/1999/02/22-rdf-syntax-ns#'><rdf:Description xmlns:dc='http://purl.org/dc/elements/1.1/'><dc:title>SECRETWORD xmp</dc:title></rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end='w'?>) /PUT pdfmark\n" +
		"[ {Catalog} << /Metadata {meta} >> /PUT pdfmark\n" +
		"[ /Page 2 /View [/XYZ null null null] /Title (SECRETWORD bookmark) /OUT pdfmark\n" +
		"/Helvetica findfont 48 scalefont setfont\n" +
		"72 700 moveto (PAGE1 plain) show showpage\n" +
		"72 700 moveto (PAGE2 SECRETWORD) show\n" +
		"[ /Rect [72 600 200 650] /Subtype /Link /Border [0 0 0] /Contents (SECRETWORD link) /ANN pdfmark\n" +
		"[ /Rect [72 500 200 550] /Subtype /Text /Contents (SECRETWORD note) /ANN pdfmark\n" +
		"showpage\n72 700 moveto (PAGE3 plain) show showpage\n%%EOF\n"
	source := filepath.Join(dir, "residue.ps")
	if err := os.WriteFile(source, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	pdf := filepath.Join(dir, "residue.pdf")
	runTool(t, "ps2pdf", source, pdf)
	return pdf
}

// TestRedactPDFDropsMetadataBookmarksAndAnnotations covers what page rasterising
// alone would not: document-level places a redacted word can hide.
func TestRedactPDFDropsMetadataBookmarksAndAnnotations(t *testing.T) {
	for _, tool := range []string{"pdftocairo", "qpdf", "ps2pdf"} {
		requireTool(t, tool)
	}
	input := buildResiduePDF(t)
	before := decodedPDF(t, input)
	for _, marker := range []string{"SECRETWORD xmp", "SECRETWORD bookmark", "SECRETWORD link", "SECRETWORD note", "SECRETWORD author"} {
		if !strings.Contains(before, marker) {
			t.Skipf("fixture does not carry %q", marker)
		}
	}
	output := filepath.Join(t.TempDir(), "redacted.pdf")
	area := RedactArea{Page: 2, X: 0.05, Y: 0.05, W: 0.9, H: 0.2}
	if err := redactPDF(context.Background(), input, output, RedactParams{Areas: []RedactArea{area}, DPI: 150}); err != nil {
		t.Fatal(err)
	}
	after := decodedPDF(t, output)
	if strings.Contains(strings.ToLower(after), "secretword") {
		t.Error("the secret survives in document properties, XMP, bookmarks or annotations")
	}
	for _, key := range []string{"/Outlines", "/Metadata", "/Annots", "/Author", "/Title"} {
		if strings.Contains(after, key) {
			t.Errorf("redacted file still carries %s", key)
		}
	}
}
