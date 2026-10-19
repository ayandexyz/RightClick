package protocol

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// supportedPresetIDs lists all preset identifiers accepted by the protocol.
func supportedPresetIDs() []string {
	return []string{
		"png", "jpg", "webp", "gif", "pdf", "svg",
		"mp4", "webm", "mp3", "wav", "zip", "tar.gz",
	}
}

// SupportedPresets returns the initial set of conversion targets with metadata.
// This list represents all presets the daemon recognizes, though their availability
// depends on the conversion engine and input format compatibility.
func SupportedPresets() []Target {
	return []Target{
		{Preset: "png", Label: "PNG Image", Format: "png", Category: "image", Enabled: true},
		{Preset: "jpg", Label: "JPEG Image", Format: "jpeg", Category: "image", Enabled: true},
		{Preset: "webp", Label: "WebP Image", Format: "webp", Category: "image", Enabled: true},
		{Preset: "gif", Label: "GIF Image", Format: "gif", Category: "image", Enabled: true},
		{Preset: "pdf", Label: "PDF Document", Format: "pdf", Category: "document", Enabled: true},
		{Preset: "svg", Label: "SVG Vector", Format: "svg", Category: "image", Enabled: true},
		{Preset: "mp4", Label: "MP4 Video", Format: "mp4", Category: "video", Enabled: true},
		{Preset: "webm", Label: "WebM Video", Format: "webm", Category: "video", Enabled: true},
		{Preset: "mp3", Label: "MP3 Audio", Format: "mp3", Category: "audio", Enabled: true},
		{Preset: "wav", Label: "WAV Audio", Format: "wav", Category: "audio", Enabled: true},
		{Preset: "zip", Label: "ZIP Archive", Format: "zip", Category: "archive", Enabled: true},
		{Preset: "tar.gz", Label: "Tar GZip Archive", Format: "tar.gz", Category: "archive", Enabled: true},
	}
}

// ValidateRequest checks a request for correctness and returns a protocol Error if invalid, or nil if valid.
func ValidateRequest(req *Request) *Error {
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
	case "jobs", "subscribe", "presets", "formats", "shutdown":
		// These methods accept empty params or no validation needed
		return nil
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
	for _, id := range supportedPresetIDs() {
		if preset == id {
			return true
		}
	}
	return false
}
