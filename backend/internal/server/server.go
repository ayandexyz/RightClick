package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/jobs"
	"file-converter/backend/internal/menu"
	"file-converter/backend/internal/protocol"
	"file-converter/backend/internal/registry"
)

type Dependencies struct {
	Jobs     *jobs.Manager
	Registry *registry.Registry
	Engine   string
	Version  string
	// MenuPath overrides the menu policy file; empty uses menu.Path().
	MenuPath string
}

type Server struct {
	deps         Dependencies
	active       atomic.Int64
	lastActivity atomic.Int64
	connections  sync.WaitGroup
	menuMu       sync.Mutex
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
			if write(protocol.Response{ID: req.ID, Result: map[string]any{"server": "fcd", "version": s.deps.Version, "protocol": 1, "engines": s.engineStatus()}}) != nil {
				return
			}
		case "engines":
			if write(protocol.Response{ID: req.ID, Result: map[string]any{"engines": s.engineStatus()}}) != nil {
				return
			}
		case "targets":
			var p struct {
				Files []string `json:"files"`
				Menu  bool     `json:"menu"`
			}
			if json.Unmarshal(req.Params, &p) != nil {
				if writeError(write, req.ID, "bad_request", "invalid targets params") != nil {
					return
				}
				continue
			}
			var targets []protocol.Target
			var err error
			if s.deps.Registry != nil && p.Menu {
				targets, err = s.deps.Registry.MenuTargetsForFiles(p.Files, s.loadMenuPolicy())
			} else if s.deps.Registry != nil {
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
		case "menu":
			result, code, msg := s.handleMenu(req.Params)
			if code != "" {
				if writeError(write, req.ID, code, msg) != nil {
					return
				}
				continue
			}
			if write(protocol.Response{ID: req.ID, Result: result}) != nil {
				return
			}
		case "convert":
			var p struct {
				Files      []string `json:"files"`
				Preset     string   `json:"preset"`
				OutputDir  string   `json:"output_dir"`
				Conflict   string   `json:"on_conflict"`
				Quality    *int     `json:"quality,omitempty"`
				MaxSize    *int     `json:"max_size,omitempty"`
				Background string   `json:"background,omitempty"`
				Recursive  bool     `json:"recursive,omitempty"`
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
			id, err := s.deps.Jobs.Submit(jobs.SubmitRequest{Files: p.Files, Preset: p.Preset, OutputDir: p.OutputDir, Conflict: protocol.ConflictPolicy(p.Conflict), Recursive: p.Recursive, Options: protocol.ImageOptions{Quality: p.Quality, MaxSize: p.MaxSize, Background: strings.ToLower(p.Background)}, Events: writeEvent(write)})
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
		case "subscribe":
			// Never let a slow subscriber stall a job: queue events and drop them when it falls behind.
			queue := make(chan protocol.Event, 256)
			stop := make(chan struct{})
			go func() {
				for {
					select {
					case event := <-queue:
						if write(event) != nil {
							return
						}
					case <-stop:
						return
					}
				}
			}()
			unsubscribe := s.deps.Jobs.Subscribe(func(event protocol.Event) {
				select {
				case queue <- event:
				default:
				}
			})
			defer func() { unsubscribe(); close(stop) }()
			if write(protocol.Response{ID: req.ID, Result: map[string]any{"subscribed": true}}) != nil {
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

func (s *Server) engineStatus() []protocol.EngineStatus {
	if s.deps.Registry != nil {
		return s.deps.Registry.Status()
	}
	return []protocol.EngineStatus{{ID: "imagemagick", Available: s.deps.Engine != ""}}
}

func (s *Server) menuPath() (string, error) {
	if s.deps.MenuPath != "" {
		return s.deps.MenuPath, nil
	}
	return menu.Path()
}

// loadMenuPolicy never fails: an unreadable or corrupt policy file falls back
// to the defaults so the file-manager menu keeps working.
func (s *Server) loadMenuPolicy() menu.Policy {
	path, err := s.menuPath()
	if err != nil {
		return menu.Policy{}
	}
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	policy, err := menu.Load(path)
	if err != nil {
		return menu.Policy{}
	}
	return policy
}

func (s *Server) handleMenu(params json.RawMessage) (map[string]any, string, string) {
	var p struct {
		Action   string   `json:"action"`
		Category string   `json:"category"`
		Formats  []string `json:"formats"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil, "bad_request", "invalid menu params"
	}
	path, err := s.menuPath()
	if err != nil {
		return nil, "internal", err.Error()
	}
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	policy, err := menu.Load(path)
	if err != nil {
		if p.Action == "show" {
			return nil, "internal", err.Error()
		}
		// A corrupt file is replaced by the edit rather than blocking it.
		policy = menu.Policy{}
	}
	if p.Action != "show" {
		if p.Action == "set" {
			policy, err = policy.Set(p.Category, p.Formats)
		} else {
			policy, err = policy.Reset(p.Category)
		}
		if err != nil {
			return nil, "bad_request", err.Error()
		}
		if err := menu.Save(path, policy); err != nil {
			return nil, "internal", err.Error()
		}
	}
	custom := []string{}
	for _, category := range menu.Categories {
		if len(policy[category]) > 0 {
			custom = append(custom, category)
		}
	}
	return map[string]any{"path": path, "menu": policy.Effective(), "custom": custom}, "", ""
}
