package formats

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

var fixtures = map[string][]byte{
	"png":     append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 8)...),
	"jpeg":    {0xff, 0xd8, 0xff, 0xe0, 0x00},
	"webp":    append([]byte("RIFF\x00\x00\x00\x00WEBP"), 0, 0, 0, 0),
	"jxl":     {0xff, 0x0a},
	"gif":     []byte("GIF89a"),
	"bmp":     []byte("BM"),
	"tiff":    {0x49, 0x49, 0x2a, 0x00},
	"bigtiff": {0x4d, 0x4d, 0x00, 0x2b},
	"ico":     append([]byte{0, 0, 1, 0, 1, 0, 1, 1, 0, 0, 1, 0, 16, 0, 16, 0, 0, 0, 22, 0, 0, 0}, make([]byte, 16)...),
	"cur":     {0, 0, 2, 0, 1, 0},
}

func fixture(t *testing.T, kind string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.bin")
	if err := os.WriteFile(path, fixtures[kind], 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDetectUsesContent(t *testing.T) {
	for kind, want := range map[string]Format{"png": PNG, "jpeg": JPEG, "webp": WebP, "jxl": JXL} {
		if got, err := Detect(fixture(t, kind)); err != nil || got != want {
			t.Fatalf("%s: got %q, %v", kind, got, err)
		}
	}
}

func TestDetectAdditionalSignatures(t *testing.T) {
	for kind, want := range map[string]Format{"gif": GIF, "bmp": BMP, "tiff": TIFF, "bigtiff": TIFF, "ico": ICO} {
		if got, err := Detect(fixture(t, kind)); err != nil || got != want {
			t.Fatalf("%s: got %q, %v", kind, got, err)
		}
	}
	if _, err := Detect(fixture(t, "cur")); err != ErrUnsupported {
		t.Fatalf("CUR got %v, want unsupported", err)
	}
}

func TestDetectISOBrands(t *testing.T) {
	for _, tc := range []struct {
		name   string
		brands []string
		want   Format
	}{
		{"avif", []string{"avif", "mif1"}, AVIF},
		{"heic", []string{"mif1"}, HEIC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, 8+4*len(tc.brands))
			binary.BigEndian.PutUint32(data, uint32(len(data)))
			copy(data[4:], "ftyp")
			for i, brand := range tc.brands {
				copy(data[8+i*4:], brand)
			}
			path := filepath.Join(t.TempDir(), "input.bin")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Detect(path)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestDetectRecognizesSVGContentIndependentOfExtension(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"plain root", `<svg xmlns="http://www.w3.org/2000/svg"/>`},
		{"bom declaration comment", "\ufeff<?xml version=\"1.0\"?>\n<!-- comment -->\n<svg/>"},
		{"extension mismatch", `<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`},
		{"trailing whitespace", "<svg xmlns=\"http://www.w3.org/2000/svg\"></svg> \n\t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.bin")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Detect(path)
			if err != nil || got != SVG {
				t.Fatalf("Detect() = %q, %v, want SVG", got, err)
			}
		})
	}
}

func TestDetectRejectsMalformedSVG(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.svg")
	if err := os.WriteFile(path, []byte(`<svg><rect></svg>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Detect(path); err != ErrUnsupported {
		t.Fatalf("Detect() error = %v, want ErrUnsupported", err)
	}
}

func TestSVGFormatAndPresets(t *testing.T) {
	if !IsSVGFormat(SVG) || IsImageFormat(SVG) {
		t.Fatalf("SVG classification is incorrect")
	}
	for _, target := range []struct {
		preset string
		format Format
	}{
		{"svg-to-png", PNG},
		{"svg-to-jpeg", JPEG},
		{"svg-to-webp", WebP},
	} {
		if got, ok := PresetFormat(target.preset); !ok || got != target.format {
			t.Errorf("PresetFormat(%q) = %q, %v; want %q, true", target.preset, got, ok, target.format)
		}
	}
	if got, ok := FormatFromExtension("drawing.SVG"); !ok || got != SVG {
		t.Fatalf("FormatFromExtension(svg) = %q, %v", got, ok)
	}
}

func TestDetectRejectsUnsupportedAndRelative(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.png")
	if err := os.WriteFile(path, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Detect(path); err != ErrUnsupported {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
	if _, err := Detect("relative.png"); err == nil {
		t.Fatal("expected relative path error")
	}
}

func TestMediaPresetFormats(t *testing.T) {
	cases := map[string]Format{
		"video-to-mp4":  MP4,
		"video-to-mov":  MOV,
		"video-to-webm": WebM,
		"video-to-mkv":  MKV,
		"video-to-avi":  AVI,
		"video-to-gif":  GIF,
		"audio-to-mp3":  MP3,
		"audio-to-wav":  WAV,
		"audio-to-flac": FLAC,
		"audio-to-aac":  AAC,
		"audio-to-m4a":  M4A,
		"audio-to-ogg":  OGG,
		"audio-to-opus": Opus,
		"audio-to-aiff": AIFF,
		"audio-to-alac": ALAC,
		"audio-to-ac3":  AC3,
		"audio-to-amr":  AMR,
		"audio-to-wma":  WMA,
	}
	for preset, want := range cases {
		t.Run(preset, func(t *testing.T) {
			got, ok := PresetFormat(preset)
			if !ok || got != want {
				t.Fatalf("PresetFormat(%q) = %q, %v", preset, got, ok)
			}
		})
	}
}

func TestFormatClassification(t *testing.T) {
	for _, format := range []Format{MP4, MOV, WebM, MKV, AVI} {
		if !IsVideoFormat(format) || IsAudioFormat(format) || IsImageFormat(format) {
			t.Errorf("%q should be classified as video only", format)
		}
	}
	for _, format := range []Format{MP3, WAV, FLAC, AAC, M4A, OGG, Opus, AIFF, ALAC, AC3, AMR, WMA} {
		if !IsAudioFormat(format) || IsVideoFormat(format) || IsImageFormat(format) {
			t.Errorf("%q should be classified as audio only", format)
		}
	}
	for _, format := range []Format{PNG, JPEG, WebP, JXL, AVIF, GIF, BMP, TIFF, ICO, HEIC} {
		if !IsImageFormat(format) || IsVideoFormat(format) || IsAudioFormat(format) {
			t.Errorf("%q should be classified as image only", format)
		}
	}
}

func TestTargetsForPDFExposeExactlyPDFPresets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7 fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Targets([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(got))
	for i := range got {
		ids[i] = got[i].Preset
	}
	want := []string{"pdf-to-png", "pdf-to-jpeg", "pdf-to-webp"}
	if !slices.Equal(ids, want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
}

func TestTargetsAreOrderedAndExcludeInputFormats(t *testing.T) {
	png, jpeg, webp := fixture(t, "png"), fixture(t, "jpeg"), fixture(t, "webp")
	for _, tc := range []struct {
		name  string
		files []string
		want  []string
	}{
		{"png", []string{png}, []string{"image-to-jpeg", "image-to-webp", "image-to-jxl", "image-to-avif", "image-to-gif", "image-to-bmp", "image-to-tiff", "image-to-ico", "image-to-heic", "video-to-mp4", "video-to-mov", "video-to-webm", "video-to-mkv", "video-to-avi", "video-to-gif", "audio-to-mp3", "audio-to-wav", "audio-to-flac", "audio-to-aac", "audio-to-m4a", "audio-to-ogg", "audio-to-opus", "audio-to-aiff", "audio-to-alac", "audio-to-ac3", "audio-to-amr", "audio-to-wma"}},
		{"mixed", []string{png, jpeg}, []string{"image-to-webp", "image-to-jxl", "image-to-avif", "image-to-gif", "image-to-bmp", "image-to-tiff", "image-to-ico", "image-to-heic", "video-to-mp4", "video-to-mov", "video-to-webm", "video-to-mkv", "video-to-avi", "video-to-gif", "audio-to-mp3", "audio-to-wav", "audio-to-flac", "audio-to-aac", "audio-to-m4a", "audio-to-ogg", "audio-to-opus", "audio-to-aiff", "audio-to-alac", "audio-to-ac3", "audio-to-amr", "audio-to-wma"}},
		{"all", []string{png, jpeg, webp}, []string{"image-to-jxl", "image-to-avif", "image-to-gif", "image-to-bmp", "image-to-tiff", "image-to-ico", "image-to-heic", "video-to-mp4", "video-to-mov", "video-to-webm", "video-to-mkv", "video-to-avi", "video-to-gif", "audio-to-mp3", "audio-to-wav", "audio-to-flac", "audio-to-aac", "audio-to-m4a", "audio-to-ogg", "audio-to-opus", "audio-to-aiff", "audio-to-alac", "audio-to-ac3", "audio-to-amr", "audio-to-wma"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Targets(tc.files)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, len(got))
			for i := range got {
				ids[i] = got[i].Preset
			}
			if !slices.Equal(ids, tc.want) {
				t.Fatalf("got %v, want %v", ids, tc.want)
			}
		})
	}
}
