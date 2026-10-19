package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"file-converter/backend/internal/formats"
)

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
		done <- engine.Convert(ctx, "/tmp/source.png", formats.PNG, "/tmp/destination.jpg", formats.JPEG)
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
