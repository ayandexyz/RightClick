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

type fakeSVGConverter struct {
	calls  int
	source formats.Format
	target formats.Format
}

func (f *fakeSVGConverter) ID() string                 { return "svg" }
func (f *fakeSVGConverter) Available() (string, error) { return "rsvg-convert", nil }
func (f *fakeSVGConverter) Supports(source, target formats.Format) bool {
	return formats.IsSVGFormat(source) && target == formats.PNG
}
func (f *fakeSVGConverter) Convert(_ context.Context, _ string, source formats.Format, destination string, target formats.Format, _ protocol.ImageOptions) error {
	f.calls++
	f.source = source
	f.target = target
	return os.WriteFile(destination, []byte("converted"), 0o600)
}

type fakeMediaEngine struct {
	mu             sync.Mutex
	calls          int
	target         formats.Format
	kind           engine.MediaKind
	probeCtx       context.Context
	probeStarted   chan struct{}
	probeBlock     bool
	convertBlock   bool
	convertStarted chan struct{}
	rejectAudio    bool
}

func (f *fakeMediaEngine) ID() string                 { return "fake-media" }
func (f *fakeMediaEngine) Available() (string, error) { return "fake-media", nil }
func (f *fakeMediaEngine) Supports(source, target formats.Format) bool {
	return (formats.IsAudioFormat(source) || formats.IsVideoFormat(source)) &&
		(formats.IsAudioFormat(target) || formats.IsVideoFormat(target)) &&
		(!formats.IsAudioFormat(source) || formats.IsAudioFormat(target)) &&
		!(f.rejectAudio && formats.IsAudioFormat(target))
}
func (f *fakeMediaEngine) Probe(ctx context.Context, _ string) (engine.MediaKind, error) {
	f.mu.Lock()
	f.probeCtx = ctx
	started := f.probeStarted
	block := f.probeBlock
	f.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return f.kind, nil
}
func (f *fakeMediaEngine) Convert(ctx context.Context, _ string, _ formats.Format, destination string, target formats.Format, _ protocol.ImageOptions) error {
	if f.convertBlock {
		if f.convertStarted != nil {
			select {
			case <-f.convertStarted:
			default:
				close(f.convertStarted)
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}
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

func TestManagerRunsSVGConversionThroughRegistryRoute(t *testing.T) {
	source := filepath.Join(t.TempDir(), "input.svg")
	if err := os.WriteFile(source, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := &fakeSVGConverter{}
	r := registry.New()
	r.Register(converter)
	m := NewManagerWithRegistry(r)
	defer m.Close()
	events := make(chan protocol.Event, 16)
	id, err := m.Submit(SubmitRequest{Files: []string{source}, Preset: "svg-to-png", OutputDir: t.TempDir(), Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }})
	if err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, events)
	if job.ID != id || job.State != "completed" || job.Files[0].State != "done" {
		t.Fatalf("unexpected job: %+v", job)
	}
	if converter.calls != 1 || converter.source != formats.SVG || converter.target != formats.PNG {
		t.Fatalf("SVG route calls = %d, %s to %s", converter.calls, converter.source, converter.target)
	}
}

func TestManagerAcceptsExplicitSVGPresets(t *testing.T) {
	m := NewManager(&fakeConverter{})
	defer m.Close()
	_, err := m.Submit(SubmitRequest{Files: []string{imageFixture(t)}, Preset: "svg-to-png", OutputDir: t.TempDir(), Conflict: protocol.ConflictRename})
	if err != nil {
		t.Fatalf("explicit SVG preset rejected: %v", err)
	}
}

func TestManagerRasterAliasesRemainRasterPresets(t *testing.T) {
	for _, tc := range []struct {
		alias string
		want  string
	}{
		{"png", "image-to-png"},
		{"jpeg", "image-to-jpeg"},
		{"webp", "image-to-webp"},
	} {
		t.Run(tc.alias, func(t *testing.T) {
			m := NewManager(&fakeConverter{})
			defer m.Close()
			events := make(chan protocol.Event, 16)
			_, err := m.Submit(SubmitRequest{Files: []string{imageFixture(t)}, Preset: tc.alias, OutputDir: t.TempDir(), Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }})
			if err != nil {
				t.Fatal(err)
			}
			job := waitJob(t, events)
			if job.Preset != tc.want {
				t.Fatalf("job preset = %q, want %q", job.Preset, tc.want)
			}
		})
	}
}

func TestManagerMediaAliasesUseCanonicalPresets(t *testing.T) {
	cases := []struct {
		alias string
		want  string
	}{
		{"mp4", "video-to-mp4"}, {"webm", "video-to-webm"}, {"mp3", "audio-to-mp3"},
		{"wav", "audio-to-wav"}, {"flac", "audio-to-flac"}, {"aac", "audio-to-aac"},
		{"m4a", "audio-to-m4a"}, {"ogg", "audio-to-ogg"}, {"opus", "audio-to-opus"},
	}
	for _, tc := range cases {
		t.Run(tc.alias, func(t *testing.T) {
			m := NewManager(&fakeConverter{})
			defer m.Close()
			events := make(chan protocol.Event, 16)
			_, err := m.Submit(SubmitRequest{Files: []string{imageFixture(t)}, Preset: tc.alias, OutputDir: t.TempDir(), Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }})
			if err != nil {
				t.Fatal(err)
			}
			job := waitJob(t, events)
			if job.Preset != tc.want {
				t.Fatalf("job preset = %q, want %q", job.Preset, tc.want)
			}
		})
	}
}

func TestManagerResolvesUnsupportedRouteBeforeConflictSkip(t *testing.T) {
	source := mediaFixture(t, "input.mp4")
	output := t.TempDir()
	if err := os.WriteFile(filepath.Join(output, "input.mp3"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := registry.New()
	r.Register(&fakeMediaEngine{kind: engine.MediaVideo, rejectAudio: true})
	m := NewManagerWithRegistry(r)
	defer m.Close()
	events := make(chan protocol.Event, 16)
	_, err := m.Submit(SubmitRequest{Files: []string{source}, Preset: "audio-to-mp3", OutputDir: output, Conflict: protocol.ConflictSkip, Events: func(e protocol.Event) { events <- e }})
	if err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, events)
	if job.Files[0].State != "failed" || job.Files[0].Error == nil || job.Files[0].Error.Code != "unsupported" {
		t.Fatalf("unexpected file result: %+v", job.Files[0])
	}
}

func TestManagerMediaProbeUsesJobContextOnCancellation(t *testing.T) {
	source := mediaFixture(t, "input.mp4")
	probeStarted := make(chan struct{})
	media := &fakeMediaEngine{kind: engine.MediaVideo, probeStarted: probeStarted, probeBlock: true}
	r := registry.New()
	r.Register(media)
	m := NewManagerWithRegistry(r)
	defer m.Close()
	events := make(chan protocol.Event, 16)
	id, err := m.Submit(SubmitRequest{Files: []string{source}, Preset: "audio-to-mp3", OutputDir: t.TempDir(), Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-probeStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("media probe did not start")
	}
	if err := m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, events)
	if job.State != "cancelled" || job.Files[0].State != "cancelled" {
		t.Fatalf("unexpected job: %+v", job)
	}
	media.mu.Lock()
	probeCtx := media.probeCtx
	media.mu.Unlock()
	if probeCtx == nil || probeCtx.Err() == nil {
		t.Fatal("probe did not receive cancelled job context")
	}
}

func TestManagerMediaConversionCancellationRemovesTemporaryOutput(t *testing.T) {
	source := mediaFixture(t, "input.mp4")
	output := t.TempDir()
	started := make(chan struct{})
	media := &fakeMediaEngine{kind: engine.MediaVideo, convertBlock: true, convertStarted: started}
	r := registry.New()
	r.Register(media)
	m := NewManagerWithRegistry(r)
	defer m.Close()
	events := make(chan protocol.Event, 16)
	id, err := m.Submit(SubmitRequest{Files: []string{source}, Preset: "audio-to-mp3", OutputDir: output, Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("media conversion did not start")
	}
	if err := m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, events)
	if job.State != "cancelled" || job.Files[0].State != "cancelled" {
		t.Fatalf("unexpected job: %+v", job)
	}
	entries, _ := os.ReadDir(output)
	if len(entries) != 0 {
		t.Fatalf("temporary output remains: %v", entries)
	}
}

func TestManagerMediaConflictPolicies(t *testing.T) {
	for _, policy := range []protocol.ConflictPolicy{protocol.ConflictRename, protocol.ConflictSkip, protocol.ConflictOverwrite} {
		t.Run(string(policy), func(t *testing.T) {
			source := mediaFixture(t, "input.mp4")
			output := t.TempDir()
			if err := os.WriteFile(filepath.Join(output, "input.mp3"), []byte("existing"), 0o600); err != nil {
				t.Fatal(err)
			}
			media := &fakeMediaEngine{kind: engine.MediaVideo}
			r := registry.New()
			r.Register(media)
			m := NewManagerWithRegistry(r)
			defer m.Close()
			events := make(chan protocol.Event, 16)
			_, err := m.Submit(SubmitRequest{Files: []string{source}, Preset: "audio-to-mp3", OutputDir: output, Conflict: policy, Events: func(e protocol.Event) { events <- e }})
			if err != nil {
				t.Fatal(err)
			}
			job := waitJob(t, events)
			file := job.Files[0]
			switch policy {
			case protocol.ConflictSkip:
				if file.State != "skipped" || len(file.Outputs) != 0 {
					t.Fatalf("skip result = %+v", file)
				}
				media.mu.Lock()
				calls := media.calls
				media.mu.Unlock()
				if calls != 0 {
					t.Fatalf("skip invoked media engine %d times", calls)
				}
			case protocol.ConflictRename:
				if file.State != "done" || len(file.Outputs) != 1 || file.Outputs[0] != filepath.Join(output, "input (1).mp3") {
					t.Fatalf("rename result = %+v", file)
				}
				data, err := os.ReadFile(file.Outputs[0])
				if err != nil || string(data) != "converted" {
					t.Fatalf("rename output = %q, err = %v", data, err)
				}
			case protocol.ConflictOverwrite:
				if file.State != "done" || len(file.Outputs) != 1 || file.Outputs[0] != filepath.Join(output, "input.mp3") {
					t.Fatalf("overwrite result = %+v", file)
				}
				data, err := os.ReadFile(file.Outputs[0])
				if err != nil || string(data) != "converted" {
					t.Fatalf("overwrite output = %q, err = %v", data, err)
				}
			}
		})
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
