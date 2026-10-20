package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

func TestParseProbeClassifiesStreams(t *testing.T) {
	tests := []struct {
		name string
		json string
		want MediaKind
	}{
		{"audio only", `{"streams":[{"codec_type":"audio"}]}`, MediaAudio},
		{"video containing", `{"streams":[{"codec_type":"audio"},{"codec_type":"video"}]}`, MediaVideo},
		{"video only", `{"streams":[{"codec_type":"video"}]}`, MediaVideo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProbe([]byte(tt.json))
			if err != nil || got != tt.want {
				t.Fatalf("parseProbe() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestFFmpegProbeUsesExpectedArguments(t *testing.T) {
	var gotExe string
	var gotArgs []string
	f := NewFFmpeg(nil, func(_ context.Context, exe string, args []string) ([]byte, error) {
		gotExe, gotArgs = exe, args
		return []byte(`{"streams":[{"codec_type":"audio"}]}`), nil
	})
	f.ffprobe = "/usr/bin/ffprobe"
	kind, err := f.Probe("/tmp/input.mp3")
	if err != nil || kind != MediaAudio {
		t.Fatalf("Probe() = %q, %v", kind, err)
	}
	want := []string{"-v", "error", "-show_entries", "stream=codec_type", "-of", "json", "--", "/tmp/input.mp3"}
	if gotExe != f.ffprobe || !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("probe command = %q %v, want %q", gotExe, gotArgs, want)
	}
}

func TestFFmpegSupportsMediaRoutes(t *testing.T) {
	f := NewFFmpeg(nil, nil)
	for _, target := range []formats.Format{formats.MP4, formats.WebM, formats.MP3, formats.Opus} {
		if !f.Supports(formats.MP4, target) {
			t.Errorf("video to %s should be supported", target)
		}
		if !f.Supports(formats.MP3, target) && formats.IsAudioFormat(target) {
			t.Errorf("audio to %s should be supported", target)
		}
	}
	if f.Supports(formats.MP3, formats.MP4) {
		t.Error("audio to video should not be supported")
	}
	if f.Supports(formats.PNG, formats.MP3) || f.Supports(formats.MP4, formats.PNG) {
		t.Error("image/media routes should not be supported")
	}
}

func TestFFmpegConvertBuildsAllOutputRoutes(t *testing.T) {
	for _, target := range []formats.Format{formats.MP4, formats.MOV, formats.WebM, formats.MKV, formats.AVI, formats.MP3, formats.WAV, formats.FLAC, formats.AAC, formats.M4A, formats.OGG, formats.Opus} {
		t.Run(string(target), func(t *testing.T) {
			var got []string
			f := NewFFmpeg(func(_ context.Context, _ string, args []string) error { got = args; return nil }, nil)
			f.executable = "/usr/bin/ffmpeg"
			output := "/tmp/out." + string(target)
			if err := f.Convert(context.Background(), "/tmp/input.mp4", formats.MP4, output, target, protocol.ImageOptions{}); err != nil {
				t.Fatal(err)
			}
			if !containsArg(got, output) || !containsArg(got, "-f") || !containsArg(got, string(target)) {
				t.Fatalf("args = %v", got)
			}
			if formats.IsAudioFormat(target) != containsArg(got, "-vn") {
				t.Fatalf("audio extraction flag mismatch for %s: %v", target, got)
			}
		})
	}
}

func TestFFmpegConvertExtractsAudio(t *testing.T) {
	var got []string
	f := NewFFmpeg(func(_ context.Context, _ string, args []string) error { got = args; return nil }, nil)
	f.executable = "/usr/bin/ffmpeg"
	if err := f.Convert(context.Background(), "/tmp/input.mp4", formats.MP4, "/tmp/out.mp3", formats.MP3, protocol.ImageOptions{}); err != nil {
		t.Fatal(err)
	}
	if !containsArg(got, "-vn") || !containsArg(got, "/tmp/out.mp3") {
		t.Fatalf("args = %v", got)
	}
}

func TestFFmpegConvertRejectsUnsafePathsAndAudioToVideo(t *testing.T) {
	f := NewFFmpeg(func(context.Context, string, []string) error { return nil }, nil)
	f.executable = "/usr/bin/ffmpeg"
	if err := f.Convert(context.Background(), "/tmp/input.mp3", formats.MP3, "/tmp/out.mp4", formats.MP4, protocol.ImageOptions{}); err == nil {
		t.Error("expected audio-to-video rejection")
	}
	if err := f.Convert(context.Background(), "/tmp/bad:name.mp4", formats.MP4, "/tmp/out.mp3", formats.MP3, protocol.ImageOptions{}); err == nil {
		t.Error("expected unsafe path rejection")
	}
}

func TestParseProbeRejectsInvalidOrUnsupportedStreams(t *testing.T) {
	for _, data := range []string{"not json", `{"streams":[]}`, `{"streams":[{"codec_type":"subtitle"}]}`} {
		if _, err := parseProbe([]byte(data)); err == nil {
			t.Errorf("parseProbe(%q) succeeded", data)
		}
	}
}

func TestFFmpegAvailableReportsMissingBinary(t *testing.T) {
	f := NewFFmpeg(nil, nil)
	f.executable = ""
	f.lookup = func(string) (string, error) { return "", errors.New("missing") }
	if _, err := f.Available(); err == nil || !strings.Contains(err.Error(), "ffmpeg not found") {
		t.Fatalf("Available() error = %v", err)
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
