package pdfops

import "context"

// QPDF shells out to qpdf for the rewrites pdfcpu cannot do. Stubs land in the
// qpdf task; this declares the shape the toolbox and operations code use.
type QPDF struct {
	runner Runner
}

// ID names the engine in status output and operation entries.
func (q *QPDF) ID() string { return EngineQPDF }

// Available reports whether the qpdf binary can be used.
func (q *QPDF) Available() error { return nil }

func (q *QPDF) Repair(_ context.Context, _, _ string) error    { return nil }
func (q *QPDF) Linearize(_ context.Context, _, _ string) error { return nil }
func (q *QPDF) Flatten(_ context.Context, _, _ string) error   { return nil }
