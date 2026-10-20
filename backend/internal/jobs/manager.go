package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
	"file-converter/backend/internal/registry"
)

type Converter interface {
	Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error
}

var planOutputPath = engine.PlanOutputPath
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
	Events    func(protocol.Event)
}

type Manager struct {
	router      Router
	mu          sync.Mutex
	outputMu    sync.Mutex
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
	m := &Manager{router: router, jobs: make(map[string]*job), subscribers: make(map[uint64]func(protocol.Event)), queue: make(chan *job, 64), done: make(chan struct{}), loopDone: make(chan struct{})}
	go m.loop()
	return m
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
		if err := formats.ValidateInput(file); err != nil {
			return "", err
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
		for _, file := range req.Files {
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
	j := &job{id: id, req: req, ctx: ctx, cancel: cancel}
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
	defer j.publicationMu.Unlock()
	if !j.published {
		j.cancel()
		for _, plan := range j.pending {
			plan.Rollback()
		}
	}
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

	workers := runtime.NumCPU()
	if workers > len(files) {
		workers = len(files)
	}
	indices := make(chan int)
	results := make(chan fileResult, len(files))
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range indices {
				results <- m.convertFile(j, index, j.req.Files[index], target)
			}
		}()
	}
	go func() {
		defer close(indices)
		for index := range j.req.Files {
			select {
			case indices <- index:
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
		emitter.files = files
		if j.ctx.Err() == nil {
			emitter.emit("job.updated", false)
		}
	}
	beforePublish()
	j.publicationMu.Lock()
	if j.ctx.Err() != nil {
		for _, plan := range j.pending {
			plan.Rollback()
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

func (m *Manager) convertMany(j *job, index int, file protocol.FileJob, route registry.MultiOutputEngine, source string, sourceFormat, target formats.Format, ext string) fileResult {
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	outputDir := j.req.OutputDir
	if outputDir == "" {
		outputDir = filepath.Dir(source)
	}
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
		for _, plan := range plans {
			plan.Rollback()
		}
	}
	for i, page := range pages {
		clean := filepath.Clean(page)
		info, statErr := os.Lstat(clean)
		if statErr != nil || filepath.Dir(clean) != filepath.Clean(renderDir) || filepath.Ext(clean) != "."+ext || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			removePlans()
			file.State = "failed"
			file.Error = &protocol.FileError{Code: "internal", Message: "multi-page engine returned unsafe output"}
			return fileResult{index: index, file: file}
		}
		plans[i], err = planOutputPath(outputDir, source, ext, i+1, j.req.Conflict)
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
	sourceFormat, err := formats.Detect(source)
	if err != nil {
		if mediaRouter, ok := m.router.(MediaSourceRouter); ok {
			sourceFormat, err = mediaRouter.MediaSource(j.ctx, source)
		}
		if err != nil {
			if j.ctx.Err() != nil {
				file.State = "cancelled"
			} else {
				file.State = "failed"
				file.Error = &protocol.FileError{Code: "unsupported", Message: err.Error()}
			}
			return fileResult{index: index, file: file}
		}
	}
	if sourceFormat == target {
		file.State = "failed"
		file.Error = &protocol.FileError{Code: "unsupported", Message: "source and target formats are identical"}
		return fileResult{index: index, file: file}
	}
	ext := string(target)
	if target == formats.JPEG {
		ext = "jpg"
	}
	route, err := m.router.Resolve(sourceFormat, target)
	if err != nil {
		file.State = "failed"
		file.Error = &protocol.FileError{Code: "unsupported", Message: err.Error()}
		return fileResult{index: index, file: file}
	}
	if multi, ok := route.(registry.MultiOutputEngine); ok {
		return m.convertMany(j, index, file, multi, source, sourceFormat, target, ext)
	}
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	plan, err := engine.PlanOutput(j.req.OutputDir, source, ext, j.req.Conflict)
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
		if j.ctx.Err() != nil {
			file.State = "cancelled"
		} else {
			file.State = "failed"
			file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
		}
		return fileResult{index: index, file: file}
	}
	if j.ctx.Err() != nil {
		plan.Rollback()
		file.State = "cancelled"
		return fileResult{index: index, file: file}
	}
	output, err := plan.Commit()
	if err != nil {
		plan.Rollback()
		file.State = "failed"
		file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
		return fileResult{index: index, file: file}
	}
	j.publicationMu.Lock()
	if j.ctx.Err() != nil {
		j.publicationMu.Unlock()
		plan.Rollback()
		file.State = "cancelled"
		return fileResult{index: index, file: file}
	}
	j.pending = append(j.pending, plan)
	j.publicationMu.Unlock()
	file.State, file.Progress, file.Outputs = "done", 1, []string{output}
	return fileResult{index: index, file: file}
}
