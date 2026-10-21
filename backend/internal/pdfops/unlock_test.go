package pdfops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func TestUnlockPlainPDFReturnsInputPath(t *testing.T) {
	b := NewPDFCPUBackend()
	plain := writeFixture(t, t.TempDir(), "plain.pdf", 1)

	got, cleanup, err := Unlock(context.Background(), b, plain, "")
	if err != nil {
		t.Fatalf("Unlock plain PDF: %v", err)
	}
	defer cleanup()

	if got != plain {
		t.Errorf("Unlock plain PDF path = %q, want %q", got, plain)
	}

	// Cleanup should be a no-op for plain PDFs
	cleanup()
}

func TestUnlockEncryptedPDFWithCorrectPassword(t *testing.T) {
	b := NewPDFCPUBackend()
	encrypted := createEncryptedFixture(t, "secret123")

	got, cleanup, err := Unlock(context.Background(), b, encrypted, "secret123")
	if err != nil {
		t.Fatalf("Unlock encrypted PDF with correct password: %v", err)
	}
	defer cleanup()

	// Should return a different path (the decrypted temp copy)
	if got == encrypted {
		t.Errorf("Unlock encrypted PDF returned input path, want temp copy")
	}

	// Verify the returned file exists and is readable
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("temp file does not exist: %v", err)
	}

	// Verify it's a valid PDF by checking page count
	count, err := b.PageCount(context.Background(), got)
	if err != nil {
		t.Fatalf("unlocked PDF PageCount: %v", err)
	}
	if count != 1 {
		t.Errorf("unlocked PDF page count = %d, want 1", count)
	}

	// Check directory permissions are 0700
	dir := filepath.Dir(got)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat temp dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Errorf("temp dir permissions = %o, want 0700", perm)
	}

	// Check file permissions are 0600
	fileInfo, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat temp file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0600 {
		t.Errorf("temp file permissions = %o, want 0600", perm)
	}

	// Cleanup should remove the directory
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("cleanup did not remove directory")
	}
}

func TestUnlockWrongPasswordReturnsPasswordIncorrect(t *testing.T) {
	b := NewPDFCPUBackend()
	encrypted := createEncryptedFixture(t, "secret123")

	_, cleanup, err := Unlock(context.Background(), b, encrypted, "wrongpass")
	if cleanup != nil {
		defer cleanup()
	}

	if !errors.Is(err, ErrPasswordIncorrect) {
		t.Fatalf("Unlock with wrong password: got %v, want ErrPasswordIncorrect", err)
	}
}

func TestUnlockEncryptedPDFWithoutPasswordReturnsPasswordRequired(t *testing.T) {
	b := NewPDFCPUBackend()
	encrypted := createEncryptedFixture(t, "secret123")

	_, cleanup, err := Unlock(context.Background(), b, encrypted, "")
	if cleanup != nil {
		defer cleanup()
	}

	if !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("Unlock encrypted PDF without password: got %v, want ErrPasswordRequired", err)
	}
}

func TestUnlockCleanupOnCancellation(t *testing.T) {
	b := NewPDFCPUBackend()
	encrypted := createEncryptedFixture(t, "secret123")

	ctx, cancel := context.WithCancel(context.Background())

	unlocked, cleanup, err := Unlock(ctx, b, encrypted, "secret123")
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	dir := filepath.Dir(unlocked)

	// Cancel the context
	cancel()

	// Cleanup should still work
	cleanup()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("cleanup after cancel did not remove directory")
	}
}

func TestUnlockPasswordNotInArguments(t *testing.T) {
	// This test will be expanded in the e2e test file to check a full operation
	// For now, just verify that Unlock itself doesn't expose the password
	// (implementation detail: pdfcpu uses it in Configuration, not argv)

	b := NewPDFCPUBackend()
	encrypted := createEncryptedFixture(t, "SENTINEL_PASSWORD_12345")

	unlocked, cleanup, err := Unlock(context.Background(), b, encrypted, "SENTINEL_PASSWORD_12345")
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	defer cleanup()

	// The unlocked file should exist and be readable without the password
	count, err := b.PageCount(context.Background(), unlocked)
	if err != nil {
		t.Fatalf("unlocked PDF not readable: %v", err)
	}
	if count != 1 {
		t.Errorf("page count = %d, want 1", count)
	}

	// The password should not appear in the unlocked file path
	if strings.Contains(unlocked, "SENTINEL") {
		t.Errorf("password appears in unlocked path: %s", unlocked)
	}
}

// createEncryptedFixture creates a password-protected PDF for testing.
func createEncryptedFixture(t *testing.T, password string) string {
	t.Helper()

	dir := t.TempDir()
	plain := writeFixture(t, dir, "plain.pdf", 1)
	encrypted := filepath.Join(dir, "encrypted.pdf")

	// Use pdfcpu to encrypt the file
	conf := model.NewDefaultConfiguration()
	conf.UserPW = password
	conf.OwnerPW = password // pdfcpu requires owner password
	conf.EncryptUsingAES = true
	conf.EncryptKeyLength = 256

	err := runPDFCPU(context.Background(), func() error {
		return api.EncryptFile(plain, encrypted, conf)
	})
	if err != nil {
		t.Fatalf("create encrypted fixture: %v", err)
	}

	return encrypted
}
