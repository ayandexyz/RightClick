package backend_test

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const oneByOnePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

type wireMessage map[string]any

func TestDaemonEndToEndImageConversion(t *testing.T) {
	if _, err := exec.LookPath("magick"); err != nil {
		if _, err := exec.LookPath("convert"); err != nil {
			t.Skip("ImageMagick is not installed")
		}
	}

	runtimeDir := t.TempDir()
	socket := filepath.Join(runtimeDir, "file-converter", "fcd.sock")
	binary := filepath.Join(t.TempDir(), "fcd")
	build := exec.Command("go", "build", "-o", binary, "./cmd/fcd")
	build.Dir = "."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fcd: %v\n%s", err, output)
	}
	daemon := exec.Command(binary, "serve", "--idle-exit", "0")
	daemon.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = daemon.Process.Kill()
		_, _ = daemon.Process.Wait()
	}()

	waitForSocket(t, socket)
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stream := newE2EStream(conn)

	hello := request(t, stream, map[string]any{
		"id": 1, "method": "hello", "params": map[string]any{"client": "e2e", "protocol": 1},
	})
	if hello["error"] != nil {
		t.Fatalf("hello failed: %#v", hello)
	}

	input := filepath.Join(t.TempDir(), "photo.png")
	decoded, err := base64.StdEncoding.DecodeString(oneByOnePNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, decoded, 0o600); err != nil {
		t.Fatal(err)
	}
	outputDir := t.TempDir()

	targets := request(t, stream, map[string]any{
		"id": 2, "method": "targets", "params": map[string]any{"files": []string{input}},
	})
	if targets["error"] != nil {
		t.Fatalf("targets failed: %#v", targets)
	}
	if !containsTarget(targets, "image-to-jpeg") {
		t.Fatalf("JPEG target missing: %#v", targets)
	}

	if err := writeMessage(conn, map[string]any{
		"id": 3, "method": "convert", "params": map[string]any{
			"files": []string{input}, "preset": "image-to-jpeg", "output_dir": outputDir,
		},
	}); err != nil {
		t.Fatal(err)
	}
	jobID, finished := waitForJob(t, stream, 3, "image-to-jpeg", 15*time.Second)
	job, ok := finished["data"].(map[string]any)
	if !ok || job["state"] != "completed" {
		t.Fatalf("unexpected finished job: %#v", finished)
	}
	if jobID == "" {
		t.Fatalf("conversion response did not include a job id: %#v", finished)
	}
	output := filepath.Join(outputDir, "photo.jpg")
	if info, err := os.Stat(output); err != nil {
		t.Fatalf("expected output %s: %v", output, err)
	} else if info.Size() == 0 {
		t.Fatalf("output %s is empty", output)
	}
	if !bytes.HasPrefix(mustRead(t, output), []byte{0xff, 0xd8, 0xff}) {
		t.Fatalf("output is not a JPEG")
	}
}

func TestDaemonEndToEndSVGConversion(t *testing.T) {
	if _, err := exec.LookPath("rsvg-convert"); err != nil {
		t.Skipf("rsvg-convert is not installed: %v", err)
	}

	conn, stream, cleanup := startE2EDaemon(t)
	defer cleanup()
	if hello := request(t, stream, map[string]any{
		"id": 1, "method": "hello", "params": map[string]any{"client": "e2e", "protocol": 1},
	}); hello["error"] != nil {
		t.Fatalf("hello failed: %#v", hello)
	}

	input := writeE2ESVG(t, `<svg xmlns="http://www.w3.org/2000/svg" width="120" height="80"><rect width="120" height="80" fill="red"/><circle cx="60" cy="40" r="20" fill="blue"/></svg>`)
	targets := request(t, stream, map[string]any{
		"id": 2, "method": "targets", "params": map[string]any{"files": []string{input}},
	})
	if targets["error"] != nil {
		t.Fatalf("SVG targets failed: %#v", targets)
	}
	if !containsTarget(targets, "svg-to-png") {
		t.Fatalf("SVG target %q missing: %#v", "svg-to-png", targets)
	}

	outputDir := t.TempDir()
	png := convertAndAssertOutput(t, conn, stream, 3, input, "svg-to-png", outputDir, ".png")
	if signature := mustRead(t, png); len(signature) < 8 || !bytes.Equal(signature[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		t.Fatalf("output is not a PNG: %x", signature[:minInt(len(signature), 8)])
	}

	for id, target := range map[int]string{4: "jpeg", 5: "webp"} {
		t.Run("svg-to-"+target, func(t *testing.T) {
			if !imageMagickCanWrite(t, target) {
				t.Skipf("ImageMagick cannot write %s", target)
			}
			if !containsTarget(targets, "svg-to-"+target) {
				t.Fatalf("SVG target %q missing: %#v", "svg-to-"+target, targets)
			}
			extension := "." + target
			if target == "jpeg" {
				extension = ".jpg"
			}
			convertAndAssertOutput(t, conn, stream, id, input, "svg-to-"+target, outputDir, extension)
		})
	}
}

func TestDaemonEndToEndRejectsUnsafeSVG(t *testing.T) {
	if _, err := exec.LookPath("rsvg-convert"); err != nil {
		t.Skipf("rsvg-convert is not installed: %v", err)
	}
	conn, stream, cleanup := startE2EDaemon(t)
	defer cleanup()
	if hello := request(t, stream, map[string]any{
		"id": 1, "method": "hello", "params": map[string]any{"client": "e2e", "protocol": 1},
	}); hello["error"] != nil {
		t.Fatalf("hello failed: %#v", hello)
	}

	input := writeE2ESVG(t, `<!DOCTYPE svg [<!ENTITY external SYSTEM "file:///etc/passwd">]><svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><text>&external;</text></svg>`)
	outputDir := t.TempDir()
	if err := writeMessage(conn, map[string]any{
		"id": 2, "method": "convert", "params": map[string]any{
			"files": []string{input}, "preset": "svg-to-png", "output_dir": outputDir,
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, finished := waitForJob(t, stream, 2, "svg-to-png", 15*time.Second)
	job, ok := finished["data"].(map[string]any)
	if !ok || job["state"] != "failed" {
		t.Fatalf("unsafe SVG was not rejected: %#v", finished)
	}
	output := filepath.Join(outputDir, "input.png")
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("unsafe SVG produced output %s: %v", output, err)
	}
}

func writeE2ESVG(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.svg")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func imageMagickCanWrite(t *testing.T, format string) bool {
	t.Helper()
	executable := ""
	for _, candidate := range []string{"magick", "convert"} {
		if path, err := exec.LookPath(candidate); err == nil {
			executable = path
			break
		}
	}
	if executable == "" {
		return false
	}
	output, err := exec.Command(executable, "-list", "format").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && strings.EqualFold(strings.TrimRight(fields[0], "*"), format) {
			return strings.ContainsRune(fields[2], 'w')
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestE2EFFmpegMediaConversion(t *testing.T) {
	for _, dependency := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(dependency); err != nil {
			t.Skipf("%s is not installed", dependency)
		}
	}

	mediaDir := t.TempDir()
	video := filepath.Join(mediaDir, "fixture.mkv")
	if output, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").CombinedOutput(); err != nil {
		t.Skipf("cannot inspect ffmpeg encoders: %v", err)
	} else if !bytes.Contains(output, []byte(" mpeg4 ")) || !bytes.Contains(output, []byte(" pcm_s16le ")) {
		t.Skip("ffmpeg does not provide the broadly available mpeg4 and pcm_s16le fixture encoders")
	}
	generate := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "color=c=black:s=16x16:r=1", "-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=8000", "-t", "1", "-c:v", "mpeg4", "-c:a", "pcm_s16le", video)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate media fixture: %v\\n%s", err, output)
	}

	conn, stream, cleanup := startE2EDaemon(t)
	defer cleanup()
	if hello := request(t, stream, map[string]any{
		"id": 1, "method": "hello", "params": map[string]any{"client": "e2e", "protocol": 1},
	}); hello["error"] != nil {
		t.Fatalf("hello failed: %#v", hello)
	}

	targets := request(t, stream, map[string]any{
		"id": 2, "method": "targets", "params": map[string]any{"files": []string{video}},
	})
	if targets["error"] != nil {
		t.Fatalf("video targets failed: %#v", targets)
	}
	assertTargetPresets(t, targets, append([]string{"video-to-mp4", "video-to-mov", "video-to-webm", "video-to-avi", "video-to-gif"}, audioPresetsForE2E()...))

	outputDir := t.TempDir()
	mp4 := convertAndAssertOutput(t, conn, stream, 3, video, "video-to-mp4", outputDir, ".mp4")
	assertMediaOutput(t, mp4, "mp4", "video")
	gif := convertAndAssertOutput(t, conn, stream, 4, video, "video-to-gif", outputDir, ".gif")
	assertMediaOutput(t, gif, "gif", "video")
	mp3 := convertAndAssertOutput(t, conn, stream, 4, video, "audio-to-mp3", outputDir, ".mp3")
	assertMediaOutput(t, mp3, "mp3", "audio")

	audioTargets := request(t, stream, map[string]any{
		"id": 5, "method": "targets", "params": map[string]any{"files": []string{mp3}},
	})
	if audioTargets["error"] != nil {
		t.Fatalf("audio targets failed: %#v", audioTargets)
	}
	assertTargetPresets(t, audioTargets, []string{
		"audio-to-wav", "audio-to-flac", "audio-to-aac", "audio-to-m4a", "audio-to-ogg", "audio-to-opus",
		"audio-to-aiff", "audio-to-alac", "audio-to-ac3", "audio-to-amr", "audio-to-wma",
	})
	for _, target := range targetPresets(audioTargets) {
		if strings.HasPrefix(target, "video-to-") {
			t.Fatalf("audio targets included video preset %q", target)
		}
	}
}

func startE2EDaemon(t *testing.T) (net.Conn, *e2eStream, func()) {
	t.Helper()
	runtimeDir := t.TempDir()
	socket := filepath.Join(runtimeDir, "file-converter", "fcd.sock")
	binary := filepath.Join(t.TempDir(), "fcd")
	build := exec.Command("go", "build", "-o", binary, "./cmd/fcd")
	build.Dir = "."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fcd: %v\\n%s", err, output)
	}
	daemon := exec.Command(binary, "serve", "--idle-exit", "0")
	daemon.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	waitForSocket(t, socket)
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		daemon.Process.Kill()
		daemon.Wait()
		t.Fatal(err)
	}
	return conn, newE2EStream(conn), func() {
		_ = conn.Close()
		_ = daemon.Process.Kill()
		_, _ = daemon.Process.Wait()
	}
}

func waitForJob(t *testing.T, stream *e2eStream, requestID int, preset string, timeout time.Duration) (string, wireMessage) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var jobID string
	for {
		message, ok := stream.takeMatching(func(message wireMessage) bool {
			responseID, responseOK := message["id"].(float64)
			if responseOK && int(responseID) == requestID {
				result, resultOK := message["result"].(map[string]any)
				if message["error"] != nil {
					t.Fatalf("%s failed: %#v", preset, message)
				}
				if !resultOK {
					t.Fatalf("%s response missing result: %#v", preset, message)
				}
				jobID, _ = result["job"].(string)
				if jobID == "" {
					t.Fatalf("%s response missing job id: %#v", preset, message)
				}
				return true
			}
			if message["event"] == "job.finished" {
				job, jobOK := message["data"].(map[string]any)
				return jobOK && jobID != "" && job["id"] == jobID && job["preset"] == preset
			}
			return false
		})
		if ok {
			job, jobOK := message["data"].(map[string]any)
			if jobOK && job["id"] == jobID && job["preset"] == preset {
				return jobID, message
			}
			continue
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", preset)
		}
		message, err := stream.read(time.Until(deadline))
		if err != nil {
			t.Fatalf("waiting for %s: %v", preset, err)
		}
		stream.pending = append(stream.pending, message)
	}
}

func convertAndAssertOutput(t *testing.T, conn net.Conn, stream *e2eStream, id int, input, preset, outputDir, extension string) string {
	t.Helper()
	if err := writeMessage(conn, map[string]any{
		"id": id, "method": "convert", "params": map[string]any{
			"files": []string{input}, "preset": preset, "output_dir": outputDir,
		},
	}); err != nil {
		t.Fatal(err)
	}
	jobID, finished := waitForJob(t, stream, id, preset, 30*time.Second)
	job, ok := finished["data"].(map[string]any)
	if !ok || job["state"] != "completed" {
		t.Fatalf("unexpected %s job: %#v", preset, finished)
	}
	if jobID == "" {
		t.Fatalf("%s response did not include a job id: %#v", preset, finished)
	}
	output := filepath.Join(outputDir, strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))+extension)
	if info, err := os.Stat(output); err != nil {
		t.Fatalf("expected %s output %s: %v", preset, output, err)
	} else if info.Size() == 0 {
		t.Fatalf("%s output %s is empty", preset, output)
	}
	return output
}

func assertMediaOutput(t *testing.T, path, format, codecType string) {
	t.Helper()
	probe := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=format_name:stream=codec_type", "-of", "json", "--", path)
	output, err := probe.Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var result struct {
		Format struct {
			Name string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("parse ffprobe output for %s: %v", path, err)
	}
	if !strings.Contains(result.Format.Name, format) {
		t.Fatalf("ffprobe format for %s = %q, want %q", path, result.Format.Name, format)
	}
	for _, stream := range result.Streams {
		if stream.CodecType == codecType {
			return
		}
	}
	t.Fatalf("ffprobe found no %s stream in %s", codecType, path)
}

func assertTargetPresets(t *testing.T, message wireMessage, want []string) {
	t.Helper()
	got := targetPresets(message)
	if len(got) != len(want) {
		t.Fatalf("target presets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("target presets = %v, want %v", got, want)
		}
	}
}

func targetPresets(message wireMessage) []string {
	result, ok := message["result"].(map[string]any)
	if !ok {
		return nil
	}
	items, ok := result["targets"].([]any)
	if !ok {
		return nil
	}
	presets := make([]string, 0, len(items))
	for _, item := range items {
		if target, ok := item.(map[string]any); ok {
			if preset, ok := target["preset"].(string); ok {
				presets = append(presets, preset)
			}
		}
	}
	return presets
}

func audioPresetsForE2E() []string {
	return []string{"audio-to-mp3", "audio-to-wav", "audio-to-flac", "audio-to-aac", "audio-to-m4a", "audio-to-ogg", "audio-to-opus", "audio-to-aiff", "audio-to-alac", "audio-to-ac3", "audio-to-amr", "audio-to-wma"}
}

type e2eStream struct {
	conn    net.Conn
	reader  *bufio.Reader
	pending []wireMessage
}

func newE2EStream(conn net.Conn) *e2eStream {
	return &e2eStream{conn: conn, reader: bufio.NewReader(conn)}
}

func (stream *e2eStream) read(timeout time.Duration) (wireMessage, error) {
	if err := stream.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	return readMessage(stream.reader)
}

func (stream *e2eStream) takeMatching(match func(wireMessage) bool) (wireMessage, bool) {
	for index, message := range stream.pending {
		if match(message) {
			stream.pending = append(stream.pending[:index], stream.pending[index+1:]...)
			return message, true
		}
	}
	return nil, false
}

func request(t *testing.T, stream *e2eStream, value map[string]any) wireMessage {
	t.Helper()
	if err := writeMessage(stream.conn, value); err != nil {
		t.Fatal(err)
	}
	message, err := stream.read(15 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func writeMessage(conn net.Conn, value map[string]any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}

func readMessage(reader *bufio.Reader) (wireMessage, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var message wireMessage
	if err := json.Unmarshal(line, &message); err != nil {
		return nil, err
	}
	return message, nil
}

func waitForSocket(t *testing.T, socket string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("daemon did not create socket %s", socket)
}

func containsTarget(message wireMessage, preset string) bool {
	result, ok := message["result"].(map[string]any)
	if !ok {
		return false
	}
	targets, ok := result["targets"].([]any)
	if !ok {
		return false
	}
	for _, item := range targets {
		if target, ok := item.(map[string]any); ok && target["preset"] == preset {
			return true
		}
	}
	return false
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(fmt.Errorf("read %s: %w", path, err))
	}
	return data
}
