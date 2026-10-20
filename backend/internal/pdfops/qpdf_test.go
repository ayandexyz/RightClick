package pdfops

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// recordRunner captures the binary and arguments used so the test can assert
// the engine's flags without running qpdf.
type recordRunner struct {
	gotName string
	gotArgs []string
	err     error
	calls   int32
}

func (r *recordRunner) run(ctx context.Context, name string, args []string) error {
	atomic.AddInt32(&r.calls, 1)
	r.gotName = name
	r.gotArgs = append([]string(nil), args...)
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
	want := []string{"--replace-input", "--quiet", "/tmp/in.pdf", "/tmp/out.pdf"}
	if !equalSlice(rec.gotArgs, want) {
		t.Fatalf("args = %v, want %v", rec.gotArgs, want)
	}
}

func TestQPDFLinearizeAndFlattenFlags(t *testing.T) {
	cases := map[string][]string{
		"linearize": {"--linearize", "--quiet", "in.pdf", "out.pdf"},
		"flatten":   {"--flatten-annotations", "--generate-appearances", "--quiet", "in.pdf", "out.pdf"},
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
	if !strings.HasPrefix(err.Error(), "qpdf --replace-input") {
		t.Fatalf("error = %q, want it to name the failing flag", err)
	}
	if !errors.Is(err, rec.err) {
		t.Fatalf("error chain does not wrap the underlying: %v", err)
	}
}

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
