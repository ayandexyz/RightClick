package pdfops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// ErrPasswordIncorrect marks an attempt to unlock with a wrong password.
var ErrPasswordIncorrect = errors.New("incorrect password")

// Unlock decrypts a password-protected PDF or returns the input path unchanged
// for a plain PDF. It returns (path, cleanup, error).
//
// For a plain PDF, path == input and cleanup is a no-op.
// For an encrypted PDF with the correct password, path points to a decrypted
// temporary copy in a private directory (mode 0700) with file mode 0600, and
// cleanup removes that directory. Cleanup is safe to call multiple times and
// after cancellation.
//
// Returns ErrPasswordRequired if the PDF is encrypted and no password is given.
// Returns ErrPasswordIncorrect if the password is wrong.
func Unlock(ctx context.Context, backend *PDFCPU, input, password string) (path string, cleanup func(), err error) {
	// Check if the file is encrypted by trying to validate it
	encrypted, err := isEncrypted(ctx, input)
	if err != nil {
		return "", nil, err
	}

	// Plain PDF: return input path unchanged
	if !encrypted {
		return input, func() {}, nil
	}

	// Encrypted PDF: password required
	if password == "" {
		return "", nil, ErrPasswordRequired
	}

	// Create private temp directory with restricted permissions
	tempDir, err := os.MkdirTemp("", "fc-unlock-")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	if err := os.Chmod(tempDir, 0700); err != nil {
		os.RemoveAll(tempDir)
		return "", nil, fmt.Errorf("chmod temp dir: %w", err)
	}

	tempFile := filepath.Join(tempDir, "unlocked.pdf")
	cleanupFn := func() {
		os.RemoveAll(tempDir)
	}

	// Try to decrypt with the password
	conf := model.NewDefaultConfiguration()
	conf.UserPW = password
	conf.OwnerPW = password

	err = runPDFCPU(ctx, func() error {
		return api.DecryptFile(input, tempFile, conf)
	})

	if err != nil {
		cleanupFn()
		// Classify the error based on spike results from Task 1
		if isWrongPasswordError(err) {
			return "", nil, ErrPasswordIncorrect
		}
		if errors.Is(err, ErrPasswordRequired) {
			// Still encrypted means wrong password per the spike
			return "", nil, ErrPasswordIncorrect
		}
		return "", nil, fmt.Errorf("decrypt: %w", err)
	}

	// Set file permissions to 0600
	if err := os.Chmod(tempFile, 0600); err != nil {
		cleanupFn()
		return "", nil, fmt.Errorf("chmod temp file: %w", err)
	}

	return tempFile, cleanupFn, nil
}

// isEncrypted checks if a PDF is encrypted by attempting to validate it.
func isEncrypted(ctx context.Context, path string) (bool, error) {
	err := runPDFCPU(ctx, func() error {
		conf := model.NewDefaultConfiguration()
		return api.ValidateFile(path, conf)
	})

	if err == nil {
		return false, nil
	}

	// If the error is password-related, the file is encrypted
	if errors.Is(err, ErrPasswordRequired) {
		return true, nil
	}

	// Other errors (context cancellation, damaged file) are real errors
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	// Damaged PDF or other failure
	return false, classifyUnreadable(ctx, err)
}

// isWrongPasswordError checks if an error indicates a wrong password based on
// the spike results from Task 1.
func isWrongPasswordError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Per Task 1 spike: pdfcpu returns "please provide the correct password"
	// for both missing and wrong passwords, but we only call this when a
	// password was provided.
	return strings.Contains(msg, "please provide the correct password")
}

// UnlockInto is Unlock for a caller that owns the directory the unlocked copy
// lives in, such as a connection that keeps one copy for as long as it is open.
// It reports copied=false and returns input unchanged for a plain PDF. The
// directory must already exist and be private; the copy is written 0600.
func UnlockInto(ctx context.Context, input, dir, password string) (path string, copied bool, err error) {
	encrypted, err := isEncrypted(ctx, input)
	if err != nil {
		return "", false, err
	}
	if !encrypted {
		return input, false, nil
	}
	if password == "" {
		return "", false, ErrPasswordRequired
	}
	out := filepath.Join(dir, "unlocked.pdf")
	conf := model.NewDefaultConfiguration()
	conf.UserPW = password
	conf.OwnerPW = password
	if err := runPDFCPU(ctx, func() error { return api.DecryptFile(input, out, conf) }); err != nil {
		_ = os.Remove(out)
		if isWrongPasswordError(err) || errors.Is(err, ErrPasswordRequired) {
			return "", false, ErrPasswordIncorrect
		}
		return "", false, fmt.Errorf("decrypt: %w", err)
	}
	if err := os.Chmod(out, 0o600); err != nil {
		_ = os.Remove(out)
		return "", false, err
	}
	return out, true, nil
}
