package registry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

type fakeEngine struct {
	id string
}

func (f fakeEngine) ID() string                 { return f.id }
func (f fakeEngine) Available() (string, error) { return "/fake", nil }
func (f fakeEngine) Supports(source, target formats.Format) bool {
	return source == formats.PNG && target == formats.WebP
}
func (f fakeEngine) Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error {
	return nil
}

func TestResolveSelectsRegisteredAvailableEngine(t *testing.T) {
	r := New()
	r.Register(fakeEngine{id: "fake"})

	got, err := r.Resolve(formats.PNG, formats.WebP)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != "fake" {
		t.Fatalf("got engine %q, want fake", got.ID())
	}
}

func TestResolveRejectsUnsupportedRoute(t *testing.T) {
	r := New()
	r.Register(fakeEngine{id: "fake"})

	if _, err := r.Resolve(formats.JPEG, formats.WebP); err == nil {
		t.Fatal("expected unsupported route error")
	}
}

func TestResolveRoutesPDFOnlyThroughPDFEngine(t *testing.T) {
	r := New()
	r.Register(anyFormatEngine{})
	r.Register(pdfImageEngine{})

	for _, target := range []formats.Format{formats.PNG, formats.JPEG, formats.WebP} {
		got, err := r.Resolve(formats.PDF, target)
		if err != nil {
			t.Fatalf("Resolve(PDF, %s): %v", target, err)
		}
		if got.ID() != "pdf" {
			t.Fatalf("Resolve(PDF, %s) selected %q, want pdf", target, got.ID())
		}
	}
}

func TestResolveRejectsRoutesOutsidePDFRasterization(t *testing.T) {
	r := New()
	r.Register(anyFormatEngine{})
	r.Register(pdfImageEngine{})

	for _, route := range []struct {
		source formats.Format
		target formats.Format
	}{
		{formats.PNG, formats.PDF},
		{formats.PDF, formats.MP4},
		{formats.PDF, formats.SVG},
	} {
		if got, err := r.Resolve(route.source, route.target); err == nil {
			t.Fatalf("Resolve(%s, %s) selected %q, want rejection", route.source, route.target, got.ID())
		}
	}
}

func TestTargetsUsesRegisteredCapabilities(t *testing.T) {
	r := New()
	r.Register(fakeEngine{id: "fake"})

	got := r.Targets(formats.PNG)
	if len(got) != 1 || got[0] != formats.WebP {
		t.Fatalf("got targets %v, want [%s]", got, formats.WebP)
	}
}

func TestTargetsNeverIncludesPDFAsAnOutput(t *testing.T) {
	r := New()
	r.Register(anyFormatEngine{})

	for _, target := range r.Targets(formats.PNG) {
		if target == formats.PDF {
			t.Fatal("PDF was exposed as an output target")
		}
	}
}

func TestTargetsForPDFInputsExposeExactlyPDFPresets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7 fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := New()
	r.Register(pdfImageEngine{})
	got, err := r.TargetsForFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	assertPresets(t, got, []string{"pdf-to-png", "pdf-to-jpeg", "pdf-to-webp"})
}

func TestTargetsForFilesUnavailablePDFEngineDisablesOnlyPDFTargets(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "document.pdf")
	png := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(pdf, []byte("%PDF-1.7 fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\nfixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := New()
	r.Register(unavailablePDFEngine{})
	r.Register(allImageEngine{})

	pdfTargets, err := r.TargetsForFiles([]string{pdf})
	if err != nil {
		t.Fatal(err)
	}
	if len(pdfTargets) != 0 {
		t.Fatalf("PDF targets = %v, want none", pdfTargets)
	}

	imageTargets, err := r.TargetsForFiles([]string{png})
	if err != nil {
		t.Fatal(err)
	}
	if len(imageTargets) == 0 {
		t.Fatal("unavailable PDF engine disabled image targets")
	}
}

func TestTargetsForFilesExcludesPDFTargetsForRasterSVGMediaAndMixedInputs(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "photo.png"), filepath.Join(dir, "vector.svg"), filepath.Join(dir, "audio.mp3"), filepath.Join(dir, "document.pdf"),
	}
	if err := os.WriteFile(paths[0], []byte("\x89PNG\r\n\x1a\nfixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths[1], []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths[2], []byte("media fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths[3], []byte("%PDF-1.7 fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, files := range [][]string{{paths[0]}, {paths[1]}, {paths[2]}, {paths[3], paths[0]}} {
		r := New()
		r.Register(pdfImageEngine{})
		r.Register(allImageEngine{})
		r.Register(mediaEngine{kind: engine.MediaAudio})
		got, err := r.TargetsForFiles(files)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range got {
			if strings.HasPrefix(target.Preset, "pdf-to-") {
				t.Fatalf("files %v exposed PDF target %q", files, target.Preset)
			}
		}
	}
}

func TestTargetsForFilesIncludesRegisteredMediaRoutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := New()
	r.Register(imageAndMediaEngine{})
	got, err := r.TargetsForFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range got {
		if target.Category == "video" || target.Category == "audio" {
			return
		}
	}
	t.Fatalf("got %v, want at least one registered media target", got)
}

func TestTargetsForAudioFileExcludeVideoTargets(t *testing.T) {
	path := mediaFixture(t, "input.mp3")

	r := New()
	r.Register(mediaEngine{kind: engine.MediaAudio})
	got, err := r.TargetsForFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	assertPresets(t, got, []string{"audio-to-wav", "audio-to-flac", "audio-to-aac", "audio-to-m4a", "audio-to-ogg", "audio-to-opus", "audio-to-aiff", "audio-to-alac", "audio-to-ac3", "audio-to-amr", "audio-to-wma"})
}

func TestTargetsForVideoFileIncludeAudioExtraction(t *testing.T) {
	path := mediaFixture(t, "input.mp4")

	r := New()
	r.Register(mediaEngine{kind: engine.MediaVideo})
	got, err := r.TargetsForFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	assertPresets(t, got, append([]string{"video-to-mov", "video-to-webm", "video-to-mkv", "video-to-avi"}, audioPresets()...))
}

func TestTargetsForMixedFilesOnlyIncludeAudioTargets(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "input.mp3")
	video := filepath.Join(dir, "input.mp4")
	for _, path := range []string{audio, video} {
		if err := os.WriteFile(path, []byte("media fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	r := New()
	r.Register(mediaEngine{kindByPath: map[string]engine.MediaKind{audio: engine.MediaAudio, video: engine.MediaVideo}})
	got, err := r.TargetsForFiles([]string{audio, video})
	if err != nil {
		t.Fatal(err)
	}
	assertPresets(t, got, []string{"audio-to-wav", "audio-to-flac", "audio-to-aac", "audio-to-m4a", "audio-to-ogg", "audio-to-opus", "audio-to-aiff", "audio-to-alac", "audio-to-ac3", "audio-to-amr", "audio-to-wma"})
}

func TestTargetsForSVGFilesExposeOnlySVGPresets(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.svg")
	second := filepath.Join(dir, "second.svg")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	r := New()
	r.Register(svgTestEngine{})
	got, err := r.TargetsForFiles([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	assertPresets(t, got, []string{"svg-to-png", "svg-to-jpeg", "svg-to-webp"})
}

func TestTargetsForFilesDoesNotExposeSVGPresetsForMixedInputs(t *testing.T) {
	dir := t.TempDir()
	svg := filepath.Join(dir, "input.svg")
	png := filepath.Join(dir, "input.png")
	if err := os.WriteFile(svg, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\nfixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := New()
	r.Register(svgTestEngine{})
	r.Register(allImageEngine{})
	got, err := r.TargetsForFiles([]string{svg, png})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range got {
		if strings.HasPrefix(target.Preset, "svg-to-") {
			t.Fatalf("mixed inputs exposed SVG preset %q", target.Preset)
		}
	}
}

func TestTargetsForFilesUnavailableMediaEngineDoesNotHideImageTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := New()
	r.Register(missingMediaEngine{})
	r.Register(allImageEngine{})
	got, err := r.TargetsForFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	assertPresets(t, got, []string{"image-to-jpeg", "image-to-webp", "image-to-jxl", "image-to-avif", "image-to-gif", "image-to-bmp", "image-to-tiff", "image-to-ico", "image-to-heic"})
}

func TestTargetsForFilesClassifiesAudioWithoutUnavailableMediaRoutes(t *testing.T) {
	audio := mediaFixture(t, "input.mp3")
	image := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(image, []byte("\x89PNG\r\n\x1a\nfixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := New()
	r.Register(missingMediaEngine{})
	r.Register(mediaEngine{kind: engine.MediaAudio, allowVideo: true})
	r.Register(allImageEngine{})

	mediaTargets, err := r.TargetsForFiles([]string{audio})
	if err != nil {
		t.Fatal(err)
	}
	assertPresets(t, mediaTargets, []string{"audio-to-wav", "audio-to-flac", "audio-to-aac", "audio-to-m4a", "audio-to-ogg", "audio-to-opus", "audio-to-aiff", "audio-to-alac", "audio-to-ac3", "audio-to-amr", "audio-to-wma"})

	imageTargets, err := r.TargetsForFiles([]string{image})
	if err != nil {
		t.Fatal(err)
	}
	assertPresets(t, imageTargets, []string{"image-to-jpeg", "image-to-webp", "image-to-jxl", "image-to-avif", "image-to-gif", "image-to-bmp", "image-to-tiff", "image-to-ico", "image-to-heic"})
}

func TestTargetsForFilesUsesIntersection(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "photo.png")
	jpeg := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\nfixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jpeg, []byte("\xff\xd8\xff\xe0fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := New()
	r.Register(allImageEngine{})
	got, err := r.TargetsForFiles([]string{png, jpeg})
	if err != nil {
		t.Fatal(err)
	}
	want := []protocol.Target{
		{Preset: "image-to-webp", Label: "WebP Image", Format: "webp", Category: "image", Enabled: true},
		{Preset: "image-to-jxl", Label: "JPEG XL Image", Format: "jxl", Category: "image", Enabled: true},
		{Preset: "image-to-avif", Label: "AVIF Image", Format: "avif", Category: "image", Enabled: true},
		{Preset: "image-to-gif", Label: "GIF Image", Format: "gif", Category: "image", Enabled: true},
		{Preset: "image-to-bmp", Label: "BMP Image", Format: "bmp", Category: "image", Enabled: true},
		{Preset: "image-to-tiff", Label: "TIFF Image", Format: "tiff", Category: "image", Enabled: true},
		{Preset: "image-to-ico", Label: "ICO Image", Format: "ico", Category: "image", Enabled: true},
		{Preset: "image-to-heic", Label: "HEIC Image", Format: "heic", Category: "image", Enabled: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func mediaFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("media fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertPresets(t *testing.T, got []protocol.Target, want []string) {
	t.Helper()
	presets := make([]string, len(got))
	for i, target := range got {
		presets[i] = target.Preset
	}
	if !reflect.DeepEqual(presets, want) {
		t.Fatalf("got presets %v, want %v", presets, want)
	}
}

func videoPresets() []string {
	return []string{"video-to-mp4", "video-to-mov", "video-to-webm", "video-to-mkv", "video-to-avi", "video-to-gif"}
}

func audioPresets() []string {
	return []string{"audio-to-mp3", "audio-to-wav", "audio-to-flac", "audio-to-aac", "audio-to-m4a", "audio-to-ogg", "audio-to-opus", "audio-to-aiff", "audio-to-alac", "audio-to-ac3", "audio-to-amr", "audio-to-wma"}
}

type mediaEngine struct {
	kind       engine.MediaKind
	kindByPath map[string]engine.MediaKind
	allowVideo bool
}

func (f mediaEngine) ID() string                 { return "media" }
func (f mediaEngine) Available() (string, error) { return "/fake-media", nil }
func (f mediaEngine) Supports(source, target formats.Format) bool {
	return (formats.IsAudioFormat(source) || formats.IsVideoFormat(source)) &&
		(formats.IsAudioFormat(target) || formats.IsVideoFormat(target)) &&
		(!formats.IsAudioFormat(source) || f.allowVideo || formats.IsAudioFormat(target))
}
func (f mediaEngine) Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error {
	return nil
}
func (f mediaEngine) Probe(_ context.Context, path string) (engine.MediaKind, error) {
	if f.kindByPath != nil {
		return f.kindByPath[path], nil
	}
	return f.kind, nil
}

type missingMediaEngine struct{}

func (missingMediaEngine) ID() string                 { return "ffmpeg" }
func (missingMediaEngine) Available() (string, error) { return "", errors.New("ffmpeg not found") }
func (missingMediaEngine) Supports(source, target formats.Format) bool {
	return (formats.IsAudioFormat(source) || formats.IsVideoFormat(source)) &&
		(formats.IsAudioFormat(target) || formats.IsVideoFormat(target))
}
func (missingMediaEngine) Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error {
	return nil
}
func (missingMediaEngine) Probe(context.Context, string) (engine.MediaKind, error) {
	return "", errors.New("ffmpeg not found")
}

type imageAndMediaEngine struct{}

func (imageAndMediaEngine) ID() string                 { return "image-and-media" }
func (imageAndMediaEngine) Available() (string, error) { return "image-and-media", nil }
func (imageAndMediaEngine) Supports(source, target formats.Format) bool {
	return source != target
}
func (imageAndMediaEngine) Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error {
	return nil
}

type svgTestEngine struct{}

func (svgTestEngine) ID() string                 { return "svg" }
func (svgTestEngine) Available() (string, error) { return "rsvg-convert", nil }
func (svgTestEngine) Supports(source, target formats.Format) bool {
	return formats.IsSVGFormat(source) && (target == formats.PNG || target == formats.JPEG || target == formats.WebP)
}
func (svgTestEngine) Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error {
	return nil
}

type anyFormatEngine struct{}

func (anyFormatEngine) ID() string                 { return "any-format" }
func (anyFormatEngine) Available() (string, error) { return "any-format", nil }
func (anyFormatEngine) Supports(source, target formats.Format) bool {
	return source != target
}
func (anyFormatEngine) Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error {
	return nil
}

type pdfImageEngine struct{}

func (pdfImageEngine) ID() string                 { return "pdf" }
func (pdfImageEngine) Available() (string, error) { return "pdf", nil }
func (pdfImageEngine) Supports(source, target formats.Format) bool {
	return formats.IsPDFFormat(source) && (target == formats.PNG || target == formats.JPEG || target == formats.WebP)
}
func (pdfImageEngine) Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error {
	return nil
}

type unavailablePDFEngine struct{ pdfImageEngine }

func (unavailablePDFEngine) Available() (string, error) {
	return "", errors.New("pdftocairo not found")
}

type allImageEngine struct{}

func (allImageEngine) ID() string                 { return "all-images" }
func (allImageEngine) Available() (string, error) { return "all-images", nil }
func (allImageEngine) Supports(source, target formats.Format) bool {
	return formats.IsImageFormat(source) && formats.IsImageFormat(target) && source != target
}
func (allImageEngine) Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error {
	return nil
}

type missingEngine struct{ fakeEngine }

func (missingEngine) Available() (string, error) { return "", errors.New("imagemagick not found") }

func TestResolveReportsUnavailableEngine(t *testing.T) {
	r := New()
	r.Register(missingEngine{fakeEngine{id: "imagemagick"}})
	_, err := r.Resolve(formats.PNG, formats.WebP)
	if err == nil || !strings.Contains(err.Error(), "imagemagick not found") {
		t.Fatalf("error = %v, want imagemagick not found", err)
	}
}
