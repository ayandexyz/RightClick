package pdfops

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrInvalidRedaction = fmt.Errorf("invalid redaction region")

// redactPDF rasterizes only pages that have redaction regions. qpdf then copies
// all other pages directly from the original, preserving their page objects.
func redactPDF(ctx context.Context, input, output string, params RedactParams) error {
	if len(params.Areas) == 0 && len(params.Terms) == 0 {
		return fmt.Errorf("%w: at least one area or term is required", ErrInvalidRedaction)
	}
	if len(params.Areas) > 500 {
		return fmt.Errorf("%w: too many regions", ErrInvalidRedaction)
	}
	dpi := params.DPI
	if dpi == 0 {
		dpi = 150
	}
	if dpi < 150 || dpi > 300 {
		return fmt.Errorf("%w: dpi must be 150-300 (got %d)", ErrInvalidRedaction, dpi)
	}
	for _, a := range params.Areas {
		if a.Page < 1 || a.X < 0 || a.Y < 0 || a.W <= 0 || a.H <= 0 || a.X+a.W > 1 || a.Y+a.H > 1 {
			return fmt.Errorf("%w: page %d", ErrInvalidRedaction, a.Page)
		}
	}
	if len(params.Terms) > 0 {
		matches, err := findRedactionAreas(ctx, input, params.Terms)
		if err != nil {
			return err
		}
		params.Areas = append(params.Areas, matches...)
	}
	if len(params.Areas) == 0 {
		if len(params.Terms) > 0 {
			if text, err := runForOutput(ctx, nil, "pdftotext", input, "-"); err == nil && strings.TrimSpace(text) == "" {
				return fmt.Errorf("%w: this page has no text layer; draw a box instead", ErrInvalidRedaction)
			}
		}
		return fmt.Errorf("%w: no redaction terms matched", ErrInvalidRedaction)
	}
	pages, err := NewPDFCPUBackend().PageCount(ctx, input)
	if err != nil {
		return err
	}
	byPage := map[int][]RedactArea{}
	for _, a := range params.Areas {
		if a.Page > pages {
			return fmt.Errorf("%w: page %d is past the document", ErrInvalidRedaction, a.Page)
		}
		byPage[a.Page] = append(byPage[a.Page], a)
	}
	work, err := os.MkdirTemp("", "fcd-redact-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	replacements := map[int]string{}
	for page, areas := range byPage {
		if err := ctx.Err(); err != nil {
			return err
		}
		prefix := filepath.Join(work, fmt.Sprintf("page-%d", page))
		if err := runCommand(ctx, "pdftocairo", "-png", "-r", strconv.Itoa(dpi), "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), input, prefix); err != nil {
			return err
		}
		files, _ := filepath.Glob(prefix + "-*.png")
		if len(files) == 0 {
			files, _ = filepath.Glob(prefix + ".png")
		}
		if len(files) == 0 {
			return fmt.Errorf("redaction rasterization produced no page %d", page)
		}
		if err := redactPNG(ctx, files[0], areas); err != nil {
			return err
		}
		repl := filepath.Join(work, fmt.Sprintf("replacement-%d.pdf", page))
		if err := NewPDFCPUBackend().ImportImages(ctx, []string{files[0]}, repl); err != nil {
			return err
		}
		replacements[page] = repl
	}
	args := []string{"--empty", "--pages"}
	start := 1
	for page := 1; page <= pages; page++ {
		if _, ok := replacements[page]; !ok {
			continue
		}
		if start < page {
			args = append(args, input, fmt.Sprintf("%d-%d", start, page-1))
		}
		args = append(args, replacements[page], "1")
		start = page + 1
	}
	if start <= pages {
		args = append(args, input, fmt.Sprintf("%d-%d", start, pages))
	}
	args = append(args, "--", output)
	return runCommand(ctx, "qpdf", args...)
}

func findRedactionAreas(ctx context.Context, input string, terms []string) ([]RedactArea, error) {
	matches, err := NewPoppler(nil).Find(ctx, input, terms)
	if err != nil {
		return nil, err
	}
	out := make([]RedactArea, 0, len(matches))
	for _, m := range matches {
		out = append(out, RedactArea{Page: m.Page, X: m.X, Y: m.Y, W: m.W, H: m.H})
	}
	return out, nil
}

func redactPNG(ctx context.Context, path string, areas []RedactArea) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	base := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "page-"), filepath.Ext(path))
	pageText := strings.Split(base, "-")[0]
	page, _ := strconv.Atoi(pageText)
	filtered := make([]RedactArea, 0, len(areas))
	for _, area := range areas {
		if area.Page == 0 || area.Page == page {
			filtered = append(filtered, area)
		}
	}
	if len(filtered) == 0 {
		return fmt.Errorf("%w: no area applies to %s", ErrInvalidRedaction, filepath.Base(path))
	}
	areas = filtered
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	img, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		return err
	}
	b := img.Bounds()
	painted := image.NewRGBA(b)
	draw.Draw(painted, b, img, b.Min, draw.Src)
	for _, a := range areas {
		x0 := b.Min.X + int(math.Floor(a.X*float64(b.Dx())))
		y0 := b.Min.Y + int(math.Floor(a.Y*float64(b.Dy())))
		x1 := b.Min.X + int(math.Ceil((a.X+a.W)*float64(b.Dx())))
		y1 := b.Min.Y + int(math.Ceil((a.Y+a.H)*float64(b.Dy())))
		draw.Draw(painted, image.Rect(x0, y0, x1, y1), image.NewUniform(color.Black), image.Point{}, draw.Src)
	}
	f, err = os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	err = png.Encode(f, painted)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
