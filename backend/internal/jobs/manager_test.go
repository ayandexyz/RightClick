package jobs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
	"file-converter/backend/internal/registry"
)

type fakeConverter struct {
	mu    sync.Mutex
	calls int
	block bool
}

type fakeMediaEngine struct {
	mu     sync.Mutex
	calls  int
	target formats.Format
	kind   engine.MediaKind
}

func (f *fakeMediaEngine) ID() string                 { return "fake-media" }
func (f *fakeMediaEngine) Available() (string, error) { return "fake-media", nil }
func (f *fakeMediaEngine) Supports(source, target formats.Format) bool {
	return (formats.IsAudioFormat(source) || formats.IsVideoFormat(source)) &&
		(formats.IsAudioFormat(target) || formats.IsVideoFormat(target)) &&
		(!formats.IsAudioFormat(source) || formats.IsAudioFormat(target))
}
func (f *fakeMediaEngine) Probe(context.Context, string) (engine.MediaKind, error) {
	return f.kind, nil
}
func (f *fakeMediaEngine) Convert(_ context.Context, _ string, _ formats.Format, destination string, target formats.Format, _ protocol.ImageOptions) error {
	f.mu.Lock()
	f.calls++
	f.target = target
	f.mu.Unlock()
	return os.WriteFile(destination, []byte("converted"), 0o600)
}

func (f *fakeConverter) Convert(ctx context.Context, _ string, _ formats.Format, destination string, _ formats.Format, _ protocol.ImageOptions) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return os.WriteFile(destination, []byte("converted"), 0o600)
}
func mediaFixture(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("media fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func imageFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "input.png")
	if err := os.WriteFile(p, []byte("\x89PNG\r\n\x1a\n00000000"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitJob(t *testing.T, events <-chan protocol.Event) protocol.Job {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Name == "job.finished" {
				data, ok := event.Data.(protocol.Job)
				if ok {
					return data
				}
				t.Fatalf("unexpected event data %T", event.Data)
			}
		case <-timeout:
			t.Fatal("timed out waiting for job")
			return protocol.Job{}
		}
	}
}

func TestManagerConvertsAndEmitsFinished(t *testing.T) {
	fake := &fakeConverter{}
	events := make(chan protocol.Event, 16)
	m := NewManager(fake)
	defer m.Close()
	id, err := m.Submit(SubmitRequest{Files: []string{imageFixture(t)}, Preset: "image-to-jpeg", OutputDir: t.TempDir(), Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }})
	if err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, events)
	if job.ID != id || job.State != "completed" || len(job.Files) != 1 || job.Files[0].State != "done" {
		t.Fatalf("unexpected job: %+v", job)
	}
	if _, err := os.Stat(job.Files[0].Outputs[0]); err != nil {
		t.Fatal(err)
	}
}

func TestManagerAcceptsCaseInsensitivePreset(t *testing.T) {
	fake := &fakeConverter{}
	events := make(chan protocol.Event, 16)
	m := NewManager(fake)
	defer m.Close()
	id, err := m.Submit(SubmitRequest{Files: []string{imageFixture(t)}, Preset: "IMAGE-TO-JPEG", OutputDir: t.TempDir(), Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }})
	if err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, events)
	if job.ID != id || job.State != "completed" {
		t.Fatalf("unexpected job: %+v", job)
	}
}

func TestManagerRoutesVideoSourceToAudioOutputThroughAtomicLifecycle(t *testing.T) {
	source := mediaFixture(t, "input.mp4")
	media := &fakeMediaEngine{kind: engine.MediaVideo}
	r := registry.New()
	r.Register(media)
	m := NewManagerWithRegistry(r)
	defer m.Close()
	events := make(chan protocol.Event, 16)
	id, err := m.Submit(SubmitRequest{Files: []string{source}, Preset: "audio-to-mp3", OutputDir: t.TempDir(), Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }})
	if err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, events)
	if job.ID != id || job.State != "completed" || job.Files[0].State != "done" {
		t.Fatalf("unexpected job: %+v", job)
	}
	media.mu.Lock()
	calls, target := media.calls, media.target
	media.mu.Unlock()
	if calls != 1 || target != formats.MP3 {
		t.Fatalf("media engine calls = %d target = %s", calls, target)
	}
}

func TestManagerAcceptsAllMediaExtensionAliases(t *testing.T) {
	for _, alias := range []string{"mp4", "mov", "webm", "mkv", "avi", "mp3", "wav", "flac", "aac", "m4a", "ogg", "opus", "aiff", "alac", "ac3", "amr", "wma"} {
		m := NewManager(&fakeConverter{})
		_, err := m.Submit(SubmitRequest{Files: []string{imageFixture(t)}, Preset: alias, OutputDir: t.TempDir(), Conflict: protocol.ConflictRename})
		if err != nil {
			m.Close()
			t.Fatalf("alias %q rejected: %v", alias, err)
		}
		m.Close()
	}
}

func TestManagerRejectsAudioToVideoBeforeQueueing(t *testing.T) {
	media := &fakeMediaEngine{kind: engine.MediaAudio}
	r := registry.New()
	r.Register(media)
	m := NewManagerWithRegistry(r)
	defer m.Close()
	_, err := m.Submit(SubmitRequest{Files: []string{mediaFixture(t, "input.mp3")}, Preset: "audio-to-mp4", OutputDir: t.TempDir(), Conflict: protocol.ConflictRename})
	if err == nil || err.Error() != "unsupported preset" {
		t.Fatalf("Submit error = %v, want unsupported preset", err)
	}
	if m.Active() != 0 {
		t.Fatalf("invalid submission queued a job")
	}
}

func TestManagerCancellationRemovesTemporaryOutput(t *testing.T) {
	fake := &fakeConverter{block: true}
	events := make(chan protocol.Event, 16)
	source := imageFixture(t)
	output := t.TempDir()
	m := NewManager(fake)
	defer m.Close()
	id, err := m.Submit(SubmitRequest{Files: []string{source}, Preset: "image-to-jpeg", OutputDir: output, Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Name == "job.updated" {
				if j, ok := e.Data.(protocol.Job); ok && j.ID == id && j.Files[0].State == "running" {
					if err := m.Cancel(id); err != nil {
						t.Fatal(err)
					}
					job := waitJob(t, events)
					if job.State != "cancelled" {
						t.Fatalf("state %s", job.State)
					}
					entries, _ := os.ReadDir(output)
					if len(entries) != 0 {
						t.Fatalf("temporary output remains: %v", entries)
					}
					return
				}
			}
		case <-deadline:
			t.Fatal("job did not start")
		}
	}
}
