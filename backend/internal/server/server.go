package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/jobs"
	"file-converter/backend/internal/protocol"
	"file-converter/backend/internal/registry"
)

type Dependencies struct {
	Jobs     *jobs.Manager
	Registry *registry.Registry
	Engine   string
	Version  string
}

type Server struct {
	deps         Dependencies
	active       atomic.Int64
	lastActivity atomic.Int64
	connections  sync.WaitGroup
}

func New(deps Dependencies) *Server {
	s := &Server{deps: deps}
	s.lastActivity.Store(time.Now().UnixNano())
	return s
}

func (s *Server) Wait() {
	s.connections.Wait()
}

func (s *Server) Serve(listener net.Listener) error {
	return s.ServeWithIdle(listener, 0)
}

func (s *Server) ServeWithIdle(listener net.Listener, idle time.Duration) error {
	if idle > 0 {
		interval := idle / 4
		if interval < 50*time.Millisecond {
			interval = 50 * time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		go func() {
			for range ticker.C {
				if s.active.Load() == 0 && s.deps.Jobs.Active() == 0 && time.Since(time.Unix(0, s.lastActivity.Load())) >= idle {
					_ = listener.Close()
					return
				}
			}
		}()
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		s.lastActivity.Store(time.Now().UnixNano())
		s.connections.Add(1)
		go func() {
			defer s.connections.Done()
			s.handle(conn)
		}()
	}
}

func (s *Server) handle(conn net.Conn) {
	s.active.Add(1)
	defer s.active.Add(-1)
	defer conn.Close()
	reader := bufio.NewScanner(conn)
	reader.Buffer(make([]byte, 4096), 8*1024*1024)
	var writeMu sync.Mutex
	write := func(message any) error {
		data, err := protocol.EncodeMessage(message)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_, err = conn.Write(data)
		return err
	}
	for reader.Scan() {
		req, err := protocol.DecodeRequest(reader.Bytes())
		if err != nil {
			if write(protocol.Error{ID: 0, Error: protocol.ErrorValue{Code: "bad_request", Message: err.Error()}}) != nil {
				return
			}
			continue
		}
		if validation := protocol.ValidateRequest(req); validation != nil {
			if write(*validation) != nil {
				return
			}
			continue
		}
		switch req.Method {
		case "hello":
			engines := []map[string]any{{"id": "imagemagick", "available": s.deps.Engine != ""}}
			if s.deps.Registry != nil {
				engines = s.deps.Registry.Status()
			}
			if write(protocol.Response{ID: req.ID, Result: map[string]any{"server": "fcd", "version": s.deps.Version, "protocol": 1, "engines": engines}}) != nil {
				return
			}
		case "targets":
			var p struct {
				Files []string `json:"files"`
			}
			if json.Unmarshal(req.Params, &p) != nil {
				if writeError(write, req.ID, "bad_request", "invalid targets params") != nil {
					return
				}
				continue
			}
			var targets []protocol.Target
			var err error
			if s.deps.Registry != nil {
				targets, err = s.deps.Registry.TargetsForFiles(p.Files)
			} else {
				targets, err = formats.Targets(p.Files)
			}
			if err != nil {
				if writeError(write, req.ID, "unsupported", err.Error()) != nil {
					return
				}
				continue
			}
			if write(protocol.Response{ID: req.ID, Result: map[string]any{"files_supported": len(targets) > 0, "targets": targets}}) != nil {
				return
			}
		case "convert":
			var p struct {
				Files     []string `json:"files"`
				Preset    string   `json:"preset"`
				OutputDir string   `json:"output_dir"`
				Conflict  string   `json:"on_conflict"`
			}
			if json.Unmarshal(req.Params, &p) != nil {
				if writeError(write, req.ID, "bad_request", "invalid convert params") != nil {
					return
				}
				continue
			}
			if p.Conflict == "" {
				p.Conflict = "rename"
			}
			id, err := s.deps.Jobs.Submit(jobs.SubmitRequest{Files: p.Files, Preset: p.Preset, OutputDir: p.OutputDir, Conflict: protocol.ConflictPolicy(p.Conflict), Events: writeEvent(write)})
			if err != nil {
				code := "unsupported"
				if errors.Is(err, formats.ErrInvalidInput) {
					code = "bad_request"
				}
				if writeError(write, req.ID, code, err.Error()) != nil {
					return
				}
				continue
			}
			if write(protocol.Response{ID: req.ID, Result: map[string]string{"job": id}}) != nil {
				return
			}
		case "cancel":
			var p struct {
				Job string `json:"job"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if err := s.deps.Jobs.Cancel(p.Job); err != nil {
				if writeError(write, req.ID, "not_found", err.Error()) != nil {
					return
				}
				continue
			}
			if write(protocol.Response{ID: req.ID, Result: map[string]any{}}) != nil {
				return
			}
		}
	}
}
func writeError(write func(any) error, id int, code, msg string) error {
	return write(protocol.Error{ID: id, Error: protocol.ErrorValue{Code: code, Message: msg}})
}
func writeEvent(write func(any) error) func(protocol.Event) {
	return func(event protocol.Event) { _ = write(event) }
}
