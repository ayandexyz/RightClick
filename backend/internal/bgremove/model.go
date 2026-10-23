// Package bgremove removes the background from an image using the
// isnet-general-use ONNX model (the same one rembg and other background
// removers use) run through onnxruntime.
package bgremove

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	modelFileName = "isnet-general-use.onnx"
	modelURL      = "https://github.com/danielgatis/rembg/releases/download/v0.0.0/isnet-general-use.onnx"
	modelMD5      = "fc16ebd8b0c10d971d3513d564d01e29"
)

// ModelDir returns the directory models are cached in:
// $XDG_DATA_HOME/file-converter/models, falling back to
// ~/.local/share/file-converter/models. Model weights are large and reused
// across every background removal, so they belong in persistent data rather
// than the thumbnail-style cache that can be cleared at any time.
func ModelDir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate the user home directory: %w", err)
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "file-converter", "models"), nil
}

// EnsureModel returns the path to the isnet-general-use ONNX model,
// downloading it to ModelDir on first use. The download is verified against
// its known checksum before being trusted.
func EnsureModel(ctx context.Context) (string, error) {
	dir, err := ModelDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, modelFileName)
	if ok, err := matchesChecksum(path, modelMD5); err == nil && ok {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create model directory: %w", err)
	}
	if err := downloadModel(ctx, path); err != nil {
		return "", err
	}
	ok, err := matchesChecksum(path, modelMD5)
	if err != nil {
		return "", err
	}
	if !ok {
		_ = os.Remove(path)
		return "", fmt.Errorf("downloaded model failed checksum verification")
	}
	return path, nil
}

func matchesChecksum(path, want string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == want, nil
}

func downloadModel(ctx context.Context, destination string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download background removal model: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download background removal model: unexpected status %s", resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".isnet-download-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("download background removal model: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, destination)
}
