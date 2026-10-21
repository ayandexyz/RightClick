package pdfops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Params carries an operation's caller-supplied parameters. The zero value
// means "not provided"; whether a given operation requires one is declared by
// the Operation itself.
type Params struct {
	Pages    string // page spec, "" means not provided
	Angle    int    // 0 means not provided
	Password string // password for encrypted PDFs, "" means not provided
	// Edit is the page list for pdf-edit. It is separate from Pages because an
	// edit says what happens to every page, including blanks, rather than which
	// pages of the source to use.
	Edit             []PageItem
	Ranges           string
	Every            int
	FilePages        []string
	EncryptUser      string   // user password for pdf-encrypt
	EncryptOwner     string   // owner password for pdf-encrypt
	PermissionsOwner string   // owner password for pdf-permissions
	Permissions      []string // permission flags for pdf-permissions
	Stamp            StampParams
	Crop             CropParams
	Resize           ResizeParams
	Layout           LayoutParams
	Metadata         MetadataParams
}

// PageItem is one page of an edit: a source page in the original document, with
// an optional clockwise rotation. Src 0 means a blank page, which is how a user
// inserts one; Rotate is a delta from the page's current rotation, so 90 turns
// a page that was already sideways a further quarter turn.
type PageItem struct {
	Src    int `json:"src"`
	Rotate int `json:"rotate,omitempty"`
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
	ImportImages(ctx context.Context, inputs []string, output string) error
	// Assemble rewrites a document from an ordered list of page items, which
	// covers reordering, deleting, duplicating, rotating and inserting blanks.
	Assemble(ctx context.Context, input, output string, items []PageItem) error
	// InsertBlankPages writes input to output with a blank page inserted beside
	// each of the given pages. It is a step of Assemble rather than a separate
	// operation, exposed so the pipeline can be asserted step by step.
	InsertBlankPages(ctx context.Context, input, output string, after []int, before bool, dim *types.Dim) error
	Crop(ctx context.Context, input, output string, params CropParams) error
	Resize(ctx context.Context, input, output string, params ResizeParams) error
	NUp(ctx context.Context, input, output string, params NUpParams) error
	Booklet(ctx context.Context, input, output string, params BookletParams) error
}

// Engine IDs name the external tool that performs an operation. They appear in
// the protocol so a client can tell which operation needs which binary.
const (
	EnginePDFCPU      = "pdfcpu"
	EngineQPDF        = "qpdf"
	EngineGhostscript = "ghostscript"
	EnginePoppler     = "poppler"
	EngineTesseract   = "tesseract"
)

// InputKind is what an operation accepts.
type InputKind string

const (
	InputPDF   InputKind = "pdf"
	InputImage InputKind = "image"
)

// errEngineMissing is the reason reported for an engine that is not installed.
func errEngineMissing(id string) error {
	return fmt.Errorf("%s is not installed", id)
}

// Operation describes one PDF tool: what it needs, what it produces, and how
// to run it. Adding an operation is a one-file change in catalogue.go.
type Operation struct {
	ID, Label, Group string
	Mode             string // "instant" or "studio"
	Engine           string // which external tool runs it; empty means pdfcpu
	Inputs           InputKind
	OutputExt        string // extension of the output; empty means pdf
	Numbered         bool   // one input produces N numbered outputs
	// OutputExtension chooses the staged files consumed by numbered operations.
	// EmptyNotice is reported as a skipped notice when none were produced.
	EmptyNotice   string
	FixedAngle    int // rotate variants: used when Params.Angle is 0
	MinInputs     int
	MaxInputs     int // 0 means unlimited
	NeedsPages    bool
	OptionalPages bool // accepts a page selection but defaults to all pages
	NeedsAngle    bool
	NeedsPassword bool   // operation requires a password (e.g. pdf-decrypt)
	Suffix        string // appended to the source name to form the output name; unused by merge
	Merges        bool   // all inputs produce one output
	Preset        string // Ghostscript compress preset: "light", "balanced", "strong"
	// Unimplemented marks an operation whose engine exists but whose behaviour
	// is not built yet. It is reported as disabled with its reason rather than
	// offered, so a menu never shows something that cannot run.
	Unimplemented string
}

// InputKind reports what the operation accepts, defaulting to PDF so entries that
// predate image support need no annotation.
func (o Operation) InputKind() InputKind {
	if o.Inputs == "" {
		return InputPDF
	}
	return o.Inputs
}

// OutputExtension reports the extension of the operation's output, defaulting to
// pdf. It is separate from the OutputExt field so the field keeps its plain name.
func (o Operation) OutputExtension() string {
	if o.OutputExt == "" {
		return "pdf"
	}
	return o.OutputExt
}

// EngineID reports which external tool runs the operation, defaulting to the
// linked-in pdfcpu.
func (o Operation) EngineID() string {
	if o.Engine == "" {
		return EnginePDFCPU
	}
	return o.Engine
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
	if o.NeedsPassword && p.Password == "" {
		return fmt.Errorf("%s requires a password", o.ID)
	}
	if o.NeedsAngle && o.FixedAngle == 0 {
		switch p.Angle {
		case 90, 180, 270:
		default:
			return fmt.Errorf("%s needs an angle of 90, 180 or 270", o.ID)
		}
	}
	if o.ID == "pdf-encrypt" && p.EncryptOwner == "" {
		return fmt.Errorf("%s requires an owner password", o.ID)
	}
	if o.ID == "pdf-permissions" && p.PermissionsOwner == "" {
		return fmt.Errorf("%s requires an owner password", o.ID)
	}
	if !o.NeedsPages && !o.OptionalPages && p.Pages != "" {
		return fmt.Errorf("%s does not take a page selection", o.ID)
	}
	// A fixed-angle variant supplies its own angle, so passing one is a mistake
	// rather than something to ignore.
	if !o.NeedsAngle && p.Angle != 0 {
		return fmt.Errorf("%s does not take an angle", o.ID)
	}
	return nil
}

// Run resolves the page spec against the input's real page count and maps the
// operation onto backend calls. Merge combines all inputs into one output;
// every other operation works on a single input, which the job layer loops
// over itself.
func (o Operation) Run(ctx context.Context, tb Toolbox, inputs []string, output string, p Params) error {
	if err := tb.Available(o.EngineID()); err != nil {
		return err
	}

	// Unlock encrypted PDFs if a password is provided and inputs are PDFs
	var cleanups []func()
	defer func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}()

	if o.InputKind() == InputPDF && p.Password != "" {
		pdfcpu, ok := tb.PDFCPU.(*PDFCPU)
		if ok {
			unlocked := make([]string, len(inputs))
			for i, input := range inputs {
				u, cleanup, err := Unlock(ctx, pdfcpu, input, p.Password)
				if err != nil {
					return err
				}
				unlocked[i] = u
				cleanups = append(cleanups, cleanup)
			}
			inputs = unlocked
		}
	}

	if o.EngineID() != EnginePDFCPU {
		return o.runExternal(ctx, tb, inputs, output, p)
	}
	b := tb.PDFCPU
	if o.Merges {
		if o.InputKind() == InputImage {
			return b.ImportImages(ctx, inputs, output)
		}
		if len(p.FilePages) == 0 {
			return b.Merge(ctx, inputs, output)
		}
		if len(p.FilePages) != len(inputs) {
			return fmt.Errorf("file_pages must have one entry per input file")
		}
		work, err := os.MkdirTemp("", "fcd-merge-pages-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(work)
		selected := make([]string, len(inputs))
		for i, input := range inputs {
			if err := ctx.Err(); err != nil {
				return err
			}
			if p.FilePages[i] == "" {
				selected[i] = input
				continue
			}
			count, err := b.PageCount(ctx, input)
			if err != nil {
				return err
			}
			pages, err := ParsePages(p.FilePages[i], count)
			if err != nil {
				return fmt.Errorf("file %d (%s): %w", i+1, filepath.Base(input), err)
			}
			if len(pages) == 0 {
				return fmt.Errorf("file %d (%s) selects no pages", i+1, filepath.Base(input))
			}
			selected[i] = filepath.Join(work, fmt.Sprintf("input-%d.pdf", i+1))
			if err := b.Collect(ctx, input, selected[i], pages); err != nil {
				return err
			}
		}
		return b.Merge(ctx, selected, output)
	}
	if len(inputs) != 1 {
		return fmt.Errorf("%s works on one file at a time", o.ID)
	}
	input := inputs[0]
	// pdf-edit is driven by its edit list, not by a page spec, so it runs even
	// though no Pages were given.
	if o.ID == "pdf-edit" {
		return o.edit(ctx, b, input, output, p)
	}
	if o.ID == "pdf-split-ranges" || o.ID == "pdf-split-every" {
		return o.split(ctx, b, input, output, p)
	}
	// Crypto operations
	pdfcpu, ok := b.(*PDFCPU)
	if !ok && (o.ID == "pdf-decrypt" || o.ID == "pdf-encrypt" || o.ID == "pdf-permissions") {
		return fmt.Errorf("%s requires pdfcpu backend", o.ID)
	}
	switch o.ID {
	case "pdf-decrypt":
		return pdfcpu.Decrypt(ctx, input, output, p)
	case "pdf-encrypt":
		return pdfcpu.Encrypt(ctx, input, output, EncryptParams{
			User:        p.EncryptUser,
			Owner:       p.EncryptOwner,
			Password:    p.Password,
			Permissions: p.Permissions,
		})
	case "pdf-permissions":
		return pdfcpu.SetPermissions(ctx, input, output, PermissionsParams{
			Owner:       p.PermissionsOwner,
			Permissions: p.Permissions,
			Password:    p.Password,
		})
	case "pdf-stamp-text", "pdf-stamp-image", "pdf-page-numbers", "pdf-header-footer", "pdf-bates":
		// The operation decides the kind, so a request cannot ask for a
		// different preset than the operation it named.
		stamp := p.Stamp
		stamp.Kind = map[string]string{
			"pdf-stamp-text": StampText, "pdf-stamp-image": StampImage, "pdf-page-numbers": StampPageNumbers,
			"pdf-header-footer": StampHeaderFooter, "pdf-bates": StampBates,
		}[o.ID]
		return pdfcpu.Stamp(ctx, input, output, stamp)
	case "pdf-crop":
		return b.Crop(ctx, input, output, p.Crop)
	case "pdf-resize":
		return b.Resize(ctx, input, output, p.Resize)
	case "pdf-nup":
		return b.NUp(ctx, input, output, NUpParams{PerSheet: p.Layout.PerSheet, Orientation: p.Layout.Orientation, Margin: p.Layout.Margin, Border: p.Layout.Border})
	case "pdf-booklet":
		return b.Booklet(ctx, input, output, BookletParams{PerSheet: p.Layout.PerSheet})
	case "pdf-metadata":
		pdfcpu, ok := b.(*PDFCPU)
		if !ok {
			return fmt.Errorf("pdf-metadata requires pdfcpu backend")
		}
		return pdfcpu.SetMetadata(ctx, input, output, p.Metadata)
	}
	if !o.NeedsPages && p.Pages == "" {
		switch {
		case o.ID == "pdf-reverse":
			return o.reverse(ctx, b, input, output)
		case o.ID == "pdf-rotate" || o.FixedAngle != 0:
			return b.Rotate(ctx, input, output, nil, o.angle(p))
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
		return b.Rotate(ctx, input, output, pages, o.angle(p))
	case "pdf-edit":
		return o.edit(ctx, b, input, output, p)
	case "pdf-reverse":
		return o.reverse(ctx, b, input, output)
	default:
		return fmt.Errorf("%s has no implementation", o.ID)
	}
}

// angle resolves the angle to rotate by: the caller's, or the variant's fixed
// one when the caller supplied nothing.
func (o Operation) angle(p Params) int {
	if p.Angle != 0 {
		return p.Angle
	}
	return o.FixedAngle
}

// reverse collects the pages back to front.
// split validates every part before writing any output, then collects each
// part into the numbered staging directory consumed by the job publisher.
func (o Operation) split(ctx context.Context, b Backend, input, output string, p Params) error {
	count, err := b.PageCount(ctx, input)
	if err != nil {
		return err
	}
	var groups [][]int
	if o.ID == "pdf-split-ranges" {
		if p.Ranges == "" {
			return fmt.Errorf("%s requires ranges", o.ID)
		}
		for i, raw := range strings.Split(p.Ranges, ";") {
			if strings.TrimSpace(raw) == "" {
				return fmt.Errorf("range group %d is empty", i+1)
			}
			pages, err := ParsePages(strings.TrimSpace(raw), count)
			if err != nil {
				return fmt.Errorf("range group %d: %w", i+1, err)
			}
			if len(pages) == 0 {
				return fmt.Errorf("range group %d selects no pages", i+1)
			}
			groups = append(groups, pages)
		}
	} else {
		if p.Every < 1 {
			return fmt.Errorf("every must be at least 1")
		}
		if p.Every >= count {
			return fmt.Errorf("split would produce a single file")
		}
		for start := 1; start <= count; start += p.Every {
			end := start + p.Every - 1
			if end > count {
				end = count
			}
			pages := make([]int, 0, end-start+1)
			for page := start; page <= end; page++ {
				pages = append(pages, page)
			}
			groups = append(groups, pages)
		}
	}
	if len(groups) > 200 {
		return fmt.Errorf("split would produce more than 200 parts")
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		return err
	}
	for i, pages := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(output, fmt.Sprintf("part-%d.pdf", i+1))
		if err := b.Collect(ctx, input, path, pages); err != nil {
			return err
		}
	}
	return nil
}

// edit runs pdf-edit. The page count is checked here rather than inside
// Assemble because it is the only place that knows the real document length,
// and the message it produces is what the user sees.
func (o Operation) edit(ctx context.Context, b Backend, input, output string, p Params) error {
	if len(p.Edit) == 0 {
		return fmt.Errorf("%s needs an edit list", o.ID)
	}
	// The list is checked here as well as inside Assemble, so the user sees one
	// clear error naming the offending item rather than a backend failure.
	if err := validateItems(p.Edit); err != nil {
		return err
	}
	real := 0
	for _, item := range p.Edit {
		if item.Src > 0 {
			real++
		}
	}
	if real == 0 {
		return fmt.Errorf("%s would produce a document of only blank pages", o.ID)
	}
	pageCount, err := b.PageCount(ctx, input)
	if err != nil {
		return err
	}
	if err := checkItemsAgainstPageCount(p.Edit, pageCount); err != nil {
		return err
	}
	return b.Assemble(ctx, input, output, p.Edit)
}

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
