package pdfops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// recordRunner captures the binary and arguments used so the test can assert
// the engine's flags without running qpdf. When write is set it is called
// instead of just recording, so engines whose output is a side effect on disk
// (pdfimages) can be exercised too.
type recordRunner struct {
	gotName string
	gotArgs []string
	err     error
	write   func(name string, args []string) error
	calls   int32
}

func (r *recordRunner) run(ctx context.Context, name string, args []string) error {
	atomic.AddInt32(&r.calls, 1)
	r.gotName = name
	r.gotArgs = append([]string(nil), args...)
	if r.write != nil {
		if err := r.write(name, args); err != nil {
			return err
		}
	}
	return r.err
}

func TestQPDFRepairFlags(t *testing.T) {
	rec := &recordRunner{}
	q := NewQPDF(rec.run)
	if err := q.Repair(context.Background(), "/tmp/in.pdf", "/tmp/out.pdf"); err != nil {
		t.Fatal(err)
	}
	if rec.gotName != "qpdf" {
		t.Fatalf("binary = %q, want qpdf", rec.gotName)
	}
	want := []string{"/tmp/in.pdf", "/tmp/out.pdf"}
	if !equalSlice(rec.gotArgs, want) {
		t.Fatalf("args = %v, want %v", rec.gotArgs, want)
	}
}

func TestQPDFLinearizeAndFlattenFlags(t *testing.T) {
	cases := map[string][]string{
		"linearize": {"--linearize", "in.pdf", "out.pdf"},
		"flatten":   {"--flatten-annotations=all", "--generate-appearances", "in.pdf", "out.pdf"},
	}
	rec := &recordRunner{}
	q := NewQPDF(rec.run)
	if err := q.Linearize(context.Background(), "in.pdf", "out.pdf"); err != nil {
		t.Fatal(err)
	}
	if !equalSlice(rec.gotArgs, cases["linearize"]) {
		t.Fatalf("linearize args = %v, want %v", rec.gotArgs, cases["linearize"])
	}
	if err := q.Flatten(context.Background(), "in.pdf", "out.pdf"); err != nil {
		t.Fatal(err)
	}
	if !equalSlice(rec.gotArgs, cases["flatten"]) {
		t.Fatalf("flatten args = %v, want %v", rec.gotArgs, cases["flatten"])
	}
}

// exit3Runner returns an exitCodeError with code 3, which is qpdf's "warnings
// but output produced" signal. Engines treat it as success.
func TestQPDFTreatsExitThreeAsSuccess(t *testing.T) {
	rec := &recordRunner{err: &exitCodeError{code: 3, name: "qpdf"}}
	q := NewQPDF(rec.run)
	if err := q.Repair(context.Background(), "in.pdf", "out.pdf"); err != nil {
		t.Fatalf("exit 3 should be success: %v", err)
	}
}

func TestQPDFPropagatesRealFailures(t *testing.T) {
	rec := &recordRunner{err: errors.New("boom")}
	q := NewQPDF(rec.run)
	err := q.Repair(context.Background(), "in.pdf", "out.pdf")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.HasPrefix(err.Error(), "qpdf rewrite") {
		t.Fatalf("error = %q, want it to name the failing call", err)
	}
	if !errors.Is(err, rec.err) {
		t.Fatalf("error chain does not wrap the underlying: %v", err)
	}
}

func TestQPDFMapsPasswordFailure(t *testing.T) {
	rec := &recordRunner{err: &exitCodeError{code: 2, name: "qpdf", err: errors.New("exit status 2: qpdf: x.pdf: invalid password")}}
	q := NewQPDF(rec.run)
	if err := q.Linearize(context.Background(), "in.pdf", "out.pdf"); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("err = %v, want ErrPasswordRequired", err)
	}
}

// The fake-runner tests above pin argument lists but cannot tell whether the
// installed qpdf accepts them, which is how an unsupported flag shipped once.
// These run the real binary.
func TestQPDFRealRewrites(t *testing.T) {
	if availableBinary("qpdf") != nil {
		t.Skip("qpdf not installed")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "in.pdf")
	if err := defaultRunTool(context.Background(), "qpdf", []string{"--empty", "--pages", ".", "1-z", "--", in}); err != nil {
		// qpdf --empty needs at least a page; build one from a minimal body.
		if werr := os.WriteFile(in, []byte(minimalPDF), 0o600); werr != nil {
			t.Fatal(werr)
		}
	}
	if _, err := os.Stat(in); err != nil {
		if werr := os.WriteFile(in, []byte(minimalPDF), 0o600); werr != nil {
			t.Fatal(werr)
		}
	}
	q := NewQPDF(nil)
	for name, run := range map[string]func(context.Context, string, string) error{
		"repair": q.Repair, "linearize": q.Linearize, "flatten": q.Flatten,
	} {
		out := filepath.Join(dir, name+".pdf")
		if err := run(context.Background(), in, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info, err := os.Stat(out); err != nil || info.Size() == 0 {
			t.Fatalf("%s wrote no output: %v", name, err)
		}
		if err := defaultRunTool(context.Background(), "qpdf", []string{"--check", out}); err != nil {
			t.Fatalf("%s output fails qpdf --check: %v", name, err)
		}
	}
	if err := defaultRunTool(context.Background(), "qpdf", []string{"--check-linearization", filepath.Join(dir, "linearize.pdf")}); err != nil {
		t.Fatalf("linearize output is not linearized: %v", err)
	}
}

// minimalPDF is a valid one-page PDF with an empty page.
const minimalPDF = "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\ntrailer<</Root 1 0 R/Size 4>>\n%%EOF\n"

// equalSlice is a tiny equality helper so we can pin argument lists without
// reaching for reflect.DeepEqual in every test.
func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
