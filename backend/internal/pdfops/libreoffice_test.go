package pdfops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindLibreOfficeBinaryPrefersSofficeAndFallsBackToLibreoffice(t *testing.T) {
	lookPath := func(names map[string]string) func(string) (string, error) {
		return func(name string) (string, error) {
			if path, ok := names[name]; ok {
				return path, nil
			}
			return "", errors.New("not found")
		}
	}
	if got, err := findLibreOfficeBinary(lookPath(map[string]string{"soffice": "/usr/bin/soffice", "libreoffice": "/usr/bin/libreoffice"})); err != nil || got != "soffice" {
		t.Fatalf("preferred binary = %q, %v; want soffice", got, err)
	}
	if got, err := findLibreOfficeBinary(lookPath(map[string]string{"libreoffice": "/usr/bin/libreoffice"})); err != nil || got != "libreoffice" {
		t.Fatalf("fallback binary = %q, %v; want libreoffice", got, err)
	}
	if _, err := findLibreOfficeBinary(lookPath(nil)); err == nil {
		t.Fatal("missing binaries unexpectedly succeeded")
	}
}

func TestLibreOfficeConvertUsesPrivateProfileAndCleansIt(t *testing.T) {
	var profile string
	runner := func(_ context.Context, name string, args []string) error {
		if name != "soffice" {
			t.Fatalf("runner name = %q, want soffice", name)
		}
		for _, arg := range args {
			if strings.HasPrefix(arg, "-env:UserInstallation=file://") {
				profile = strings.TrimPrefix(arg, "-env:UserInstallation=file://")
			}
		}
		if profile == "" {
			t.Fatal("missing private profile argument")
		}
		info, err := os.Stat(profile)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("profile permissions = %o, want 700", info.Mode().Perm())
		}
		return nil
	}
	engine := NewLibreOffice(runner)
	input := filepath.Join(t.TempDir(), "report.docx")
	output := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(input, []byte("office"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := engine.Convert(context.Background(), input, output); err == nil {
		t.Fatal("Convert succeeded without a generated PDF")
	}
	if profile == "" {
		t.Fatal("runner did not observe profile")
	}
	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile still exists after failure: %v", err)
	}
}

func TestLibreOfficeConvertCleansProfileOnSuccess(t *testing.T) {
	var profile string
	runner := func(_ context.Context, _ string, args []string) error {
		var outDir string
		var input string
		for i, arg := range args {
			if strings.HasPrefix(arg, "-env:UserInstallation=file://") {
				profile = strings.TrimPrefix(arg, "-env:UserInstallation=file://")
			}
			if arg == "--outdir" {
				outDir = args[i+1]
			}
			if arg == "--" {
				input = args[i+1]
			}
		}
		return os.WriteFile(filepath.Join(outDir, strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))+".pdf"), []byte("%PDF-1.7\n"), 0o600)
	}
	engine := NewLibreOffice(runner)
	input := filepath.Join(t.TempDir(), "report.docx")
	output := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(input, []byte("office"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := engine.Convert(context.Background(), input, output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile still exists after success: %v", err)
	}
}

func TestLibreOfficeConvertAppliesTimeout(t *testing.T) {
	started := make(chan struct{})
	runner := func(ctx context.Context, _ string, _ []string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	engine := NewLibreOffice(runner)
	input := filepath.Join(t.TempDir(), "report.docx")
	if err := os.WriteFile(input, []byte("office"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- engine.Convert(ctx, input, filepath.Join(t.TempDir(), "report.pdf")) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Convert error = %v, want cancellation", err)
	}
}

func TestLibreOfficeConvertRejectsInvalidGeneratedPDF(t *testing.T) {
	runner := func(_ context.Context, _ string, args []string) error {
		var outDir, input string
		for i, arg := range args {
			if arg == "--outdir" {
				outDir = args[i+1]
			}
			if arg == "--" {
				input = args[i+1]
			}
		}
		return os.WriteFile(filepath.Join(outDir, strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))+".pdf"), []byte("not a pdf"), 0o600)
	}
	input := filepath.Join(t.TempDir(), "report.docx")
	if err := os.WriteFile(input, []byte("office"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "report.pdf")
	if err := NewLibreOffice(runner).Convert(context.Background(), input, output); err == nil {
		t.Fatal("invalid generated PDF unexpectedly succeeded")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid output was published: %v", err)
	}
}

func TestLibreOfficeConvertCleansProfileOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var profile string
	runner := func(_ context.Context, _ string, args []string) error {
		for _, arg := range args {
			if strings.HasPrefix(arg, "-env:UserInstallation=file://") {
				profile = strings.TrimPrefix(arg, "-env:UserInstallation=file://")
			}
		}
		cancel()
		return ctx.Err()
	}
	engine := NewLibreOffice(runner)
	input := filepath.Join(t.TempDir(), "report.docx")
	if err := os.WriteFile(input, []byte("office"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := engine.Convert(ctx, input, filepath.Join(t.TempDir(), "report.pdf")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Convert error = %v, want cancellation", err)
	}
	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile still exists after cancellation: %v", err)
	}
}

func TestToolboxMissingLibreOfficeOnlyDisablesOffice(t *testing.T) {
	tb := Toolbox{PDFCPU: &fakeBackend{}}
	office, ok := Lookup("office-to-pdf")
	if !ok {
		t.Fatal("office-to-pdf missing from catalogue")
	}
	if got := office.UnavailableReason(tb, []string{"report.docx"}); got != "libreoffice is not installed" {
		t.Fatalf("office reason = %q", got)
	}
	pdf, _ := Lookup("pdf-reverse")
	if got := pdf.UnavailableReason(tb, []string{"report.pdf"}); got != "" {
		t.Fatalf("pdf-reverse unexpectedly unavailable: %q", got)
	}
}
