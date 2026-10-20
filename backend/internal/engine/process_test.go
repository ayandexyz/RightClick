package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

func TestFFmpegCancellationKillsProcessGroupAndReturnsContextCanceled(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-ffmpeg.sh")
	ready := filepath.Join(dir, "ready")
	finished := filepath.Join(dir, "finished")
	childPID := filepath.Join(dir, "child.pid")
	content := "#!/bin/sh\nprintf ready > \"$READY\"\n( sleep 30; printf finished > \"$FINISHED\" ) &\nchild=$!\nprintf '%s' \"$child\" > \"$CHILD_PID\"\nwait \"$child\"\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("READY", ready); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("FINISHED", finished); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("CHILD_PID", childPID); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("READY")
	defer os.Unsetenv("FINISHED")
	defer os.Unsetenv("CHILD_PID")

	engine := NewFFmpeg(nil, nil)
	engine.executable = script
	engine.ffprobe = script
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- engine.Convert(ctx, "/tmp/source.mp4", formats.MP4, "/tmp/destination.mp4", formats.MP4, protocol.ImageOptions{})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatal("fake process did not start")
	}
	var pidText []byte
	for time.Now().Before(deadline) {
		pidText, _ = os.ReadFile(childPID)
		if len(pidText) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	child, err := strconv.Atoi(string(pidText))
	if err != nil {
		t.Fatalf("fake process did not record child PID: %q: %v", pidText, err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("conversion error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("conversion did not stop after cancellation")
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(finished); err == nil {
		t.Fatal("child process survived cancellation")
	}
	if err := syscall.Kill(child, 0); err == nil {
		t.Fatalf("child process %d survived cancellation", child)
	}
}

func TestImageMagickCancellationKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-magick.sh")
	ready := filepath.Join(dir, "ready")
	finished := filepath.Join(dir, "finished")
	content := "#!/bin/sh\nprintf ready > \"$READY\"\nsleep 30\nprintf finished > \"$FINISHED\"\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("READY", ready); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("FINISHED", finished); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("READY")
	defer os.Unsetenv("FINISHED")

	engine := NewImageMagick(nil)
	engine.executable = script
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- engine.Convert(ctx, "/tmp/source.png", formats.PNG, "/tmp/destination.jpg", formats.JPEG, protocol.ImageOptions{})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatal("fake process did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("conversion did not stop after cancellation")
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(finished); err == nil {
		t.Fatal("child process survived cancellation")
	}
}
