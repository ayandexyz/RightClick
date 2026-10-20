package pdfops

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Poppler shells out to the Poppler utilities for the two operations that read
// a PDF rather than rewrite it: pulling out its text, and pulling out the
// images embedded in its pages.
type Poppler struct {
	runner Runner
}

// NewPoppler wires the engine. Tests substitute a recording runner.
func NewPoppler(runner Runner) *Poppler {
	if runner == nil {
		runner = defaultRunTool
	}
	return &Poppler{runner: runner}
}

// ID names the engine in status output and operation entries.
func (p *Poppler) ID() string { return EnginePoppler }

// Available checks that both utilities the two operations need are present. One
// is not enough: reporting the engine available when half its operations would
// fail is worse than disabling both.
func (p *Poppler) Available() error {
	for _, binary := range []string{"pdftotext", "pdfimages"} {
		if err := availableBinary(binary); err != nil {
			return err
		}
	}
	return nil
}

// Text extracts the document's text into a plain-text file. -layout keeps the
// original column positions, which matters for tabular documents that would
// otherwise come out as a single run-on line.
func (p *Poppler) Text(ctx context.Context, input, output string) error {
	if err := p.runner(ctx, "pdftotext", []string{"-layout", input, output}); err != nil {
		return fmt.Errorf("pdftotext: %w", err)
	}
	return nil
}

// Images extracts every embedded image as a PNG under dir, which pdfimages names
// img-000.png, img-001.png, and so on. It returns the files written in numeric
// order so the job layer can report them without guessing at names.
//
// -png is chosen over -all so every output has one predictable extension; -all
// would emit JPEG or JPEG2000 files with different names depending on what the
// source image happened to use.
func (p *Poppler) Images(ctx context.Context, input, dir string) ([]string, error) {
	root := filepath.Join(dir, "img")
	if err := p.runner(ctx, "pdfimages", []string{"-png", input, root}); err != nil {
		return nil, fmt.Errorf("pdfimages: %w", err)
	}
	return imageOutputs(root)
}

// imageOutputs lists the files pdfimages wrote for a given root, sorted by the
// numeric suffix so page order is preserved. It globs rather than trusting a
// naming pattern alone, because the number of digits pdfimages uses varies with
// the image count.
func imageOutputs(root string) ([]string, error) {
	dir, base := filepath.Split(root)
	matches, err := filepath.Glob(filepath.Join(dir, base+"-*.png"))
	if err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool {
		return imageOrdinal(matches[i], base) < imageOrdinal(matches[j], base)
	})
	return matches, nil
}

// imageOrdinal extracts the number pdfimages appended to a file name.
func imageOrdinal(path, base string) int {
	name := strings.TrimPrefix(filepath.Base(path), base)
	name = strings.TrimPrefix(name, "-")
	name = strings.TrimSuffix(name, filepath.Ext(name))
	n := 0
	for _, r := range name {
		if r < '0' || r > '9' {
			continue
		}
		n = n*10 + int(r-'0')
	}
	return n
}
