package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

func TestPDFSupportsOnlyPDFToRasterTargets(t *testing.T) {
	e := NewPDF(nil)
	for _, test := range []struct {
		source, target formats.Format
		want           bool
	}{
		{formats.PDF, formats.PNG, true},
		{formats.PDF, formats.JPEG, true},
		{formats.PDF, formats.WebP, true},
		{formats.PNG, formats.PNG, false},
		{formats.PDF, formats.PDF, false},
	} {
		if got := e.Supports(test.source, test.target); got != test.want {
			t.Errorf("Supports(%q, %q) = %v, want %v", test.source, test.target, got, test.want)
		}
	}
	if e.ID() != "pdf" {
		t.Fatalf("ID() = %q, want pdf", e.ID())
	}
}

func TestPDFAvailableReportsMissingExecutable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := NewPDF(nil).Available(); err == nil {
		t.Fatal("Available() succeeded without pdftocairo")
	}
}

func TestPDFConvertManyRejectsUnsafeAndNonPDFSources(t *testing.T) {
	dir := t.TempDir()
	unsafe := filepath.Join(dir, "bad[1].pdf")
	if err := os.WriteFile(unsafe, []byte("%PDF-1.7"), 0o600); err != nil {
		t.Fatal(err)
	}
	notPDF := filepath.Join(dir, "plain.pdf")
	if err := os.WriteFile(notPDF, []byte("not a PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := NewPDF(func(context.Context, string, []string) error {
		t.Fatal("runner called for invalid source")
		return nil
	})
	for _, source := range []string{unsafe, notPDF} {
		if _, err := e.ConvertMany(context.Background(), source, formats.PDF, filepath.Join(dir, "render"), formats.PNG, protocol.ImageOptions{}); err == nil {
			t.Fatalf("ConvertMany(%q) succeeded", source)
		}
	}
	if _, err := e.ConvertMany(context.Background(), notPDF, formats.PNG, filepath.Join(dir, "render"), formats.PNG, protocol.ImageOptions{}); err == nil {
		t.Fatal("ConvertMany accepted a non-PDF source format")
	}
}

func TestPDFConvertManyUsesExactArgumentsAndOrdersPages(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "report $(touch nope); final.pdf")
	if err := os.WriteFile(source, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tempDir := filepath.Join(root, ".file-converter-render-output; safe")
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var got []string
	e := NewPDF(func(_ context.Context, executable string, args []string) error {
		if executable != "pdftocairo" {
			t.Fatalf("executable = %q, want pdftocairo", executable)
		}
		got = append([]string(nil), args...)
		prefix := args[len(args)-1]
		for _, page := range []string{"10", "2", "1"} {
			if err := os.WriteFile(prefix+"-"+page+".png", []byte(page), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(tempDir, "page-not-a-page.png"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tempDir, "other-3.png"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return nil
	})

	outputs, err := e.ConvertMany(context.Background(), source, formats.PDF, tempDir, formats.PNG, protocol.ImageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"-r", "150", "-png", "--", source, filepath.Join(tempDir, "page")}
	if !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("arguments = %#v, want %#v", got, wantArgs)
	}
	wantOutputs := []string{
		filepath.Join(tempDir, "page-1.png"),
		filepath.Join(tempDir, "page-2.png"),
		filepath.Join(tempDir, "page-10.png"),
	}
	if !reflect.DeepEqual(outputs, wantOutputs) {
		t.Fatalf("outputs = %#v, want %#v", outputs, wantOutputs)
	}
}

func TestPDFConvertManyUsesJPEGRendererFlag(t *testing.T) {
	dir, source := pdfTestInput(t)
	var got []string
	e := NewPDF(func(_ context.Context, _ string, args []string) error {
		got = append([]string(nil), args...)
		return os.WriteFile(args[len(args)-1]+"-1.jpg", []byte("jpg"), 0o600)
	})
	if _, err := e.ConvertMany(context.Background(), source, formats.PDF, dir, formats.JPEG, protocol.ImageOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"-r", "150", "-jpeg", "--", source, filepath.Join(dir, "page")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v, want %#v", got, want)
	}
}

func TestPDFConvertManyRendersPNGThenConvertsOrderedWebPPages(t *testing.T) {
	dir, source := pdfTestInput(t)
	var calls [][]string
	e := NewPDF(func(_ context.Context, executable string, args []string) error {
		calls = append(calls, append([]string{executable}, args...))
		if executable == "pdftocairo" {
			prefix := args[len(args)-1]
			if err := os.WriteFile(prefix+"-2.png", []byte("2"), 0o600); err != nil {
				return err
			}
			return os.WriteFile(prefix+"-1.png", []byte("1"), 0o600)
		}
		destination := args[len(args)-1]
		return os.WriteFile(destination[len("webp:"):], []byte("webp"), 0o600)
	})

	outputs, err := e.ConvertMany(context.Background(), source, formats.PDF, dir, formats.WebP, protocol.ImageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[0][0] != "pdftocairo" {
		t.Fatalf("calls = %#v", calls)
	}
	want := []string{filepath.Join(dir, "page-1.webp"), filepath.Join(dir, "page-2.webp")}
	if !reflect.DeepEqual(outputs, want) {
		t.Fatalf("outputs = %#v, want %#v", outputs, want)
	}
	for i, call := range calls[1:] {
		input := "png:" + filepath.Join(dir, "page-"+string(rune('1'+i))+".png")
		if call[0] != "magick" || !containsArgument(call, input) || call[len(call)-1] != "webp:"+want[i] {
			t.Fatalf("ImageMagick call %d = %#v", i, call)
		}
	}
	for _, intermediate := range []string{filepath.Join(dir, "page-1.png"), filepath.Join(dir, "page-2.png")} {
		if _, err := os.Stat(intermediate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("intermediate %q was not removed", intermediate)
		}
	}
}

func TestPDFConvertManyRejectsEmptyOutputAndCleansRendererFilesOnError(t *testing.T) {
	dir, source := pdfTestInput(t)
	// A runner error after creating a partial output must remove that output.
	e := NewPDF(func(_ context.Context, _ string, args []string) error {
		if err := os.WriteFile(args[len(args)-1]+"-1.png", []byte("partial"), 0o600); err != nil {
			return err
		}
		return errors.New("renderer failed")
	})
	if _, err := e.ConvertMany(context.Background(), source, formats.PDF, dir, formats.PNG, protocol.ImageOptions{}); err == nil {
		t.Fatal("ConvertMany succeeded after renderer failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "page-1.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial renderer output was not removed")
	}

	e.runner = func(context.Context, string, []string) error { return nil }
	if _, err := e.ConvertMany(context.Background(), source, formats.PDF, dir, formats.PNG, protocol.ImageOptions{}); err == nil {
		t.Fatal("ConvertMany accepted empty renderer output")
	}
}

func TestPDFConvertManyRejectsUncontrolledSymlinkAndNonEmptyTempDirs(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "document.pdf")
	if err := os.WriteFile(source, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controlled := filepath.Join(root, ".file-converter-render-owned")
	if err := os.Mkdir(controlled, 0o700); err != nil {
		t.Fatal(err)
	}
	preexisting := filepath.Join(controlled, "page-1.png")
	if err := os.WriteFile(preexisting, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	uncontrolled := filepath.Join(root, "render-owned")
	if err := os.Mkdir(uncontrolled, 0o700); err != nil {
		t.Fatal(err)
	}
	insecure := filepath.Join(root, ".file-converter-render-insecure")
	if err := os.Mkdir(insecure, 0o755); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(root, ".file-converter-render-real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, ".file-converter-render-link")
	if err := os.Symlink(realDir, symlink); err != nil {
		t.Fatal(err)
	}

	called := false
	e := NewPDF(func(context.Context, string, []string) error {
		called = true
		return nil
	})
	for _, dir := range []string{uncontrolled, insecure, symlink, controlled} {
		if _, err := e.ConvertMany(context.Background(), source, formats.PDF, dir, formats.PNG, protocol.ImageOptions{}); err == nil {
			t.Errorf("ConvertMany accepted temporary directory %q", dir)
		}
	}
	if called {
		t.Fatal("runner called for rejected temporary directory")
	}
	contents, err := os.ReadFile(preexisting)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("pre-existing page changed: contents=%q, err=%v", contents, err)
	}
}

func TestPDFConvertManyEnforcesPageLimitAndCleansOutputs(t *testing.T) {
	dir, source := pdfTestInput(t)
	e := NewPDF(func(_ context.Context, _ string, args []string) error {
		prefix := args[len(args)-1]
		for page := 1; page <= maxPDFPages+1; page++ {
			if err := os.WriteFile(fmt.Sprintf("%s-%d.png", prefix, page), nil, 0o600); err != nil {
				return err
			}
		}
		return nil
	})
	if _, err := e.ConvertMany(context.Background(), source, formats.PDF, dir, formats.PNG, protocol.ImageOptions{}); err == nil {
		t.Fatal("ConvertMany accepted more than the maximum page count")
	}
	if matches, err := filepath.Glob(filepath.Join(dir, "page-*.png")); err != nil || len(matches) != 0 {
		t.Fatalf("renderer outputs after page limit failure = %v, err=%v", matches, err)
	}
}

func TestPDFConvertManyCleansWebPOutputsAfterMidSequenceFailure(t *testing.T) {
	dir, source := pdfTestInput(t)
	e := NewPDF(func(_ context.Context, executable string, args []string) error {
		if executable == "pdftocairo" {
			prefix := args[len(args)-1]
			if err := os.WriteFile(prefix+"-1.png", []byte("1"), 0o600); err != nil {
				return err
			}
			return os.WriteFile(prefix+"-2.png", []byte("2"), 0o600)
		}
		if strings.HasSuffix(args[len(args)-1], "page-2.webp") {
			return errors.New("webp conversion failed")
		}
		destination := strings.TrimPrefix(args[len(args)-1], "webp:")
		return os.WriteFile(destination, []byte("webp"), 0o600)
	})
	if _, err := e.ConvertMany(context.Background(), source, formats.PDF, dir, formats.WebP, protocol.ImageOptions{}); err == nil {
		t.Fatal("ConvertMany succeeded after a mid-sequence WebP failure")
	}
	for _, pattern := range []string{"page-*.png", "page-*.webp"} {
		if matches, err := filepath.Glob(filepath.Join(dir, pattern)); err != nil || len(matches) != 0 {
			t.Fatalf("outputs matching %q after failure = %v, err=%v", pattern, matches, err)
		}
	}
}

func TestPDFConvertManyConcurrentCallsDoNotMutateEngineState(t *testing.T) {
	root := t.TempDir()
	e := NewPDF(func(_ context.Context, executable string, args []string) error {
		if executable == "pdftocairo" {
			return os.WriteFile(args[len(args)-1]+"-1.png", []byte("png"), 0o600)
		}
		destination := strings.TrimPrefix(args[len(args)-1], "webp:")
		return os.WriteFile(destination, []byte("webp"), 0o600)
	})
	const conversions = 12
	var wg sync.WaitGroup
	errs := make(chan error, conversions)
	for i := 0; i < conversions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dir := filepath.Join(root, fmt.Sprintf(".file-converter-render-%d", i))
			if err := os.Mkdir(dir, 0o700); err != nil {
				errs <- err
				return
			}
			source := filepath.Join(root, fmt.Sprintf("document-%d.pdf", i))
			if err := os.WriteFile(source, []byte("%PDF-1.7\n"), 0o600); err != nil {
				errs <- err
				return
			}
			_, err := e.ConvertMany(context.Background(), source, formats.PDF, dir, formats.WebP, protocol.ImageOptions{})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func containsArgument(arguments []string, want string) bool {
	for _, argument := range arguments {
		if argument == want {
			return true
		}
	}
	return false
}

func pdfTestInput(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".file-converter-render-test")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "document.pdf")
	if err := os.WriteFile(source, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, source
}
