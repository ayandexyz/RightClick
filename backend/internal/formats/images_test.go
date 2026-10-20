package formats

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

var fixtures = map[string][]byte{
	"png":  append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 8)...),
	"jpeg": {0xff, 0xd8, 0xff, 0xe0, 0x00},
	"webp": append([]byte("RIFF\x00\x00\x00\x00WEBP"), 0, 0, 0, 0),
	"jxl":  {0xff, 0x0a},
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

func TestTargetsAreOrderedAndExcludeInputFormats(t *testing.T) {
	png, jpeg, webp := fixture(t, "png"), fixture(t, "jpeg"), fixture(t, "webp")
	for _, tc := range []struct {
		name  string
		files []string
		want  []string
	}{
		{"png", []string{png}, []string{"image-to-jpeg", "image-to-webp", "image-to-jxl"}},
		{"mixed", []string{png, jpeg}, []string{"image-to-webp", "image-to-jxl"}},
		{"all", []string{png, jpeg, webp}, []string{"image-to-jxl"}},
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
