package pdfops

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Poppler shells out to Poppler utilities for PDF text and image operations.
type Poppler struct{ runner Runner }

func NewPoppler(runner Runner) *Poppler {
	if runner == nil {
		runner = defaultRunTool
	}
	return &Poppler{runner: runner}
}
func (p *Poppler) ID() string { return EnginePoppler }
func (p *Poppler) Available() error {
	for _, binary := range []string{"pdftotext", "pdfimages"} {
		if err := availableBinary(binary); err != nil {
			return err
		}
	}
	return nil
}
func (p *Poppler) Text(ctx context.Context, input, output string) error {
	if err := p.runner(ctx, "pdftotext", []string{"-layout", input, output}); err != nil {
		return fmt.Errorf("pdftotext: %w", err)
	}
	return nil
}

// Find returns visual-page fractional boxes for terms found by pdftotext.
func (p *Poppler) Find(ctx context.Context, input string, terms []string) ([]FindMatch, error) {
	return p.FindPassword(ctx, input, terms, "")
}

func (p *Poppler) FindPassword(ctx context.Context, input string, terms []string, password string) ([]FindMatch, error) {
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
	var matches []FindMatch
	for pageNo, page := range doc.Pages {
		pw, ph := page.Width, page.Height
		if pw <= 0 || ph <= 0 {
			continue
		}
		for _, word := range page.Words {
			for _, term := range terms {
				if term != "" && strings.Contains(strings.ToLower(word.Text), strings.ToLower(term)) {
					matches = append(matches, FindMatch{Page: pageNo + 1, X: word.XMin / pw, Y: word.YMin / ph, W: (word.XMax - word.XMin) / pw, H: (word.YMax - word.YMin) / ph, Term: term})
				}
			}
		}
	}
	return matches, nil
}

type bboxDocument struct {
	Pages []bboxPage `xml:"page"`
}
type bboxPage struct {
	Width  float64    `xml:"width,attr"`
	Height float64    `xml:"height,attr"`
	Words  []bboxWord `xml:"word"`
}
type bboxWord struct {
	Text string  `xml:",chardata"`
	XMin float64 `xml:"xMin,attr"`
	YMin float64 `xml:"yMin,attr"`
	XMax float64 `xml:"xMax,attr"`
	YMax float64 `xml:"yMax,attr"`
}

func (p *Poppler) Images(ctx context.Context, input, dir string) ([]string, error) {
	root := filepath.Join(dir, "img")
	if err := p.runner(ctx, "pdfimages", []string{"-png", input, root}); err != nil {
		return nil, fmt.Errorf("pdfimages: %w", err)
	}
	return imageOutputs(root)
}
func imageOutputs(root string) ([]string, error) {
	dir, base := filepath.Split(root)
	matches, err := filepath.Glob(filepath.Join(dir, base+"-*.png"))
	if err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool { return imageOrdinal(matches[i], base) < imageOrdinal(matches[j], base) })
	return matches, nil
}
func imageOrdinal(path, base string) int {
	name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), base+"-"), filepath.Ext(path))
	n := 0
	for _, r := range name {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		}
	}
	return n
}
