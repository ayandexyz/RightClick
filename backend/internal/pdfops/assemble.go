package pdfops

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Assemble rewrites a document from an ordered list of page items: a reordering,
// a deletion, a duplication, a rotation, or a blank page inserted anywhere, all
// described at once. It is the one operation a page editor needs, because every
// one of those actions is the same request with a different list.
//
// The items are assembled in three passes, each of which rewrites the whole
// document into a new file:
//
//  1. Collect the real pages, in order, keeping duplicates. A duplicated page is
//     simply its source number appearing twice.
//  2. Insert the blank pages at their final positions. Working from the last
//     position backwards keeps the indexes of the pages still to come valid.
//  3. Rotate, once per distinct angle, the final page positions that asked for
//     it.
//
// Each pass reads and writes a different file, so a failure part-way through
// never leaves a half-edited document in place of the user's original.
func (b *PDFCPU) Assemble(ctx context.Context, input, output string, items []PageItem) error {
	if len(items) == 0 {
		return fmt.Errorf("an edit needs at least one page")
	}
	if err := validateItems(items); err != nil {
		return err
	}
	real := 0
	for _, item := range items {
		if item.Src > 0 {
			real++
		}
	}
	if real == 0 {
		return fmt.Errorf("an edit of only blank pages is not an edit of this document")
	}

	// The working directory holds the intermediate documents and is removed on
	// every path, including a cancelled one: these files contain the user's
	// pages at a predictable path.
	work, err := os.MkdirTemp("", "fcd-edit-")
	if err != nil {
		return fmt.Errorf("make a work directory: %w", err)
	}
	defer os.RemoveAll(work)

	stage := output
	paths := make([]string, 0, 3)
	step := func(extend func(string) error) error {
		next := filepath.Join(work, fmt.Sprintf("step%d.pdf", len(paths)+1))
		paths = append(paths, next)
		if err := extend(next); err != nil {
			return err
		}
		stage = next
		return nil
	}

	// Pass 1: the real pages, in order.
	pages := make([]int, 0, real)
	for _, item := range items {
		if item.Src > 0 {
			pages = append(pages, item.Src)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := step(func(next string) error {
		return b.Collect(ctx, input, next, pages)
	}); err != nil {
		return err
	}

	// Pass 2: blank pages, from the last position backwards so the positions
	// still to be handled keep referring to the pages they did.
	//
	// A blank takes the size of the page it is inserted next to, so adding one
	// never changes the shape of the document around it. pdfcpu takes that size
	// from the page it is inserted beside when no explicit size is given.
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Src > 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// At this point the document holds the collected pages plus the blanks
		// already placed to the right of this one. Counting the real pages at or
		// before item i gives the page the blank belongs beside.
		anchor, before := anchorFor(items, i)
		// pdfcpu will not copy the neighbour's page size for us, so read it and
		// hand it over. Without this a blank lands at the document default,
		// which is A4 for most files, and visibly changes the page grid.
		dim := b.neighbourSize(ctx, stage, anchor)
		if err := step(func(next string) error {
			return b.InsertBlankPages(ctx, stage, next, []int{anchor}, before, dim)
		}); err != nil {
			return err
		}
	}

	// Pass 3: rotations, one call per distinct angle. pdfcpu's rotation is
	// relative to a page's existing rotation, which is what "clockwise delta"
	// means, so it passes through unchanged.
	rotations := map[int][]int{}
	for i, item := range items {
		if item.Rotate == 0 {
			continue
		}
		rotations[item.Rotate] = append(rotations[item.Rotate], i+1)
	}
	for _, angle := range distinctAngles(items) {
		if err := ctx.Err(); err != nil {
			return err
		}
		targets := rotations[angle]
		if err := step(func(next string) error {
			return b.Rotate(ctx, stage, next, targets, angle)
		}); err != nil {
			return err
		}
	}

	// Move the last stage into place. The intermediates go with the temp
	// directory's removal.
	if err := os.Rename(stage, output); err != nil {
		// A rename across filesystems fails, which happens when the output
		// directory is not the temp directory's filesystem. Copying is slower
		// but keeps the operation working instead of failing at the last step.
		if err := copyFile(stage, output); err != nil {
			return fmt.Errorf("write the edited document: %w", err)
		}
	}
	return nil
}

// anchorFor returns the 1-based page in the partially assembled document that a
// blank at item i belongs beside, and whether it goes before or after it.
//
// Blanks are inserted from the end backwards, so every page to the right of item
// i is already in place and the count of real pages at or before i is the page
// the blank attaches to. A blank before the first real page has no page on its
// left to follow, so it is inserted before the first one instead.
func anchorFor(items []PageItem, i int) (int, bool) {
	real := 0
	for j := 0; j <= i; j++ {
		if items[j].Src > 0 {
			real++
		}
	}
	if real == 0 {
		return 1, true
	}
	return real, false
}

// distinctAngles returns the non-zero angles in the items, in a stable order so
// a given edit always makes the same number of passes.
func distinctAngles(items []PageItem) []int {
	var angles []int
	seen := map[int]bool{}
	for _, item := range items {
		if item.Rotate == 0 || seen[item.Rotate] {
			continue
		}
		seen[item.Rotate] = true
		angles = append(angles, item.Rotate)
	}
	return angles
}

// validateItems rejects an item list the operation cannot act on.
func validateItems(items []PageItem) error {
	if len(items) > maxPages {
		return fmt.Errorf("an edit may list at most %d pages, got %d", maxPages, len(items))
	}
	for i, item := range items {
		if item.Src < 0 {
			return fmt.Errorf("item %d: source page %d is not valid", i+1, item.Src)
		}
		switch item.Rotate {
		case 0, 90, 180, 270:
		default:
			return fmt.Errorf("item %d: rotation %d must be 0, 90, 180 or 270", i+1, item.Rotate)
		}
	}
	return nil
}

// checkItemsAgainstPageCount verifies every source page exists. It runs after
// the page count is known, and its message names the page and the count because
// a page grid that raced another edit leaves the user no other way to tell.
func checkItemsAgainstPageCount(items []PageItem, pageCount int) error {
	for i, item := range items {
		if item.Src > pageCount {
			return fmt.Errorf("item %d: page %d is past the last page (%d)", i+1, item.Src, pageCount)
		}
	}
	return nil
}

// InsertBlankPages writes a copy of input to output with a blank page inserted
// at each anchor. A nil dim falls back to the pdfcpu default (A4), but the
// Assemble pipeline always supplies the neighbour's size so the new page
// matches.
//
// pdfcpu v0.12.0 does not copy the neighbouring page's MediaBox when no
// dimension is given; it falls back to the document default, which for most
// documents is A4 whatever the pages being edited are.
func (b *PDFCPU) InsertBlankPages(ctx context.Context, input, output string, after []int, before bool, dim *types.Dim) error {
	if len(after) == 0 {
		return fmt.Errorf("inserting a blank page needs a page to insert it beside")
	}
	selection := make([]string, len(after))
	for i, page := range after {
		selection[i] = strconv.Itoa(page)
	}
	pageConf := &pdfcpu.PageConfiguration{}
	if dim != nil {
		pageConf.PageDim = dim
	}
	return runPDFCPU(ctx, func() error {
		return api.InsertPagesFile(input, output, selection, before, pageConf, model.NewDefaultConfiguration())
	})
}

// neighbourSize returns the size of one page of a document, for handing to
// pdfcpu so an inserted blank matches the page it sits beside. It returns nil
// when the size cannot be read, which lets pdfcpu apply its own default rather
// than failing the edit over a cosmetic detail.
func (b *PDFCPU) neighbourSize(ctx context.Context, path string, page int) *types.Dim {
	out, err := runForOutput(ctx, nil, "pdfinfo", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), path)
	if err != nil {
		return nil
	}
	sizes, _, err := ParsePDFInfo(out)
	if err != nil {
		return nil
	}
	for _, size := range sizes {
		if size.W <= 0 || size.H <= 0 {
			continue
		}
		// A page whose /Rotate is 90 or 270 presents its MediaBox rotated, so
		// the box must be swapped to give the blank the shape the reader sees.
		if size.Rotation == 90 || size.Rotation == 270 {
			size.W, size.H = size.H, size.W
		}
		return &types.Dim{Width: size.W, Height: size.H}
	}
	return nil
}

// copyFile copies src to dst, replacing dst. It is the fallback for a rename
// that fails because the two are on different filesystems, which happens when
// the output directory is a mounted share.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	// The output keeps the process umask, so its mode is set explicitly rather
	// than left to whatever the user's umask happens to be.
	return out.Close()
}
