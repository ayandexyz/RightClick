package pdfops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// pageText returns what pdftotext reads from one page of a document.
func stampPageText(t *testing.T, path string, page int) string {
	t.Helper()
	out, err := exec.Command("pdftotext", "-raw", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), path, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext page %d: %v", page, err)
	}
	return string(out)
}

func stampReal(t *testing.T, pages int, p StampParams) (string, error) {
	t.Helper()
	requireTool(t, "pdftotext")
	in := buildTextPDF(t, pages)
	out := filepath.Join(t.TempDir(), "out.pdf")
	err := NewPDFCPUBackend().Stamp(context.Background(), in, out, p)
	return out, err
}

func TestStampPageNumbersReal(t *testing.T) {
	out, err := stampReal(t, 4, StampParams{Kind: StampPageNumbers, Format: "of-total", Start: 5, Pages: "2-3"})
	if err != nil {
		t.Fatal(err)
	}
	for page, want := range map[int]string{2: "6 of 4", 3: "7 of 4"} {
		if got := stampPageText(t, out, page); !strings.Contains(got, want) {
			t.Fatalf("page %d = %q, want %q", page, got, want)
		}
	}
	for _, page := range []int{1, 4} {
		if got := stampPageText(t, out, page); strings.Contains(got, " of ") {
			t.Fatalf("page %d outside the selection was numbered: %q", page, got)
		}
	}
}

func TestStampBatesReal(t *testing.T) {
	out, err := stampReal(t, 5, StampParams{Kind: StampBates, Prefix: "ABC-", Start: 8})
	if err != nil {
		t.Fatal(err)
	}
	for page, want := range []string{"ABC-000008", "ABC-000009", "ABC-000010", "ABC-000011", "ABC-000012"} {
		if got := stampPageText(t, out, page+1); !strings.Contains(got, want) {
			t.Fatalf("page %d = %q, want %q", page+1, got, want)
		}
	}
}

func TestStampHeaderFooterReal(t *testing.T) {
	out, err := stampReal(t, 2, StampParams{Kind: StampHeaderFooter, Left: "Left", Centre: "Page %p of %P", Right: "Right", Edge: "bottom"})
	if err != nil {
		t.Fatal(err)
	}
	got := stampPageText(t, out, 2)
	for _, want := range []string{"Left", "Page 2 of 2", "Right"} {
		if !strings.Contains(got, want) {
			t.Fatalf("page 2 = %q, want %q", got, want)
		}
	}
}

func TestStampTextRejectsWhatTheFontCannotDraw(t *testing.T) {
	for _, text := range []string{"আমি", "Привет", "日本", "100%", "%x", "bad\ntext"} {
		if _, err := stampReal(t, 1, StampParams{Kind: StampText, Text: text}); err == nil {
			t.Errorf("stamp text %q was accepted", text)
		}
	}
	out, err := stampReal(t, 1, StampParams{Kind: StampText, Text: "Café déjà-vu"})
	if err != nil {
		t.Fatalf("accented Latin was rejected: %v", err)
	}
	if got := stampPageText(t, out, 1); !strings.Contains(got, "Café") {
		t.Fatalf("accented text missing: %q", got)
	}
}

func TestStampImageValidation(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "ok.png")
	makePNG(t, png)
	fake := filepath.Join(dir, "fake.png")
	if err := os.WriteFile(fake, []byte("just text"), 0o600); err != nil {
		t.Fatal(err)
	}
	huge := filepath.Join(dir, "huge.png")
	f, _ := os.Create(huge)
	_, _ = f.Write([]byte("\x89PNG\r\n\x1a\n"))
	_ = f.Truncate(maxStampImageBytes + 1)
	f.Close()
	for name, image := range map[string]string{"missing": filepath.Join(dir, "none.png"), "directory": dir, "text renamed": fake, "huge": huge, "relative": "ok.png"} {
		if _, err := stampReal(t, 1, StampParams{Kind: StampImage, Image: image}); err == nil {
			t.Errorf("%s image was accepted", name)
		}
	}
	if _, err := stampReal(t, 1, StampParams{Kind: StampImage, Image: png, Opacity: 50}); err != nil {
		t.Fatalf("valid image rejected: %v", err)
	}
}

func TestStampPassesNeverNeedMoreThanTheDigitGroups(t *testing.T) {
	passes, err := stampPasses(StampParams{Kind: StampBates, Start: 1}, 120, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 1-9, 10-99 and 100-120 are three digit widths, so three calls however
	// many pages there are.
	if len(passes) != 3 {
		t.Fatalf("passes = %d, want 3", len(passes))
	}
}

// A stamp on a page that carries /Rotate lands where the user sees the page's
// corner, not where its unrotated corner would be.
func TestStampOnRotatedPageReal(t *testing.T) {
	requireTool(t, "qpdf")
	requireTool(t, "pdftotext")
	in := buildTextPDF(t, 1)
	dir := t.TempDir()
	rotated, out := filepath.Join(dir, "rot.pdf"), filepath.Join(dir, "out.pdf")
	be := NewPDFCPUBackend()
	if err := be.Rotate(context.Background(), in, rotated, []int{1}, 90); err != nil {
		t.Fatal(err)
	}
	if err := be.Stamp(context.Background(), rotated, out, StampParams{Kind: StampText, Text: "MARK", Position: "tl", Size: 20}); err != nil {
		t.Fatal(err)
	}
	box, err := exec.Command("pdftotext", "-raw", "-bbox", out, "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	var x, y float64
	found := false
	for _, line := range strings.Split(string(box), "\n") {
		if strings.Contains(line, ">MARK<") {
			found = true
			fields := strings.Fields(line)
			for _, f := range fields {
				if v, ok := strings.CutPrefix(f, `xMin="`); ok {
					x, _ = strconv.ParseFloat(strings.TrimSuffix(v, `"`), 64)
				}
				if v, ok := strings.CutPrefix(f, `yMin="`); ok {
					y, _ = strconv.ParseFloat(strings.TrimSuffix(v, `"`), 64)
				}
			}
		}
	}
	if !found {
		t.Fatal("the stamp is missing from a rotated page")
	}
	// The page is 792 wide after rotation; top-left means near the left edge
	// and near the top, whatever the stored orientation was.
	if x > 100 || y > 100 {
		t.Fatalf("stamp at (%.0f, %.0f), want the top-left corner", x, y)
	}
}
