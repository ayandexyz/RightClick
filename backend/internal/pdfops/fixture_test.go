package pdfops

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// These helpers drive the real PDF tools. Each skips the calling test when a
// tool is missing, so a machine without poppler still runs the rest of the
// suite rather than failing for the wrong reason.

var errBadFixture = errors.New("pdfinfo did not report a page count")

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not installed", name)
	}
}

// runTool runs a build-time tool and skips the test if it fails, because a
// fixture that cannot be built says nothing about the code under test.
func runTool(t *testing.T, name string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not installed", name)
	}
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Skipf("%s failed: %v: %s", name, err, out)
	}
}

// buildTextPDF writes a PDF with the given number of pages, each carrying the
// text PAGE1, PAGE2 and so on. The text is what proves the order an edit
// produced: a reordering that silently fell back to the source order would
// still be a valid PDF, but not the right one.
//
// The pages are written as PostScript and converted with ps2pdf, because
// ps2pdf emits real text objects that pdftotext can read back. Rasterised
// fixtures would make every order assertion vacuous.
func buildTextPDF(t *testing.T, pages int) string {
	t.Helper()
	dir := t.TempDir()

	var script strings.Builder
	script.WriteString("%!PS-Adobe-3.0\n")
	fmt.Fprintf(&script, "%%%%Pages: %d\n", pages)
	script.WriteString("%%%%EndComments\n")
	script.WriteString("/Helvetica findfont 72 scalefont setfont\n")
	for i := 1; i <= pages; i++ {
		// Letter size in points, so every page has the same box and a blank
		// inserted beside one is visibly the right shape.
		fmt.Fprintf(&script, "72 700 moveto (PAGE%d) show\n", i)
		script.WriteString("showpage\n")
	}
	script.WriteString("%%%%EOF\n")

	source := filepath.Join(dir, "fixture.ps")
	if err := os.WriteFile(source, []byte(script.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	pdf := filepath.Join(dir, "fixture.pdf")
	runTool(t, "ps2pdf", source, pdf)

	if got, err := pageCountViaPDFInfo(t, pdf); err != nil {
		t.Skipf("the fixture has no page count: %v", err)
	} else if got != pages {
		t.Skipf("the fixture has %d pages, want %d", got, pages)
	}
	return pdf
}

// documentText reads the whole document's text with whitespace normalised, so
// a test can compare the order the pages came out in without tripping on the
// line breaks pdftotext puts between pages.
func documentText(t *testing.T, path string) string {
	t.Helper()
	requireTool(t, "pdftotext")
	out, err := exec.Command("pdftotext", "-layout", path, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	return strings.Join(strings.Fields(string(out)), " ")
}

// pdfinfoFields runs pdfinfo and returns its raw output.
func pdfinfoFields(t *testing.T, path string) string {
	t.Helper()
	requireTool(t, "pdfinfo")
	out, err := exec.Command("pdfinfo", path).CombinedOutput()
	if err != nil {
		t.Fatalf("pdfinfo: %v: %s", err, out)
	}
	return string(out)
}

func pageCountViaPDFInfo(t *testing.T, path string) (int, error) {
	t.Helper()
	for _, line := range strings.Split(pdfinfoFields(t, path), "\n") {
		if !strings.HasPrefix(line, "Pages:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, errBadFixture
		}
		return strconv.Atoi(fields[1])
	}
	return 0, errBadFixture
}

// pdfinfoRange asks pdfinfo about every page, which is what produces the
// per-page "rot:" and "size:" lines the helpers below read.
func pdfinfoRange(t *testing.T, path string) string {
	t.Helper()
	count, err := pageCountViaPDFInfo(t, path)
	if err != nil {
		t.Fatalf("pdfinfo: %v", err)
	}
	out, err := exec.Command("pdfinfo", "-f", "1", "-l", strconv.Itoa(count), path).CombinedOutput()
	if err != nil {
		t.Fatalf("pdfinfo: %v: %s", err, out)
	}
	return string(out)
}

// pageSizes returns each page's size in points, as reported by pdfinfo.
func pageSizes(t *testing.T, path string) []string {
	t.Helper()
	var sizes []string
	for _, line := range strings.Split(pdfinfoRange(t, path), "\n") {
		// Only per-page lines carry useful size information; "File size:" and
		// friends share the substring "size:" without being page entries.
		if !strings.HasPrefix(line, "Page") {
			continue
		}
		_, size, _ := strings.Cut(line, "size:")
		fields := strings.Fields(size)
		if len(fields) >= 2 {
			sizes = append(sizes, fields[0]+"x"+fields[1])
		}
	}
	return sizes
}

// pageRotations returns each page's /Rotate value in degrees.
func pageRotations(t *testing.T, path string) []int {
	t.Helper()
	var rotations []int
	for _, line := range strings.Split(pdfinfoRange(t, path), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] != "Page" || fields[2] != "rot:" {
			continue
		}
		value, err := strconv.Atoi(fields[3])
		if err != nil {
			continue
		}
		rotations = append(rotations, value)
	}
	return rotations
}
