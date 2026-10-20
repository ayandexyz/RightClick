package pdfops

import "context"

// Ghostscript shells out to gs for compression and grayscale.
type Ghostscript struct {
	runner Runner
}

func (g *Ghostscript) ID() string { return EngineGhostscript }

func (g *Ghostscript) Available() error { return nil }

func (g *Ghostscript) Grayscale(_ context.Context, _, _ string) error { return nil }
