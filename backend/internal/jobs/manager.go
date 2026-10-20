package jobs

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
	"file-converter/backend/internal/registry"
)

type Converter interface {
	Convert(context.Context, string, formats.Format, string, formats.Format) error
}

type Router interface {
	Resolve(source, target formats.Format) (registry.Engine, error)
}

type SubmitRequest struct {
	Files     []string
	Preset    string
	OutputDir string
	Conflict  protocol.ConflictPolicy
	Events    func(protocol.Event)
}

type Manager struct {
	router      Router
	mu          sync.Mutex
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
func (e converterEngine) Convert(ctx context.Context, source string, sourceFormat formats.Format, destination string, target formats.Format) error {
	return e.converter.Convert(ctx, source, sourceFormat, destination, target)
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
	targetPreset := req.Preset
	switch targetPreset {
	case "png":
		targetPreset = "image-to-png"
	case "jpg", "jpeg":
		targetPreset = "image-to-jpeg"
	case "webp":
		targetPreset = "image-to-webp"
	}
	if _, ok := formats.PresetFormat(targetPreset); !ok {
		return "", fmt.Errorf("unsupported preset")
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
	j.cancel()
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
	if j.ctx.Err() != nil {
		for i := range files {
			if files[i].State == "pending" || files[i].State == "running" {
				files[i].State = "cancelled"
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

func (m *Manager) convertFile(j *job, index int, source string, target formats.Format) fileResult {
	file := protocol.FileJob{Source: source, State: "running", Outputs: []string{}}
	if j.ctx.Err() != nil {
		file.State = "cancelled"
		return fileResult{index: index, file: file}
	}
	sourceFormat, err := formats.Detect(source)
	if err != nil {
		file.State = "failed"
		file.Error = &protocol.FileError{Code: "unsupported", Message: err.Error()}
		return fileResult{index: index, file: file}
	}
	ext := string(target)
	if target == formats.JPEG {
		ext = "jpg"
	}
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
	route, err := m.router.Resolve(sourceFormat, target)
	if err == nil {
		err = route.Convert(j.ctx, source, sourceFormat, plan.Temp, target)
	}
	if err != nil {
		plan.RemoveTemp()
		if j.ctx.Err() != nil {
			file.State = "cancelled"
		} else {
			file.State = "failed"
			file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
		}
		return fileResult{index: index, file: file}
	}
	output, err := plan.Commit()
	if err != nil {
		plan.RemoveTemp()
		file.State = "failed"
		file.Error = &protocol.FileError{Code: "internal", Message: err.Error()}
		return fileResult{index: index, file: file}
	}
	file.State, file.Progress, file.Outputs = "done", 1, []string{output}
	return fileResult{index: index, file: file}
}
