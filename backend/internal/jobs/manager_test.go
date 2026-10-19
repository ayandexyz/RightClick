package jobs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

type fakeConverter struct {
	mu    sync.Mutex
	calls int
	block bool
}

func (f *fakeConverter) Convert(ctx context.Context, _ string, _ formats.Format, destination string, _ formats.Format) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return os.WriteFile(destination, []byte("converted"), 0o600)
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
