package jobs

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"file-converter/backend/internal/batch"
	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

// BackgroundRemover removes the background from one image file, writing a
// transparent PNG to dst. RemoveFile is called concurrently by the job
// manager's worker pool, so an implementation that cannot run concurrently
// (a single ONNX session, say) must serialize calls internally.
type BackgroundRemover interface {
	RemoveFile(ctx context.Context, src, dst string) error
}

// BGRemoveStatus is an optional interface a BackgroundRemover can implement
// to report whether it can actually run right now (its shared library or
// model is missing, say), rather than only failing the first real request.
type BGRemoveStatus interface {
	Available() (string, error)
}

// BGRemoveRequest asks for the background to be removed from a set of images.
type BGRemoveRequest struct {
	Files     []string
	OutputDir string
	Conflict  protocol.ConflictPolicy
	Events    func(protocol.Event)
}

// SetBackgroundRemover installs the engine that performs background removal.
// Without one, SubmitBackgroundRemoval reports it as unavailable.
func (m *Manager) SetBackgroundRemover(r BackgroundRemover) {
	m.bgMu.Lock()
	m.bgRemover = r
	m.bgMu.Unlock()
}

func (m *Manager) backgroundRemover() BackgroundRemover {
	m.bgMu.Lock()
	defer m.bgMu.Unlock()
	return m.bgRemover
}

// BackgroundRemovalStatus reports whether background removal is ready to run,
// for the hello/engines surfaces.
func (m *Manager) BackgroundRemovalStatus() (bool, string) {
	r := m.backgroundRemover()
	if r == nil {
		return false, "background removal is not available"
	}
	if status, ok := r.(BGRemoveStatus); ok {
		if _, err := status.Available(); err != nil {
			return false, err.Error()
		}
	}
	return true, ""
}

// SubmitBackgroundRemoval validates a background-removal request and queues
// it. Only image inputs are accepted; the output is always a PNG, since the
// result needs an alpha channel to mark the removed background transparent.
func (m *Manager) SubmitBackgroundRemoval(req BGRemoveRequest) (string, error) {
	if len(req.Files) == 0 {
		return "", fmt.Errorf("%w: no files", formats.ErrInvalidInput)
	}
	if m.backgroundRemover() == nil {
		return "", fmt.Errorf("background removal is not available")
	}
	for _, file := range req.Files {
		if err := formats.ValidateInput(file); err != nil {
			return "", err
		}
		format, err := formats.Detect(file)
		if err != nil || !formats.IsImageFormat(format) {
			return "", fmt.Errorf("%w: %s is not an image", formats.ErrInvalidInput, filepath.Base(file))
		}
	}
	if req.Conflict == "" {
		req.Conflict = protocol.ConflictRename
	}
	if req.OutputDir != "" {
		if err := formats.ValidatePath(req.OutputDir); err != nil {
			return "", fmt.Errorf("%w: %v", formats.ErrInvalidInput, err)
		}
	}
	files := append([]string(nil), req.Files...)
	j := &job{req: SubmitRequest{Preset: "remove-background", OutputDir: req.OutputDir, Conflict: req.Conflict, Events: req.Events}}
	for _, file := range files {
		j.req.Files = append(j.req.Files, file)
		j.items = append(j.items, batch.Item{Path: file, Explicit: true})
	}
	j.bg = &BGRemoveRequest{Files: files, OutputDir: req.OutputDir, Conflict: req.Conflict, Events: req.Events}
	j.id = fmt.Sprintf("job-%d", m.next.Add(1))
	ctx, cancel := context.WithCancel(context.Background())
	j.ctx, j.cancel = ctx, cancel
	m.mu.Lock()
	m.jobs[j.id] = j
	m.mu.Unlock()
	m.queue <- j
	return j.id, nil
}

// bgOutputName builds the output file's base name: the source stem with
// "-no-bg" appended, so a PNG input (the common case) never collides with its
// own name the way a bare extension swap would.
func bgOutputName(source string) string {
	return strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)) + "-no-bg"
}

// runBackgroundRemoval runs one unit of work per input file, mirroring
// runOperation's per-file fan-out across the worker pool.
func (m *Manager) runBackgroundRemoval(j *job) []protocol.FileJob {
	files := make([]protocol.FileJob, len(j.bg.Files))
	for i, source := range j.bg.Files {
		files[i] = protocol.FileJob{Source: source, State: "pending", Outputs: []string{}}
	}
	results := make(chan fileResult, len(files))
	var wg sync.WaitGroup
	limit := int(m.workers.Load())
	indices := make(chan int)
	for worker := 0; worker < limit; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range indices {
				results <- m.runOneBackgroundRemoval(j, index, j.bg.Files[index])
			}
		}()
	}
	go func() {
		defer close(indices)
		for i := range files {
			select {
			case indices <- i:
			case <-j.ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	for result := range results {
		files[result.index] = result.file
	}
	return files
}

func (m *Manager) runOneBackgroundRemoval(j *job, index int, source string) fileResult {
	file := protocol.FileJob{Source: source, State: "running", Outputs: []string{}}
	if j.ctx.Err() != nil {
		file.State = "cancelled"
		return fileResult{index: index, file: file}
	}
	outputDir := j.bg.OutputDir
	if outputDir == "" {
		outputDir = filepath.Dir(source)
	}
	plan, release, err := m.reserveOutput(func(taken func(string) bool) (engine.OutputPlan, error) {
		return engine.PlanNamedOutputExcluding(outputDir, source, bgOutputName(source), "png", j.bg.Conflict, taken)
	})
	defer release()
	if err != nil {
		file.State = "failed"
		file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
		return fileResult{index: index, file: file}
	}
	if plan.Skipped() {
		file.State = "skipped"
		return fileResult{index: index, file: file}
	}
	if err := m.backgroundRemover().RemoveFile(j.ctx, source, plan.Temp); err != nil {
		plan.Rollback()
		if j.ctx.Err() != nil {
			file.State = "cancelled"
			return fileResult{index: index, file: file}
		}
		file.State = "failed"
		file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
		return fileResult{index: index, file: file}
	}
	output, file := m.publishPlan(j, plan, file)
	if file.State != "done" {
		return fileResult{index: index, file: file}
	}
	file.Progress, file.Outputs = 1, []string{output}
	return fileResult{index: index, file: file}
}
