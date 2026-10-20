package protocol

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var backgroundPattern = regexp.MustCompile(`^(white|black|transparent|#[0-9a-f]{3}|#[0-9a-f]{6}|#[0-9a-f]{8})$`)

const maxImageOption = 32768

var supportedPresets = []Target{
	{Preset: "image-to-png", Label: "PNG Image", Format: "png", Category: "image", Enabled: true},
	{Preset: "image-to-jpeg", Label: "JPEG Image", Format: "jpeg", Category: "image", Enabled: true},
	{Preset: "image-to-webp", Label: "WebP Image", Format: "webp", Category: "image", Enabled: true},
	{Preset: "image-to-jxl", Label: "JPEG XL Image", Format: "jxl", Category: "image", Enabled: true},
	{Preset: "image-to-avif", Label: "AVIF Image", Format: "avif", Category: "image", Enabled: true},
	{Preset: "image-to-gif", Label: "GIF Image", Format: "gif", Category: "image", Enabled: true},
	{Preset: "image-to-bmp", Label: "BMP Image", Format: "bmp", Category: "image", Enabled: true},
	{Preset: "image-to-tiff", Label: "TIFF Image", Format: "tiff", Category: "image", Enabled: true},
	{Preset: "image-to-ico", Label: "ICO Image", Format: "ico", Category: "image", Enabled: true},
	{Preset: "image-to-heic", Label: "HEIC Image", Format: "heic", Category: "image", Enabled: true},
	{Preset: "video-to-mp4", Label: "MP4 Video", Format: "mp4", Category: "video", Enabled: true},
	{Preset: "video-to-mov", Label: "MOV Video", Format: "mov", Category: "video", Enabled: true},
	{Preset: "video-to-webm", Label: "WebM Video", Format: "webm", Category: "video", Enabled: true},
	{Preset: "video-to-mkv", Label: "MKV Video", Format: "mkv", Category: "video", Enabled: true},
	{Preset: "video-to-avi", Label: "AVI Video", Format: "avi", Category: "video", Enabled: true},
	{Preset: "audio-to-mp3", Label: "MP3 Audio", Format: "mp3", Category: "audio", Enabled: true},
	{Preset: "audio-to-wav", Label: "WAV Audio", Format: "wav", Category: "audio", Enabled: true},
	{Preset: "audio-to-flac", Label: "FLAC Audio", Format: "flac", Category: "audio", Enabled: true},
	{Preset: "audio-to-aac", Label: "AAC Audio", Format: "aac", Category: "audio", Enabled: true},
	{Preset: "audio-to-m4a", Label: "M4A Audio", Format: "m4a", Category: "audio", Enabled: true},
	{Preset: "audio-to-ogg", Label: "OGG Audio", Format: "ogg", Category: "audio", Enabled: true},
	{Preset: "audio-to-opus", Label: "Opus Audio", Format: "opus", Category: "audio", Enabled: true},
}

// SupportedPresets returns the image conversion targets supported by this slice.
func SupportedPresets() []Target {
	return append([]Target(nil), supportedPresets...)
}

// ValidateRequest checks a request for correctness and returns a protocol Error if invalid, or nil if valid.
func ValidateRequest(req Request) *Error {
	if req.Method == "" {
		return &Error{
			ID: req.ID,
			Error: ErrorValue{
				Code:    "bad_request",
				Message: "method is required",
			},
		}
	}

	var code, msg string
	switch req.Method {
	case "hello":
		code, msg = validateHello(req.Params)
	case "targets":
		code, msg = validateTargets(req.Params)
	case "convert":
		code, msg = validateConvert(req.Params)
	case "cancel":
		code, msg = validateCancel(req.Params)
	case "subscribe":
		code, msg = validateEmptyParams(req.Params)
	default:
		code = "bad_request"
		msg = fmt.Sprintf("unknown method: %s", req.Method)
	}

	if code != "" {
		return &Error{
			ID: req.ID,
			Error: ErrorValue{
				Code:    code,
				Message: msg,
			},
		}
	}
	return nil
}

func validateHello(params json.RawMessage) (string, string) {
	var p struct {
		Client   string `json:"client"`
		Protocol int    `json:"protocol"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "bad_request", "invalid hello params"
	}
	if p.Client == "" {
		return "bad_request", "client name is required"
	}
	if p.Protocol != 1 {
		return "unsupported", fmt.Sprintf("protocol %d not supported", p.Protocol)
	}
	return "", ""
}

func validateTargets(params json.RawMessage) (string, string) {
	var p struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "bad_request", "invalid targets params"
	}
	if len(p.Files) == 0 {
		return "bad_request", "files list cannot be empty"
	}
	for _, file := range p.Files {
		if !filepath.IsAbs(file) {
			return "bad_request", fmt.Sprintf("path must be absolute: %s", file)
		}
	}
	return "", ""
}

func validateConvert(params json.RawMessage) (string, string) {
	var p struct {
		Files      []string `json:"files"`
		Preset     string   `json:"preset"`
		OutputDir  string   `json:"output_dir,omitempty"`
		OnConflict string   `json:"on_conflict,omitempty"`
		Quality    *int     `json:"quality,omitempty"`
		MaxSize    *int     `json:"max_size,omitempty"`
		Background string   `json:"background,omitempty"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "bad_request", "invalid convert params"
	}
	if len(p.Files) == 0 {
		return "bad_request", "files list cannot be empty"
	}
	for _, file := range p.Files {
		if !filepath.IsAbs(file) {
			return "bad_request", fmt.Sprintf("path must be absolute: %s", file)
		}
	}
	if p.Preset == "" {
		return "bad_request", "preset is required"
	}
	if !isPresetSupported(p.Preset) {
		return "unsupported", fmt.Sprintf("preset not supported: %s", p.Preset)
	}
	if p.OutputDir != "" && !filepath.IsAbs(p.OutputDir) {
		return "bad_request", "output_dir must be absolute"
	}
	if p.OnConflict != "" {
		if p.OnConflict != "rename" && p.OnConflict != "skip" && p.OnConflict != "overwrite" {
			return "bad_request", fmt.Sprintf("invalid on_conflict: %s (must be rename, skip, or overwrite)", p.OnConflict)
		}
	}
	if p.Quality != nil && (*p.Quality < 1 || *p.Quality > 100) {
		return "bad_request", "quality must be between 1 and 100"
	}
	if p.MaxSize != nil && (*p.MaxSize < 1 || *p.MaxSize > maxImageOption) {
		return "bad_request", fmt.Sprintf("max_size must be between 1 and %d", maxImageOption)
	}
	if p.Background != "" {
		background := strings.ToLower(p.Background)
		if !backgroundPattern.MatchString(background) {
			return "bad_request", "background must be white, black, transparent, or a 3/4/6/8-digit hex color"
		}
		if (strings.EqualFold(p.Preset, "image-to-jpeg") || strings.EqualFold(p.Preset, "image-to-bmp") || strings.EqualFold(p.Preset, "image-to-gif")) && (background == "transparent" || len(background) == 5 || len(background) == 9) {
			return "bad_request", "transparent background is not supported for this format"
		}
	}
	return "", ""
}

func validateEmptyParams(params json.RawMessage) (string, string) {
	if len(params) == 0 || string(params) == "null" {
		return "", ""
	}
	var value map[string]any
	if err := json.Unmarshal(params, &value); err != nil {
		return "bad_request", "invalid params"
	}
	return "", ""
}

func validateCancel(params json.RawMessage) (string, string) {
	var p struct {
		Job string `json:"job"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "bad_request", "invalid cancel params"
	}
	if p.Job == "" {
		return "bad_request", "job id is required"
	}
	return "", ""
}

func isPresetSupported(preset string) bool {
	preset = strings.ToLower(preset)
	for _, target := range supportedPresets {
		if preset == target.Preset {
			return true
		}
	}
	return false
}
