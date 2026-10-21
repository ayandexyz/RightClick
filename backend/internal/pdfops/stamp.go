package pdfops

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// StampParams is the typed, allowlisted input for text and image stamps.
type StampParams struct {
	Kind     string
	Text     string
	Image    string
	Position string
	Opacity  int
	Rotation int
	Size     int
	Color    string
	Pages    string
	OnTop    bool

	// The preset kinds (page-numbers, header-footer, bates) are built from
	// these instead of Text.
	Start  int    // first number for page-numbers and bates; 0 means 1
	Format string // page-numbers: "number" (default) or "of-total"
	Prefix string // bates: text before the counter
	Left   string // header-footer zones
	Centre string
	Right  string
	Edge   string // header-footer: "top" (default) or "bottom"
}

var stampColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var stampPositions = map[string]bool{"tl": true, "tc": true, "tr": true, "ml": true, "mc": true, "mr": true, "bl": true, "bc": true, "br": true}

// positionMargin moves a stamp inward from the page edge it is anchored to, so
// numbers and headers do not sit against the trim. A stamp anchored to the
// middle of an axis is not moved on that axis.
func positionMargin(position string) (dx, dy int) {
	if len(position) != 2 {
		return 0, 0
	}
	switch position[0] {
	case 't':
		dy = -stampMargin
	case 'b':
		dy = stampMargin
	}
	switch position[1] {
	case 'l':
		dx = stampMargin
	case 'r':
		dx = -stampMargin
	}
	return dx, dy
}

func BuildStampDescription(p StampParams) (string, error) {
	if p.Position == "" {
		p.Position = "bc"
	}
	if p.Opacity == 0 {
		p.Opacity = 100
	}
	if p.Size == 0 {
		p.Size = 12
	}
	if p.Color == "" {
		p.Color = "#000000"
	}
	if !stampPositions[p.Position] {
		return "", fmt.Errorf("invalid stamp position: %s", p.Position)
	}
	if p.Opacity < 0 || p.Opacity > 100 {
		return "", fmt.Errorf("stamp opacity must be between 0 and 100")
	}
	if p.Size < 4 || p.Size > 200 {
		return "", fmt.Errorf("stamp size must be between 4 and 200")
	}
	if !stampColor.MatchString(p.Color) {
		return "", fmt.Errorf("stamp color must be #rrggbb")
	}
	if strings.ContainsAny(p.Text, "\r\n") {
		return "", fmt.Errorf("stamp text may not contain a newline")
	}
	if len([]rune(p.Text)) > 200 {
		return "", fmt.Errorf("stamp text must be at most 200 characters")
	}
	if p.Rotation < -180 || p.Rotation > 180 {
		return "", fmt.Errorf("stamp rotation must be between -180 and 180")
	}
	opacity := strconv.FormatFloat(float64(p.Opacity)/100, 'f', -1, 64)
	if strings.HasPrefix(opacity, "0.") {
		opacity = strings.TrimPrefix(opacity, "0")
	}
	color := strings.ToLower(p.Color)
	parts := []string{"pos:" + p.Position, "op:" + opacity, "points:" + strconv.Itoa(p.Size)}
	if dx, dy := positionMargin(p.Position); dx != 0 || dy != 0 {
		parts = append(parts, fmt.Sprintf("offset:%d %d", dx, dy))
	}
	// pdfcpu stamps text diagonally unless told otherwise, so a rotation of 0
	// has to be spelled out for a number or header to sit level.
	parts = append(parts, "rot:"+strconv.Itoa(p.Rotation))
	if p.Kind != StampImage {
		// Without an absolute scale pdfcpu stretches text to half the page
		// width and ignores the font size.
		parts = append(parts, "scalefactor:1 abs")
	}
	parts = append(parts, "fillcol:"+color)
	return strings.Join(parts, ", "), nil
}

// Stamp kinds. A stamp is one operation family with several presets, so the
// client never has to build a placeholder string.
const (
	StampText         = "text"
	StampImage        = "image"
	StampPageNumbers  = "page-numbers"
	StampHeaderFooter = "header-footer"
	StampBates        = "bates"
)

const (
	maxStampImageBytes = 20 << 20
	maxStampStart      = 999_999_999
	batesMinWidth      = 6
	stampMargin        = 36
)

// validateStampText rejects text the stamp font cannot draw. pdfcpu accepts any
// UTF-8 and silently drops glyphs the core font lacks, which would write a
// document with missing characters, so the first offending character is named
// instead. placeholders allows %p, %P and %t; every other % is refused because
// pdfcpu would otherwise read the character after it as a placeholder.
func validateStampText(text string, placeholders bool) error {
	if strings.ContainsAny(text, "\r\n") {
		return fmt.Errorf("stamp text may not contain a newline")
	}
	if len([]rune(text)) > 200 {
		return fmt.Errorf("stamp text must be at most 200 characters")
	}
	runes := []rune(text)
	for i, r := range runes {
		if (r < 0x20) || (r > 0x7e && r < 0xa0) || r > 0xff {
			return fmt.Errorf("the stamp font cannot draw %q; use Latin letters, digits and punctuation", string(r))
		}
		if r == '%' {
			if !placeholders || i+1 >= len(runes) || !strings.ContainsRune("pPt", runes[i+1]) {
				return fmt.Errorf("stamp text may not contain %% except as %%p, %%P or %%t")
			}
		}
	}
	return nil
}

// validateStampImage checks an image path before pdfcpu opens it: an absolute
// regular file, small enough, and PNG or JPEG by its header rather than its name.
func validateStampImage(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("stamp image must be an absolute path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stamp image: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("stamp image is not a regular file")
	}
	if info.Size() > maxStampImageBytes {
		return fmt.Errorf("stamp image is larger than %d MB", maxStampImageBytes>>20)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("stamp image: %w", err)
	}
	defer f.Close()
	header := make([]byte, 8)
	n, _ := io.ReadFull(f, header)
	header = header[:n]
	if !bytes.HasPrefix(header, []byte("\x89PNG\r\n\x1a\n")) && !bytes.HasPrefix(header, []byte("\xff\xd8\xff")) {
		return fmt.Errorf("stamp image must be a PNG or JPEG file")
	}
	return nil
}

// stampPass is one pdfcpu call: a text or an image, placed with one style on a
// set of pages. The preset kinds are several passes.
type stampPass struct {
	text  string
	image bool
	style StampParams
	pages []string
}

func digits(n int) int { return len(strconv.Itoa(n)) }

func pageStrings(pages []int) []string {
	out := make([]string, len(pages))
	for i, page := range pages {
		out[i] = strconv.Itoa(page)
	}
	return out
}

// pageOffset renders the %p offset for a first number: %p alone is page number
// 1 on page 1, and %p<N> adds N to the page number.
func pageOffset(start int) string {
	if start <= 1 {
		return "%p"
	}
	return "%p" + strconv.Itoa(start-1)
}

// stampPasses expands a request into the pdfcpu calls that carry it out.
// selected is the validated page list, or nil for every page of a count-page
// document.
func stampPasses(p StampParams, count int, selected []int) ([]stampPass, error) {
	if selected == nil {
		selected = make([]int, count)
		for i := range selected {
			selected[i] = i + 1
		}
	}
	if p.Pages == "" {
		// pdfcpu treats an empty selection as every page, which avoids a very
		// long argument list on large documents.
		selected = nil
	}
	pages := pageStrings(selected)
	if p.Start < 0 || p.Start > maxStampStart {
		return nil, fmt.Errorf("the first number must be between 1 and %d", maxStampStart)
	}
	switch p.Kind {
	case StampText, "":
		if p.Text == "" {
			return nil, fmt.Errorf("stamp text is required")
		}
		if err := validateStampText(p.Text, true); err != nil {
			return nil, err
		}
		return []stampPass{{text: p.Text, style: p, pages: pages}}, nil
	case StampImage:
		if err := validateStampImage(p.Image); err != nil {
			return nil, err
		}
		return []stampPass{{image: true, style: p, pages: pages}}, nil
	case StampPageNumbers:
		text := pageOffset(p.Start)
		switch p.Format {
		case "", "number":
		case "of-total":
			text += " of %P"
		default:
			return nil, fmt.Errorf("page number format must be number or of-total")
		}
		return []stampPass{{text: text, style: p, pages: pages}}, nil
	case StampHeaderFooter:
		edge := p.Edge
		if edge == "" {
			edge = "top"
		}
		if edge != "top" && edge != "bottom" {
			return nil, fmt.Errorf("header/footer edge must be top or bottom")
		}
		row := "t"
		if edge == "bottom" {
			row = "b"
		}
		var out []stampPass
		for _, zone := range []struct{ col, text string }{{"l", p.Left}, {"c", p.Centre}, {"r", p.Right}} {
			if zone.text == "" {
				continue
			}
			if err := validateStampText(zone.text, true); err != nil {
				return nil, err
			}
			style := p
			style.Position = row + zone.col
			out = append(out, stampPass{text: zone.text, style: style, pages: pages})
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("a header or footer needs at least one of left, centre or right")
		}
		return out, nil
	case StampBates:
		if err := validateStampText(p.Prefix, false); err != nil {
			return nil, err
		}
		if p.Start == 0 {
			p.Start = 1
		}
		if p.Start+count-1 > maxStampStart {
			return nil, fmt.Errorf("the numbers would pass %d", maxStampStart)
		}
		// pdfcpu cannot zero-pad a counter, so pages are grouped by how many
		// digits their number has and each group carries its own run of zeros.
		width := batesMinWidth
		if w := digits(p.Start + count - 1); w > width {
			width = w
		}
		if selected == nil {
			selected = make([]int, count)
			for i := range selected {
				selected[i] = i + 1
			}
		}
		groups := map[int][]int{}
		var order []int
		for _, page := range selected {
			d := digits(p.Start - 1 + page)
			if _, ok := groups[d]; !ok {
				order = append(order, d)
			}
			groups[d] = append(groups[d], page)
		}
		var out []stampPass
		for _, d := range order {
			text := p.Prefix + strings.Repeat("0", width-d) + pageOffset(p.Start)
			out = append(out, stampPass{text: text, style: p, pages: pageStrings(groups[d])})
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown stamp kind: %s", p.Kind)
}

// Stamp writes input to output with the requested stamp. Presets that need
// several pdfcpu calls chain them through private temporary files, so a failure
// part-way never leaves a half-stamped document at output.
func (b *PDFCPU) Stamp(ctx context.Context, input, output string, p StampParams) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	count, err := b.PageCount(ctx, input)
	if err != nil {
		return err
	}
	var selected []int
	if p.Pages != "" {
		if selected, err = ParsePages(p.Pages, count); err != nil {
			return err
		}
	}
	passes, err := stampPasses(p, count, selected)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "fcd-stamp-")
	if err != nil {
		return fmt.Errorf("make a work directory: %w", err)
	}
	defer os.RemoveAll(work)

	// pdfcpu draws nothing on a page that has /Rotate set, so a rotated
	// document is made upright first and a stamp lands where the user chose on
	// the page as they see it.
	current, err := uprightCopy(ctx, input, work, pdfPageSizes(ctx, input, count))
	if err != nil {
		return fmt.Errorf("stamping a rotated page needs qpdf: %w", err)
	}
	for i, pass := range passes {
		if err := ctx.Err(); err != nil {
			return err
		}
		desc, err := BuildStampDescription(pass.style)
		if err != nil {
			return err
		}
		next := output
		if i < len(passes)-1 {
			next = filepath.Join(work, fmt.Sprintf("pass%d.pdf", i+1))
		}
		from := current
		err = runPDFCPU(ctx, func() error {
			conf := model.NewDefaultConfiguration()
			if pass.image {
				return api.AddImageWatermarksFile(from, next, pass.pages, pass.style.OnTop, pass.style.Image, desc, conf)
			}
			return api.AddTextWatermarksFile(from, next, pass.pages, pass.style.OnTop, pass.text, desc, conf)
		})
		if err != nil {
			return err
		}
		current = next
	}
	return nil
}
