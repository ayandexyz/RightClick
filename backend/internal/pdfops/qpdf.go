package pdfops

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// QPDF shells out to qpdf for the three structural rewrites pdfcpu cannot do:
// repairing damaged PDFs, linearizing them for web delivery, and flattening
// form annotations so the rest of the document no longer depends on them.
type QPDF struct {
	runner Runner
}

// NewQPDF wires a fresh engine. A nil runner falls back to a default that uses
// the qpdf binary on PATH; tests can pass a fake to assert argument lists.
func NewQPDF(runner Runner) *QPDF {
	if runner == nil {
		runner = defaultRunTool
	}
	return &QPDF{runner: runner}
}

// ID names the engine in status output and operation entries.
func (q *QPDF) ID() string { return EngineQPDF }

// Available checks whether the qpdf binary is on PATH and answers to a
// query. exec.LookPath is the surface every other engine uses too.
func (q *QPDF) Available() error {
	return availableBinary("qpdf")
}

// Repair rewrites a damaged PDF so the rest of the toolchain can open it; qpdf
// reconstructs a broken cross-reference table as part of any rewrite. qpdf's exit code 3 is "warnings written but output produced"; any other
// non-zero exit is a real error worth reporting.
func (q *QPDF) Repair(ctx context.Context, input, output string) error {
	return q.runQPDF(ctx, []string{input, output})
}

// Linearize writes a web-optimized copy, where the catalog data is rearranged
// so the first byte already serves the first page's contents.
func (q *QPDF) Linearize(ctx context.Context, input, output string) error {
	return q.runQPDF(ctx, []string{"--linearize", input, output})
}

// Flatten pushes form annotations and appearances into the page content so
// the document is no longer dependent on a LiveCycle / AcroForm interpreter.
func (q *QPDF) Flatten(ctx context.Context, input, output string) error {
	return q.runQPDF(ctx, []string{"--flatten-annotations=all", "--generate-appearances", input, output})
}

// runQPDF dispatches the configured runner and decodes the exit code. qpdf
// uses three values: 0 ok, 3 warnings (still produced output), anything else a
// failure.
func (q *QPDF) runQPDF(ctx context.Context, args []string) error {
	err := q.runner(ctx, "qpdf", args)
	if err == nil {
		return nil
	}
	var exitErr *exitCodeError
	if errors.As(err, &exitErr) && exitErr.code == 3 {
		return nil
	}
	if strings.Contains(err.Error(), "invalid password") {
		return ErrPasswordRequired
	}
	return fmt.Errorf("qpdf %s: %w", operationName(args), err)
}

// operationName names the qpdf call for error messages: its leading option, or
// "rewrite" for the plain repair call that has none.
func operationName(args []string) string {
	if len(args) > 0 && strings.HasPrefix(args[0], "--") {
		return args[0]
	}
	return "rewrite"
}
