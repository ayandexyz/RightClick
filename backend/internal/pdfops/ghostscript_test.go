package pdfops

import (
	"context"
	"errors"
	"os"
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
	gs := NewGhostscript(rec.run)
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

func TestGhostscriptRejectsUnknownPreset(t *testing.T) {
	gs := NewGhostscript(func(_ context.Context, _ string, _ []string) error { return nil })
	err := gs.Compress(context.Background(), "in.pdf", "out.pdf", "extreme")
	if err == nil || !strings.Contains(err.Error(), "extreme") {
		t.Fatalf("err = %v, want it to name the preset", err)
	}
}

func TestGhostscriptGrayscaleFlags(t *testing.T) {
	rec := &recordRunner{err: nil}
	gs := NewGhostscript(rec.run)
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
	gs := NewGhostscript(func(_ context.Context, _ string, _ []string) error {
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
	gs := NewGhostscript(func(_ context.Context, _ string, _ []string) error {
		if err := os.WriteFile(out, make([]byte, 512), 0o600); err != nil {
			return err
		}
		return nil
	})
	if err := gs.Compress(context.Background(), in, out, "light"); err != nil {
		t.Fatalf("err = %v, want nil", err)
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
