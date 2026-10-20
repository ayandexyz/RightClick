package registry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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

func TestTargetsUsesRegisteredCapabilities(t *testing.T) {
	r := New()
	r.Register(fakeEngine{id: "fake"})

	got := r.Targets(formats.PNG)
	if len(got) != 1 || got[0] != formats.WebP {
		t.Fatalf("got targets %v, want [%s]", got, formats.WebP)
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

type imageAndMediaEngine struct{}

func (imageAndMediaEngine) ID() string                 { return "image-and-media" }
func (imageAndMediaEngine) Available() (string, error) { return "image-and-media", nil }
func (imageAndMediaEngine) Supports(source, target formats.Format) bool {
	return source != target
}
func (imageAndMediaEngine) Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error {
	return nil
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
