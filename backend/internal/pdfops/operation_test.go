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
