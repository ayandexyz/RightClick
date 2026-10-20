package pdfops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPopplerTextFlags(t *testing.T) {
	rec := &recordRunner{}
	p := NewPoppler(rec.run)
	if err := p.Text(context.Background(), "/tmp/in.pdf", "/tmp/out.txt"); err != nil {
		t.Fatal(err)
	}
	if rec.gotName != "pdftotext" {
		t.Fatalf("binary = %q, want pdftotext", rec.gotName)
	}
	if !equalSlice(rec.gotArgs, []string{"-layout", "/tmp/in.pdf", "/tmp/out.txt"}) {
		t.Fatalf("args = %v, want [-layout in out]", rec.gotArgs)
	}
}

func TestPopplerImagesFlagsAndOrdering(t *testing.T) {
	dir := t.TempDir()
	// pdfimages names its output img-000.png, img-001.png, ... The fake runner
	// writes them out of order so the ordering guarantee is actually tested.
	rec := &recordRunner{write: func(name string, args []string) error {
		for i, suffix := range []string{"001", "000", "002"} {
			path := filepath.Join(dir, "img-"+suffix+".png")
			if err := os.WriteFile(path, []byte(suffix), 0o600); err != nil {
				return err
			}
			_ = i
		}
		return nil
	}}
	p := NewPoppler(rec.run)
	files, err := p.Images(context.Background(), "/tmp/in.pdf", dir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.gotName != "pdfimages" {
		t.Fatalf("binary = %q, want pdfimages", rec.gotName)
	}
	if !equalSlice(rec.gotArgs, []string{"-png", "/tmp/in.pdf", filepath.Join(dir, "img")}) {
		t.Fatalf("args = %v, want [-png in dir/img]", rec.gotArgs)
	}
	if len(files) != 3 {
		t.Fatalf("files = %v, want three", files)
	}
	for i, want := range []string{"000", "001", "002"} {
		if !strings.HasSuffix(files[i], "img-"+want+".png") {
			t.Fatalf("files[%d] = %s, want img-%s.png", i, files[i], want)
		}
	}
}

// A document with no embedded images produces no files at all. That is a real
// outcome the job layer reports as skipped, not a failure.
func TestPopplerImagesReturnsEmptyWhenNothingEmbedded(t *testing.T) {
	dir := t.TempDir()
	rec := &recordRunner{}
	p := NewPoppler(rec.run)
	files, err := p.Images(context.Background(), "/tmp/in.pdf", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("files = %v, want none", files)
	}
}

// pdfimages widens the numeric suffix as the count grows, so sorting must
// compare numbers rather than strings: "img-2" sorts before "img-10" as text.
func TestImageOrdinalSortsNumerically(t *testing.T) {
	dir := t.TempDir()
	for _, suffix := range []string{"000", "002", "010"} {
		if err := os.WriteFile(filepath.Join(dir, "img-"+suffix+".png"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := imageOutputs(filepath.Join(dir, "img"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %v, want three", files)
	}
	if !strings.HasSuffix(files[1], "img-002.png") || !strings.HasSuffix(files[2], "img-010.png") {
		t.Fatalf("files = %v, want numeric order 000,002,010", files)
	}
}

func TestPopplerAvailableNeedsBothTools(t *testing.T) {
	// The engine is only usable when both utilities are present, so this is a
	// single probe over two binaries rather than a per-operation split.
	if availableBinary("pdftotext") != nil || availableBinary("pdfimages") != nil {
		t.Skip("poppler-utils not installed")
	}
	if err := NewPoppler(nil).Available(); err != nil {
		t.Fatalf("Available() = %v, want nil", err)
	}
}
