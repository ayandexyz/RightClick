package pdfops

import (
	"context"
	"testing"
)

type phase4Backend struct {
	fakeBackend
	last string
}

func newPhase4Backend(pages int) *phase4Backend {
	return &phase4Backend{fakeBackend: *newFakeBackend(pages)}
}

func (f *phase4Backend) Crop(context.Context, string, string, CropParams) error {
	f.last = "crop"
	return nil
}
func (f *phase4Backend) Resize(context.Context, string, string, ResizeParams) error {
	f.last = "resize"
	return nil
}
func (f *phase4Backend) NUp(context.Context, string, string, NUpParams) error {
	f.last = "nup"
	return nil
}
func (f *phase4Backend) Booklet(context.Context, string, string, BookletParams) error {
	f.last = "booklet"
	return nil
}

func TestPhase4FakeBackendCompiles(t *testing.T) {
	_ = newPhase4Backend(1)
}
