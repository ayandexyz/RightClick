package menu

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

func TestParseValidLists(t *testing.T) {
	p, err := Parse([]byte("# my menu\nvideo = [\"mp4\", 'webm'] # trailing\naudio = [\n  \"mp3\",\n  \"wav\",\n]\nimage = []\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := Policy{"video": {"mp4", "webm"}, "audio": {"mp3", "wav"}, "image": {}}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("got %#v, want %#v", p, want)
	}
}

func TestParseIgnoresUnknownCategoriesAndFormats(t *testing.T) {
	p, err := Parse([]byte("video = [\"mp4\", \"hologram\"]\nebook = [\"epub\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Policy{"video": {"mp4"}}); !reflect.DeepEqual(p, want) {
		t.Fatalf("got %#v, want %#v", p, want)
	}
}

func TestParseCollapsesDuplicateFormats(t *testing.T) {
	p, err := Parse([]byte(`image = ["png", "jpeg", "png"]`))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"png", "jpeg"}; !reflect.DeepEqual(p["image"], want) {
		t.Fatalf("got %#v, want %#v", p["image"], want)
	}
}

func TestParseRejectsMalformedValues(t *testing.T) {
	for name, input := range map[string]string{
		"not an array":    `video = "mp4"`,
		"unquoted item":   `video = [mp4]`,
		"number item":     `video = [1]`,
		"unterminated":    `video = ["mp4]`,
		"missing comma":   `video = ["mp4" "webm"]`,
		"missing value":   `video`,
		"table header":    `[video]`,
		"duplicate key":   "video = [\"mp4\"]\nvideo = [\"webm\"]",
		"unclosed array":  "video = [\"mp4\",\n",
		"escape sequence": `video = ["m\p4"]`,
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("%s: expected error for %q", name, input)
		}
	}
}

func TestLoadMissingFileIsEmptyPolicy(t *testing.T) {
	p, err := Load(filepath.Join(t.TempDir(), "nope", "menu.toml"))
	if err != nil || len(p) != 0 {
		t.Fatalf("got %#v, %v", p, err)
	}
}

func TestSaveLoadRoundTripIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "menu.toml")
	p, err := Policy{}.Set("video", []string{"webm", "mp4"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("got %#v, %v", got, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("leftover files after save: %v", entries)
	}
	if err := Save(path, Policy{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(path); len(got) != 0 {
		t.Fatalf("overwrite failed: %#v", got)
	}
}

func TestSetValidatesAndDoesNotMutate(t *testing.T) {
	base := Policy{"audio": {"mp3"}}
	if _, err := base.Set("ebook", []string{"png"}); err == nil {
		t.Fatal("unknown category accepted")
	}
	if _, err := base.Set("video", []string{"hologram"}); err == nil {
		t.Fatal("unknown format accepted")
	}
	next, err := base.Set("video", []string{"mp4", "mp4"})
	if err != nil || !reflect.DeepEqual(next["video"], []string{"mp4"}) {
		t.Fatalf("got %#v, %v", next, err)
	}
	if _, ok := base["video"]; ok {
		t.Fatal("Set mutated receiver")
	}
}

func TestResetCategoryAndAll(t *testing.T) {
	base := Policy{"audio": {"mp3"}, "video": {"mp4"}}
	one, err := base.Reset("audio")
	if err != nil || !reflect.DeepEqual(one, Policy{"video": {"mp4"}}) {
		t.Fatalf("got %#v, %v", one, err)
	}
	all, _ := base.Reset("")
	if len(all) != 0 {
		t.Fatalf("got %#v", all)
	}
	if _, err := base.Reset("ebook"); err == nil {
		t.Fatal("unknown category accepted")
	}
}

func TestEffectiveUsesDefaultsForUnsetAndEmpty(t *testing.T) {
	eff := Policy{"video": {"webm"}, "audio": {}}.Effective()
	if !reflect.DeepEqual(eff["video"], []string{"webm"}) {
		t.Fatalf("video: %#v", eff["video"])
	}
	if !reflect.DeepEqual(eff["audio"], []string{"mp3", "m4a", "wav"}) {
		t.Fatalf("audio: %#v", eff["audio"])
	}
	if !reflect.DeepEqual(eff["image"], []string{"jpeg", "png", "webp"}) {
		t.Fatalf("image: %#v", eff["image"])
	}
}

func targets(formats ...string) []protocol.Target {
	out := make([]protocol.Target, len(formats))
	for i, f := range formats {
		out[i] = protocol.Target{Preset: "p-" + f, Format: f, Enabled: true}
	}
	return out
}

func formatsOf(ts []protocol.Target) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.Format)
	}
	return out
}

func TestTargetsForSourceFiltersToPolicyInListOrder(t *testing.T) {
	p := Policy{"video": {"webm", "mp4"}}
	got := formatsOf(p.TargetsForSource([]string{"video"}, targets("mp4", "mov", "webm", "mkv", "mp3")))
	if want := []string{"webm", "mp4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTargetsForSourceFallsBackToDefaultsWhenCustomListUnreachable(t *testing.T) {
	p := Policy{"video": {"avi"}}
	got := formatsOf(p.TargetsForSource([]string{"video"}, targets("mp4", "mov", "webm")))
	if want := []string{"mp4", "mov", "webm"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTargetsForSourceFallsBackToAllReachableAsLastResort(t *testing.T) {
	got := formatsOf(Policy{}.TargetsForSource([]string{"video"}, targets("mkv", "avi")))
	if want := []string{"mkv", "avi"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTargetsForSourceMixedUsesUnionOfListsOverReachable(t *testing.T) {
	// video + audio selection: only audio targets are reachable.
	got := formatsOf(Policy{}.TargetsForSource([]string{"video", "audio"}, targets("mp3", "wav", "flac", "m4a")))
	if want := []string{"mp3", "m4a", "wav"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTargetsForSourceEmptyReachableStaysEmpty(t *testing.T) {
	if got := (Policy{}).TargetsForSource([]string{"image"}, nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestCategoryMapsSourceFormats(t *testing.T) {
	for format, want := range map[formats.Format]string{
		formats.MP4: "video", formats.MP3: "audio", formats.PDF: "pdf",
		formats.PNG: "image", formats.SVG: "image",
	} {
		if got := Category(format); got != want {
			t.Errorf("Category(%s) = %q, want %q", format, got, want)
		}
	}
}

func TestPathPrefersXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x")
	t.Setenv("HOME", "/h")
	if got, _ := Path(); got != "/x/file-converter/menu.toml" {
		t.Fatalf("got %s", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, _ := Path(); !strings.HasSuffix(got, "/h/.config/file-converter/menu.toml") {
		t.Fatalf("got %s", got)
	}
}

func TestEncodeIsDeterministicAndOnlyCustomised(t *testing.T) {
	got := string(Policy{"video": {"mp4"}, "audio": {"mp3", "wav"}}.Encode())
	if want := "video = [\"mp4\"]\naudio = [\"mp3\", \"wav\"]\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
