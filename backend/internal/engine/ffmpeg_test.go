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
	kind, err := f.Probe(context.Background(), "/tmp/input.mp3")
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
	video := []formats.Format{formats.MP4, formats.MOV, formats.WebM, formats.MKV, formats.AVI}
	audio := []formats.Format{formats.MP3, formats.WAV, formats.FLAC, formats.AAC, formats.M4A, formats.OGG, formats.Opus}
	images := []formats.Format{formats.PNG, formats.JPEG, formats.WebP, formats.JXL, formats.AVIF, formats.GIF, formats.BMP, formats.TIFF, formats.ICO, formats.HEIC}
	for _, source := range video {
		for _, target := range append(append([]formats.Format(nil), video...), audio...) {
			if !f.Supports(source, target) {
				t.Errorf("video to %s should be supported", target)
			}
		}
	}
	for _, source := range audio {
		for _, target := range audio {
			if !f.Supports(source, target) {
				t.Errorf("audio to %s should be supported", target)
			}
		}
		for _, target := range video {
			if f.Supports(source, target) {
				t.Errorf("audio to %s should be rejected", target)
			}
		}
	}
	for _, source := range images {
		for _, target := range append(append([]formats.Format(nil), video...), audio...) {
			if f.Supports(source, target) {
				t.Errorf("image to %s should be rejected", target)
			}
		}
	}
	for _, source := range append(append([]formats.Format(nil), video...), audio...) {
		for _, target := range images {
			if f.Supports(source, target) {
				t.Errorf("media to image %s should be rejected", target)
			}
		}
	}
}

func TestFFmpegConvertBuildsEveryRequiredRoute(t *testing.T) {
	targets := []formats.Format{formats.MP4, formats.MOV, formats.WebM, formats.MKV, formats.AVI, formats.MP3, formats.WAV, formats.FLAC, formats.AAC, formats.M4A, formats.OGG, formats.Opus}
	sources := []formats.Format{formats.MP4, formats.MOV, formats.WebM, formats.MKV, formats.AVI, formats.MP3, formats.WAV, formats.FLAC, formats.AAC, formats.M4A, formats.OGG, formats.Opus}
	for _, source := range sources {
		for _, target := range targets {
			t.Run(string(source)+"-to-"+string(target), func(t *testing.T) {
				if !NewFFmpeg(nil, nil).Supports(source, target) {
					if formats.IsAudioFormat(source) && formats.IsVideoFormat(target) {
						return
					}
					t.Fatalf("route should be supported")
				}
				var got []string
				f := NewFFmpeg(func(_ context.Context, _ string, args []string) error {
					got = append([]string(nil), args...)
					return nil
				}, nil)
				f.executable = "/usr/bin/ffmpeg"
				f.ffprobe = "/usr/bin/ffprobe"
				output := "/tmp/out." + string(target)
				if err := f.Convert(context.Background(), "/tmp/input."+string(source), source, output, target, protocol.ImageOptions{}); err != nil {
					t.Fatal(err)
				}
				want := []string{"-y", "-i", "/tmp/input." + string(source)}
				if formats.IsAudioFormat(target) {
					want = append(want, "-vn")
				}
				want = append(want, "-f", string(target))
				want = append(want, targetDefaults(target)...)
				want = append(want, output)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("args = %v, want %v", got, want)
				}
			})
		}
	}
}

func TestFFmpegVideoDefaultsAreNotAppliedToAudioExtraction(t *testing.T) {
	var got []string
	f := NewFFmpeg(func(_ context.Context, _ string, args []string) error { got = args; return nil }, nil)
	f.executable = "/usr/bin/ffmpeg"
	f.ffprobe = "/usr/bin/ffprobe"
	if err := f.Convert(context.Background(), "/tmp/input.mp4", formats.MP4, "/tmp/out.mp3", formats.MP3, protocol.ImageOptions{}); err != nil {
		t.Fatal(err)
	}
	if containsArg(got, "-c:v") || containsArg(got, "-c:a") {
		t.Fatalf("audio extraction received video defaults: %v", got)
	}
}

func TestFFmpegConvertExtractsAudio(t *testing.T) {
	var got []string
	f := NewFFmpeg(func(_ context.Context, _ string, args []string) error { got = args; return nil }, nil)
	f.executable = "/usr/bin/ffmpeg"
	f.ffprobe = "/usr/bin/ffprobe"
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
	f.ffprobe = "/usr/bin/ffprobe"
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

func TestFFmpegAvailableRequiresFFprobe(t *testing.T) {
	f := NewFFmpeg(nil, nil)
	f.lookup = func(name string) (string, error) {
		if name == "ffmpeg" {
			return "/usr/bin/ffmpeg", nil
		}
		return "", errors.New("missing")
	}
	if _, err := f.Available(); err == nil || !strings.Contains(err.Error(), "ffprobe not found") {
		t.Fatalf("Available() error = %v", err)
	}
}

func TestFFmpegProbePassesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got context.Context
	f := NewFFmpeg(nil, func(probeCtx context.Context, _ string, _ []string) ([]byte, error) {
		got = probeCtx
		return []byte(`{"streams":[{"codec_type":"audio"}]}`), nil
	})
	f.ffprobe = "/usr/bin/ffprobe"
	if _, err := f.Probe(ctx, "/tmp/input.mp3"); err != nil {
		t.Fatal(err)
	}
	if got != ctx {
		t.Fatal("probe did not receive caller context")
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
