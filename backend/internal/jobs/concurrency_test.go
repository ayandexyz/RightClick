package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
	"file-converter/backend/internal/registry"
)

// gateEngine converts anything to anything, holding each conversion for a
// per-file duration while recording how many ran at once.
type gateEngine struct {
	mu                sync.Mutex
	running           int
	maxRunning        int
	videoRunning      int
	maxVideo          int
	imageDuringVideo  bool
	started           []string
	holds             map[string]time.Duration
	blockUntilContext bool
	startedCh         chan string
}

func (g *gateEngine) ID() string                 { return "gate" }
func (g *gateEngine) Available() (string, error) { return "gate", nil }
func (g *gateEngine) Supports(source, target formats.Format) bool {
	if formats.IsAudioFormat(source) && formats.IsVideoFormat(target) {
		return false
	}
	return source != target
}
func (g *gateEngine) Probe(_ context.Context, path string) (engine.MediaKind, error) {
	switch filepath.Ext(path) {
	case ".mp4":
		return engine.MediaVideo, nil
	case ".mp3":
		return engine.MediaAudio, nil
	}
	return "", fmt.Errorf("not media")
}
func (g *gateEngine) Convert(ctx context.Context, source string, sourceFormat formats.Format, destination string, target formats.Format, _ protocol.ImageOptions) error {
	isVideo := formats.IsVideoFormat(sourceFormat) || formats.IsVideoFormat(target)
	g.mu.Lock()
	g.running++
	if g.running > g.maxRunning {
		g.maxRunning = g.running
	}
	if isVideo {
		g.videoRunning++
		if g.videoRunning > g.maxVideo {
			g.maxVideo = g.videoRunning
		}
	} else if g.videoRunning > 0 {
		g.imageDuringVideo = true
	}
	g.started = append(g.started, filepath.Base(source))
	hold := g.holds[filepath.Base(source)]
	g.mu.Unlock()
	if g.startedCh != nil {
		g.startedCh <- filepath.Base(source)
	}
	defer func() {
		g.mu.Lock()
		g.running--
		if isVideo {
			g.videoRunning--
		}
		g.mu.Unlock()
	}()
	if g.blockUntilContext {
		<-ctx.Done()
		return ctx.Err()
	}
	if hold == 0 {
		hold = 30 * time.Millisecond
	}
	select {
	case <-time.After(hold):
	case <-ctx.Done():
		return ctx.Err()
	}
	return os.WriteFile(destination, []byte("converted"), 0o600)
}

func gateFiles(t *testing.T, dir string, names ...string) []string {
	t.Helper()
	var out []string
	for _, name := range names {
		content := "\x89PNG\r\n\x1a\nfixture"
		if strings.HasSuffix(name, ".mp4") || strings.HasSuffix(name, ".mp3") {
			content = "media fixture"
		}
		out = append(out, writeFixture(t, filepath.Join(dir, name), content))
	}
	return out
}

func newGateManager(t *testing.T, gate *gateEngine, workers int) *Manager {
	t.Helper()
	r := registry.New()
	r.Register(gate)
	m := NewManagerWithRegistry(r)
	m.SetMaxWorkers(workers)
	t.Cleanup(m.Close)
	return m
}

func submitAndWait(t *testing.T, m *Manager, files []string, preset string) protocol.Job {
	t.Helper()
	events := make(chan protocol.Event, 256)
	if _, err := m.Submit(SubmitRequest{Files: files, Preset: preset, OutputDir: t.TempDir(), Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }}); err != nil {
		t.Fatal(err)
	}
	return waitJob(t, events)
}

func TestDefaultWorkersIsBoundedByCPUCount(t *testing.T) {
	n := DefaultWorkers()
	if n < 1 || n > runtime.NumCPU() {
		t.Fatalf("DefaultWorkers() = %d with %d CPUs", n, runtime.NumCPU())
	}
}

func TestManagerNeverRunsMoreConversionsThanMaxWorkers(t *testing.T) {
	gate := &gateEngine{}
	m := newGateManager(t, gate, 2)
	files := gateFiles(t, t.TempDir(), "1.png", "2.png", "3.png", "4.png", "5.png", "6.png")
	job := submitAndWait(t, m, files, "image-to-jpeg")
	if job.State != "completed" {
		t.Fatalf("job = %+v", job)
	}
	if gate.maxRunning != 2 {
		t.Fatalf("max concurrent conversions = %d, want exactly 2", gate.maxRunning)
	}
}

func TestSetMaxWorkersClampsToAtLeastOne(t *testing.T) {
	gate := &gateEngine{}
	m := newGateManager(t, gate, 0)
	files := gateFiles(t, t.TempDir(), "1.png", "2.png", "3.png")
	submitAndWait(t, m, files, "image-to-jpeg")
	if gate.maxRunning != 1 {
		t.Fatalf("max concurrent = %d, want 1", gate.maxRunning)
	}
}

func TestManagerNeverOverlapsTwoVideoConversions(t *testing.T) {
	gate := &gateEngine{}
	m := newGateManager(t, gate, 4)
	files := gateFiles(t, t.TempDir(), "a.mp4", "b.mp4", "c.mp4", "d.mp4")
	job := submitAndWait(t, m, files, "video-to-mkv")
	if job.State != "completed" {
		t.Fatalf("job = %+v", job)
	}
	if gate.maxVideo != 1 {
		t.Fatalf("overlapping video conversions = %d, want 1", gate.maxVideo)
	}
}

func TestVideoLaneLeavesRemainingWorkersForOtherFiles(t *testing.T) {
	gate := &gateEngine{holds: map[string]time.Duration{
		"a.mp4": 150 * time.Millisecond, "b.mp4": 150 * time.Millisecond, "c.mp4": 150 * time.Millisecond,
	}}
	m := newGateManager(t, gate, 3)
	dir := t.TempDir()
	// video-to-gif is the only preset that accepts both video and image sources in this fake.
	files := gateFiles(t, dir, "a.mp4", "b.mp4", "c.mp4", "1.png", "2.png", "3.png")
	job := submitAndWait(t, m, files, "image-to-gif")
	_ = job
	if gate.maxVideo > 1 {
		t.Fatalf("overlapping video conversions = %d", gate.maxVideo)
	}
	if gate.maxRunning > 3 {
		t.Fatalf("max concurrent = %d, want <= 3", gate.maxRunning)
	}
	if !gate.imageDuringVideo {
		t.Fatal("non-video files never ran while the video lane was busy")
	}
}

func TestManagerKeepsInputOrderWhenFilesFinishOutOfOrder(t *testing.T) {
	gate := &gateEngine{holds: map[string]time.Duration{"1.png": 150 * time.Millisecond, "2.png": 10 * time.Millisecond, "3.png": 10 * time.Millisecond}}
	m := newGateManager(t, gate, 3)
	files := gateFiles(t, t.TempDir(), "1.png", "2.png", "3.png")
	job := submitAndWait(t, m, files, "image-to-jpeg")
	for i, f := range job.Files {
		if f.Source != files[i] || f.State != "done" {
			t.Fatalf("file %d = %+v, want source %s", i, f, files[i])
		}
	}
}

func TestQueuedJobIsCancelledWithoutRunningAndRunningJobGetsContextCancel(t *testing.T) {
	gate := &gateEngine{blockUntilContext: true, startedCh: make(chan string, 8)}
	m := newGateManager(t, gate, 2)
	events := make(chan protocol.Event, 256)
	handler := func(e protocol.Event) { events <- e }
	first, err := m.Submit(SubmitRequest{Files: gateFiles(t, t.TempDir(), "first.png"), Preset: "image-to-jpeg", OutputDir: t.TempDir(), Conflict: protocol.ConflictRename, Events: handler})
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Submit(SubmitRequest{Files: gateFiles(t, t.TempDir(), "second.png"), Preset: "image-to-jpeg", OutputDir: t.TempDir(), Conflict: protocol.ConflictRename, Events: handler})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case name := <-gate.startedCh:
		if name != "first.png" {
			t.Fatalf("started %s first", name)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first job never started")
	}
	// The second job is still queued behind the running first one.
	if err := m.Cancel(second); err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel(first); err != nil {
		t.Fatal(err)
	}
	finished := map[string]protocol.Job{}
	deadline := time.After(3 * time.Second)
	for len(finished) < 2 {
		select {
		case e := <-events:
			if job, ok := e.Data.(protocol.Job); ok && e.Name == "job.finished" {
				finished[job.ID] = job
			}
		case <-deadline:
			t.Fatalf("timed out; finished = %v", finished)
		}
	}
	for _, id := range []string{first, second} {
		job := finished[id]
		if job.State != "cancelled" || job.Files[0].State != "cancelled" {
			t.Fatalf("%s = %+v", id, job)
		}
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	for _, name := range gate.started {
		if name == "second.png" {
			t.Fatal("cancelled queued job still ran a conversion")
		}
	}
}

func TestConcurrentConversionsWithSameStemGetDistinctOutputs(t *testing.T) {
	gate := &gateEngine{}
	m := newGateManager(t, gate, 6)
	base := t.TempDir()
	var files []string
	for i := 0; i < 6; i++ {
		files = append(files, gateFiles(t, filepath.Join(base, fmt.Sprint("src", i)), "image.png")...)
	}
	out := t.TempDir()
	events := make(chan protocol.Event, 256)
	if _, err := m.Submit(SubmitRequest{Files: files, Preset: "image-to-jpeg", OutputDir: out, Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, events)
	seen := map[string]bool{}
	for _, f := range job.Files {
		if f.State != "done" || len(f.Outputs) != 1 || seen[f.Outputs[0]] {
			t.Fatalf("bad file result %+v (duplicates: %v)", f, seen)
		}
		seen[f.Outputs[0]] = true
	}
	if gate.maxRunning < 2 {
		t.Fatalf("conversions did not overlap (max %d), so the collision case was not exercised", gate.maxRunning)
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 6 {
		t.Fatalf("output dir has %d entries, want 6 (leftover temp files?)", len(entries))
	}
}
