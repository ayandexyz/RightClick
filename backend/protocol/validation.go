package protocol

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Supported presets for validation.
func SupportedPresets() []string {
	return []string{
		"png", "jpg", "webp", "gif", "pdf", "svg",
		"mp4", "webm", "mp3", "wav", "zip", "tar.gz",
	}
}

// ValidateRequest checks a request for correctness and returns an error code and message if invalid.
func ValidateRequest(req *Request) (string, string) {
	if req.Method == "" {
		return "bad_request", "method is required"
	}

	switch req.Method {
	case "hello":
		return validateHello(req.Params)
	case "targets":
		return validateTargets(req.Params)
	case "convert":
		return validateConvert(req.Params)
	case "cancel":
		return validateCancel(req.Params)
	case "jobs", "subscribe", "presets", "formats", "shutdown":
		// These methods accept empty params or no validation needed
		return "", ""
	default:
		return "bad_request", fmt.Sprintf("unknown method: %s", req.Method)
	}
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
	for _, supported := range SupportedPresets() {
		if preset == supported {
			return true
		}
	}
	return false
}
