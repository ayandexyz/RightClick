package pdfops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffPagesIgnoresWhitespaceAndReportsChangedLines(t *testing.T) {
	a := [][]string{{"same line", "old value"}, {"unchanged"}}
	b := [][]string{{" same   line ", "new value"}, {"unchanged"}}
	report, err := DiffPages(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "page 1") || !strings.Contains(report, "-old value") || !strings.Contains(report, "+new value") {
		t.Fatalf("report = %q", report)
	}
	if strings.Contains(report, "same") {
		t.Fatalf("whitespace-only line should not be reported: %q", report)
	}
}

func TestDiffPagesReportsInsertedPageWithoutShiftingEveryFollowingPage(t *testing.T) {
	report, err := DiffPages(
		[][]string{{"one"}, {"two"}, {"three"}},
		[][]string{{"one"}, {"inserted"}, {"two"}, {"three"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "page 2 added") || strings.Contains(report, "page 3 differs") {
		t.Fatalf("report = %q", report)
	}
}

func TestDiffPagesEnforcesBounds(t *testing.T) {
	pages := make([][]string, maxComparePages+1)
	if _, err := DiffPages(pages, pages); !errors.Is(err, ErrCompareTooLarge) {
		t.Fatalf("error = %v, want ErrCompareTooLarge", err)
	}
}

func TestPopplerCompareReportsNoText(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "a.pdf"), filepath.Join(dir, "b.pdf")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("pdf"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := NewPoppler(func(_ context.Context, name string, args []string) error {
		if name != "pdftotext" {
			return nil
		}
		return os.WriteFile(args[len(args)-1], nil, 0o600)
	})
	err := p.Compare(context.Background(), first, second, filepath.Join(dir, "report.txt"))
	if !errors.Is(err, ErrNoTextToCompare) {
		t.Fatalf("error = %v, want ErrNoTextToCompare", err)
	}
}

func TestCompareOutputNameUsesBothInputs(t *testing.T) {
	op, ok := Lookup("pdf-compare")
	if !ok {
		t.Fatal("pdf-compare missing")
	}
	if got := CompareOutputName(op, "/tmp/alpha.pdf", "/tmp/beta.pdf"); got != "alpha-compare-beta" {
		t.Fatalf("name = %q", got)
	}
}

// buildLinesPDF makes a PDF with one page per entry, each line drawn on its own
// row, so the extracted text is exactly those lines.
func buildLinesPDF(t *testing.T, pages [][]string) string {
	t.Helper()
	requireTool(t, "ps2pdf")
	var script strings.Builder
	script.WriteString("%!PS-Adobe-3.0\n/Helvetica findfont 14 scalefont setfont\n")
	for _, lines := range pages {
		for i, line := range lines {
			fmt.Fprintf(&script, "72 %d moveto (%s) show\n", 700-i*24, line)
		}
		script.WriteString("showpage\n")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "doc.ps")
	if err := os.WriteFile(source, []byte(script.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	pdf := filepath.Join(dir, "doc.pdf")
	runTool(t, "ps2pdf", source, pdf)
	return pdf
}

func compareReport(t *testing.T, first, second string) (string, error) {
	t.Helper()
	requireTool(t, "pdftotext")
	out := filepath.Join(t.TempDir(), "report.txt")
	err := NewPoppler(nil).Compare(context.Background(), first, second, out)
	if err != nil {
		return "", err
	}
	data, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatal(rerr)
	}
	return string(data), nil
}

func TestCompareRealPDFsNamesTheChangedPageOnly(t *testing.T) {
	a := buildLinesPDF(t, [][]string{{"alpha one"}, {"beta two"}, {"gamma three", "keep this"}, {"delta four"}})
	b := buildLinesPDF(t, [][]string{{"alpha one"}, {"beta two"}, {"gamma THREE", "keep this"}, {"delta four"}})
	report, err := compareReport(t, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "page 3 differs") || !strings.Contains(report, "-gamma three") || !strings.Contains(report, "+gamma THREE") {
		t.Fatalf("report = %q", report)
	}
	for _, other := range []string{"page 1", "page 2", "page 4", "keep this"} {
		if strings.Contains(report, other) {
			t.Errorf("report mentions %q, which did not change: %q", other, report)
		}
	}
}

func TestCompareRealPDFsEveryPageDiffersAndIdenticalIsSkipped(t *testing.T) {
	a := buildLinesPDF(t, [][]string{{"one a"}, {"two a"}, {"three a"}})
	b := buildLinesPDF(t, [][]string{{"one b"}, {"two b"}, {"three b"}})
	report, err := compareReport(t, a, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"page 1 differs", "page 2 differs", "page 3 differs"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q: %q", want, report)
		}
	}
	if _, err := compareReport(t, a, a); !errors.Is(err, ErrIdenticalText) {
		t.Fatalf("identical documents: error = %v, want ErrIdenticalText", err)
	}
}

func TestCompareRealPDFsPageInsertedOrAppended(t *testing.T) {
	base := [][]string{{"one"}, {"two"}, {"three"}}
	a := buildLinesPDF(t, base)
	inserted := buildLinesPDF(t, [][]string{{"one"}, {"extra"}, {"two"}, {"three"}})
	report, err := compareReport(t, a, inserted)
	if err != nil || strings.TrimSpace(report) != "page 2 added" {
		t.Fatalf("inserted: %q %v, want exactly 'page 2 added'", report, err)
	}
	appended := buildLinesPDF(t, append(append([][]string{}, base...), []string{"four"}))
	report, err = compareReport(t, a, appended)
	if err != nil || strings.TrimSpace(report) != "page 4 added" {
		t.Fatalf("appended: %q %v, want exactly 'page 4 added'", report, err)
	}
	report, err = compareReport(t, appended, a)
	if err != nil || strings.TrimSpace(report) != "page 4 removed" {
		t.Fatalf("removed: %q %v, want exactly 'page 4 removed'", report, err)
	}
}

// A scan has pictures of words, not words. Comparing it with a text document
// would report every line as changed, which is wrong in a misleading way, so it
// is refused.
func TestCompareRealPDFsRefusesAScan(t *testing.T) {
	requireTool(t, "pdftocairo")
	text := buildLinesPDF(t, [][]string{{"scanned words"}})
	dir := t.TempDir()
	prefix := filepath.Join(dir, "page")
	runTool(t, "pdftocairo", "-png", "-r", "100", "-singlefile", text, prefix)
	scan := filepath.Join(dir, "scan.pdf")
	if err := NewPDFCPUBackend().ImportImages(context.Background(), []string{prefix + ".png"}, scan); err != nil {
		t.Skipf("cannot build a scan: %v", err)
	}
	for _, pair := range [][2]string{{scan, text}, {text, scan}, {scan, scan}} {
		if _, err := compareReport(t, pair[0], pair[1]); !errors.Is(err, ErrNoTextToCompare) {
			t.Errorf("error = %v, want ErrNoTextToCompare", err)
		}
	}
}

func TestDiffPagesLabelsShiftedPagesWithBothNumbers(t *testing.T) {
	report, err := DiffPages(
		[][]string{{"one"}, {"two"}, {"three old"}},
		[][]string{{"intro"}, {"one"}, {"two"}, {"three new"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "page 1 added") || !strings.Contains(report, "page 3 of the first, page 4 of the second, differs") {
		t.Fatalf("report = %q", report)
	}
}

func TestDiffPagesSurvivesAHugePageOfLines(t *testing.T) {
	long := func(prefix string) [][]string {
		lines := make([]string, 40000)
		for i := range lines {
			lines[i] = fmt.Sprintf("%s %d", prefix, i)
		}
		return [][]string{lines}
	}
	report, err := DiffPages(long("a"), long("b"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "page 1 differs") || !strings.Contains(report, "too long") {
		t.Fatalf("report = %.200q", report)
	}
}
