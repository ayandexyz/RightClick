package pdfops

import (
	"context"
	"testing"
)

// unavailableQPDF stands in for an engine whose binary is missing.
func TestToolboxAvailableReportsMissingEngines(t *testing.T) {
	tb := Toolbox{PDFCPU: &fakeBackend{}}
	if err := tb.Available(EnginePDFCPU); err != nil {
		t.Fatalf("pdfcpu Available() = %v, want nil", err)
	}
	for _, id := range []string{EngineQPDF, EngineGhostscript, EnginePoppler, EngineTesseract} {
		err := tb.Available(id)
		if err == nil {
			t.Fatalf("%s Available() = nil, want a reason", id)
		}
	}
	if err := tb.Available("nonexistent"); err == nil {
		t.Fatal("an unknown engine should report unavailable")
	}
}

func TestToolboxAvailableUsesEachEngine(t *testing.T) {
	tb := Toolbox{PDFCPU: &fakeBackend{}, QPDF: &QPDF{}}
	if err := tb.Available(EngineQPDF); err != nil {
		t.Fatalf("Available() = %v, want nil", err)
	}
}

func TestValidateFixedAngleVariants(t *testing.T) {
	files := []string{"a.pdf"}
	for _, id := range []string{"pdf-rotate-cw", "pdf-rotate-ccw", "pdf-rotate-180"} {
		op, ok := Lookup(id)
		if !ok {
			t.Fatalf("%s is not in the catalogue", id)
		}
		if op.NeedsAngle {
			t.Fatalf("%s should not ask for an angle", id)
		}
		if err := op.Validate(files, Params{}); err != nil {
			t.Fatalf("%s Validate with no params = %v, want nil", id, err)
		}
		// An explicit angle is meaningless on a fixed variant and must be
		// rejected rather than silently ignored.
		if err := op.Validate(files, Params{Angle: 90}); err == nil {
			t.Fatalf("%s accepted an explicit angle", id)
		}
	}
}

func TestValidateFixedAngleSatisfiesTheAngleRequirement(t *testing.T) {
	// pdf-rotate still needs an angle because it is not fixed; the variants are
	// the fixed ones. This guards the two from drifting together.
	op, ok := Lookup("pdf-rotate")
	if !ok {
		t.Fatal("pdf-rotate is not in the catalogue")
	}
	if !op.NeedsAngle || op.FixedAngle != 0 {
		t.Fatalf("pdf-rotate = %+v, want NeedsAngle with no fixed angle", op)
	}
	if err := op.Validate([]string{"a.pdf"}, Params{}); err == nil {
		t.Fatal("pdf-rotate should require an angle")
	}
}

func TestRotateVariantsRunWithTheirFixedAngle(t *testing.T) {
	for id, want := range map[string]int{"pdf-rotate-cw": 90, "pdf-rotate-ccw": 270, "pdf-rotate-180": 180} {
		op, ok := Lookup(id)
		if !ok {
			t.Fatalf("%s is not in the catalogue", id)
		}
		backend := newFakeBackend(4, "a.pdf")
		if err := op.Run(context.Background(), Toolbox{PDFCPU: backend}, []string{"a.pdf"}, "out.pdf", Params{}); err != nil {
			t.Fatalf("%s Run = %v", id, err)
		}
		if len(backend.rotated) != 1 {
			t.Fatalf("%s produced %d rotations, want 1", id, len(backend.rotated))
		}
		if got := backend.rotated[0].angle; got != want {
			t.Fatalf("%s angle = %d, want %d", id, got, want)
		}
	}
}

func TestRotateVariantsAreInstantAndRotateIsStudio(t *testing.T) {
	for _, id := range []string{"pdf-rotate-cw", "pdf-rotate-ccw", "pdf-rotate-180"} {
		op, _ := Lookup(id)
		if op.Mode != "instant" {
			t.Fatalf("%s mode = %q, want instant", id, op.Mode)
		}
	}
	rotate, _ := Lookup("pdf-rotate")
	if rotate.Mode != "studio" {
		t.Fatalf("pdf-rotate mode = %q, want studio", rotate.Mode)
	}
}
