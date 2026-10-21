package pdfops

import (
	"context"
	"errors"
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
