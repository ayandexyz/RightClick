package pdfops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGhostscriptCompressFlags(t *testing.T) {
	cases := map[string]string{
		"light":    "/printer",
		"balanced": "/ebook",
		"strong":   "/screen",
	}
	rec := &recordRunner{err: nil}
	gs := newTestGhostscript(rec.run)
	for preset, wantFlag := range cases {
		err := gs.Compress(context.Background(), writeSource(t, 4096), "/tmp/out.pdf", preset)
		if !errors.Is(err, ErrNoGain) && err != nil {
			// We expect ErrNoGain because /tmp/out.pdf does not exist; the
			// success branch exits before reaching checkShrink. Either way the
			// only thing that matters is the args.
			t.Fatalf("preset %s returned %v, want ErrNoGain from checkShrink", preset, err)
		}
		settings := -1
		for i, a := range rec.gotArgs {
			if a == "-dPDFSETTINGS="+wantFlag {
				settings = i
				break
			}
		}
		if settings < 0 {
			t.Fatalf("preset %s args = %v, want %s in the list", preset, rec.gotArgs, "-dPDFSETTINGS="+wantFlag)
		}
		if rec.gotArgs[settings] != "-dPDFSETTINGS="+wantFlag {
			t.Fatalf("preset %s flag = %q, want -dPDFSETTINGS=%s", preset, rec.gotArgs[settings], wantFlag)
		}
	}
}

func TestGhostscriptPDFALevelBuildsDaemonDiscoveredArguments(t *testing.T) {
	dir := t.TempDir()
	definition := filepath.Join(dir, "PDFA_def.ps")
	icc := filepath.Join(dir, "srgb.icc")
	if err := os.WriteFile(definition, []byte("/ICCProfile (srgb.icc)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(icc, []byte("icc"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &recordRunner{}
	gs := newTestGhostscript(rec.run)
	gs.pdfaDefinition = func() (string, string, error) { return definition, icc, nil }
	if err := gs.PDFA(context.Background(), "in.pdf", filepath.Join(dir, "out.pdf"), "2b"); err != nil {
		t.Fatal(err)
	}
	if !containsString(rec.gotArgs, "-dPDFA=2") || !containsString(rec.gotArgs, "-dPDFACompatibilityPolicy=1") {
		t.Fatalf("PDF/A args = %v", rec.gotArgs)
	}
	if !containsString(rec.gotArgs, filepath.Join(dir, ".PDFA_def.ps")) {
		t.Fatalf("PDF/A args = %v, want discovered definition path", rec.gotArgs)
	}
	if containsString(rec.gotArgs, "-sICCProfile="+icc) {
		t.Fatal("ICC profile must be supplied through the daemon-discovered definition search path")
	}
}

func TestGhostscriptRejectsUnknownPDFALevel(t *testing.T) {
	gs := newTestGhostscript(nil)
	if err := gs.PDFA(context.Background(), "in.pdf", "out.pdf", "4b"); err == nil || !strings.Contains(err.Error(), "level") {
		t.Fatalf("err = %v, want invalid level", err)
	}
}

func TestDiscoverPDFAAssetsUsesDefinitionAndICC(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	iccDir := filepath.Join(dir, "iccprofiles")
	if err := os.MkdirAll(lib, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(iccDir, 0o700); err != nil {
		t.Fatal(err)
	}
	definition := filepath.Join(lib, "PDFA_def.ps")
	icc := filepath.Join(iccDir, "srgb.icc")
	if err := os.WriteFile(definition, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(icc, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	gotDef, gotICC, err := discoverPDFAAssets([]string{dir})
	if err != nil || gotDef != definition || gotICC != icc {
		t.Fatalf("assets = %q, %q, %v", gotDef, gotICC, err)
	}
}

func TestGhostscriptRejectsUnknownPreset(t *testing.T) {
	gs := newTestGhostscript(func(_ context.Context, _ string, _ []string) error { return nil })
	err := gs.Compress(context.Background(), "in.pdf", "out.pdf", "extreme")
	if err == nil || !strings.Contains(err.Error(), "extreme") {
		t.Fatalf("err = %v, want it to name the preset", err)
	}
}

func TestGhostscriptGrayscaleFlags(t *testing.T) {
	rec := &recordRunner{err: nil}
	gs := newTestGhostscript(rec.run)
	if err := gs.Grayscale(context.Background(), "in.pdf", "out.pdf"); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"-sColorConversionStrategy=Gray", "-dProcessColorModel=/DeviceGray"} {
		if !containsString(rec.gotArgs, flag) {
			t.Fatalf("grayscale args = %v, want it to include %s", rec.gotArgs, flag)
		}
	}
}

func TestGhostscriptCompressReturnsErrNoGainWhenBigger(t *testing.T) {
	dir := t.TempDir()
	in := writeSource(t, 1024)
	out := dir + "/same.pdf"
	if err := os.WriteFile(out, make([]byte, 1024), 0o600); err != nil {
		t.Fatal(err)
	}
	gs := newTestGhostscript(func(_ context.Context, _ string, _ []string) error {
		// Produce an "output" the same size as the input so checkShrink fires.
		if err := os.WriteFile(out, make([]byte, 1024), 0o600); err != nil {
			return err
		}
		return nil
	})
	err := gs.Compress(context.Background(), in, out, "light")
	if !errors.Is(err, ErrNoGain) {
		t.Fatalf("err = %v, want ErrNoGain", err)
	}
}

func TestGhostscriptCompressSucceedsWhenSmaller(t *testing.T) {
	dir := t.TempDir()
	in := writeSource(t, 1024)
	out := dir + "/shrink.pdf"
	gs := newTestGhostscript(func(_ context.Context, _ string, _ []string) error {
		if err := os.WriteFile(out, make([]byte, 512), 0o600); err != nil {
			return err
		}
		return nil
	})
	if err := gs.Compress(context.Background(), in, out, "light"); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

// newTestGhostscript builds an engine whose page counter accepts the fake
// outputs these tests write, which are not real PDFs.
func newTestGhostscript(run Runner) *Ghostscript {
	gs := NewGhostscript(run)
	gs.pages = func(context.Context, string) (int, error) { return 1, nil }
	return gs
}

func TestGhostscriptRejectsLockedInputWithoutRunning(t *testing.T) {
	rec := &recordRunner{}
	gs := NewGhostscript(rec.run)
	gs.pages = func(context.Context, string) (int, error) { return 0, ErrPasswordRequired }
	for name, run := range map[string]func() error{
		"compress":  func() error { return gs.Compress(context.Background(), "in.pdf", "out.pdf", "light") },
		"grayscale": func() error { return gs.Grayscale(context.Background(), "in.pdf", "out.pdf") },
	} {
		if err := run(); !errors.Is(err, ErrPasswordRequired) {
			t.Fatalf("%s err = %v, want ErrPasswordRequired", name, err)
		}
	}
	if rec.calls != 0 {
		t.Fatalf("gs ran %d times for a locked input", rec.calls)
	}
}

// Runs the real binaries: a locked PDF must fail, not publish an empty file.
func TestGhostscriptRealLockedPDFFails(t *testing.T) {
	if availableBinary("gs") != nil || availableBinary("qpdf") != nil {
		t.Skip("gs and qpdf are required")
	}
	dir := t.TempDir()
	plain, locked, out := filepath.Join(dir, "p.pdf"), filepath.Join(dir, "l.pdf"), filepath.Join(dir, "o.pdf")
	if err := os.WriteFile(plain, []byte(minimalPDF), 0o600); err != nil {
		t.Fatal(err)
	}
	// Exit 3 is qpdf's "succeeded with warnings", expected for the hand-written fixture.
	var exitErr *exitCodeError
	if err := defaultRunTool(context.Background(), "qpdf", []string{"--encrypt", "u", "o", "256", "--", plain, locked}); err != nil && !(errors.As(err, &exitErr) && exitErr.code == 3) {
		t.Fatalf("cannot build an encrypted fixture: %v", err)
	}
	gs := NewGhostscript(nil)
	if err := gs.Compress(context.Background(), locked, out, "light"); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("compress err = %v, want ErrPasswordRequired", err)
	}
	if err := gs.Grayscale(context.Background(), locked, out); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("grayscale err = %v, want ErrPasswordRequired", err)
	}
}

// writeSource writes a file of the given size so checkShrink has something to
// measure against without an actual gs run.
func writeSource(t *testing.T, size int) string {
	t.Helper()
	path := t.TempDir() + "/src.pdf"
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsString(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
