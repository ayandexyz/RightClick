package jobs

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/protocol"
)

type Converter interface {
	Convert(context.Context, string, formats.Format, string, formats.Format) error
}

type SubmitRequest struct {
	Files     []string
	Preset    string
	OutputDir string
	Conflict  protocol.ConflictPolicy
	Events    func(protocol.Event)
}

type Manager struct {
	converter Converter
	mu        sync.Mutex
	next      atomic.Uint64
	jobs      map[string]*job
	queue     chan *job
	done      chan struct{}
	loopDone  chan struct{}
	closeOnce sync.Once
}

type job struct {
	id     string
	req    SubmitRequest
	ctx    context.Context
	cancel context.CancelFunc
}

func NewManager(converter Converter) *Manager {
	m := &Manager{converter: converter, jobs: make(map[string]*job), queue: make(chan *job, 64), done: make(chan struct{}), loopDone: make(chan struct{})}
	go m.loop()
	return m
}

func (m *Manager) Submit(req SubmitRequest) (string, error) {
	if len(req.Files) == 0 {
		return "", fmt.Errorf("no files")
	}
	targetPreset := req.Preset
	if targetPreset == "png" {
		targetPreset = "image-to-png"
	} else if targetPreset == "jpg" || targetPreset == "jpeg" {
		targetPreset = "image-to-jpeg"
	} else if targetPreset == "webp" {
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
	if j.cancel != nil {
		j.cancel()
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

func (m *Manager) emit(j *job, name string, data protocol.Job) {
	if j.req.Events != nil {
		j.req.Events(protocol.Event{Name: name, Data: data})
	}
}

func (m *Manager) run(j *job) {
	ctx := j.ctx
	target, _ := formats.PresetFormat(j.req.Preset)
	files := make([]protocol.FileJob, len(j.req.Files))
	for i, source := range j.req.Files {
		files[i] = protocol.FileJob{Source: source, State: "pending", Outputs: []string{}}
	}
	state := "running"
	publish := func(event string) {
		done := 0
		for _, f := range files {
			if f.State == "done" || f.State == "skipped" || f.State == "failed" || f.State == "cancelled" {
				done++
			}
		}
		m.emit(j, event, protocol.Job{ID: j.id, Preset: j.req.Preset, Label: j.req.Preset, State: state, Progress: float64(done) / float64(len(files)), Files: append([]protocol.FileJob(nil), files...)})
	}
	publish("job.updated")
	success, failed := 0, 0
	for i, source := range j.req.Files {
		if ctx.Err() != nil {
			files[i].State = "cancelled"
			continue
		}
		files[i].State = "running"
		publish("job.updated")
		sourceFormat, err := formats.Detect(source)
		if err != nil {
			files[i].State = "failed"
			files[i].Error = &protocol.FileError{Code: "unsupported", Message: err.Error()}
			failed++
			publish("job.updated")
			continue
		}
		ext := string(target)
		if target == formats.JPEG {
			ext = "jpg"
		}
		plan, err := engine.PlanOutput(j.req.OutputDir, source, ext, j.req.Conflict)
		if err != nil {
			files[i].State = "failed"
			files[i].Error = &protocol.FileError{Code: "internal", Message: err.Error()}
			failed++
			publish("job.updated")
			continue
		}
		if plan.Skipped() {
			files[i].State = "skipped"
			publish("job.updated")
			continue
		}
		err = m.converter.Convert(ctx, source, sourceFormat, plan.Temp, target)
		if err != nil {
			plan.RemoveTemp()
			if ctx.Err() != nil {
				files[i].State = "cancelled"
			} else {
				files[i].State = "failed"
				files[i].Error = &protocol.FileError{Code: "internal", Message: err.Error()}
				failed++
			}
			publish("job.updated")
			continue
		}
		output, err := plan.Commit()
		if err != nil {
			plan.RemoveTemp()
			files[i].State = "failed"
			files[i].Error = &protocol.FileError{Code: "internal", Message: err.Error()}
			failed++
			publish("job.updated")
			continue
		}
		files[i].State, files[i].Progress, files[i].Outputs = "done", 1, []string{output}
		success++
		publish("job.updated")
	}
	if ctx.Err() != nil {
		state = "cancelled"
	} else if failed > 0 && success > 0 {
		state = "completed_with_errors"
	} else if failed > 0 {
		state = "failed"
	} else {
		state = "completed"
	}
	final := protocol.Job{ID: j.id, Preset: j.req.Preset, Label: j.req.Preset, State: state, Progress: 1, Files: files}
	m.emit(j, "job.finished", final)
	j.cancel()
	m.mu.Lock()
	delete(m.jobs, j.id)
	m.mu.Unlock()
}
