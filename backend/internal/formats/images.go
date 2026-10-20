package formats

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"file-converter/backend/internal/protocol"
)

type Format string

const (
	PNG  Format = "png"
	JPEG Format = "jpeg"
	WebP Format = "webp"
	JXL  Format = "jxl"
)

var ErrUnsupported = errors.New("unsupported image format")
var ErrInvalidInput = errors.New("invalid input file")

func ValidateInput(path string) error {
	if !filepath.IsAbs(path) || unsafeInputBase(filepath.Base(path)) {
		return fmt.Errorf("%w: unsafe or non-absolute path", ErrInvalidInput)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: not a regular file", ErrInvalidInput)
	}
	return nil
}

func unsafeInputBase(base string) bool {
	return filepath.Base(base) != base || containsAny(base, "[]:") || len(base) > 0 && base[0] == '@'
}

func containsAny(value, chars string) bool {
	for _, char := range value {
		for _, forbidden := range chars {
			if char == forbidden {
				return true
			}
		}
	}
	return false
}

func Detect(path string) (Format, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute: %w", os.ErrInvalid)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var header [16]byte
	n, err := f.Read(header[:])
	if err != nil && n == 0 {
		return "", err
	}
	if n >= 8 && string(header[:8]) == "\x89PNG\r\n\x1a\n" {
		return PNG, nil
	}
	if n >= 3 && header[0] == 0xff && header[1] == 0xd8 && header[2] == 0xff {
		return JPEG, nil
	}
	if n >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP" {
		return WebP, nil
	}
	if n >= 2 && header[0] == 0xff && header[1] == 0x0a {
		return JXL, nil
	}
	if n >= 12 && string(header[:12]) == "\x00\x00\x00\x0cJXL \x0d\x0a\x87\x0a" {
		return JXL, nil
	}
	return "", ErrUnsupported
}

func PresetFormat(preset string) (Format, bool) {
	switch preset {
	case "image-to-png":
		return PNG, true
	case "image-to-jpeg":
		return JPEG, true
	case "image-to-webp":
		return WebP, true
	case "image-to-jxl":
		return JXL, true
	default:
		return "", false
	}
}

func Targets(paths []string) ([]protocol.Target, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("files list cannot be empty")
	}
	inputFormats := make(map[Format]bool, len(paths))
	for _, path := range paths {
		format, err := Detect(path)
		if err != nil {
			return nil, err
		}
		inputFormats[format] = true
	}
	out := make([]protocol.Target, 0, len(protocol.SupportedPresets()))
	for _, target := range protocol.SupportedPresets() {
		format, ok := PresetFormat(target.Preset)
		if ok && !inputFormats[format] {
			out = append(out, target)
		}
	}
	return out, nil
}
