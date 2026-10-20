package pdfops

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// ErrNoGain is returned when Ghostscript compresses a PDF but the resulting
// file is no smaller than the source. The daemon uses it to suggest the user
// try a different preset or skip the operation.
var ErrNoGain = errors.New("ghostscript did not shrink the file")

// Ghostscript shells out to gs for the operations only Ghostscript can do:
// compressing existing PDFs and decolouring them to grayscale. The compress
// call uses -dPDFSETTINGS so the user-facing choice is high-level (light,
// medium, heavy).
type Ghostscript struct {
	runner Runner
	// pages counts a PDF's pages; a field so tests can run without real PDFs.
	pages func(ctx context.Context, path string) (int, error)
}

// NewGhostscript wires the engine. Tests substitute a recording runner.
func NewGhostscript(runner Runner) *Ghostscript {
	if runner == nil {
		runner = defaultRunTool
	}
	return &Ghostscript{runner: runner, pages: NewPDFCPUBackend().PageCount}
}

// ID names the engine in status output and operation entries.
func (g *Ghostscript) ID() string { return EngineGhostscript }

// Available checks whether the gs binary is on PATH.
func (g *Ghostscript) Available() error {
	return availableBinary("gs")
}

// Compress rewrites input to output at the chosen preset level. The preset is
// one of "light", "medium", or "heavy", mapped to gs's -dPDFSETTINGS values:
// /screen for heavy, /printer for medium, /prepress for light.
func (g *Ghostscript) Compress(ctx context.Context, input, output, preset string) error {
	settings, err := pdfSettings(preset)
	if err != nil {
		return err
	}
	if err := g.rejectLocked(ctx, input); err != nil {
		return err
	}
	args := []string{
		"-sDEVICE=pdfwrite",
		"-dCompatibilityLevel=1.4",
		fmt.Sprintf("-dPDFSETTINGS=%s", settings),
		"-dSAFER", "-dNOPAUSE", "-dBATCH", "-dQUIET",
		fmt.Sprintf("-sOutputFile=%s", output),
		input,
	}
	if err := g.runner(ctx, "gs", args); err != nil {
		return fmt.Errorf("gs compress: %w", err)
	}
	return checkShrink(input, output)
}

// Grayscale renders every page through a color transform so the output contains
// only black, white, and grays.
func (g *Ghostscript) Grayscale(ctx context.Context, input, output string) error {
	if err := g.rejectLocked(ctx, input); err != nil {
		return err
	}
	args := []string{
		"-sDEVICE=pdfwrite",
		"-sColorConversionStrategy=Gray",
		"-dProcessColorModel=/DeviceGray",
		"-dCompatibilityLevel=1.4",
		"-dSAFER", "-dNOPAUSE", "-dBATCH", "-dQUIET",
		fmt.Sprintf("-sOutputFile=%s", output),
		input,
	}
	if err := g.runner(ctx, "gs", args); err != nil {
		return fmt.Errorf("gs grayscale: %w", err)
	}
	return nil
}

// rejectLocked fails a password-protected input before Ghostscript runs.
// Ghostscript reports "requires a password" but still exits 0 and writes a
// pageless document, so its output cannot be trusted to signal the problem,
// and pdfcpu even counts that pageless file as one page. Only the input probe
// is reliable. Any other probe failure is ignored here: the job layer
// classifies unreadable files after the operation fails.
func (g *Ghostscript) rejectLocked(ctx context.Context, input string) error {
	if _, err := g.pages(ctx, input); errors.Is(err, ErrPasswordRequired) {
		return ErrPasswordRequired
	}
	return nil
}

// pdfSettings maps the user-facing preset name to gs's -dPDFSETTINGS value.
// The mapping keeps the same names Ghostscript users already know (screen /
// printer / prepress) while letting the UI stay in plain English.
func pdfSettings(preset string) (string, error) {
	switch preset {
	case "light":
		return "/printer", nil
	case "balanced":
		return "/ebook", nil
	case "strong":
		return "/screen", nil
	default:
		return "", fmt.Errorf("unknown preset %q", preset)
	}
}

// checkShrink makes compress return ErrNoGain when the output is at least the
// same size as the input: rewriting has happened and the file is no smaller,
// so offering the operation again is misleading. We measure rather than read
// the gs banner because the preset choice is orthogonal to gs versions.
func checkShrink(input, output string) error {
	in, err := os.Stat(input)
	if err != nil {
		return err
	}
	out, err := os.Stat(output)
	if err != nil {
		return err
	}
	if out.Size() >= in.Size() {
		return ErrNoGain
	}
	return nil
}
