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
}

// NewGhostscript wires the engine. Tests substitute a recording runner.
func NewGhostscript(runner Runner) *Ghostscript {
	if runner == nil {
		runner = defaultRunTool
	}
	return &Ghostscript{runner: runner}
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
	args := []string{
		"-sDEVICE=pdfwrite",
		"-dCompatibilityLevel=1.4",
		fmt.Sprintf("-dPDFSETTINGS=%s", settings),
		"-dNOPAUSE", "-dBATCH", "-dQUIET",
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
	args := []string{
		"-sDEVICE=pdfwrite",
		"-sColorConversionStrategy=Gray",
		"-dProcessColorModel=/DeviceGray",
		"-dCompatibilityLevel=1.4",
		"-dNOPAUSE", "-dBATCH", "-dQUIET",
		fmt.Sprintf("-sOutputFile=%s", output),
		input,
	}
	if err := g.runner(ctx, "gs", args); err != nil {
		return fmt.Errorf("gs grayscale: %w", err)
	}
	return nil
}

// pdfSettings maps the user-facing preset name to gs's -dPDFSETTINGS value.
// The mapping keeps the same names Ghostscript users already know (screen /
// printer / prepress) while letting the UI stay in plain English.
func pdfSettings(preset string) (string, error) {
	switch preset {
	case "light":
		return "/prepress", nil
	case "medium":
		return "/printer", nil
	case "heavy":
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
