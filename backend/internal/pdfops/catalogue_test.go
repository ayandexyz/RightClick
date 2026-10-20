package pdfops

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestCatalogueListsPhase2OrganiseOperations guards the organise catalogue: it
// grows as new operations land (Task 3 added "fix" so this list stays put).
func TestCatalogueListsPhase2OrganiseOperations(t *testing.T) {
	want := []string{
		"pdf-merge", "pdf-extract", "pdf-delete", "pdf-rotate",
		"pdf-rotate-cw", "pdf-rotate-ccw", "pdf-rotate-180", "pdf-reverse",
	}
	gotIDs := map[string]bool{}
	for _, op := range Catalogue() {
		gotIDs[op.ID] = true
	}
	for _, id := range want {
		if !gotIDs[id] {
			t.Fatalf("Catalogue missing %s; have %v", id, gotIDs)
		}
	}
	for _, op := range Catalogue() {
		if op.Label == "" || op.Group == "" || op.Mode == "" {
			t.Fatalf("operation %s is missing a label, group or mode: %+v", op.ID, op)
		}
		if _, ok := Lookup(op.ID); !ok {
			t.Fatalf("Lookup(%q) did not find the operation in the catalogue", op.ID)
		}
	}
	if _, ok := Lookup("pdf-nope"); ok {
		t.Fatal("Lookup found an operation that is not in the catalogue")
	}
}

func TestOperationInputCounts(t *testing.T) {
	files := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "/tmp/a.pdf"
		}
		return out
	}
	tests := []struct {
		op      string
		count   int
		wantErr string
	}{
		{op: "pdf-merge", count: 1, wantErr: "at least 2"},
		{op: "pdf-merge", count: 0, wantErr: "at least 2"},
		{op: "pdf-merge", count: 2},
		{op: "pdf-extract", count: 0, wantErr: "at least 1"},
		{op: "pdf-extract", count: 1},
		{op: "pdf-reverse", count: 3},
	}
	for _, tc := range tests {
		op, _ := Lookup(tc.op)
		err := op.Validate(files(tc.count), Params{})
		if tc.wantErr == "" {
			if err != nil && tc.op != "pdf-extract" {
				t.Fatalf("%s with %d files: %v", tc.op, tc.count, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s with %d files should fail", tc.op, tc.count)
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s with %d files: error %q should contain %q", tc.op, tc.count, err, tc.wantErr)
		}
	}
}

func TestValidateRequiredParams(t *testing.T) {
	file := []string{"/tmp/a.pdf"}
	merge := []string{"/tmp/a.pdf", "/tmp/b.pdf"}
	tests := []struct {
		op      string
		files   []string
		params  Params
		wantErr string
	}{
		{op: "pdf-extract", files: file, params: Params{}, wantErr: "page selection"},
		{op: "pdf-extract", files: file, params: Params{Pages: "1-3"}},
		{op: "pdf-delete", files: file, params: Params{}, wantErr: "page selection"},
		{op: "pdf-delete", files: file, params: Params{Pages: "2"}},
		{op: "pdf-rotate", files: file, params: Params{}, wantErr: "angle"},
		{op: "pdf-rotate", files: file, params: Params{Angle: 45}, wantErr: "angle"},
		{op: "pdf-rotate", files: file, params: Params{Angle: 90}},
		{op: "pdf-rotate", files: file, params: Params{Angle: 180}},
		{op: "pdf-rotate", files: file, params: Params{Angle: 270}},
		{op: "pdf-rotate", files: file, params: Params{Angle: 90, Pages: "1-2"}},
		{op: "pdf-reverse", files: file, params: Params{Pages: "1"}, wantErr: "page selection"},
		{op: "pdf-rotate", files: file, params: Params{Angle: -90}, wantErr: "angle"},
		{op: "pdf-rotate", files: file, params: Params{Angle: 0}, wantErr: "angle"},
		{op: "pdf-merge", files: merge, params: Params{Pages: "1"}, wantErr: "page selection"},
		{op: "pdf-merge", files: merge, params: Params{Angle: 90}, wantErr: "angle"},
		{op: "pdf-reverse", files: file, params: Params{Pages: "1"}, wantErr: "page selection"},
		{op: "pdf-reverse", files: file, params: Params{Angle: 90}, wantErr: "angle"},
	}
	for _, tc := range tests {
		op, ok := Lookup(tc.op)
		if !ok {
			t.Fatalf("unknown operation %q", tc.op)
		}
		err := op.Validate(tc.files, tc.params)
		if tc.wantErr == "" {
			if err != nil {
				t.Fatalf("%s with %+v: %v", tc.op, tc.params, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s with %+v should fail", tc.op, tc.params)
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s with %+v: error %q should contain %q", tc.op, tc.params, err, tc.wantErr)
		}
	}
}

func TestRunReverse(t *testing.T) {
	op, _ := Lookup("pdf-reverse")
	fake := newFakeBackend(4)
	if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"/tmp/a.pdf"}, "/tmp/a-reversed.pdf", Params{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.collected) != 1 {
		t.Fatalf("reverse should call Collect once, got %v", fake.calls)
	}
	want := []int{4, 3, 2, 1}
	got := fake.collected[0]
	if len(got) != len(want) {
		t.Fatalf("reverse collected %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("reverse collected %v, want %v", got, want)
		}
	}
}

func TestRunDelete(t *testing.T) {
	op, _ := Lookup("pdf-delete")
	fake := newFakeBackend(4)
	if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"/tmp/a.pdf"}, "/tmp/a-edited.pdf", Params{Pages: "2"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []int{1, 3, 4}
	got := fake.collected[0]
	if len(got) != len(want) {
		t.Fatalf("delete collected %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("delete collected %v, want %v", got, want)
		}
	}
}

func TestRunDeleteAllPagesIsRejected(t *testing.T) {
	op, _ := Lookup("pdf-delete")
	fake := newFakeBackend(3)
	err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"/tmp/a.pdf"}, "/tmp/a-edited.pdf", Params{Pages: "1-3"})
	if err == nil {
		t.Fatal("deleting every page should fail")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("backend should not run when the selection is empty, got %v", fake.calls)
	}
}

func TestRunExtractKeepsOrder(t *testing.T) {
	op, _ := Lookup("pdf-extract")
	fake := newFakeBackend(6)
	if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"/tmp/a.pdf"}, "/tmp/a-extracted.pdf", Params{Pages: "5,1-3"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []int{5, 1, 2, 3}
	got := fake.collected[0]
	if len(got) != len(want) {
		t.Fatalf("extract collected %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("extract collected %v, want %v", got, want)
		}
	}
}

func TestRunRotate(t *testing.T) {
	op, _ := Lookup("pdf-rotate")
	fake := newFakeBackend(5)
	if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"/tmp/a.pdf"}, "/tmp/a-rotated.pdf", Params{Angle: 90}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.rotated) != 1 {
		t.Fatalf("rotate should call Rotate once, got %v", fake.calls)
	}
	if fake.rotated[0].angle != 90 {
		t.Fatalf("rotate angle = %d, want 90", fake.rotated[0].angle)
	}
	if len(fake.rotated[0].pages) != 0 {
		t.Fatalf("rotate with no pages spec should pass an empty selection, got %v", fake.rotated[0].pages)
	}

	fake = newFakeBackend(5)
	if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"/tmp/a.pdf"}, "/tmp/a-rotated.pdf", Params{Angle: 180, Pages: "1-2"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fake.rotated[0].pages; len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("rotate pages = %v, want [1 2]", got)
	}
}

func TestRunMerge(t *testing.T) {
	op, _ := Lookup("pdf-merge")
	fake := newFakeBackend(2)
	inputs := []string{"/tmp/a.pdf", "/tmp/b.pdf", "/tmp/c.pdf"}
	if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, inputs, "/tmp/merged.pdf", Params{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.merged) != 1 {
		t.Fatalf("merge should be one call, got %v", fake.calls)
	}
	merged := fake.merged[0]
	if len(merged) != len(inputs) {
		t.Fatalf("merge got %v, want %v", merged, inputs)
	}
	for i := range merged {
		if merged[i] != inputs[i] {
			t.Fatalf("merge got %v, want input order %v", merged, inputs)
		}
	}
}

func TestRunRejectsBadPageSpec(t *testing.T) {
	op, _ := Lookup("pdf-extract")
	fake := newFakeBackend(3)
	err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"/tmp/a.pdf"}, "/tmp/a-extracted.pdf", Params{Pages: "9"})
	if err == nil {
		t.Fatal("a page past the end should fail")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("backend should not run for a bad spec, got %v", fake.calls)
	}
}

func TestRunPropagatesBackendError(t *testing.T) {
	op, _ := Lookup("pdf-reverse")
	sentinel := errors.New("backend broke")
	fake := newFakeBackend(3)
	fake.err = sentinel
	if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"/tmp/a.pdf"}, "/tmp/out.pdf", Params{}); !errors.Is(err, sentinel) {
		t.Fatalf("Run error = %v, want %v", err, sentinel)
	}
}

func TestRunMergeUsesEveryInputRegardlessOfCount(t *testing.T) {
	// Run itself does not re-check input counts; Validate owns that rule, and
	// the job layer calls it first. Merge still has to pass every input on.
	op, _ := Lookup("pdf-merge")
	fake := newFakeBackend(1)
	inputs := []string{"/tmp/a.pdf"}
	if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, inputs, "/tmp/merged.pdf", Params{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.merged) != 1 || len(fake.merged[0]) != 1 {
		t.Fatalf("merge should forward all inputs, got %v", fake.merged)
	}
}
