package pdfops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrCompareTooLarge = errors.New("PDF comparison exceeds the comparison limit")
	ErrNoTextToCompare = errors.New("no text to compare")
	ErrIdenticalText   = errors.New("the two documents have identical text")
)

const (
	maxComparePages = 1000
	maxCompareLines = 100000
)

// DiffPages compares page text after normalising whitespace. Page alignment is
// done with an LCS of complete pages, so an inserted page does not make every
// subsequent page appear changed.
func DiffPages(a, b [][]string) (string, error) {
	if len(a) > maxComparePages || len(b) > maxComparePages {
		return "", ErrCompareTooLarge
	}
	lines := 0
	for _, pages := range [][][]string{a, b} {
		for _, page := range pages {
			lines += len(page)
		}
	}
	if lines > maxCompareLines {
		return "", ErrCompareTooLarge
	}
	na, nb := normalPages(a), normalPages(b)
	pairs := lcsPagePairs(na, nb)
	var out []string
	ai, bi := 0, 0
	for _, pair := range append(pairs, [2]int{len(na), len(nb)}) {
		for ai < pair[0] || bi < pair[1] {
			switch {
			case ai == pair[0]:
				out = append(out, fmt.Sprintf("page %d added", bi+1))
				bi++
			case bi == pair[1]:
				out = append(out, fmt.Sprintf("page %d removed", ai+1))
				ai++
			default:
				if ai == bi {
					out = append(out, fmt.Sprintf("page %d differs", ai+1))
				} else {
					out = append(out, fmt.Sprintf("page %d of the first, page %d of the second, differs", ai+1, bi+1))
				}
				out = append(out, diffLines(a[ai], b[bi])...)
				ai++
				bi++
			}
		}
		if pair[0] < len(na) {
			ai, bi = pair[0]+1, pair[1]+1
		}
	}
	if len(out) == 0 {
		return "", ErrIdenticalText
	}
	return strings.Join(out, "\n") + "\n", nil
}

func hasText(pages [][]string) bool {
	for _, page := range pages {
		if len(normalLines(page)) > 0 {
			return true
		}
	}
	return false
}
func normalPages(pages [][]string) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = strings.Join(normalLines(p), "\n")
	}
	return out
}
func normalLines(lines []string) []string {
	out := []string{}
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
func lcsPagePairs(a, b []string) [][2]int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out [][2]int
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] == b[j] {
			out = append(out, [2]int{i, j})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return out
}

// maxDiffCells bounds the table a page's line diff builds. Two pages of
// 2,000 lines each fit; a "page" of tens of thousands of lines, as a text dump
// can produce, would take gigabytes, so it is reported without a line diff.
const maxDiffCells = 4_000_000

func diffLines(a, b []string) []string {
	a, b = normalLines(a), normalLines(b)
	if len(a)*len(b) > maxDiffCells {
		return []string{fmt.Sprintf("(too long to compare line by line: %d lines against %d)", len(a), len(b))}
	}
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	out := []string{}
	for i, j := 0, 0; i < len(a) || j < len(b); {
		if i < len(a) && j < len(b) && a[i] == b[j] {
			i++
			j++
			continue
		}
		if i < len(a) && (j == len(b) || dp[i+1][j] >= dp[i][j+1]) {
			out = append(out, "-"+a[i])
			i++
			continue
		}
		out = append(out, "+"+b[j])
		j++
	}
	return out
}

func (p *Poppler) Compare(ctx context.Context, first, second, output string) error {
	dir, err := os.MkdirTemp("", "fcd-compare-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	a, err := p.extractPages(ctx, first, filepath.Join(dir, "a.txt"))
	if err != nil {
		return err
	}
	b, err := p.extractPages(ctx, second, filepath.Join(dir, "b.txt"))
	if err != nil {
		return err
	}
	if !hasText(a) || !hasText(b) {
		return ErrNoTextToCompare
	}
	report, err := DiffPages(a, b)
	if err != nil {
		return err
	}
	return os.WriteFile(output, []byte(report), 0o600)
}
func (p *Poppler) extractPages(ctx context.Context, input, output string) ([][]string, error) {
	if err := p.runner(ctx, "pdftotext", []string{"-layout", input, output}); err != nil {
		return nil, fmt.Errorf("pdftotext: %w", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return nil, err
	}
	raw := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\f")
	// pdftotext ends every page, the last included, with a form feed, which
	// leaves an empty piece after it that is not a page.
	if len(raw) > 1 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	pages := make([][]string, len(raw))
	for i, s := range raw {
		pages[i] = strings.Split(strings.TrimSpace(s), "\n")
	}
	if len(pages) > maxComparePages {
		return nil, ErrCompareTooLarge
	}
	return pages, nil
}

// CompareOutputName returns the extensionless report name for two inputs.
func CompareOutputName(_ Operation, first, second string) string {
	return strings.TrimSuffix(filepath.Base(first), filepath.Ext(first)) + "-compare-" + strings.TrimSuffix(filepath.Base(second), filepath.Ext(second))
}
