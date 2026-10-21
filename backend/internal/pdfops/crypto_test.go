package pdfops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDecryptOperation tests pdf-decrypt removes password protection.
func TestDecryptOperation(t *testing.T) {
	b := NewPDFCPUBackend()
	encrypted := createEncryptedFixture(t, "testpass")
	output := filepath.Join(t.TempDir(), "unlocked.pdf")

	params := Params{Password: "testpass"}
	err := b.Decrypt(context.Background(), encrypted, output, params)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	// Verify output exists
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("output file missing: %v", err)
	}

	// Verify the output can be opened without a password
	count, err := b.PageCount(context.Background(), output)
	if err != nil {
		t.Fatalf("decrypted PDF still requires password: %v", err)
	}
	if count != 1 {
		t.Errorf("page count = %d, want 1", count)
	}
}

// TestDecryptRequiresPassword verifies decrypt fails without password.
func TestDecryptRequiresPassword(t *testing.T) {
	b := NewPDFCPUBackend()
	encrypted := createEncryptedFixture(t, "testpass")
	output := filepath.Join(t.TempDir(), "unlocked.pdf")

	params := Params{} // no password
	err := b.Decrypt(context.Background(), encrypted, output, params)
	if err == nil {
		t.Fatal("Decrypt without password should fail")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Errorf("error should mention password, got: %v", err)
	}
}

// TestEncryptWithUserAndOwnerPasswords tests pdf-encrypt with both passwords.
func TestEncryptWithUserAndOwnerPasswords(t *testing.T) {
	skipIfMissingQPDF(t)

	b := NewPDFCPUBackend()
	plain := writeFixture(t, t.TempDir(), "plain.pdf", 2)
	output := filepath.Join(t.TempDir(), "protected.pdf")

	params := EncryptParams{
		User:  "userpass",
		Owner: "ownerpass",
	}
	err := b.Encrypt(context.Background(), plain, output, params)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Verify output exists
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("output file missing: %v", err)
	}

	// Verify encryption with qpdf (AESv3 is AES-256)
	encryption := getEncryptionInfo(t, output, "userpass")
	if !strings.Contains(encryption, "AESv3") && !strings.Contains(encryption, "AES-256") {
		t.Errorf("expected AES-256 encryption (AESv3), got: %s", encryption)
	}

	// Verify it needs a password (PageCount returns ErrPasswordRequired)
	_, err = b.PageCount(context.Background(), output)
	if !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("encrypted file should require password, got err: %v", err)
	}

	// Verify it opens with the user password
	unlocked, cleanup, err := Unlock(context.Background(), b, output, "userpass")
	if err != nil {
		t.Fatalf("unlock with user password: %v", err)
	}
	defer cleanup()

	count, err := b.PageCount(context.Background(), unlocked)
	if err != nil {
		t.Fatalf("read unlocked: %v", err)
	}
	if count != 2 {
		t.Errorf("page count = %d, want 2", count)
	}
}

// TestEncryptOwnerOnly tests encryption with only owner password.
func TestEncryptOwnerOnly(t *testing.T) {
	b := NewPDFCPUBackend()
	plain := writeFixture(t, t.TempDir(), "plain.pdf", 2)
	output := filepath.Join(t.TempDir(), "restricted.pdf")

	params := EncryptParams{
		Owner: "ownerpass",
	}
	err := b.Encrypt(context.Background(), plain, output, params)
	if err != nil {
		t.Fatalf("Encrypt with owner only: %v", err)
	}

	// Per spike results: owner-only encryption with empty user password
	// means the file opens without a password but restrictions are enforced
	count, err := b.PageCount(context.Background(), output)
	if err != nil {
		t.Fatalf("owner-only encrypted file should open without password: %v", err)
	}
	if count != 2 {
		t.Errorf("page count = %d, want 2", count)
	}
}

// TestEncryptAlreadyEncrypted verifies encrypt refuses an encrypted input
// unless password is provided.
func TestEncryptAlreadyEncrypted(t *testing.T) {
	b := NewPDFCPUBackend()
	encrypted := createEncryptedFixture(t, "oldpass")
	output := filepath.Join(t.TempDir(), "reencrypted.pdf")

	// Without password should fail with clear message
	params := EncryptParams{
		Owner: "newpass",
	}
	err := b.Encrypt(context.Background(), encrypted, output, params)
	if err == nil {
		t.Fatal("Encrypt already-encrypted file should fail without password")
	}
	if !strings.Contains(err.Error(), "encrypted") && !strings.Contains(err.Error(), "password") {
		t.Errorf("error should mention encrypted/password, got: %v", err)
	}

	// With password should succeed (decrypt then re-encrypt)
	params.Password = "oldpass"
	err = b.Encrypt(context.Background(), encrypted, output, params)
	if err != nil {
		t.Fatalf("Encrypt with password: %v", err)
	}
}

// TestSetPermissions tests pdf-permissions.
func TestSetPermissions(t *testing.T) {
	skipIfMissingQPDF(t)

	b := NewPDFCPUBackend()
	plain := writeFixture(t, t.TempDir(), "plain.pdf", 1)
	output := filepath.Join(t.TempDir(), "restricted.pdf")

	params := PermissionsParams{
		Owner:       "ownerpass",
		Permissions: []string{"print"},
	}
	err := b.SetPermissions(context.Background(), plain, output, params)
	if err != nil {
		t.Fatalf("SetPermissions: %v", err)
	}

	// Verify with qpdf
	encryption := getEncryptionInfo(t, output, "")
	// qpdf output shows "print low resolution: allowed" and "print high resolution: allowed"
	if !strings.Contains(encryption, "print low resolution: allowed") {
		t.Errorf("print should be allowed: %s", encryption)
	}
	// Copy, modify, annotate should be denied
	if !strings.Contains(encryption, "extract for any purpose: not allowed") {
		t.Errorf("extract should be denied: %s", encryption)
	}
	if !strings.Contains(encryption, "modify anything: not allowed") {
		t.Errorf("modify should be denied: %s", encryption)
	}
	if !strings.Contains(encryption, "modify annotations: not allowed") {
		t.Errorf("annotate should be denied: %s", encryption)
	}
}

// TestSetPermissionsValidation tests permission validation.
func TestSetPermissionsValidation(t *testing.T) {
	b := NewPDFCPUBackend()
	plain := writeFixture(t, t.TempDir(), "plain.pdf", 1)
	output := filepath.Join(t.TempDir(), "restricted.pdf")

	tests := []struct {
		name        string
		permissions []string
		wantErr     bool
	}{
		{"valid print", []string{"print"}, false},
		{"valid copy", []string{"copy"}, false},
		{"valid modify", []string{"modify"}, false},
		{"valid annotate", []string{"annotate"}, false},
		{"valid multiple", []string{"print", "copy"}, false},
		{"invalid permission", []string{"delete"}, true},
		{"empty string", []string{""}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := PermissionsParams{
				Owner:       "ownerpass",
				Permissions: tt.permissions,
			}
			err := b.SetPermissions(context.Background(), plain, output, params)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetPermissions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestEncryptParams_Validate tests parameter validation.
func TestEncryptParamsValidate(t *testing.T) {
	tests := []struct {
		name    string
		params  EncryptParams
		wantErr bool
	}{
		{"user and owner", EncryptParams{User: "u", Owner: "o"}, false},
		{"owner only", EncryptParams{Owner: "o"}, false},
		{"user only fails", EncryptParams{User: "u"}, true},
		{"neither fails", EncryptParams{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Helper to get encryption info from a PDF using qpdf
func getEncryptionInfo(t *testing.T, path, password string) string {
	t.Helper()

	args := []string{"--show-encryption", path}
	if password != "" {
		args = append([]string{"--password=" + password}, args...)
	}

	out, err := exec.Command("qpdf", args...).CombinedOutput()
	if err != nil {
		// Try without password if it failed
		if password != "" {
			out, err = exec.Command("qpdf", "--show-encryption", path).CombinedOutput()
		}
		if err != nil {
			t.Fatalf("qpdf --show-encryption: %v", err)
		}
	}
	return string(out)
}

// Helper to skip test if qpdf is not available
func skipIfMissingQPDF(t *testing.T) {
	t.Helper()
	if _, err := exec.Command("qpdf", "--version").CombinedOutput(); err != nil {
		t.Skip("qpdf not installed")
	}
}

// Password-protecting a document must not also forbid printing and copying
// unless asked: a reader with the user password can do everything by default.
func TestEncryptAllowsEverythingByDefaultAndHonoursARestriction(t *testing.T) {
	skipIfMissingQPDF(t)
	b := NewPDFCPUBackend()
	plain := writeFixture(t, t.TempDir(), "plain.pdf", 1)

	open := filepath.Join(t.TempDir(), "open.pdf")
	if err := b.Encrypt(context.Background(), plain, open, EncryptParams{User: "u", Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	info := getEncryptionInfo(t, open, "u")
	for _, want := range []string{"print low resolution: allowed", "extract for any purpose: allowed"} {
		if !strings.Contains(info, want) {
			t.Errorf("default encrypt: %q missing from\n%s", want, info)
		}
	}

	limited := filepath.Join(t.TempDir(), "limited.pdf")
	if err := b.Encrypt(context.Background(), plain, limited, EncryptParams{User: "u", Owner: "o", Permissions: []string{"print"}}); err != nil {
		t.Fatal(err)
	}
	info = getEncryptionInfo(t, limited, "u")
	if !strings.Contains(info, "print low resolution: allowed") || strings.Contains(info, "extract for any purpose: allowed") {
		t.Errorf("print-only encrypt reported:\n%s", info)
	}
	if err := b.Encrypt(context.Background(), plain, filepath.Join(t.TempDir(), "x.pdf"), EncryptParams{Owner: "o", Permissions: []string{"fly"}}); err == nil {
		t.Error("an unknown permission was accepted")
	}
}
