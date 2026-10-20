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
	{Preset: "svg-to-png", Label: "SVG to PNG", Format: "png", Category: "image", Enabled: true},
	{Preset: "svg-to-jpeg", Label: "SVG to JPEG", Format: "jpeg", Category: "image", Enabled: true},
	{Preset: "svg-to-webp", Label: "SVG to WebP", Format: "webp", Category: "image", Enabled: true},
	{Preset: "pdf-to-png", Label: "PDF to PNG", Format: "png", Category: "image", Enabled: true},
	{Preset: "pdf-to-jpeg", Label: "PDF to JPEG", Format: "jpeg", Category: "image", Enabled: true},
	{Preset: "pdf-to-webp", Label: "PDF to WebP", Format: "webp", Category: "image", Enabled: true},
	{Preset: "video-to-mp4", Label: "MP4 Video", Format: "mp4", Category: "video", Enabled: true},
	{Preset: "video-to-mov", Label: "MOV Video", Format: "mov", Category: "video", Enabled: true},
	{Preset: "video-to-webm", Label: "WebM Video", Format: "webm", Category: "video", Enabled: true},
	{Preset: "video-to-mkv", Label: "MKV Video", Format: "mkv", Category: "video", Enabled: true},
	{Preset: "video-to-avi", Label: "AVI Video", Format: "avi", Category: "video", Enabled: true},
	{Preset: "video-to-gif", Label: "GIF Animation", Format: "gif", Category: "video", Enabled: true},
	{Preset: "audio-to-mp3", Label: "MP3 Audio", Format: "mp3", Category: "audio", Enabled: true},
	{Preset: "audio-to-wav", Label: "WAV Audio", Format: "wav", Category: "audio", Enabled: true},
	{Preset: "audio-to-flac", Label: "FLAC Audio", Format: "flac", Category: "audio", Enabled: true},
	{Preset: "audio-to-aac", Label: "AAC Audio", Format: "aac", Category: "audio", Enabled: true},
	{Preset: "audio-to-m4a", Label: "M4A Audio", Format: "m4a", Category: "audio", Enabled: true},
	{Preset: "audio-to-ogg", Label: "OGG Audio", Format: "ogg", Category: "audio", Enabled: true},
	{Preset: "audio-to-opus", Label: "Opus Audio", Format: "opus", Category: "audio", Enabled: true},
	{Preset: "audio-to-aiff", Label: "AIFF Audio", Format: "aiff", Category: "audio", Enabled: true},
	{Preset: "audio-to-alac", Label: "ALAC Audio", Format: "alac", Category: "audio", Enabled: true},
	{Preset: "audio-to-ac3", Label: "AC3 Audio", Format: "ac3", Category: "audio", Enabled: true},
	{Preset: "audio-to-amr", Label: "AMR Audio", Format: "amr", Category: "audio", Enabled: true},
	{Preset: "audio-to-wma", Label: "WMA Audio", Format: "wma", Category: "audio", Enabled: true},
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
	case "operations":
		code, msg = validateOperations(req.Params)
	case "pdf_info":
		code, msg = validatePDFInfo(req.Params)
	case "pdf_thumbnails":
		code, msg = validatePDFThumbnails(req.Params)
	case "cache_clear":
		// No params: clearing the whole cache is the only operation, and it
		// carries no path a client could get wrong.
		code, msg = validateCacheClear(req.Params)
	case "run_operation":
		code, msg = validateRunOperation(req.Params)
	case "cancel":
		code, msg = validateCancel(req.Params)
	case "menu":
		code, msg = validateMenu(req.Params)
	case "engines", "subscribe":
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

func validateMenu(params json.RawMessage) (string, string) {
	var p struct {
		Action   string    `json:"action"`
		Category string    `json:"category"`
		Formats  *[]string `json:"formats"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "bad_request", "invalid menu params"
	}
	switch p.Action {
	case "show":
		if p.Category != "" || p.Formats != nil {
			return "bad_request", "menu show takes no category or formats"
		}
	case "set":
		if p.Category == "" || p.Formats == nil {
			return "bad_request", "menu set requires category and formats"
		}
	case "reset":
		if p.Formats != nil {
			return "bad_request", "menu reset takes no formats"
		}
	default:
		return "bad_request", "menu action must be show, set or reset"
	}
	return "", ""
}

func validateTargets(params json.RawMessage) (string, string) {
	var p struct {
		Files []string `json:"files"`
		Menu  bool     `json:"menu"`
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
		Recursive  bool     `json:"recursive,omitempty"`
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
	if p.OnConflict != "" && !isConflictPolicy(p.OnConflict) {
		return "bad_request", fmt.Sprintf("invalid on_conflict: %s (must be rename, skip, or overwrite)", p.OnConflict)
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

// validateOperations checks the shape of an operations request. Which
// operations apply to the selection is decided by the server, not here.
// validatePDFInfo checks the single-file request pdf_info takes. The file must
// be absolute and non-empty; whether it is a readable PDF is the daemon's
// answer, not a reason to reject the request.
func validatePDFInfo(params json.RawMessage) (string, string) {
	var p struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "bad_request", "invalid pdf_info params"
	}
	if p.File == "" {
		return "bad_request", "file is required"
	}
	if !filepath.IsAbs(p.File) {
		return "bad_request", fmt.Sprintf("path must be absolute: %s", p.File)
	}
	return "", ""
}

// validatePDFThumbnails checks a thumbnail request. The document must be an
// absolute path; the width and page-count bounds are enforced by the thumb
// cache itself, which is their single source of truth, so this only rejects
// requests that cannot mean anything.
func validatePDFThumbnails(params json.RawMessage) (string, string) {
	var p struct {
		File  string `json:"file"`
		Pages []int  `json:"pages"`
		Width int    `json:"width"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "bad_request", "invalid pdf_thumbnails params"
	}
	if p.File == "" {
		return "bad_request", "file is required"
	}
	if !filepath.IsAbs(p.File) {
		return "bad_request", fmt.Sprintf("path must be absolute: %s", p.File)
	}
	if len(p.Pages) == 0 {
		return "bad_request", "pdf_thumbnails needs at least one page"
	}
	for _, page := range p.Pages {
		if page < 1 {
			return "bad_request", fmt.Sprintf("page numbers start at 1, got %d", page)
		}
	}
	return "", ""
}

// validateCacheClear accepts anything: there is no per-request state, and a
// client that sends params anyway has not made a mistake worth reporting.
func validateCacheClear(json.RawMessage) (string, string) {
	return "", ""
}

func validateOperations(params json.RawMessage) (string, string) {
	var p struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "bad_request", "invalid operations params"
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

// validateRunOperation checks the shape of a run_operation request. Which
// parameters an operation requires, and whether it exists at all, is decided by
// the pdfops catalogue.
func validateRunOperation(params json.RawMessage) (string, string) {
	var p struct {
		Op         string   `json:"op"`
		Files      []string `json:"files"`
		Params     Params   `json:"params"`
		OutputDir  string   `json:"output_dir,omitempty"`
		OnConflict string   `json:"on_conflict,omitempty"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "bad_request", "invalid run_operation params"
	}
	if p.Op == "" {
		return "bad_request", "op is required"
	}
	if len(p.Files) == 0 {
		return "bad_request", "files list cannot be empty"
	}
	for _, file := range p.Files {
		if !filepath.IsAbs(file) {
			return "bad_request", fmt.Sprintf("path must be absolute: %s", file)
		}
	}
	if p.OutputDir != "" && !filepath.IsAbs(p.OutputDir) {
		return "bad_request", "output_dir must be absolute"
	}
	if p.OnConflict != "" && !isConflictPolicy(p.OnConflict) {
		return "bad_request", fmt.Sprintf("invalid on_conflict: %s (must be rename, skip, or overwrite)", p.OnConflict)
	}
	if p.Op == "pdf-edit" {
		if p.Params.Pages != nil || p.Params.Angle != nil {
			return "bad_request", "pdf-edit does not accept pages or angle"
		}
		if len(p.Params.Edit) == 0 {
			return "bad_request", "pdf-edit requires a non-empty edit list"
		}
		if len(p.Params.Edit) > 1000 {
			return "bad_request", "pdf-edit allows at most 1000 items"
		}
		for i, item := range p.Params.Edit {
			if item.Src < 0 {
				return "bad_request", fmt.Sprintf("edit item %d has a negative source page", i+1)
			}
			switch item.Rotate {
			case 0, 90, 180, 270:
			default:
				return "bad_request", fmt.Sprintf("edit item %d has an invalid rotation", i+1)
			}
		}
	}
	return "", ""
}

func isConflictPolicy(value string) bool {
	return value == string(ConflictRename) || value == string(ConflictSkip) || value == string(ConflictOverwrite)
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
