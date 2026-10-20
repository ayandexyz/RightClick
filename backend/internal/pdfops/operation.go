package pdfops

import (
	"context"
	"fmt"
)

// Params carries an operation's caller-supplied parameters. The zero value
// means "not provided"; whether a given operation requires one is declared by
// the Operation itself.
type Params struct {
	Pages string // page spec, "" means not provided
	Angle int    // 0 means not provided
}

// Backend performs the PDF work. Only the structural operations ship in phase
// 1, so this is deliberately small: reverse, delete and extract all reduce to
// collecting pages in a given order.
type Backend interface {
	ID() string
	Available() error
	PageCount(ctx context.Context, path string) (int, error)
	Merge(ctx context.Context, inputs []string, output string) error
	// Collect writes the given pages, in the order given, to output. Pages are
	// 1-based and duplicates are allowed, which is what reordering needs.
	Collect(ctx context.Context, input, output string, pages []int) error
	Rotate(ctx context.Context, input, output string, pages []int, angle int) error
}

// Operation describes one PDF tool: what it needs, what it produces, and how
// to run it. Adding an operation is a one-file change in catalogue.go.
type Operation struct {
	ID, Label, Group string
	Mode             string // "instant" or "studio"
	MinInputs        int
	MaxInputs        int // 0 means unlimited
	NeedsPages       bool
	NeedsAngle       bool
	Suffix           string // appended to the source name to form the output name; unused by merge
	Merges           bool   // all inputs produce one output
}

// Catalogue returns every known operation in display order.
func Catalogue() []Operation {
	return append([]Operation(nil), catalogue...)
}

// Lookup finds an operation by ID.
func Lookup(id string) (Operation, bool) {
	for _, op := range catalogue {
		if op.ID == id {
			return op, true
		}
	}
	return Operation{}, false
}

// Validate checks the parameters and input count for one operation, without
// opening any file: the job layer calls it before queuing so a bad request
// fails synchronously.
func (o Operation) Validate(files []string, p Params) error {
	if o.MinInputs == 0 && o.MaxInputs == 0 && !o.Merges && !o.NeedsPages && !o.NeedsAngle && len(files) == 0 {
		return fmt.Errorf("%s needs at least one file", o.ID)
	}
	if o.Merges {
		if len(files) < 2 {
			return fmt.Errorf("%s needs at least 2 files", o.ID)
		}
	} else if len(files) < o.MinInputs {
		return fmt.Errorf("%s needs at least %d file", o.ID, o.MinInputs)
	}
	if o.MaxInputs > 0 && len(files) > o.MaxInputs {
		return fmt.Errorf("%s accepts at most %d files", o.ID, o.MaxInputs)
	}
	if o.NeedsPages && p.Pages == "" {
		return fmt.Errorf("%s needs a page selection", o.ID)
	}
	if o.NeedsAngle {
		switch p.Angle {
		case 90, 180, 270:
		default:
			return fmt.Errorf("%s needs an angle of 90, 180 or 270", o.ID)
		}
	}
	if !o.NeedsPages && p.Pages != "" {
		return fmt.Errorf("%s does not take a page selection", o.ID)
	}
	if !o.NeedsAngle && p.Angle != 0 {
		return fmt.Errorf("%s does not take an angle", o.ID)
	}
	return nil
}

// Run resolves the page spec against the input's real page count and maps the
// operation onto backend calls. Merge combines all inputs into one output;
// every other operation works on a single input, which the job layer loops
// over itself.
func (o Operation) Run(ctx context.Context, b Backend, inputs []string, output string, p Params) error {
	if o.Merges {
		return b.Merge(ctx, inputs, output)
	}
	if len(inputs) != 1 {
		return fmt.Errorf("%s works on one file at a time", o.ID)
	}
	input := inputs[0]
	if !o.NeedsPages && p.Pages == "" {
		if o.ID == "pdf-reverse" {
			return o.reverse(ctx, b, input, output)
		}
		if o.ID == "pdf-rotate" {
			return b.Rotate(ctx, input, output, nil, p.Angle)
		}
		return fmt.Errorf("%s has no implementation", o.ID)
	}
	pageCount, err := b.PageCount(ctx, input)
	if err != nil {
		return err
	}
	switch o.ID {
	case "pdf-extract":
		pages, err := ParsePages(p.Pages, pageCount)
		if err != nil {
			return err
		}
		return b.Collect(ctx, input, output, pages)
	case "pdf-delete":
		pages, err := ParsePages(p.Pages, pageCount)
		if err != nil {
			return err
		}
		remaining := Complement(pages, pageCount)
		if len(remaining) == 0 {
			return fmt.Errorf("%s would remove every page of the document", o.ID)
		}
		return b.Collect(ctx, input, output, remaining)
	case "pdf-rotate":
		var pages []int
		if p.Pages != "" {
			pages, err = ParsePages(p.Pages, pageCount)
			if err != nil {
				return err
			}
		}
		return b.Rotate(ctx, input, output, pages, p.Angle)
	case "pdf-reverse":
		return o.reverse(ctx, b, input, output)
	default:
		return fmt.Errorf("%s has no implementation", o.ID)
	}
}

// reverse collects the pages back to front.
func (o Operation) reverse(ctx context.Context, b Backend, input, output string) error {
	pageCount, err := b.PageCount(ctx, input)
	if err != nil {
		return err
	}
	pages := make([]int, pageCount)
	for i := range pages {
		pages[i] = pageCount - i
	}
	return b.Collect(ctx, input, output, pages)
}
