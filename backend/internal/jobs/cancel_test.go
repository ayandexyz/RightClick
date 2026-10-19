package jobs

import (
	"testing"
	"time"

	"file-converter/backend/internal/protocol"
)

func TestCancellationDoesNotReportRunningAtFullProgress(t *testing.T) {
	fake := &fakeConverter{block: true}
	events := make(chan protocol.Event, 16)
	m := NewManager(fake)
	defer m.Close()
	id, err := m.Submit(SubmitRequest{Files: []string{imageFixture(t)}, Preset: "image-to-jpeg", OutputDir: t.TempDir(), Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			job, ok := event.Data.(protocol.Job)
			if !ok || job.ID != id {
				continue
			}
			if event.Name == "job.updated" && job.State == "running" && job.Files[0].State == "running" {
				if err := m.Cancel(id); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if event.Name == "job.updated" && job.State == "running" && job.Progress >= 1 {
				t.Fatalf("cancellation reported running at full progress: %+v", job)
			}
			if event.Name == "job.finished" {
				if job.State != "cancelled" {
					t.Fatalf("unexpected final state: %+v", job)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for cancellation")
		}
	}
}
