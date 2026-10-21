package pdfops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LibreOffice converts Office documents using an isolated, disposable user
// profile so concurrent jobs never touch the user's LibreOffice session.
type LibreOffice struct {
	runner Runner
}

func NewLibreOffice(runner Runner) *LibreOffice {
	if runner == nil {
		runner = defaultRunTool
	}
	return &LibreOffice{runner: runner}
}

func (l *LibreOffice) ID() string { return EngineLibreOffice }

func findLibreOfficeBinary(lookPath func(string) (string, error)) (string, error) {
	for _, name := range []string{"soffice", "libreoffice"} {
		if _, err := lookPath(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("libreoffice is not installed")
}

func (l *LibreOffice) Available() error {
	_, err := findLibreOfficeBinary(exec.LookPath)
	return err
}

const libreOfficeTimeout = 120 * time.Second

func (l *LibreOffice) Convert(ctx context.Context, input, output string) error {
	binary, err := findLibreOfficeBinary(exec.LookPath)
	if err != nil {
		return err
	}
	convertCtx, cancel := context.WithTimeout(ctx, libreOfficeTimeout)
	defer cancel()
	if err := convertCtx.Err(); err != nil {
		return err
	}
	profile, err := os.MkdirTemp("", "file-converter-lo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profile)
	outDir, err := os.MkdirTemp("", "file-converter-lo-out-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(outDir)
	args := []string{"--headless", "--norestore", "--convert-to", "pdf", "--outdir", outDir,
		"-env:UserInstallation=file://" + profile, "--", input}
	if err := l.runner(convertCtx, binary, args); err != nil {
		if convertCtx.Err() != nil {
			return convertCtx.Err()
		}
		return fmt.Errorf("soffice convert: %w", err)
	}
	generated := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))+".pdf")
	data, err := os.ReadFile(generated)
	if err != nil {
		return fmt.Errorf("soffice produced no PDF: %w", err)
	}
	if len(data) < len("%PDF-") || string(data[:len("%PDF-")]) != "%PDF-" {
		return fmt.Errorf("soffice produced an invalid PDF")
	}
	if err := os.WriteFile(output, data, 0o600); err != nil {
		return fmt.Errorf("publish soffice PDF: %w", err)
	}
	return nil
}
