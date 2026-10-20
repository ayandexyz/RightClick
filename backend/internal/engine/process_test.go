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

func TestPlanOutputExcludingTreatsInFlightDestinationsAsTaken(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(source, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	taken := map[string]bool{filepath.Join(dir, "image.jpg"): true}
	isTaken := func(path string) bool { return taken[path] }

	rename, err := PlanOutputExcluding(dir, source, "jpg", protocol.ConflictRename, isTaken)
	if err != nil {
		t.Fatal(err)
	}
	defer rename.Rollback()
	if want := filepath.Join(dir, "image (1).jpg"); rename.Destination != want {
		t.Fatalf("rename destination = %s, want %s", rename.Destination, want)
	}

	skip, err := PlanOutputExcluding(dir, source, "jpg", protocol.ConflictSkip, isTaken)
	if err != nil || !skip.Skipped() {
		t.Fatalf("skip plan = %+v, %v", skip, err)
	}

	// An in-flight sibling has no file yet to back up, so overwrite must not clobber it.
	overwrite, err := PlanOutputExcluding(dir, source, "jpg", protocol.ConflictOverwrite, isTaken)
	if err != nil {
		t.Fatal(err)
	}
	defer overwrite.Rollback()
	if want := filepath.Join(dir, "image (1).jpg"); overwrite.Destination != want && overwrite.Destination != filepath.Join(dir, "image (2).jpg") {
		t.Fatalf("overwrite destination = %s", overwrite.Destination)
	}
	if overwrite.Destination == filepath.Join(dir, "image.jpg") {
		t.Fatal("overwrite claimed an in-flight destination")
	}
}

func TestPlanOutputExcludingSkipsTakenRenameCandidates(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(source, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	taken := map[string]bool{filepath.Join(dir, "image.jpg"): true, filepath.Join(dir, "image (1).jpg"): true}
	plan, err := PlanOutputExcluding(dir, source, "jpg", protocol.ConflictRename, func(p string) bool { return taken[p] })
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Rollback()
	if want := filepath.Join(dir, "image (2).jpg"); plan.Destination != want {
		t.Fatalf("destination = %s, want %s", plan.Destination, want)
	}
}
