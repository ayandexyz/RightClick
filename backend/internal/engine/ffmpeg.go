package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"syscall"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

type MediaKind string

const (
	MediaAudio MediaKind = "audio"
	MediaVideo MediaKind = "video"
)

type Probe func(context.Context, string, []string) ([]byte, error)

type FFmpeg struct {
	executable string
	ffprobe    string
	runner     Runner
	probe      Probe
	lookup     func(string) (string, error)
}

func NewFFmpeg(runner Runner, probe Probe) *FFmpeg {
	return &FFmpeg{runner: runner, probe: probe, lookup: exec.LookPath}
}

func (e *FFmpeg) ID() string { return "ffmpeg" }

func (e *FFmpeg) Available() (string, error) {
	lookup := e.lookup
	if lookup == nil {
		lookup = exec.LookPath
	}
	if e.executable == "" {
		path, err := lookup("ffmpeg")
		if err != nil {
			return "", fmt.Errorf("ffmpeg not found: %w", err)
		}
		e.executable = path
	}
	if e.ffprobe == "" {
		path, err := lookup("ffprobe")
		if err != nil {
			return "", fmt.Errorf("ffprobe not found: %w", err)
		}
		e.ffprobe = path
	}
	return e.executable, nil
}

func (e *FFmpeg) Probe(ctx context.Context, path string) (MediaKind, error) {
	if err := formats.ValidatePath(path); err != nil {
		return "", fmt.Errorf("unsafe media path: %w", err)
	}
	args := []string{"-v", "error", "-show_entries", "stream=codec_type", "-of", "json", "--", path}
	var output []byte
	var err error
	if e.probe != nil {
		output, err = e.probe(ctx, e.ffprobe, args)
	} else {
		if e.ffprobe == "" {
			lookup := e.lookup
			if lookup == nil {
				lookup = exec.LookPath
			}
			e.ffprobe, err = lookup("ffprobe")
			if err != nil {
				return "", fmt.Errorf("ffprobe not found: %w", err)
			}
		}
		output, err = runOutputCommand(ctx, e.ffprobe, args)
	}
	if err != nil {
		return "", fmt.Errorf("ffprobe failed: %w", err)
	}
	return parseProbe(output)
}

func parseProbe(data []byte) (MediaKind, error) {
	var result struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("invalid ffprobe output: %w", err)
	}
	foundAudio := false
	for _, stream := range result.Streams {
		switch stream.CodecType {
		case "video":
			return MediaVideo, nil
		case "audio":
			foundAudio = true
		}
	}
	if foundAudio {
		return MediaAudio, nil
	}
	return "", fmt.Errorf("ffprobe found no audio or video streams")
}

func (e *FFmpeg) Supports(source, target formats.Format) bool {
	return (formats.IsAudioFormat(source) || formats.IsVideoFormat(source)) &&
		(formats.IsAudioFormat(target) || formats.IsVideoFormat(target)) &&
		(!formats.IsAudioFormat(source) || formats.IsAudioFormat(target))
}

func (e *FFmpeg) Convert(ctx context.Context, source string, sourceFormat formats.Format, destination string, target formats.Format, _ protocol.ImageOptions) error {
	if !e.Supports(sourceFormat, target) {
		return fmt.Errorf("unsupported media conversion: %s to %s", sourceFormat, target)
	}
	if err := formats.ValidatePath(source); err != nil {
		return fmt.Errorf("unsafe media path: %w", err)
	}
	if err := formats.ValidatePath(destination); err != nil {
		return fmt.Errorf("unsafe media path: %w", err)
	}
	executable := e.executable
	var err error
	if e.runner == nil {
		executable, err = e.Available()
		if err != nil {
			return err
		}
	}
	args := []string{"-y", "-i", source}
	if formats.IsAudioFormat(target) {
		args = append(args, "-vn")
	}
	args = append(args, "-f", string(target))
	args = append(args, targetDefaults(target)...)
	args = append(args, destination)
	if e.runner != nil {
		return e.runner(ctx, executable, args)
	}
	return runCommand(ctx, executable, args)
}

func runOutputCommand(ctx context.Context, executable string, args []string) ([]byte, error) {
	cmd := exec.Command(executable, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	result := make(chan struct {
		output []byte
		err    error
	}, 1)
	go func() {
		output, readErr := io.ReadAll(stdout)
		waitErr := cmd.Wait()
		if readErr != nil {
			result <- struct {
				output []byte
				err    error
			}{nil, readErr}
			return
		}
		result <- struct {
			output []byte
			err    error
		}{output, waitErr}
	}()
	select {
	case result := <-result:
		return result.output, result.err
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-result
		return nil, ctx.Err()
	}
}

func runCommand(ctx context.Context, executable string, args []string) error {
	cmd := exec.Command(executable, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		return err
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-wait
		return ctx.Err()
	}
}

func targetDefaults(target formats.Format) []string {
	switch target {
	case formats.AMR:
		return []string{"-ar", "8000", "-ac", "1"}
	case formats.WebM:
		return []string{"-c:v", "libvpx-vp9", "-c:a", "libopus"}
	case formats.MP4, formats.MOV, formats.MKV, formats.AVI:
		return []string{"-c:v", "libx264", "-c:a", "aac"}
	default:
		return nil
	}
}
