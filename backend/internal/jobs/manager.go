package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"file-converter/backend/internal/batch"
	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/pdfops"
	"file-converter/backend/internal/protocol"
	"file-converter/backend/internal/registry"
)

type Converter interface {
	Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error
}

var planOutputPath = engine.PlanOutputPathExcluding
var commitOutput = func(ctx context.Context, plan engine.OutputPlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := plan.Commit()
	return err
}
var beforePublish = func() {}

type Router interface {
	Resolve(source, target formats.Format) (registry.Engine, error)
}

type MediaSourceRouter interface {
	MediaSource(context.Context, string) (formats.Format, error)
}

type SubmitRequest struct {
	Files     []string
	Preset    string
	OutputDir string
	Conflict  protocol.ConflictPolicy
	Options   protocol.ImageOptions
	// Recursive makes folders in Files contribute every file below them
	// instead of only their top-level files.
	Recursive bool
	Events    func(protocol.Event)
}

// maxDefaultWorkers keeps the default pool from oversubscribing machines with
// many cores: ImageMagick and FFmpeg are themselves multithreaded.
const maxDefaultWorkers = 8

// DefaultWorkers is the number of conversions run at once unless overridden.
func DefaultWorkers() int {
	n := runtime.NumCPU()
	if n > maxDefaultWorkers {
		n = maxDefaultWorkers
	}
	if n < 1 {
		n = 1
	}
	return n
}

type Manager struct {
	router      Router
	pdf         pdfops.Toolbox // set by SetPDFToolbox; zero until then
	pdfMu       sync.Mutex
	mu          sync.Mutex
	outputMu    sync.Mutex
	reserved    map[string]struct{} // destinations planned but not yet written; guarded by outputMu
	videoMu     sync.Mutex          // at most one video conversion runs at a time
	workers     atomic.Int64
	subscribers map[uint64]func(protocol.Event)
	next        atomic.Uint64
	nextSub     atomic.Uint64
	jobs        map[string]*job
	queue       chan *job
	done        chan struct{}
	loopDone    chan struct{}
	closeOnce   sync.Once
}

type job struct {
	id     string
	req    SubmitRequest
	ctx    context.Context
	cancel context.CancelFunc
	items  []batch.Item
	// preSkip holds a reason for folder entries that can never be converted.
	preSkip map[int]string
	// op is set for PDF operation jobs; run dispatches to runOperation then.
	op *OperationRequest

	publicationMu sync.Mutex
	published     bool
	pending       []engine.OutputPlan
}

func NewManager(converter Converter) *Manager {
	r := registry.New()
	r.Register(converterEngine{converter: converter})
	return NewManagerWithRegistry(r)
}

func NewManagerWithRegistry(router Router) *Manager {
	m := &Manager{router: router, reserved: make(map[string]struct{}), jobs: make(map[string]*job), subscribers: make(map[uint64]func(protocol.Event)), queue: make(chan *job, 64), done: make(chan struct{}), loopDone: make(chan struct{})}
	m.workers.Store(int64(DefaultWorkers()))
	go m.loop()
	return m
}

// SetMaxWorkers bounds how many conversions run at once; values below one
// mean one. Jobs already running keep the bound they started with.
func (m *Manager) SetMaxWorkers(n int) {
	if n < 1 {
		n = 1
	}
	m.workers.Store(int64(n))
}

func (m *Manager) isReserved(path string) bool {
	_, ok := m.reserved[path]
	return ok
}

type converterEngine struct{ converter Converter }

func (e converterEngine) ID() string                                   { return "converter" }
func (e converterEngine) Available() (string, error)                   { return "converter", nil }
func (e converterEngine) Supports(formats.Format, formats.Format) bool { return true }
func (e converterEngine) Convert(ctx context.Context, source string, sourceFormat formats.Format, destination string, target formats.Format, options protocol.ImageOptions) error {
	return e.converter.Convert(ctx, source, sourceFormat, destination, target, options)
}

func (m *Manager) Submit(req SubmitRequest) (string, error) {
	if len(req.Files) == 0 {
		return "", fmt.Errorf("no files")
	}
	for _, file := range req.Files {
		if info, err := os.Stat(file); err == nil && info.IsDir() && filepath.IsAbs(file) {
			continue
		}
		if err := formats.ValidateInput(file); err != nil {
			return "", err
		}
	}
	items, err := batch.ExpandInputs(req.Files, req.Recursive)
	if err != nil {
		return "", fmt.Errorf("%w: %v", formats.ErrInvalidInput, err)
	}
	if len(items) == 0 {
		return "", fmt.Errorf("%w: no files to convert", formats.ErrInvalidInput)
	}
	preSkip := map[int]string{}
	req.Files = make([]string, len(items))
	for i, item := range items {
		req.Files[i] = item.Path
		if !item.Explicit {
			if err := formats.ValidateInput(item.Path); err != nil {
				preSkip[i] = err.Error()
			}
		}
	}
	targetPreset := strings.ToLower(req.Preset)
	switch targetPreset {
	case "png":
		targetPreset = "image-to-png"
	case "jpg", "jpeg":
		targetPreset = "image-to-jpeg"
	case "webp":
		targetPreset = "image-to-webp"
	case "jxl":
		targetPreset = "image-to-jxl"
	case "avif", "bmp", "tiff", "ico", "heic":
		targetPreset = "image-to-" + targetPreset
	case "tif":
		targetPreset = "image-to-tiff"
	case "heif":
		targetPreset = "image-to-heic"
	case "mp4", "mov", "webm", "mkv", "avi", "gif":
		targetPreset = "video-to-" + targetPreset
	case "mp3", "wav", "flac", "aac", "m4a", "ogg", "opus", "aiff", "alac", "ac3", "amr", "wma":
		targetPreset = "audio-to-" + targetPreset
	}
	targetFormat, ok := formats.PresetFormat(targetPreset)
	if !ok {
		return "", fmt.Errorf("unsupported preset")
	}
	if formats.IsVideoFormat(targetFormat) {
		// Media probing is repeated by the worker, but reject audio-to-video
		// requests synchronously so the protocol returns a stable error.
		for i, file := range req.Files {
			if !items[i].Explicit {
				continue
			}
			sourceFormat, detectErr := formats.Detect(file)
			if detectErr != nil {
				if mediaRouter, ok := m.router.(MediaSourceRouter); ok {
					sourceFormat, detectErr = mediaRouter.MediaSource(context.Background(), file)
				}
			}
			if detectErr == nil && formats.IsAudioFormat(sourceFormat) {
				return "", fmt.Errorf("unsupported media conversion: audio to video")
			}
		}
	}
	req.Preset = targetPreset
	id := fmt.Sprintf("job-%d", m.next.Add(1))
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{id: id, req: req, ctx: ctx, cancel: cancel, items: items, preSkip: preSkip}
	m.mu.Lock()
	m.jobs[id] = j
	m.mu.Unlock()
	m.queue <- j
	return id, nil
}

func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("job not found")
	}
	j.publicationMu.Lock()
	if j.published {
		j.publicationMu.Unlock()
		return nil
	}
	j.cancel()
	j.publicationMu.Unlock()
	return nil
}

func (m *Manager) Close() {
	m.closeOnce.Do(func() { close(m.done) })
	<-m.loopDone
}

func (m *Manager) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.jobs)
}

func (m *Manager) Subscribe(events func(protocol.Event)) func() {
	id := m.nextSub.Add(1)
	m.mu.Lock()
	m.subscribers[id] = events
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.subscribers, id)
		m.mu.Unlock()
	}
}

func (m *Manager) broadcast(event protocol.Event) {
	m.mu.Lock()
	subscribers := make([]func(protocol.Event), 0, len(m.subscribers))
	for _, subscriber := range m.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	m.mu.Unlock()
	for _, subscriber := range subscribers {
		subscriber(event)
	}
}

func (m *Manager) loop() {
	defer close(m.loopDone)
	for {
		select {
		case j := <-m.queue:
			m.run(j)
		case <-m.done:
			for {
				select {
				case j := <-m.queue:
					m.run(j)
				default:
					return
				}
			}
		}
	}
}

type eventEmitter struct {
	manager   *Manager
	job       *job
	files     []protocol.FileJob
	last      time.Time
	state     string
	forceNext bool
}

func (e *eventEmitter) emit(name string, force bool) {
	now := time.Now()
	if name == "job.updated" && !force && !e.last.IsZero() && now.Sub(e.last) < 100*time.Millisecond {
		return
	}
	e.last = now
	files := append([]protocol.FileJob(nil), e.files...)
	done := 0
	for _, file := range files {
		if file.State == "done" || file.State == "skipped" || file.State == "failed" || file.State == "cancelled" {
			done++
		}
	}
	progress := 0.0
	if len(files) > 0 {
		progress = float64(done) / float64(len(files))
	}
	event := protocol.Event{Name: name, Data: protocol.Job{ID: e.job.id, Preset: e.job.req.Preset, Label: e.job.req.Preset, State: e.state, Progress: progress, Files: files}}
	if e.job.req.Events != nil {
		e.job.req.Events(event)
	}
	e.manager.broadcast(event)
}

type lane struct {
	order   []int
	workers int
}

// planLanes splits a job's files across the worker budget. Video work gets a
// lane of one worker, because FFmpeg already uses every core, and the other
// files share the remaining workers. Lane choice only affects scheduling:
// convertFile enforces the video limit itself, so a file whose kind is only
// known after probing is still serialized.
func (m *Manager) planLanes(j *job, target formats.Format) []lane {
	limit := int(m.workers.Load())
	var video, other []int
	for i, source := range j.req.Files {
		sourceFormat, _ := formats.FormatFromExtension(source)
		if formats.IsVideoFormat(sourceFormat) || formats.IsVideoFormat(target) {
			video = append(video, i)
		} else {
			other = append(other, i)
		}
	}
	all := make([]int, len(j.req.Files))
	for i := range all {
		all[i] = i
	}
	if limit < 2 || len(video) == 0 {
		return []lane{{order: all, workers: min(limit, len(all))}}
	}
	lanes := []lane{{order: video, workers: 1}}
	if len(other) > 0 {
		lanes = append(lanes, lane{order: other, workers: min(limit-1, len(other))})
	}
	return lanes
}

type fileResult struct {
	index int
	file  protocol.FileJob
}

func (m *Manager) run(j *job) {
	target, _ := formats.PresetFormat(j.req.Preset)
	files := make([]protocol.FileJob, len(j.req.Files))
	for i, source := range j.req.Files {
		files[i] = protocol.FileJob{Source: source, State: "pending", Outputs: []string{}}
	}
	emitter := &eventEmitter{manager: m, job: j, files: files, state: "queued"}
	emitter.emit("job.updated", true)
	if j.ctx.Err() == nil {
		emitter.state = "running"
		for i := range files {
			files[i].State = "running"
		}
		emitter.files = files
		emitter.emit("job.updated", true)
	}

	results := make(chan fileResult, len(files))
	if j.op != nil {
		// A PDF operation supplies its own workers but shares everything after
		// them: publication, rollback and the finished event.
		op, _ := pdfops.Lookup(j.op.Op)
		for i := range files {
			files[i].State = "running"
		}
		opFiles := m.runOperation(j, op)
		copy(files, opFiles)
		emitter.files = files
		if j.ctx.Err() == nil {
			emitter.emit("job.updated", true)
		}
		m.finishJob(j, emitter, files)
		return
	}
	var wg sync.WaitGroup
	for _, l := range m.planLanes(j, target) {
		indices := make(chan int)
		for worker := 0; worker < l.workers; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for index := range indices {
					results <- m.convertFile(j, index, j.req.Files[index], target)
				}
			}()
		}
		go func(order []int) {
			defer close(indices)
			for _, index := range order {
				select {
				case indices <- index:
				case <-j.ctx.Done():
					return
				}
			}
		}(l.order)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	for result := range results {
		files[result.index] = result.file
		emitter.files = files
		if j.ctx.Err() == nil {
			emitter.emit("job.updated", false)
		}
	}
	m.finishJob(j, emitter, files)
}

// finishJob commits (or rolls back) the job's pending plans, decides its final
// state from the per-file results, emits job.finished and removes it from the
// manager. Both the conversion path and the PDF operation path end here.
func (m *Manager) finishJob(j *job, emitter *eventEmitter, files []protocol.FileJob) {
	beforePublish()
	j.publicationMu.Lock()
	if j.ctx.Err() != nil {
		for i := len(j.pending) - 1; i >= 0; i-- {
			j.pending[i].Rollback()
		}
		j.pending = nil
	} else {
		j.published = true
		for _, plan := range j.pending {
			plan.Finalize()
		}
		j.pending = nil
	}
	j.publicationMu.Unlock()
	if j.ctx.Err() != nil {
		for i := range files {
			if files[i].State == "pending" || files[i].State == "running" || files[i].State == "done" || files[i].State == "skipped" {
				files[i].State = "cancelled"
				files[i].Progress = 0
				files[i].Outputs = nil
			}
		}
		emitter.files = files
	}

	state := "completed"
	hasDone, hasFailed := false, false
	for _, file := range files {
		switch file.State {
		case "done":
			hasDone = true
		case "failed":
			hasFailed = true
		}
	}
	if j.ctx.Err() != nil {
		state = "cancelled"
	} else if hasFailed && hasDone {
		state = "completed_with_errors"
	} else if hasFailed {
		state = "failed"
	}
	emitter.state = state
	emitter.emit("job.finished", true)
	j.cancel()
	m.mu.Lock()
	delete(m.jobs, j.id)
	m.mu.Unlock()
}

func (m *Manager) convertMany(j *job, index int, file protocol.FileJob, route registry.MultiOutputEngine, source string, sourceFormat, target formats.Format, ext, outputDir string) fileResult {
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		file.State = "failed"
		file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
		return fileResult{index: index, file: file}
	}
	renderDir, err := os.MkdirTemp(outputDir, ".file-converter-render-")
	if err != nil {
		file.State = "failed"
		file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
		return fileResult{index: index, file: file}
	}
	defer os.RemoveAll(renderDir)
	pages, err := route.ConvertMany(j.ctx, source, sourceFormat, renderDir, target, j.req.Options)
	if err != nil {
		if j.ctx.Err() != nil {
			file.State = "cancelled"
		} else {
			file.State = "failed"
			file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
		}
		return fileResult{index: index, file: file}
	}
	if len(pages) == 0 {
		file.State = "failed"
		file.Error = &protocol.FileError{Code: "internal", Message: "multi-page engine returned no outputs"}
		return fileResult{index: index, file: file}
	}
	if j.ctx.Err() != nil {
		file.State = "cancelled"
		return fileResult{index: index, file: file}
	}
	plans := make([]engine.OutputPlan, len(pages))
	removePlans := func() {
		for i := len(plans) - 1; i >= 0; i-- {
			plans[i].Rollback()
		}
	}
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	for i, page := range pages {
		clean := filepath.Clean(page)
		info, statErr := os.Lstat(clean)
		if statErr != nil || filepath.Dir(clean) != filepath.Clean(renderDir) || filepath.Ext(clean) != "."+ext || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			removePlans()
			file.State = "failed"
			file.Error = &protocol.FileError{Code: "internal", Message: "multi-page engine returned unsafe output"}
			return fileResult{index: index, file: file}
		}
		plans[i], err = planOutputPath(outputDir, source, ext, i+1, j.req.Conflict, m.isReserved)
		if err != nil {
			removePlans()
			file.State = "failed"
			file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
			return fileResult{index: index, file: file}
		}
	}
	if j.ctx.Err() != nil {
		removePlans()
		file.State = "cancelled"
		return fileResult{index: index, file: file}
	}
	for i, plan := range plans {
		if plan.Skipped() {
			continue
		}
		if err := os.Rename(pages[i], plan.Temp); err != nil {
			removePlans()
			file.State = "failed"
			file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
			return fileResult{index: index, file: file}
		}
	}
	if j.ctx.Err() != nil {
		removePlans()
		file.State = "cancelled"
		return fileResult{index: index, file: file}
	}
	for _, plan := range plans {
		if !plan.Skipped() {
			if err := commitOutput(j.ctx, plan); err != nil {
				removePlans()
				file.Outputs = nil
				if j.ctx.Err() != nil {
					file.State = "cancelled"
				} else {
					file.State = "failed"
					file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
				}
				return fileResult{index: index, file: file}
			}
			file.Outputs = append(file.Outputs, plan.Destination)
			if j.ctx.Err() != nil {
				removePlans()
				file.Outputs = nil
				file.State = "cancelled"
				return fileResult{index: index, file: file}
			}
		}
	}
	if j.ctx.Err() != nil {
		removePlans()
		file.Outputs = nil
		file.State = "cancelled"
		return fileResult{index: index, file: file}
	}
	j.publicationMu.Lock()
	if j.ctx.Err() != nil {
		j.publicationMu.Unlock()
		removePlans()
		file.Outputs = nil
		file.State = "cancelled"
		return fileResult{index: index, file: file}
	}
	j.pending = append(j.pending, plans...)
	j.publicationMu.Unlock()
	if len(file.Outputs) == 0 {
		file.State = "skipped"
	} else {
		file.State, file.Progress = "done", 1
	}
	return fileResult{index: index, file: file}
}

func (m *Manager) convertFile(j *job, index int, source string, target formats.Format) fileResult {
	file := protocol.FileJob{Source: source, State: "running", Outputs: []string{}}
	if j.ctx.Err() != nil {
		file.State = "cancelled"
		return fileResult{index: index, file: file}
	}
	if reason, ok := j.preSkip[index]; ok {
		file.State, file.Notice = "skipped", reason
		return fileResult{index: index, file: file}
	}
	// Files found by scanning a folder were not asked for by name, so being
	// unable to convert one is a skip, not a failure of the whole request.
	unsupported := func(message string) fileResult {
		if !j.items[index].Explicit {
			file.State, file.Notice = "skipped", message
		} else {
			file.State = "failed"
			file.Error = &protocol.FileError{Code: "unsupported", Message: message}
		}
		return fileResult{index: index, file: file}
	}
	sourceFormat, err := formats.Detect(source)
	if err != nil {
		if mediaRouter, ok := m.router.(MediaSourceRouter); ok {
			sourceFormat, err = mediaRouter.MediaSource(j.ctx, source)
		}
		if err != nil {
			if j.ctx.Err() != nil {
				file.State = "cancelled"
				return fileResult{index: index, file: file}
			}
			return unsupported(err.Error())
		}
	}
	if sourceFormat == target {
		return unsupported("source and target formats are identical")
	}
	ext := string(target)
	if target == formats.JPEG {
		ext = "jpg"
	}
	route, err := m.router.Resolve(sourceFormat, target)
	if err != nil {
		return unsupported(err.Error())
	}
	if formats.IsVideoFormat(sourceFormat) || formats.IsVideoFormat(target) {
		m.videoMu.Lock()
		defer m.videoMu.Unlock()
		if j.ctx.Err() != nil {
			file.State = "cancelled"
			return fileResult{index: index, file: file}
		}
	}
	outputDir := j.items[index].OutputDir(j.req.OutputDir)
	if multi, ok := route.(registry.MultiOutputEngine); ok {
		if outputDir == "" {
			outputDir = filepath.Dir(source)
		}
		return m.convertMany(j, index, file, multi, source, sourceFormat, target, ext, outputDir)
	}
	// Only planning is serialized: the destination stays reserved in memory
	// until this file is committed, so other workers cannot pick the same
	// name while the conversion itself runs unlocked.
	plan, release, err := m.reserveOutput(func(taken func(string) bool) (engine.OutputPlan, error) {
		return engine.PlanOutputExcluding(outputDir, source, ext, j.req.Conflict, taken)
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
	err = route.Convert(j.ctx, source, sourceFormat, plan.Temp, target, j.req.Options)
	if err != nil {
		plan.Rollback()
		file = m.failOperationFile(j, file, err)
		return fileResult{index: index, file: file}
	}
	output, file := m.publishPlan(j, plan, file)
	if file.State != "done" {
		return fileResult{index: index, file: file}
	}
	file.Progress, file.Outputs = 1, []string{output}
	return fileResult{index: index, file: file}
}

// reserveOutput plans one destination while holding the reservation lock, and
// returns the plan plus a function that releases the reservation. Conversions
// and PDF operations share this so a destination is never picked twice.
func (m *Manager) reserveOutput(plan func(taken func(string) bool) (engine.OutputPlan, error)) (engine.OutputPlan, func(), error) {
	var out engine.OutputPlan
	m.outputMu.Lock()
	p, err := plan(m.isReserved)
	out = p
	if err == nil && !p.Skipped() {
		m.reserved[p.Destination] = struct{}{}
	}
	m.outputMu.Unlock()
	release := func() {
		m.outputMu.Lock()
		delete(m.reserved, out.Destination)
		m.outputMu.Unlock()
	}
	return out, release, err
}

// publishPlan commits a finished plan and records it on the job so a later
// cancel can roll it back. It returns the published path and the file entry
// updated to done or to the reason it could not be published. A skipped plan
// leaves the file skipped.
func (m *Manager) publishPlan(j *job, plan engine.OutputPlan, file protocol.FileJob) (string, protocol.FileJob) {
	if plan.Skipped() {
		file.State = "skipped"
		return "", file
	}
	if j.ctx.Err() != nil {
		plan.Rollback()
		file.State = "cancelled"
		return "", file
	}
	j.publicationMu.Lock()
	defer j.publicationMu.Unlock()
	if err := commitOutput(j.ctx, plan); err != nil {
		plan.Rollback()
		file.State = "failed"
		file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
		return "", file
	}
	output := plan.Destination
	if j.ctx.Err() != nil {
		plan.Rollback()
		file.State = "cancelled"
		return "", file
	}
	j.pending = append(j.pending, plan)
	file.State = "done"
	return output, file
}

// failOperationFile turns a backend error into a file error, honouring
// cancellation, the encrypted-PDF sentinel, and a damaged input. A failure is
// only reported as invalid_pdf when the input itself cannot be read; otherwise
// it belongs to the operation and stays internal.
func (m *Manager) failOperationFile(j *job, file protocol.FileJob, err error) protocol.FileJob {
	if j.ctx.Err() != nil {
		file.State = "cancelled"
		return file
	}
	code := operationErrorCode(err)
	if code == "internal" && m.inputUnreadable(j, file.Source) {
		code = "invalid_pdf"
	}
	file.State = "failed"
	file.Error = &protocol.FileError{Code: code, Message: err.Error()}
	return file
}

// inputUnreadable probes an input and reports whether it cannot be opened as a
// PDF. A password error is not a damaged file, and cancellation is never a
// verdict on the input at all.
func (m *Manager) inputUnreadable(j *job, source string) bool {
	if j.ctx.Err() != nil {
		return false
	}
	if m.pdfToolbox().PDFCPU == nil {
		return false
	}
	_, err := m.pdfToolbox().PDFCPU.PageCount(j.ctx, source)
	if err == nil {
		return false
	}
	if errors.Is(err, pdfops.ErrPasswordRequired) {
		return false
	}
	return j.ctx.Err() == nil
}
