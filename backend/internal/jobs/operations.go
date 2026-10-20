package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"file-converter/backend/internal/batch"
	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/pdfops"
	"file-converter/backend/internal/protocol"
)

// OperationRequest asks for one PDF operation over a selection of files. It
// mirrors SubmitRequest but names a catalogue operation instead of a preset.
type OperationRequest struct {
	Op        string
	Files     []string
	Params    pdfops.Params
	OutputDir string
	Conflict  protocol.ConflictPolicy
	Events    func(protocol.Event)
}

// SetPDFToolbox installs the engines that perform PDF operations. Without a
// PDF engine every operation request is rejected.
func (m *Manager) SetPDFToolbox(tb pdfops.Toolbox) {
	m.pdfMu.Lock()
	m.pdf = tb
	m.pdfMu.Unlock()
}

func (m *Manager) pdfToolbox() pdfops.Toolbox {
	m.pdfMu.Lock()
	defer m.pdfMu.Unlock()
	return m.pdf
}

// pdfBackend returns the structural engine, which is the one that can read a
// PDF and is therefore the one used to probe inputs.
func (m *Manager) pdfBackend() pdfops.Backend {
	return m.pdfToolbox().PDFCPU
}

// pdfValidateInput accepts only absolute regular files carrying a %PDF- header.
// Directories are not expanded: a PDF operation names its inputs exactly.
func pdfValidateInput(path string) error {
	if err := formats.ValidateInput(path); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: %v", formats.ErrInvalidInput, err)
	}
	defer file.Close()
	header := make([]byte, 5)
	if _, err := file.Read(header); err != nil {
		return fmt.Errorf("%w: cannot read PDF header", formats.ErrInvalidInput)
	}
	if string(header) != "%PDF-" {
		return fmt.Errorf("%w: missing PDF header", formats.ErrInvalidInput)
	}
	return nil
}

// SubmitOperation validates a PDF operation request synchronously and queues
// it. Validation failures come back as bad_request-class errors so the
// protocol can report them before a job exists.
func (m *Manager) SubmitOperation(req OperationRequest) (string, error) {
	if len(req.Files) == 0 {
		return "", fmt.Errorf("%w: no files", formats.ErrInvalidInput)
	}
	if m.pdfToolbox().PDFCPU == nil {
		return "", fmt.Errorf("%w: no PDF backend is available", formats.ErrInvalidInput)
	}
	op, ok := pdfops.Lookup(req.Op)
	if !ok {
		return "", fmt.Errorf("%w: unknown PDF operation: %s", formats.ErrInvalidInput, req.Op)
	}
	if err := op.Validate(req.Files, req.Params); err != nil {
		return "", fmt.Errorf("%w: %v", formats.ErrInvalidInput, err)
	}
	for _, file := range req.Files {
		if op.InputKind() == pdfops.InputImage {
			// Image inputs are checked by content, the way the conversion path does.
			if err := formats.ValidateInput(file); err != nil {
				return "", err
			}
			if format, err := formats.Detect(file); err != nil || !formats.IsImageFormat(format) {
				return "", fmt.Errorf("%w: %s is not an image", formats.ErrInvalidInput, filepath.Base(file))
			}
			continue
		}
		if err := pdfValidateInput(file); err != nil {
			return "", err
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
	// The operation ID rides on Preset so existing clients and event consumers
	// keep working without a new field.
	j := &job{req: SubmitRequest{Preset: op.ID, OutputDir: req.OutputDir, Conflict: req.Conflict, Events: req.Events}}
	for _, file := range files {
		j.req.Files = append(j.req.Files, file)
		j.items = append(j.items, batch.Item{Path: file, Explicit: true})
	}
	j.op = &OperationRequest{Op: op.ID, Files: files, Params: req.Params, OutputDir: req.OutputDir, Conflict: req.Conflict, Events: req.Events}
	j.id = fmt.Sprintf("job-%d", m.next.Add(1))
	ctx, cancel := context.WithCancel(context.Background())
	j.ctx, j.cancel = ctx, cancel
	m.mu.Lock()
	m.jobs[j.id] = j
	m.mu.Unlock()
	m.queue <- j
	return j.id, nil
}

// operationOutputName builds the output file name for one operation. Merge
// writes one merged.pdf; the rest append their suffix to the source stem.
func operationOutputName(op pdfops.Operation, source string) string {
	if op.Merges {
		if op.InputKind() == pdfops.InputImage {
			return "images"
		}
		return "merged"
	}
	return strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)) + op.Suffix
}

// numberedName is the base name for the Nth output of a numbered operation:
// <stem>-image-1, <stem>-image-2, and so on, with the extension added by the
// planner.
func numberedName(op pdfops.Operation, source string, n int) string {
	return strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)) + op.Suffix + "-" + strconv.Itoa(n)
}

// runOperation performs one unit of work: a merge is a single unit covering
// every input, everything else is one unit per input file.
func (m *Manager) runOperation(j *job, op pdfops.Operation) (files []protocol.FileJob) {
	if op.Merges {
		return m.mergeOperation(j, op)
	}
	files = make([]protocol.FileJob, len(j.op.Files))
	for i, source := range j.op.Files {
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
				results <- m.runOneOperation(j, op, index, j.op.Files[index])
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

// mergeOperation treats every input as one unit of work producing one output.
// The output path is reported on the first entry; the rest are marked done with
// no outputs of their own, so the job reads as one result for N inputs.
func (m *Manager) mergeOperation(j *job, op pdfops.Operation) []protocol.FileJob {
	files := make([]protocol.FileJob, len(j.op.Files))
	for i, source := range j.op.Files {
		files[i] = protocol.FileJob{Source: source, State: "pending", Outputs: []string{}}
	}
	failed := func(err error) []protocol.FileJob {
		for i := range files {
			files[i].State = "failed"
			files[i].Error = &protocol.FileError{Code: operationErrorCode(err), Message: err.Error()}
		}
		return files
	}
	if j.ctx.Err() != nil {
		for i := range files {
			files[i].State = "cancelled"
		}
		return files
	}
	// Attribute an unreadable input to that file alone, so a single damaged or
	// locked file does not fail the whole selection with no explanation.
	// Image inputs are not PDFs, so a PDF page-count probe says nothing about
	// them; they were validated by content at submit time.
	if op.InputKind() == pdfops.InputPDF {
		if err := m.firstUnreadableInput(j); err != nil {
			var input *pdfops.InputError
			if errors.As(err, &input) {
				return mergeInputFailure(files, input, operationErrorCode(input.Err))
			}
			return failed(err)
		}
	}
	outputDir := j.op.OutputDir
	if outputDir == "" {
		outputDir = filepath.Dir(j.op.Files[0])
	}
	// The merge output must not collide with any of its own inputs.
	source := j.op.Files[0]
	plan, release, err := m.reserveOutput(func(taken func(string) bool) (engine.OutputPlan, error) {
		return engine.PlanNamedOutputExcluding(outputDir, source, operationOutputName(op, source), op.OutputExtension(), j.op.Conflict, taken)
	})
	defer release()
	if err != nil {
		return failed(err)
	}
	if plan.Skipped() {
		for i := range files {
			files[i].State = "skipped"
		}
		return files
	}
	if err := op.Run(j.ctx, m.pdfToolbox(), j.op.Files, plan.Temp, j.op.Params); err != nil {
		plan.Rollback()
		return failed(err)
	}
	output, file := m.publishPlan(j, plan, protocol.FileJob{Source: source, State: "running"})
	if file.State != "done" {
		for i := range files {
			files[i].State = file.State
		}
		return files
	}
	file.Progress, file.Outputs = 1, []string{output}
	files[0] = file
	for i := 1; i < len(files); i++ {
		files[i].State, files[i].Progress = "done", 1
	}
	return files
}

// firstUnreadableInput probes every merge input in order and returns an
// InputError for the first one that cannot be read. The probe error is mapped
// onto the sentinel codes so callers classify it without inspecting pdfcpu's
// wording; anything else is reported as a damaged PDF, which is what a probe
// failure on an otherwise well-formed request means.
func (m *Manager) firstUnreadableInput(j *job) error {
	backend := m.pdfBackend()
	if backend == nil {
		return nil
	}
	for _, source := range j.op.Files {
		if j.ctx.Err() != nil {
			return nil
		}
		if _, err := backend.PageCount(j.ctx, source); err != nil {
			if errors.Is(err, pdfops.ErrPasswordRequired) {
				return &pdfops.InputError{Path: source, Err: pdfops.ErrPasswordRequired}
			}
			return &pdfops.InputError{Path: source, Err: pdfops.ErrInvalidPDF}
		}
	}
	return nil
}

// mergeInputFailure marks the unreadable file failed with its own code and
// every other input skipped, explaining which file stopped the merge.
func mergeInputFailure(files []protocol.FileJob, input *pdfops.InputError, code string) []protocol.FileJob {
	notice := "not merged: " + filepath.Base(input.Path) + " could not be read"
	for i := range files {
		if files[i].Source == input.Path {
			files[i].State = "failed"
			files[i].Error = &protocol.FileError{Code: code, Message: input.Error()}
			continue
		}
		files[i].State = "skipped"
		files[i].Notice = notice
	}
	return files
}

// runOneOperation performs one operation on one input file.
func (m *Manager) runOneOperation(j *job, op pdfops.Operation, index int, source string) fileResult {
	file := protocol.FileJob{Source: source, State: "running", Outputs: []string{}}
	if j.ctx.Err() != nil {
		file.State = "cancelled"
		return fileResult{index: index, file: file}
	}
	outputDir := j.op.OutputDir
	if outputDir == "" {
		outputDir = filepath.Dir(source)
	}
	if op.Numbered {
		return fileResult{index: index, file: m.runNumberedOperation(j, op, source, file)}
	}
	plan, release, err := m.reserveOutput(func(taken func(string) bool) (engine.OutputPlan, error) {
		return engine.PlanNamedOutputExcluding(outputDir, source, operationOutputName(op, source), op.OutputExtension(), j.op.Conflict, taken)
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
	if err := op.Run(j.ctx, m.pdfToolbox(), []string{source}, plan.Temp, j.op.Params); err != nil {
		plan.Rollback()
		// Ghostscript compress reports ErrNoGain when the output is no smaller
		// than the input: treat it as a skip-with-notice rather than a failure.
		if errors.Is(err, pdfops.ErrNoGain) {
			file.State = "skipped"
			file.Notice = "already as small as it can get"
			return fileResult{index: index, file: file}
		}
		return fileResult{index: index, file: m.failOperationFile(j, file, err)}
	}
	output, file := m.publishPlan(j, plan, file)
	if file.State != "done" {
		return fileResult{index: index, file: file}
	}
	file.Progress, file.Outputs = 1, []string{output}
	return fileResult{index: index, file: file}
}

// operationErrorCode maps an engine error onto the protocol code. The mapping
// lives in pdfops so every surface classifies failures identically.
func operationErrorCode(err error) string { return pdfops.ErrorCode(err) }
