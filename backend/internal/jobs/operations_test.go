package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/pdfops"
	"file-converter/backend/internal/protocol"
	"file-converter/backend/internal/registry"
)

// fakePDFBackend writes small PDF-shaped files and records what it was asked to
// do. fail holds an optional error to return per source path.
type fakePDFBackend struct {
	mu           sync.Mutex
	pages        int
	fail         map[string]error
	pageCountErr error // fails the operation while the PageCount probe succeeds
	destinations []string
	merged       [][]string
	imported     []int
}

func newFakePDFBackend(pages int) *fakePDFBackend {
	return &fakePDFBackend{pages: pages, fail: map[string]error{}}
}

func (f *fakePDFBackend) ID() string       { return "fake-pdf" }
func (f *fakePDFBackend) Available() error { return nil }

// unreadable marks paths that PageCount fails on, so tests can tell a damaged
// input apart from an operation that failed on a readable one.
func (f *fakePDFBackend) PageCount(_ context.Context, path string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.fail[path]; ok && err != nil {
		return 0, err
	}
	return f.pages, nil
}

func (f *fakePDFBackend) Merge(_ context.Context, inputs []string, output string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pageCountErr != nil {
		return f.pageCountErr
	}
	if err, ok := f.fail[inputs[0]]; ok && err != nil {
		return err
	}
	f.merged = append(f.merged, inputs)
	return writePDFStub(output)
}

func (f *fakePDFBackend) Collect(_ context.Context, input, output string, pages []int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pageCountErr != nil {
		return f.pageCountErr
	}
	if err, ok := f.fail[input]; ok && err != nil {
		return err
	}
	f.destinations = append(f.destinations, output)
	return writePDFStub(output)
}

func (f *fakePDFBackend) Rotate(_ context.Context, input, output string, _ []int, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pageCountErr != nil {
		return f.pageCountErr
	}
	if err, ok := f.fail[input]; ok && err != nil {
		return err
	}
	f.destinations = append(f.destinations, output)
	return writePDFStub(output)
}

func (f *fakePDFBackend) StripMetadata(_ context.Context, _ string, _ string) error { return nil }
func (f *fakePDFBackend) ImportImages(_ context.Context, inputs []string, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imported = append(f.imported, len(inputs))
	return nil
}

func writePDFStub(path string) error {
	return os.WriteFile(path, []byte("%PDF-1.7\nstub\n"), 0o600)
}

func writePDFSource(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("%PDF-1.7\nsource\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newOperationManager(t *testing.T, backend pdfops.Backend) *Manager {
	t.Helper()
	m := NewManagerWithRegistry(registry.New())
	m.SetPDFToolbox(pdfops.Toolbox{PDFCPU: backend})
	t.Cleanup(m.Close)
	return m
}

func submitOp(t *testing.T, m *Manager, req OperationRequest) protocol.Job {
	t.Helper()
	events := make(chan protocol.Event, 64)
	req.Events = func(e protocol.Event) { events <- e }
	if _, err := m.SubmitOperation(req); err != nil {
		t.Fatalf("SubmitOperation: %v", err)
	}
	return waitJob(t, events)
}

func TestSubmitOperationRejectsBadRequests(t *testing.T) {
	dir := t.TempDir()
	source := writePDFSource(t, dir, "a.pdf")
	m := newOperationManager(t, newFakePDFBackend(4))

	if _, err := m.SubmitOperation(OperationRequest{Op: "pdf-nope", Files: []string{source}}); err == nil {
		t.Fatal("an unknown operation should be rejected")
	}
	if _, err := m.SubmitOperation(OperationRequest{Op: "pdf-merge", Files: []string{source}}); err == nil {
		t.Fatal("merging one file should be rejected")
	}
	if _, err := m.SubmitOperation(OperationRequest{Op: "pdf-extract", Files: []string{source}}); err == nil {
		t.Fatal("extract without pages should be rejected")
	}
	if _, err := m.SubmitOperation(OperationRequest{Op: "pdf-extract", Files: []string{source}, Params: pdfops.Params{Pages: "1-3"}}); err != nil {
		t.Fatalf("a valid extract should be accepted: %v", err)
	}

	notPDF := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notPDF, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := m.SubmitOperation(OperationRequest{Op: "pdf-reverse", Files: []string{notPDF}})
	if err == nil {
		t.Fatal("a non-PDF input should be rejected")
	}
	if !errors.Is(err, formats.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestOperationExtractPerInputFile(t *testing.T) {
	dir := t.TempDir()
	first := writePDFSource(t, dir, "one.pdf")
	second := writePDFSource(t, dir, "two.pdf")
	m := newOperationManager(t, newFakePDFBackend(4))

	job := submitOp(t, m, OperationRequest{Op: "pdf-extract", Files: []string{first, second}, Params: pdfops.Params{Pages: "1-2"}, Conflict: protocol.ConflictRename})
	if job.State != "completed" {
		t.Fatalf("state = %q, want completed", job.State)
	}
	if len(job.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(job.Files))
	}
	want := []string{filepath.Join(dir, "one-extracted.pdf"), filepath.Join(dir, "two-extracted.pdf")}
	for i, file := range job.Files {
		if file.State != "done" {
			t.Fatalf("file %d state = %q, want done", i, file.State)
		}
		if len(file.Outputs) != 1 || file.Outputs[0] != want[i] {
			t.Fatalf("file %d outputs = %v, want %v", i, file.Outputs, want[i])
		}
	}
	for _, output := range want {
		if _, err := os.Stat(output); err != nil {
			t.Fatalf("output %s missing: %v", output, err)
		}
	}
}

func TestOperationMergeProducesOneOutput(t *testing.T) {
	dir := t.TempDir()
	first := writePDFSource(t, dir, "one.pdf")
	second := writePDFSource(t, dir, "two.pdf")
	third := writePDFSource(t, dir, "three.pdf")
	backend := newFakePDFBackend(2)
	m := newOperationManager(t, backend)

	job := submitOp(t, m, OperationRequest{Op: "pdf-merge", Files: []string{first, second, third}, Conflict: protocol.ConflictRename})
	if job.State != "completed" {
		t.Fatalf("state = %q, want completed", job.State)
	}
	if len(job.Files) != 3 {
		t.Fatalf("files = %d, want one entry per input", len(job.Files))
	}
	if len(backend.merged) != 1 {
		t.Fatalf("merge ran %d times, want 1", len(backend.merged))
	}
	if got := backend.merged[0]; len(got) != 3 || got[0] != first || got[1] != second || got[2] != third {
		t.Fatalf("merged inputs = %v, want input order", got)
	}
	merged := filepath.Join(dir, "merged.pdf")
	if !fileExists(merged) {
		t.Fatalf("merged output %s missing", merged)
	}
	if len(job.Files[0].Outputs) != 1 || job.Files[0].Outputs[0] != merged {
		t.Fatalf("first file outputs = %v, want %v", job.Files[0].Outputs, []string{merged})
	}
	for _, file := range job.Files[1:] {
		if file.State != "done" {
			t.Fatalf("state = %q, want done", file.State)
		}
		if len(file.Outputs) != 0 {
			t.Fatalf("only the first entry should carry the output, got %v", file.Outputs)
		}
	}
}

func TestOperationConflictPolicies(t *testing.T) {
	dir := t.TempDir()
	source := writePDFSource(t, dir, "doc.pdf")

	t.Run("rename", func(t *testing.T) {
		m := newOperationManager(t, newFakePDFBackend(3))
		first := submitOp(t, m, OperationRequest{Op: "pdf-reverse", Files: []string{source}, Conflict: protocol.ConflictRename})
		if got := first.Files[0].Outputs[0]; filepath.Base(got) != "doc-reversed.pdf" {
			t.Fatalf("first output = %q", got)
		}
		second := submitOp(t, m, OperationRequest{Op: "pdf-reverse", Files: []string{source}, Conflict: protocol.ConflictRename})
		if got := second.Files[0].Outputs[0]; filepath.Base(got) != "doc-reversed (1).pdf" {
			t.Fatalf("second output = %q, want a renamed file", got)
		}
	})

	t.Run("skip", func(t *testing.T) {
		m := newOperationManager(t, newFakePDFBackend(3))
		submitOp(t, m, OperationRequest{Op: "pdf-reverse", Files: []string{source}, Conflict: protocol.ConflictSkip})
		existing := filepath.Join(dir, "doc-reversed.pdf")
		before, err := os.Stat(existing)
		if err != nil {
			t.Fatal(err)
		}
		job := submitOp(t, m, OperationRequest{Op: "pdf-reverse", Files: []string{source}, Conflict: protocol.ConflictSkip})
		if job.Files[0].State != "skipped" {
			t.Fatalf("state = %q, want skipped", job.Files[0].State)
		}
		after, err := os.Stat(existing)
		if err != nil {
			t.Fatal(err)
		}
		if !before.ModTime().Equal(after.ModTime()) {
			t.Fatal("skip must not rewrite the existing output")
		}
	})

	t.Run("overwrite", func(t *testing.T) {
		m := newOperationManager(t, newFakePDFBackend(3))
		target := filepath.Join(dir, "doc-reversed.pdf")
		if err := os.WriteFile(target, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
		job := submitOp(t, m, OperationRequest{Op: "pdf-reverse", Files: []string{source}, Conflict: protocol.ConflictOverwrite})
		if job.Files[0].Outputs[0] != target {
			t.Fatalf("outputs = %v, want %v", job.Files[0].Outputs, target)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) == "stale" {
			t.Fatal("overwrite must replace the old contents")
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), "backup") {
				t.Fatalf("a backup file was left behind: %s", entry.Name())
			}
		}
	})
}

func TestOperationNeverOverwritesItsSource(t *testing.T) {
	dir := t.TempDir()
	// A source whose own name collides with the operation's output name.
	source := writePDFSource(t, dir, "doc-reversed.pdf")
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	m := newOperationManager(t, newFakePDFBackend(3))
	job := submitOp(t, m, OperationRequest{Op: "pdf-reverse", Files: []string{source}, Conflict: protocol.ConflictRename})
	if job.Files[0].Outputs[0] == source {
		t.Fatalf("output overwrote the source: %v", job.Files[0].Outputs)
	}
	after, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the source file changed")
	}
}

func TestOperationReportsPartialFailure(t *testing.T) {
	dir := t.TempDir()
	good := writePDFSource(t, dir, "good.pdf")
	bad := writePDFSource(t, dir, "bad.pdf")
	backend := newFakePDFBackend(3)
	backend.fail[bad] = errors.New("synthetic failure")
	m := newOperationManager(t, backend)

	job := submitOp(t, m, OperationRequest{Op: "pdf-extract", Files: []string{good, bad}, Params: pdfops.Params{Pages: "1"}, Conflict: protocol.ConflictRename})
	if job.State != "completed_with_errors" {
		t.Fatalf("state = %q, want completed_with_errors", job.State)
	}
	if job.Files[0].State != "done" {
		t.Fatalf("good file state = %q, want done", job.Files[0].State)
	}
	if job.Files[1].State != "failed" || job.Files[1].Error == nil {
		t.Fatalf("bad file = %+v, want a failure", job.Files[1])
	}
}

func TestOperationReportsPasswordRequired(t *testing.T) {
	dir := t.TempDir()
	source := writePDFSource(t, dir, "locked.pdf")
	backend := newFakePDFBackend(3)
	backend.fail[source] = pdfops.ErrPasswordRequired
	m := newOperationManager(t, backend)

	job := submitOp(t, m, OperationRequest{Op: "pdf-reverse", Files: []string{source}, Conflict: protocol.ConflictRename})
	if job.Files[0].Error == nil {
		t.Fatalf("file = %+v, want an error", job.Files[0])
	}
	if job.Files[0].Error.Code != "password_required" {
		t.Fatalf("code = %q, want password_required", job.Files[0].Error.Code)
	}
}

func TestOperationHonoursOutputDir(t *testing.T) {
	dir := t.TempDir()
	source := writePDFSource(t, dir, "doc.pdf")
	out := t.TempDir()
	m := newOperationManager(t, newFakePDFBackend(3))
	job := submitOp(t, m, OperationRequest{Op: "pdf-extract", Files: []string{source}, Params: pdfops.Params{Pages: "1"}, OutputDir: out, Conflict: protocol.ConflictRename})
	want := filepath.Join(out, "doc-extracted.pdf")
	if len(job.Files[0].Outputs) != 1 || job.Files[0].Outputs[0] != want {
		t.Fatalf("outputs = %v, want %v", job.Files[0].Outputs, want)
	}
}

// Two operation jobs racing for the same destination must not both win it.
func TestOperationJobsDoNotShareADestination(t *testing.T) {
	dir := t.TempDir()
	source := writePDFSource(t, dir, "doc.pdf")
	m := newOperationManager(t, newFakePDFBackend(3))

	var jobs [2]protocol.Job
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			channel := make(chan protocol.Event, 64)
			if _, err := m.SubmitOperation(OperationRequest{Op: "pdf-reverse", Files: []string{source}, Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { channel <- e }}); err != nil {
				t.Errorf("SubmitOperation: %v", err)
				return
			}
			jobs[index] = waitJob(t, channel)
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, job := range jobs {
		for _, file := range job.Files {
			for _, output := range file.Outputs {
				if seen[output] {
					t.Fatalf("two jobs wrote the same destination: %s", output)
				}
				seen[output] = true
			}
		}
	}
	if len(seen) != 2 {
		t.Fatalf("got %d distinct outputs, want 2", len(seen))
	}
}

func TestOperationCancelLeavesNoFiles(t *testing.T) {
	dir := t.TempDir()
	source := writePDFSource(t, dir, "doc.pdf")
	backend := newFakePDFBackend(3)
	blocking := &blockingPDFBackend{
		fakePDFBackend: backend,
		started:        make(chan struct{}),
		release:        make(chan struct{}),
	}
	m := newOperationManager(t, blocking)

	events := make(chan protocol.Event, 128)
	id, err := m.SubmitOperation(OperationRequest{Op: "pdf-reverse", Files: []string{source}, Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }})
	if err != nil {
		t.Fatal(err)
	}
	<-blocking.started
	if err := m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	close(blocking.release)
	job := waitJob(t, events)
	if job.State != "cancelled" {
		t.Fatalf("state = %q, want cancelled", job.State)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(source) {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("cancel left files behind: %v", names)
	}
}

// blockingPDFBackend signals when it starts and waits to be released, so a test
// can cancel a job while the operation is still in flight.
type blockingPDFBackend struct {
	*fakePDFBackend
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingPDFBackend) Collect(ctx context.Context, input, output string, pages []int) error {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return b.fakePDFBackend.Collect(ctx, input, output, pages)
}

// Merge blocks the same way Collect does, so cancellation can be tested on the
// merge path.
func (b *blockingPDFBackend) Merge(ctx context.Context, inputs []string, output string) error {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return b.fakePDFBackend.Merge(ctx, inputs, output)
}

// PageCount blocks once, so a test can cancel a job while it is being probed.
func (b *blockingPDFBackend) PageCount(ctx context.Context, path string) (int, error) {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return b.fakePDFBackend.PageCount(ctx, path)
}

func TestOperationWithoutBackendIsRejected(t *testing.T) {
	dir := t.TempDir()
	source := writePDFSource(t, dir, "doc.pdf")
	m := NewManagerWithRegistry(registry.New())
	defer m.Close()
	if _, err := m.SubmitOperation(OperationRequest{Op: "pdf-reverse", Files: []string{source}}); err == nil {
		t.Fatal("running an operation without a backend should be rejected")
	}
}

func TestOperationJobPresetCarriesTheOperationID(t *testing.T) {
	dir := t.TempDir()
	source := writePDFSource(t, dir, "doc.pdf")
	m := newOperationManager(t, newFakePDFBackend(3))
	job := submitOp(t, m, OperationRequest{Op: "pdf-rotate", Files: []string{source}, Params: pdfops.Params{Angle: 90}, Conflict: protocol.ConflictRename})
	if job.Preset != "pdf-rotate" || job.Label != "pdf-rotate" {
		t.Fatalf("preset = %q, label = %q, want pdf-rotate", job.Preset, job.Label)
	}
	if job.Files[0].Outputs[0] != filepath.Join(dir, "doc-rotated.pdf") {
		t.Fatalf("outputs = %v", job.Files[0].Outputs)
	}
}

func TestOperationRunsEveryInputThroughTheWorkerPool(t *testing.T) {
	dir := t.TempDir()
	names := []string{"a.pdf", "b.pdf", "c.pdf", "d.pdf"}
	var files []string
	for _, name := range names {
		files = append(files, writePDFSource(t, dir, name))
	}
	m := newOperationManager(t, newFakePDFBackend(2))
	m.SetMaxWorkers(1)
	job := submitOp(t, m, OperationRequest{Op: "pdf-reverse", Files: files, Conflict: protocol.ConflictRename})
	if job.State != "completed" {
		t.Fatalf("state = %q, want completed", job.State)
	}
	for i, file := range job.Files {
		if file.State != "done" {
			t.Fatalf("file %d = %+v, want done", i, file)
		}
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// A finished operation job must not linger in the manager.
func TestOperationsManagerReleasesFinishedJobs(t *testing.T) {
	dir := t.TempDir()
	source := writePDFSource(t, dir, "doc.pdf")
	m := newOperationManager(t, newFakePDFBackend(2))
	submitOp(t, m, OperationRequest{Op: "pdf-reverse", Files: []string{source}, Conflict: protocol.ConflictRename})
	deadline := time.Now().Add(2 * time.Second)
	for m.Active() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m.Active() != 0 {
		t.Fatalf("Active = %d after the job finished", m.Active())
	}
}

func TestOperationReportsInvalidPDF(t *testing.T) {
	dir := t.TempDir()
	source := writePDFSource(t, dir, "damaged.pdf")
	backend := newFakePDFBackend(3)
	// Both the probe and the operation fail: the file is unreadable, so this
	// is a damaged document rather than a failed operation.
	backend.fail[source] = errors.New("cannot read cross-reference table")
	m := newOperationManager(t, backend)

	job := submitOp(t, m, OperationRequest{Op: "pdf-reverse", Files: []string{source}, Conflict: protocol.ConflictRename})
	if job.Files[0].Error == nil {
		t.Fatalf("file = %+v, want an error", job.Files[0])
	}
	if job.Files[0].Error.Code != "invalid_pdf" {
		t.Fatalf("code = %q, want invalid_pdf", job.Files[0].Error.Code)
	}
}

func TestOperationFailureOnReadableFileStaysInternal(t *testing.T) {
	dir := t.TempDir()
	source := writePDFSource(t, dir, "readable.pdf")
	backend := newFakePDFBackend(3)
	// The probe succeeds, so the failure belongs to the operation.
	backend.pageCountErr = errors.New("cannot read cross-reference table")
	m := newOperationManager(t, backend)

	job := submitOp(t, m, OperationRequest{Op: "pdf-reverse", Files: []string{source}, Conflict: protocol.ConflictRename})
	if job.Files[0].Error == nil {
		t.Fatalf("file = %+v, want an error", job.Files[0])
	}
	if job.Files[0].Error.Code != "internal" {
		t.Fatalf("code = %q, want internal", job.Files[0].Error.Code)
	}
}

func TestMergeAttributesFailureToTheUnreadableFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"locked", pdfops.ErrPasswordRequired, "password_required"},
		{"damaged", errors.New("cannot read cross-reference table"), "invalid_pdf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			good := writePDFSource(t, dir, "good.pdf")
			bad := writePDFSource(t, dir, "bad.pdf")
			backend := newFakePDFBackend(3)
			backend.fail[bad] = tc.err
			m := newOperationManager(t, backend)

			job := submitOp(t, m, OperationRequest{Op: "pdf-merge", Files: []string{good, bad}, Conflict: protocol.ConflictRename})
			if job.State != "failed" {
				t.Fatalf("state = %q, want failed", job.State)
			}
			badIndex := indexOfSource(job.Files, bad)
			if job.Files[badIndex].State != "failed" || job.Files[badIndex].Error == nil {
				t.Fatalf("bad file = %+v, want a failure", job.Files[badIndex])
			}
			if job.Files[badIndex].Error.Code != tc.code {
				t.Fatalf("code = %q, want %s", job.Files[badIndex].Error.Code, tc.code)
			}
			goodIndex := indexOfSource(job.Files, good)
			if job.Files[goodIndex].State != "skipped" {
				t.Fatalf("good file state = %q, want skipped", job.Files[goodIndex].State)
			}
			want := "not merged: " + filepath.Base(bad) + " could not be read"
			if job.Files[goodIndex].Notice != want {
				t.Fatalf("notice = %q, want %q", job.Files[goodIndex].Notice, want)
			}
			if len(backend.merged) != 0 {
				t.Fatalf("the merge should not have run: %v", backend.merged)
			}
		})
	}
}

func TestMergeCancellationIsNotClassifiedAsInvalid(t *testing.T) {
	dir := t.TempDir()
	good := writePDFSource(t, dir, "good.pdf")
	bad := writePDFSource(t, dir, "bad.pdf")
	backend := newFakePDFBackend(3)
	blocking := &blockingPDFBackend{fakePDFBackend: backend, started: make(chan struct{}), release: make(chan struct{})}
	m := newOperationManager(t, blocking)

	events := make(chan protocol.Event, 128)
	id, err := m.SubmitOperation(OperationRequest{Op: "pdf-merge", Files: []string{good, bad}, Conflict: protocol.ConflictRename, Events: func(e protocol.Event) { events <- e }})
	if err != nil {
		t.Fatal(err)
	}
	// The pre-merge probe blocks, which is where classification would decide the
	// file is damaged. Cancelling first must win over invalid_pdf.
	<-blocking.started
	if err := m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	close(blocking.release)
	job := waitJob(t, events)
	if job.State != "cancelled" {
		t.Fatalf("state = %q, want cancelled", job.State)
	}
	for _, file := range job.Files {
		if file.Error != nil && file.Error.Code == "invalid_pdf" {
			t.Fatalf("a cancelled job reported %q", file.Error.Code)
		}
	}
}

func indexOfSource(files []protocol.FileJob, source string) int {
	for i, file := range files {
		if file.Source == source {
			return i
		}
	}
	return -1
}
