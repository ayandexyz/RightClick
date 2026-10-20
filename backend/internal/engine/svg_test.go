package engine

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

func TestSVGSupportsOnlySVGToRasterTargets(t *testing.T) {
	e := NewSVG(nil)
	for _, target := range []formats.Format{formats.PNG, formats.JPEG, formats.WebP} {
		if !e.Supports(formats.SVG, target) {
			t.Errorf("Supports(SVG, %s) = false", target)
		}
	}
	for _, source := range []formats.Format{formats.PNG, formats.JPEG, formats.WebP, formats.MP4, formats.MP3} {
		if e.Supports(source, formats.PNG) {
			t.Errorf("Supports(%s, PNG) = true", source)
		}
	}
	for _, target := range []formats.Format{formats.SVG, formats.GIF, formats.JXL} {
		if e.Supports(formats.SVG, target) {
			t.Errorf("Supports(SVG, %s) = true", target)
		}
	}
}

func TestSVGConvertUsesSafeRendererArgumentsForAllTargets(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "input ; $file.svg")
	if err := os.WriteFile(source, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []formats.Format{formats.PNG, formats.JPEG, formats.WebP} {
		t.Run(string(target), func(t *testing.T) {
			var calls [][]string
			e := NewSVG(func(_ context.Context, executable string, args []string) error {
				calls = append(calls, append([]string{executable}, args...))
				return nil
			})
			e.executable = "rsvg-convert"
			e.maxBytes = 1024
			e.imageMagick = NewImageMagick(func(_ context.Context, executable string, args []string) error {
				calls = append(calls, append([]string{executable}, args...))
				return nil
			})
			e.imageMagick.executable = "magick"
			destination := filepath.Join(t.TempDir(), "out ; $file."+string(target))
			if err := e.Convert(context.Background(), source, formats.SVG, destination, target, protocol.ImageOptions{}); err != nil {
				t.Fatal(err)
			}
			if target == formats.PNG && len(calls) != 1 {
				t.Fatalf("runner calls = %d, want 1: %v", len(calls), calls)
			}
			wantDestination := destination
			if target != formats.PNG {
				if len(calls) != 2 {
					t.Fatalf("runner calls = %d, want 2: %v", len(calls), calls)
				}
				wantDestination = calls[0][4]
			}
			want := []string{"rsvg-convert", "--format", "png", "--output", wantDestination, source}
			if !reflect.DeepEqual(calls[0], want) {
				t.Fatalf("command = %#v, want %#v", calls[0], want)
			}
		})
	}
}

func TestSVGConvertUsesImageMagickForJPEGAndWebP(t *testing.T) {
	source := writeSVG(t, `<svg xmlns="http://www.w3.org/2000/svg"/>`)
	var calls [][]string
	e := NewSVG(func(_ context.Context, executable string, args []string) error {
		calls = append(calls, append([]string{executable}, args...))
		return nil
	})
	e.executable = "rsvg-convert"
	e.maxBytes = 1024
	e.imageMagick = NewImageMagick(func(_ context.Context, executable string, args []string) error {
		calls = append(calls, append([]string{executable}, args...))
		return nil
	})
	e.imageMagick.executable = "magick"
	destination := filepath.Join(t.TempDir(), "out.jpg")
	if err := e.Convert(context.Background(), source, formats.SVG, destination, formats.JPEG, protocol.ImageOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("runner calls = %d, want 2: %v", len(calls), calls)
	}
	if !reflect.DeepEqual(calls[0][1:], []string{"--format", "png", "--output", calls[0][4], source}) {
		t.Fatalf("renderer command = %#v", calls[0])
	}
	if calls[1][0] != "magick" || !containsPNGInput(calls[1]) || calls[1][len(calls[1])-1] != "jpeg:"+destination {
		t.Fatalf("ImageMagick command = %#v", calls[1])
	}
}

func containsPNGInput(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "png:") {
			return true
		}
	}
	return false
}

func TestValidateSVGAcceptsSafeDocument(t *testing.T) {
	path := writeSVG(t, `<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`)
	if err := ValidateSVG(path, 1024); err != nil {
		t.Fatalf("ValidateSVG() error = %v", err)
	}
}

func TestValidateSVGRejectsInvalidPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.svg")
	if err := ValidateSVG(path, 1024); err == nil {
		t.Fatal("missing file accepted")
	}
	if err := ValidateSVG("relative.svg", 1024); err == nil {
		t.Fatal("relative path accepted")
	}
	if err := ValidateSVG(t.TempDir(), 1024); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestValidateSVGRejectsFilesOverLimit(t *testing.T) {
	path := writeSVG(t, `<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	if err := ValidateSVG(path, 10); err == nil {
		t.Fatal("oversized SVG accepted")
	}
}

func TestValidateSVGRejectsMalformedXML(t *testing.T) {
	path := writeSVG(t, `<svg><rect></svg>`)
	if err := ValidateSVG(path, 1024); err == nil {
		t.Fatal("malformed SVG accepted")
	}
}

func TestValidateSVGRejectsUnsafeConstructs(t *testing.T) {
	cases := []string{
		`<!DOCTYPE svg><svg/>`,
		`<!DOCTYPE svg [<!ENTITY xxe "file:///etc/passwd">]><svg>&xxe;</svg>`,
		`<svg><script>alert(1)</script></svg>`,
		`<svg onload="alert(1)"/>`,
		`<svg><image href="file:///tmp/image.png"/></svg>`,
		`<svg><image href="http://example.com/image.png"/></svg>`,
		`<svg><image href="https://example.com/image.png"/></svg>`,
		`<svg><image href="data:image/png;base64,AAAA"/></svg>`,
		`<svg><image href="../image.png"/></svg>`,
	}
	for _, document := range cases {
		t.Run(strings.ReplaceAll(document[:min(len(document), 24)], " ", "_"), func(t *testing.T) {
			if err := ValidateSVG(writeSVG(t, document), 4096); err == nil {
				t.Fatalf("unsafe SVG accepted: %s", document)
			}
		})
	}
}

func writeSVG(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.svg")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
