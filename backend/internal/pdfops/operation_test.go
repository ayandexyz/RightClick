package pdfops

import (
	"context"
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
