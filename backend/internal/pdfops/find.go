package pdfops

import (
	"context"
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Find returns boxes, as fractions of the visual page, for every place a term
// appears. See FindPassword.
func (p *Poppler) Find(ctx context.Context, input string, terms []string) ([]FindMatch, error) {
	return p.FindPassword(ctx, input, terms, "")
}

// FindPassword searches the words pdftotext reports for each term, ignoring
// case. A term of several words matches a run of consecutive words, across a
// line break too; the result then holds one box per line it touches. A term
// matches inside a word as well ("number" finds "numbers"), and the whole word
// is boxed: for redaction it is safer to cover too much than too little. A word
// hyphenated across two lines is not rejoined.
//
// Boxes are in the page as the reader sees it, including any /Rotate, which is
// what pdftocairo renders and what redaction paints on.
func (p *Poppler) FindPassword(ctx context.Context, input string, terms []string, password string) ([]FindMatch, error) {
	if len(terms) == 0 {
		return nil, fmt.Errorf("find requires at least one term")
	}
	if len(terms) > 50 {
		return nil, fmt.Errorf("find allows at most 50 terms")
	}
	for _, term := range terms {
		if strings.TrimSpace(term) == "" {
			return nil, fmt.Errorf("find term must not be empty")
		}
		if len([]rune(term)) > 100 {
			return nil, fmt.Errorf("find term must be at most 100 characters")
		}
	}
	dir, err := os.MkdirTemp("", "fcd-find-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "text.xml")
	args := []string{"-bbox-layout"}
	if password != "" {
		args = append(args, "-upw", password)
	}
	args = append(args, input, out)
	if err := p.runner(ctx, "pdftotext", args); err != nil {
		return nil, fmt.Errorf("pdftotext: %w", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	var doc bboxDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse pdftotext boxes: %w", err)
	}
	rotated := rotatedPages(ctx, input, password, len(doc.Pages))

	var matches []FindMatch
	for index, page := range doc.Pages {
		pw, ph := page.Width, page.Height
		// pdftotext reports the unrotated size but coordinates on the page as
		// the reader sees it, so a sideways page has its sides swapped.
		if rotated[index] {
			pw, ph = ph, pw
		}
		if pw <= 0 || ph <= 0 {
			continue
		}
		words := make([]string, len(page.Words))
		for i, w := range page.Words {
			words[i] = strings.ToLower(w.Text)
		}
		for _, term := range terms {
			tokens := strings.Fields(strings.ToLower(term))
			if len(tokens) == 0 {
				continue
			}
			for _, run := range findRuns(words, tokens) {
				for _, box := range lineBoxes(page.Words[run[0]:run[1]], pw, ph, rotated[index]) {
					box.Page, box.Term = index+1, term
					matches = append(matches, box)
				}
			}
		}
	}
	return matches, nil
}

// findRuns returns [start, end) word ranges where tokens match consecutive
// words: the first token may end its word, the last may start its word, and any
// token between must be the whole word. A single token may match anywhere.
func findRuns(words, tokens []string) [][2]int {
	var runs [][2]int
	n := len(tokens)
	for i := 0; i+n <= len(words); i++ {
		ok := true
		for k, tok := range tokens {
			w := words[i+k]
			switch {
			case n == 1:
				ok = strings.Contains(w, tok)
			case k == 0:
				ok = strings.HasSuffix(w, tok)
			case k == n-1:
				ok = strings.HasPrefix(w, tok)
			default:
				ok = w == tok
			}
			if !ok {
				break
			}
		}
		if ok {
			runs = append(runs, [2]int{i, i + n})
		}
	}
	return runs
}

// lineBoxes merges words into one box per line. On a page turned a quarter the
// text runs top to bottom, so words share a line when they overlap sideways.
func lineBoxes(words []bboxWord, pw, ph float64, vertical bool) []FindMatch {
	var boxes []FindMatch
	var cur *bboxWord
	flush := func() {
		if cur != nil {
			boxes = append(boxes, FindMatch{X: cur.XMin / pw, Y: cur.YMin / ph, W: (cur.XMax - cur.XMin) / pw, H: (cur.YMax - cur.YMin) / ph})
		}
	}
	for _, w := range words {
		w := w
		if cur != nil && sameLine(*cur, w, vertical) {
			cur.XMin, cur.XMax = math.Min(cur.XMin, w.XMin), math.Max(cur.XMax, w.XMax)
			cur.YMin, cur.YMax = math.Min(cur.YMin, w.YMin), math.Max(cur.YMax, w.YMax)
			continue
		}
		flush()
		cur = &w
	}
	flush()
	return boxes
}

func sameLine(a, b bboxWord, vertical bool) bool {
	lo, hi, size := math.Max(a.YMin, b.YMin), math.Min(a.YMax, b.YMax), math.Min(a.YMax-a.YMin, b.YMax-b.YMin)
	if vertical {
		lo, hi, size = math.Max(a.XMin, b.XMin), math.Min(a.XMax, b.XMax), math.Min(a.XMax-a.XMin, b.XMax-b.XMin)
	}
	return hi-lo > size/2
}

// rotatedPages reports, per page, whether it is turned a quarter. A missing
// pdfinfo reports none rotated.
func rotatedPages(ctx context.Context, input, password string, count int) []bool {
	rotated := make([]bool, count)
	if count == 0 {
		return rotated
	}
	args := []string{"-f", "1", "-l", strconv.Itoa(count)}
	if password != "" {
		args = append(args, "-upw", password)
	}
	out, err := runForOutput(ctx, nil, "pdfinfo", append(args, input)...)
	if err != nil {
		return rotated
	}
	sizes, _, err := ParsePDFInfo(out)
	if err != nil {
		return rotated
	}
	for i, s := range sizes {
		if i < count {
			rotated[i] = s.Rotation == 90 || s.Rotation == 270
		}
	}
	return rotated
}
