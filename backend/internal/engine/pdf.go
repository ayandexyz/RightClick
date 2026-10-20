package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

const maxPDFPages = 1000

type PDF struct {
	runner Runner
}

func NewPDF(runner Runner) *PDF { return &PDF{runner: runner} }

func (e *PDF) ID() string { return "pdf" }

func (e *PDF) Available() (string, error) {
	path, err := exec.LookPath("pdftocairo")
	if err != nil {
		return "", fmt.Errorf("pdftocairo not found: %w", err)
	}
	return path, nil
}

func (e *PDF) Supports(source, target formats.Format) bool {
	if source != formats.PDF {
		return false
	}
	switch target {
	case formats.PNG, formats.JPEG, formats.WebP:
		return true
	default:
		return false
	}
}

func (e *PDF) Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error {
	return fmt.Errorf("PDF conversion produces multiple outputs")
}

func (e *PDF) ConvertMany(ctx context.Context, source string, sourceFormat formats.Format, tempDir string, target formats.Format, options protocol.ImageOptions) (outputs []string, err error) {
	if !e.Supports(sourceFormat, target) {
		return nil, fmt.Errorf("unsupported PDF conversion: %s to %s", sourceFormat, target)
	}
	if err := validatePDF(source); err != nil {
		return nil, fmt.Errorf("validate PDF: %w", err)
	}
	if err := validatePDFTempDir(tempDir); err != nil {
		return nil, err
	}

	executable := "pdftocairo"
	if e.runner == nil {
		executable, err = e.Available()
		if err != nil {
			return nil, err
		}
	}

	renderTarget := target
	if target == formats.WebP {
		renderTarget = formats.PNG
	}
	suffix, flag := ".png", "-png"
	if renderTarget == formats.JPEG {
		suffix, flag = ".jpg", "-jpeg"
	}
	prefix := filepath.Join(tempDir, "page")
	defer func() {
		if err != nil {
			removePDFOutputs(tempDir, suffix)
			if target == formats.WebP {
				removePDFOutputs(tempDir, ".webp")
			}
		}
	}()

	args := []string{"-r", "150", flag, "--", source, prefix}
	if e.runner != nil {
		err = e.runner(ctx, executable, args)
	} else {
		err = runCommand(ctx, executable, args)
	}
	if err != nil {
		return nil, fmt.Errorf("pdftocairo failed: %w", err)
	}

	pages, err := collectPDFOutputs(tempDir, suffix)
	if err != nil {
		return nil, err
	}
	if target != formats.WebP {
		return pages, nil
	}

	defer removePDFOutputs(tempDir, ".png")
	imageMagick := NewImageMagick(e.runner)
	if e.runner != nil {
		imageMagick.executable = "magick"
	}
	outputs = make([]string, 0, len(pages))
	for _, page := range pages {
		destination := strings.TrimSuffix(page, ".png") + ".webp"
		if err = imageMagick.Convert(ctx, page, formats.PNG, destination, formats.WebP, options); err != nil {
			return nil, fmt.Errorf("convert rendered PNG to WebP: %w", err)
		}
		outputs = append(outputs, destination)
	}
	return outputs, nil
}

func validatePDF(path string) error {
	if err := formats.ValidateInput(path); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 5)
	if _, err := io.ReadFull(file, header); err != nil {
		return fmt.Errorf("read PDF header: %w", err)
	}
	if string(header) != "%PDF-" {
		return fmt.Errorf("missing PDF header")
	}
	return nil
}

func validatePDFTempDir(path string) error {
	if err := formats.ValidatePath(path); err != nil {
		return fmt.Errorf("unsafe PDF temporary directory: %w", err)
	}
	if !strings.HasPrefix(filepath.Base(path), ".file-converter-render-") {
		return fmt.Errorf("uncontrolled PDF temporary directory")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect PDF temporary directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("PDF temporary path is not a real directory")
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("PDF temporary directory is not private")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read PDF temporary directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("PDF temporary directory is not empty")
	}
	return nil
}

type pdfPage struct {
	index int
	path  string
}

func collectPDFOutputs(dir, suffix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read PDF outputs: %w", err)
	}
	pages := make([]pdfPage, 0)
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			index, ok := pdfPageIndex(entry.Name(), suffix)
			if ok {
				pages = append(pages, pdfPage{index: index, path: filepath.Join(dir, entry.Name())})
			}
		}
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("pdftocairo produced no pages")
	}
	if len(pages) > maxPDFPages {
		return nil, fmt.Errorf("PDF exceeds maximum page count of %d", maxPDFPages)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].index < pages[j].index })
	outputs := make([]string, len(pages))
	for i, page := range pages {
		outputs[i] = page.path
	}
	return outputs, nil
}

func pdfPageIndex(name, suffix string) (int, bool) {
	if !strings.HasPrefix(name, "page-") || !strings.HasSuffix(name, suffix) {
		return 0, false
	}
	number := strings.TrimSuffix(strings.TrimPrefix(name, "page-"), suffix)
	if number == "" {
		return 0, false
	}
	for _, char := range number {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	index, err := strconv.Atoi(number)
	return index, err == nil && index > 0
}

func removePDFOutputs(dir, suffix string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if _, ok := pdfPageIndex(entry.Name(), suffix); ok {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
