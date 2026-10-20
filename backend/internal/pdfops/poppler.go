package pdfops

import "context"

// Poppler shells out to the Poppler utilities for text and image extraction.
type Poppler struct {
	runner Runner
}

func (p *Poppler) ID() string { return EnginePoppler }

func (p *Poppler) Available() error { return nil }

func (p *Poppler) Text(_ context.Context, _, _ string) error { return nil }
