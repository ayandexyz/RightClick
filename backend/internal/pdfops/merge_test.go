package pdfops

import (
	"context"
	"testing"
)

func TestMergeWithPerFileRangesCollectsThenMergesInOrder(t *testing.T) {
	fake := newFakeBackend(5)
	op, _ := Lookup("pdf-merge")
	params := Params{FilePages: []string{"1-2", "", "4-5"}}
	if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"a.pdf", "b.pdf", "c.pdf"}, t.TempDir()+"/merged.pdf", params); err != nil {
		t.Fatal(err)
	}
	if len(fake.collected) != 2 {
		t.Fatalf("collect calls = %d, want 2", len(fake.collected))
	}
	if len(fake.merged) != 1 || len(fake.merged[0]) != 3 {
		t.Fatalf("merge inputs = %v, want three inputs in order", fake.merged)
	}
	if fake.merged[0][1] != "b.pdf" {
		t.Fatalf("all-pages input should remain unchanged, got %v", fake.merged[0])
	}
}

func TestMergeFilePagesLengthMismatch(t *testing.T) {
	fake := newFakeBackend(5)
	op, _ := Lookup("pdf-merge")
	if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"a.pdf", "b.pdf"}, t.TempDir()+"/merged.pdf", Params{FilePages: []string{"1"}}); err == nil {
		t.Fatal("expected file_pages length mismatch")
	}
	if len(fake.collected) != 0 || len(fake.merged) != 0 {
		t.Fatal("invalid file_pages must not touch the backend")
	}
}

func TestMergeBadFilePagesFailsBeforeMerge(t *testing.T) {
	fake := newFakeBackend(5)
	op, _ := Lookup("pdf-merge")
	if err := op.Run(context.Background(), Toolbox{PDFCPU: fake}, []string{"a.pdf", "b.pdf"}, t.TempDir()+"/merged.pdf", Params{FilePages: []string{"1-2", "9"}}); err == nil {
		t.Fatal("expected out-of-range file page spec")
	}
	if len(fake.merged) != 0 {
		t.Fatal("bad file page spec must not merge")
	}
}
