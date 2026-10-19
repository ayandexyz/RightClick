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
	reader := bufio.NewReader(conn)

	hello := request(t, conn, reader, map[string]any{
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

	targets := request(t, conn, reader, map[string]any{
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
	var jobID string
	var finished wireMessage
	deadline := time.After(15 * time.Second)
	for finished == nil {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for job.finished")
		default:
		}
		message, err := readMessage(reader)
		if err != nil {
			t.Fatal(err)
		}
		if result, ok := message["result"].(map[string]any); ok {
			jobID, _ = result["job"].(string)
		}
		if message["event"] == "job.finished" {
			finished = message
		}
	}
	if jobID == "" {
		t.Fatalf("conversion response did not include a job id: %#v", finished)
	}
	job, ok := finished["data"].(map[string]any)
	if !ok || job["state"] != "completed" {
		t.Fatalf("unexpected finished job: %#v", finished)
	}
	output := filepath.Join(outputDir, "photo.jpg")
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("expected output %s: %v", output, err)
	}
	if !bytes.HasPrefix(mustRead(t, output), []byte{0xff, 0xd8, 0xff}) {
		t.Fatalf("output is not a JPEG")
	}
}

func request(t *testing.T, conn net.Conn, reader *bufio.Reader, value map[string]any) wireMessage {
	t.Helper()
	if err := writeMessage(conn, value); err != nil {
		t.Fatal(err)
	}
	message, err := readMessage(reader)
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
