package formats

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateInputRejectsMissingAndUnsafePaths(t *testing.T) {
	for _, path := range []string{filepath.Join(t.TempDir(), "missing.png"), filepath.Join(t.TempDir(), "we[0].png")} {
		if err := ValidateInput(path); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("ValidateInput(%q) error = %v, want ErrInvalidInput", path, err)
		}
	}
}

func TestValidateInputAcceptsRegularAbsoluteImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateInput(path); err != nil {
		t.Fatal(err)
	}
}
