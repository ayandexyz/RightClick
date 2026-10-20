package pdfops

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// ErrPasswordRequired marks an encrypted PDF. Passwords arrive in a later
// phase, so the job layer turns this into a distinct error code.
var ErrPasswordRequired = errors.New("pdf is encrypted")

// ErrInvalidPDF marks a file that cannot be read as a PDF at all: damaged,
// truncated, or not a PDF. The job layer turns this into the invalid_pdf code.
var ErrInvalidPDF = errors.New("pdf is damaged or not a valid PDF")

// InputError names the input a multi-file operation could not read. Merge uses
// it to attribute a failure to one file instead of the whole selection.
type InputError struct {
	Path string
	Err  error
}

func (e *InputError) Error() string {
	return fmt.Sprintf("%s: %v", filepath.Base(e.Path), e.Err)
}

func (e *InputError) Unwrap() error { return e.Err }

// pdfcpu reports an unreadable encrypted file either by refusing outright or by
// asking for the password, depending on which code path reads it.
var pdfcpuEncrypted = []string{
	"this file is encrypted",
	"please provide the correct password",
}

// PDFCPU implements Backend with pdfcpu, which is linked in rather than
// shelled out to, so it is always available.
type PDFCPU struct{}

// NewPDFCPUBackend returns the pdfcpu-backed Backend.
func NewPDFCPUBackend() *PDFCPU {
	disableConfigDirOnce()
	return &PDFCPU{}
}

var disableConfigDirOnce = sync.OnceFunc(api.DisableConfigDir)

func (b *PDFCPU) ID() string { return "pdfcpu" }

// Available always succeeds: pdfcpu is compiled in. Configuration-directory
// creation is disabled at construction so the daemon never writes to the
// user's home directory.
func (b *PDFCPU) Available() error { return nil }

func (b *PDFCPU) PageCount(ctx context.Context, path string) (int, error) {
	var count int
	if err := runPDFCPU(ctx, func() error {
		var err error
		count, err = api.PageCountFile(path)
		return err
	}); err != nil {
		return 0, err
	}
	return count, nil
}

func (b *PDFCPU) Merge(ctx context.Context, inputs []string, output string) error {
	if len(inputs) < 2 {
		return fmt.Errorf("merge needs at least 2 files")
	}
	return runPDFCPU(ctx, func() error {
		return api.MergeCreateFile(inputs, output, false, model.NewDefaultConfiguration())
	})
}

func (b *PDFCPU) Collect(ctx context.Context, input, output string, pages []int) error {
	if len(pages) == 0 {
		return fmt.Errorf("collect needs at least one page")
	}
	selection := make([]string, len(pages))
	for i, page := range pages {
		selection[i] = strconv.Itoa(page)
	}
	return runPDFCPU(ctx, func() error {
		return api.CollectFile(input, output, selection, model.NewDefaultConfiguration())
	})
}

func (b *PDFCPU) Rotate(ctx context.Context, input, output string, pages []int, angle int) error {
	switch angle {
	case 90, 180, 270:
	default:
		return fmt.Errorf("rotate needs an angle of 90, 180 or 270")
	}
	var selection []string
	if len(pages) > 0 {
		selection = make([]string, len(pages))
		for i, page := range pages {
			selection[i] = strconv.Itoa(page)
		}
	}
	return runPDFCPU(ctx, func() error {
		// pdfcpu's rotation is already clockwise-positive, which is what the
		// user-facing angle means, so it passes straight through.
		return api.RotateFile(input, output, angle, selection, model.NewDefaultConfiguration())
	})
}

// runPDFCPU runs one pdfcpu call, converting a panic into an error and
// reporting a cancelled context. pdfcpu's API is not context-aware, so
// cancellation abandons the call rather than stopping it; the goroutine is
// left to finish writing its output, which the job layer's rollback removes.
func runPDFCPU(ctx context.Context, call func() (err error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- fmt.Errorf("pdfcpu panicked: %v", recovered)
			}
		}()
		done <- call()
	}()
	select {
	case err := <-done:
		return pdfcpuError(err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pdfcpuError maps pdfcpu's error text onto the sentinel the job layer needs.
func pdfcpuError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, encrypted := range pdfcpuEncrypted {
		if strings.Contains(message, encrypted) {
			return ErrPasswordRequired
		}
	}
	return err
}
