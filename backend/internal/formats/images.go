package formats

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"file-converter/backend/internal/protocol"
)

type Format string

const (
	PDF  Format = "pdf"
	PNG  Format = "png"
	JPEG Format = "jpeg"
	WebP Format = "webp"
	JXL  Format = "jxl"
	AVIF Format = "avif"
	GIF  Format = "gif"
	BMP  Format = "bmp"
	TIFF Format = "tiff"
	ICO  Format = "ico"
	HEIC Format = "heic"
	MP4  Format = "mp4"
	MOV  Format = "mov"
	WebM Format = "webm"
	MKV  Format = "mkv"
	AVI  Format = "avi"
	MP3  Format = "mp3"
	WAV  Format = "wav"
	FLAC Format = "flac"
	AAC  Format = "aac"
	M4A  Format = "m4a"
	OGG  Format = "ogg"
	Opus Format = "opus"
	AIFF Format = "aiff"
	ALAC Format = "alac"
	AC3  Format = "ac3"
	AMR  Format = "amr"
	WMA  Format = "wma"
	SVG  Format = "svg"
)

var ErrUnsupported = errors.New("unsupported image format")
var ErrInvalidInput = errors.New("invalid input file")

const detectPrefixLimit = 64 * 1024

func ValidatePath(path string) error {
	if !filepath.IsAbs(path) || unsafeInputBase(filepath.Base(path)) {
		return fmt.Errorf("%w: unsafe or non-absolute path", ErrInvalidInput)
	}
	return nil
}

func ValidateInput(path string) error {
	if err := ValidatePath(path); err != nil {
		return err
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
	prefix, err := io.ReadAll(io.LimitReader(f, detectPrefixLimit))
	if err != nil {
		return "", err
	}
	header := prefix
	n := len(header)
	if n >= 5 && string(header[:5]) == "%PDF-" {
		return PDF, nil
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
	if n >= 6 && (string(header[:6]) == "GIF87a" || string(header[:6]) == "GIF89a") {
		return GIF, nil
	}
	if n >= 2 && string(header[:2]) == "BM" {
		return BMP, nil
	}
	if n >= 4 && ((header[0] == 'I' && header[1] == 'I' && header[2] == '*' && header[3] == 0) ||
		(header[0] == 'M' && header[1] == 'M' && header[2] == 0 && header[3] == '*') ||
		(header[0] == 'I' && header[1] == 'I' && header[2] == '+' && header[3] == 0) ||
		(header[0] == 'M' && header[1] == 'M' && header[2] == 0 && header[3] == '+')) {
		return TIFF, nil
	}
	if n >= 6 && header[0] == 0 && header[1] == 0 && header[2] == 1 && header[3] == 0 {
		count := int(binary.LittleEndian.Uint16(header[4:6]))
		if count > 0 && icoEntriesValid(f, count, info.Size()) {
			return ICO, nil
		}
	}
	if format, ok := detectSVG(prefix); ok {
		return format, nil
	}
	if n >= 12 && string(header[4:8]) == "ftyp" {
		if format := brandInFTYP(path, binary.BigEndian.Uint32(header[:4])); format != "" {
			return format, nil
		}
	}
	return "", ErrUnsupported
}

func detectSVG(prefix []byte) (Format, bool) {
	prefix = bytes.TrimPrefix(prefix, []byte{0xef, 0xbb, 0xbf})
	prefix = bytes.TrimSpace(prefix)
	decoder := xml.NewDecoder(bytes.NewReader(prefix))
	depth := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", false
		}
		switch element := token.(type) {
		case xml.StartElement:
			if depth == 0 && element.Name.Local != "svg" {
				return "", false
			}
			depth++
		case xml.EndElement:
			depth--
			if depth == 0 {
				if _, err := decoder.Token(); err == io.EOF {
					return SVG, true
				}
				return "", false
			}
		}
	}
}

func icoEntriesValid(f *os.File, count int, fileSize int64) bool {
	if count > 1024 || int64(6+count*16) > fileSize {
		return false
	}
	entries := make([]byte, count*16)
	if _, err := f.ReadAt(entries, 6); err != nil {
		return false
	}
	for i := 0; i < count; i++ {
		entry := entries[i*16:]
		size := int64(binary.LittleEndian.Uint32(entry[8:12]))
		offset := int64(binary.LittleEndian.Uint32(entry[12:16]))
		if size <= 0 || offset < 6+int64(count*16) || offset+size > fileSize {
			return false
		}
	}
	return true
}

func brandInFTYP(path string, size uint32) Format {
	if size < 12 || size > 1<<20 {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	box := make([]byte, size)
	if _, err := io.ReadFull(f, box); err != nil || string(box[4:8]) != "ftyp" {
		return ""
	}
	avif, heic := false, false
	if len(box) < 12 {
		return ""
	}
	for i := 8; i+4 <= len(box); i += 4 {
		brand := string(box[i : i+4])
		switch brand {
		case "avif", "avis":
			avif = true
		case "heic", "heix", "heim", "heis", "hevc", "hevx", "mif1", "msf1":
			heic = true
		}
	}
	if avif {
		return AVIF
	}
	if heic {
		return HEIC
	}
	return ""
}

func FormatFromExtension(path string) (Format, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4":
		return MP4, true
	case ".mov":
		return MOV, true
	case ".webm":
		return WebM, true
	case ".mkv":
		return MKV, true
	case ".avi":
		return AVI, true
	case ".mp3":
		return MP3, true
	case ".wav":
		return WAV, true
	case ".flac":
		return FLAC, true
	case ".aac":
		return AAC, true
	case ".m4a":
		return M4A, true
	case ".ogg":
		return OGG, true
	case ".opus":
		return Opus, true
	case ".aiff", ".aif":
		return AIFF, true
	case ".alac":
		return ALAC, true
	case ".ac3":
		return AC3, true
	case ".amr":
		return AMR, true
	case ".wma":
		return WMA, true
	case ".pdf":
		return PDF, true
	case ".svg":
		return SVG, true
	default:
		return "", false
	}
}

func IsVideoFormat(format Format) bool {
	switch format {
	case MP4, MOV, WebM, MKV, AVI:
		return true
	default:
		return false
	}
}

func IsAudioFormat(format Format) bool {
	switch format {
	case MP3, WAV, FLAC, AAC, M4A, OGG, Opus, AIFF, ALAC, AC3, AMR, WMA:
		return true
	default:
		return false
	}
}

func IsSVGFormat(format Format) bool {
	return format == SVG
}

func IsPDFFormat(format Format) bool {
	return format == PDF
}

func IsImageFormat(format Format) bool {
	switch format {
	case PNG, JPEG, WebP, JXL, AVIF, GIF, BMP, TIFF, ICO, HEIC:
		return true
	default:
		return false
	}
}

func PresetFormat(preset string) (Format, bool) {
	switch preset {
	case "pdf-to-png":
		return PNG, true
	case "pdf-to-jpeg":
		return JPEG, true
	case "pdf-to-webp":
		return WebP, true
	case "image-to-png", "svg-to-png":
		return PNG, true
	case "image-to-jpeg", "svg-to-jpeg":
		return JPEG, true
	case "image-to-webp", "svg-to-webp":
		return WebP, true
	case "image-to-jxl":
		return JXL, true
	case "image-to-avif":
		return AVIF, true
	case "image-to-gif":
		return GIF, true
	case "image-to-bmp":
		return BMP, true
	case "image-to-tiff":
		return TIFF, true
	case "image-to-ico":
		return ICO, true
	case "image-to-heic":
		return HEIC, true
	case "video-to-mp4":
		return MP4, true
	case "video-to-mov":
		return MOV, true
	case "video-to-webm":
		return WebM, true
	case "video-to-mkv":
		return MKV, true
	case "video-to-avi":
		return AVI, true
	case "video-to-gif":
		return GIF, true
	case "audio-to-mp3":
		return MP3, true
	case "audio-to-wav":
		return WAV, true
	case "audio-to-flac":
		return FLAC, true
	case "audio-to-aac":
		return AAC, true
	case "audio-to-m4a":
		return M4A, true
	case "audio-to-ogg":
		return OGG, true
	case "audio-to-opus":
		return Opus, true
	case "audio-to-aiff":
		return AIFF, true
	case "audio-to-alac":
		return ALAC, true
	case "audio-to-ac3":
		return AC3, true
	case "audio-to-amr":
		return AMR, true
	case "audio-to-wma":
		return WMA, true
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
		if strings.HasPrefix(target.Preset, "svg-to-") && !inputFormats[SVG] {
			continue
		}
		if inputFormats[PDF] && !strings.HasPrefix(target.Preset, "pdf-to-") {
			continue
		}
		if strings.HasPrefix(target.Preset, "pdf-to-") && !inputFormats[PDF] {
			continue
		}
		format, ok := PresetFormat(target.Preset)
		if ok && !inputFormats[format] {
			out = append(out, target)
		}
	}
	return out, nil
}
