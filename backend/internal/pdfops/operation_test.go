package pdfops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSplitRangesAndEvery(t *testing.T) {
	for _, tc := range []struct {
		id     string
		params Params
		want   int
	}{
		{"pdf-split-ranges", Params{Ranges: "1-3;4-6;7-"}, 3},
		{"pdf-split-ranges", Params{Ranges: "1-3;2-4"}, 2},
		{"pdf-split-every", Params{Every: 3}, 3},
	} {
		t.Run(tc.id+tc.params.Ranges, func(t *testing.T) {
			fake := newFakeBackend(7)
			op, _ := Lookup(tc.id)
			if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"input.pdf"}, t.TempDir(), tc.params); err != nil {
				t.Fatal(err)
			}
			if len(fake.collected) != tc.want {
				t.Fatalf("collect calls = %d, want %d", len(fake.collected), tc.want)
			}
		})
	}
}

func TestPhase4GeometryAndLayoutOperationsAreCatalogueEntries(t *testing.T) {
	for _, id := range []string{"pdf-crop", "pdf-resize", "pdf-nup", "pdf-booklet"} {
		op, ok := Lookup(id)
		if !ok {
			t.Fatalf("Lookup(%q) missing", id)
		}
		if op.Mode != "studio" || op.MinInputs != 1 || op.MaxInputs != 1 {
			t.Fatalf("Lookup(%q) = %+v, want a single-input studio operation", id, op)
		}
	}
}

func TestPhase4GeometryAndLayoutDispatch(t *testing.T) {
	for _, tc := range []struct {
		id     string
		params Params
		want   string
	}{
		{"pdf-crop", Params{Crop: CropParams{Unit: "pt", Top: 1, Right: 2, Bottom: 3, Left: 4}}, "crop"},
		{"pdf-resize", Params{Resize: ResizeParams{To: "A4", Fit: "scale"}}, "resize"},
		{"pdf-nup", Params{Layout: LayoutParams{PerSheet: 4, Orientation: "portrait"}}, "nup"},
		{"pdf-booklet", Params{Layout: LayoutParams{PerSheet: 2}}, "booklet"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			fake := newPhase4Backend(4)
			op, _ := Lookup(tc.id)
			if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"input.pdf"}, t.TempDir()+"/out.pdf", tc.params); err != nil {
				t.Fatal(err)
			}
			if fake.last != tc.want {
				t.Fatalf("backend call = %q, want %q", fake.last, tc.want)
			}
		})
	}
}

func TestSplitRejectsInvalidBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		id     string
		params Params
	}{
		{"pdf-split-ranges", Params{Ranges: "1-3;;5"}},
		{"pdf-split-ranges", Params{Ranges: "1-9"}},
		{"pdf-split-every", Params{Every: 0}},
		{"pdf-split-every", Params{Every: 7}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			fake := newFakeBackend(7)
			op, _ := Lookup(tc.id)
			if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"input.pdf"}, t.TempDir(), tc.params); err == nil {
				t.Fatal("expected rejection")
			}
			if len(fake.collected) != 0 {
				t.Fatalf("collected %d parts before rejecting", len(fake.collected))
			}
		})
	}
}

// TestEveryInstantOperationOnALockedFileAsksForThePassword runs the real tools
// on an encrypted PDF with no password. Nautilus offers these operations for a
// locked file, and each must fail with the one reason a user can act on, not
// with a tool's own message ("Incorrect password", a qpdf usage error).
func TestEveryInstantOperationOnALockedFileAsksForThePassword(t *testing.T) {
	for _, tool := range []string{"qpdf", "pdftotext", "pdfimages", "gs"} {
		requireTool(t, tool)
	}
	plain := buildTextPDF(t, 2)
	locked := filepath.Join(t.TempDir(), "locked.pdf")
	if out, err := exec.Command("qpdf", "--encrypt", "user-pw", "owner-pw", "256", "--", plain, locked).CombinedOutput(); err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 3 {
			t.Fatalf("qpdf --encrypt: %v\n%s", err, out)
		}
	}
	toolbox := Toolbox{
		PDFCPU:  NewPDFCPUBackend(),
		QPDF:    NewQPDF(nil),
		GS:      NewGhostscript(nil),
		Poppler: NewPoppler(nil),
	}
	ran := 0
	for _, op := range Catalogue() {
		if op.Mode != "instant" || op.InputKind() != InputPDF || op.MinInputs > 1 || op.handlesItsOwnPasswords() {
			continue
		}
		if toolbox.Available(op.EngineID()) != nil {
			continue
		}
		ran++
		t.Run(op.ID, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "out"+filepath.Ext(op.ID))
			err := op.Run(context.Background(), toolbox, []string{locked}, output, Params{Pages: "1", Angle: 90})
			if !errors.Is(err, ErrPasswordRequired) {
				t.Errorf("%s on a locked file = %v, want ErrPasswordRequired", op.ID, err)
			}
		})
	}
	if ran < 10 {
		t.Fatalf("only %d operations were exercised; the loop is not covering the catalogue", ran)
	}
}

// A damaged file keeps its own error: the locked-file translation must not
// swallow it, or pdf-repair's input problems would read as a password issue.
func TestADamagedFileIsNotReportedAsLocked(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "broken.pdf")
	if err := os.WriteFile(broken, []byte("%PDF-1.4\nnot really a pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	op, _ := Lookup("pdf-reverse")
	err := op.Run(context.Background(), Toolbox{PDFCPU: NewPDFCPUBackend()}, []string{broken}, filepath.Join(t.TempDir(), "out.pdf"), Params{})
	if err == nil || errors.Is(err, ErrPasswordRequired) {
		t.Errorf("damaged input = %v, want a non-password error", err)
	}
}
