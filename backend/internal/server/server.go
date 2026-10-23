package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/jobs"
	"file-converter/backend/internal/menu"
	"file-converter/backend/internal/pdfops"
	"file-converter/backend/internal/protocol"
	"file-converter/backend/internal/registry"
	"file-converter/backend/internal/thumbs"
)

type Dependencies struct {
	Jobs       *jobs.Manager
	Registry   *registry.Registry
	Engine     string
	Version    string
	PDFToolbox pdfops.Toolbox
	// PDFInfo serves pdf_info. The interface lives in pdfops so tests can pass
	// a stub; the daemon constructs the real implementation.
	PDFInfo pdfops.InfoBackend

	// Thumbs renders and caches thumbnails for pdf_thumbnails and cache_clear.
	// It is optional: without pdftocairo the daemon still serves every other
	// method, and only thumbnail requests report that it is unavailable.
	Thumbs *thumbs.Cache
	// MenuPath overrides the menu policy file; empty uses menu.Path().
	MenuPath string
	// RuntimeDir is where a connection keeps the unlocked copy and thumbnails of
	// a password-protected document. It should be private and memory-backed
	// (XDG_RUNTIME_DIR); empty falls back to the system temporary directory.
	RuntimeDir string
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

// handle serves one client connection. A thumbnail request can take a while, so
// the connection's context is cancelled once the loop ends or the connection
// fails; that is what stops a render the client has abandoned.
//
// The context is deliberately not tied to a reader goroutine: this loop already
// owns the connection's reads, and a second reader would race it for bytes.
func (s *Server) handle(conn net.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.active.Add(1)
	defer s.active.Add(-1)
	defer conn.Close()
	var fork func(string) *thumbs.Cache
	if s.deps.Thumbs != nil {
		fork = s.deps.Thumbs.Fork
	}
	locked := newLockedSession(s.lockedBase(), fork)
	defer locked.Close()
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
			if write(protocol.Response{ID: req.ID, Result: map[string]any{"server": "fcd", "version": s.deps.Version, "protocol": 1, "engines": s.EngineStatus()}}) != nil {
				return
			}
		case "engines":
			if write(protocol.Response{ID: req.ID, Result: map[string]any{"engines": s.EngineStatus()}}) != nil {
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
		case "remove_background":
			var p struct {
				Files     []string `json:"files"`
				OutputDir string   `json:"output_dir"`
				Conflict  string   `json:"on_conflict"`
			}
			if json.Unmarshal(req.Params, &p) != nil {
				if writeError(write, req.ID, "bad_request", "invalid remove_background params") != nil {
					return
				}
				continue
			}
			if p.Conflict == "" {
				p.Conflict = "rename"
			}
			id, err := s.deps.Jobs.SubmitBackgroundRemoval(jobs.BGRemoveRequest{
				Files: p.Files, OutputDir: p.OutputDir, Conflict: protocol.ConflictPolicy(p.Conflict), Events: writeEvent(write),
			})
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
		case "operations":
			if write(protocol.Response{ID: req.ID, Result: s.handleOperations(req.Params)}) != nil {
				return
			}
		case "pdf_thumbnails":
			result, code, msg := s.handlePDFThumbnails(ctx, req.Params, locked)
			if code != "" {
				if writeError(write, req.ID, code, msg) != nil {
					return
				}
				continue
			}
			if write(protocol.Response{ID: req.ID, Result: result}) != nil {
				return
			}
		case "cache_clear":
			result, code, msg := s.handleCacheClear()
			if code != "" {
				if writeError(write, req.ID, code, msg) != nil {
					return
				}
				continue
			}
			if write(protocol.Response{ID: req.ID, Result: result}) != nil {
				return
			}
		case "pdf_find":
			result, code, msg := s.handlePDFFind(ctx, req.Params)
			if code != "" {
				if writeError(write, req.ID, code, msg) != nil {
					return
				}
				continue
			}
			if write(protocol.Response{ID: req.ID, Result: result}) != nil {
				return
			}
		case "pdf_info":
			result, code, msg := s.handlePDFInfo(ctx, req.Params)
			if code != "" {
				if writeError(write, req.ID, code, msg) != nil {
					return
				}
				// Keep serving: a bad document is this request's problem, not a
				// reason to drop a client that may retry with another file.
				continue
			}
			if write(protocol.Response{ID: req.ID, Result: result}) != nil {
				return
			}
		case "run_operation":
			var p struct {
				Op        string          `json:"op"`
				Files     []string        `json:"files"`
				Params    protocol.Params `json:"params"`
				Password  *string         `json:"password"`
				OutputDir string          `json:"output_dir"`
				Conflict  string          `json:"on_conflict"`
			}
			if json.Unmarshal(req.Params, &p) != nil {
				if writeError(write, req.ID, "bad_request", "invalid run_operation params") != nil {
					return
				}
				continue
			}
			if p.Password != nil && p.Params.Password == nil {
				p.Params.Password = p.Password
			}
			if p.Conflict == "" {
				p.Conflict = string(protocol.ConflictRename)
			}
			id, err := s.deps.Jobs.SubmitOperation(jobs.OperationRequest{
				Op:    p.Op,
				Files: p.Files,
				Params: pdfops.Params{
					Pages:            derefString(p.Params.Pages),
					Angle:            derefInt(p.Params.Angle),
					Password:         derefString(p.Params.Password),
					Edit:             editItems(p.Params.Edit),
					Ranges:           derefString(p.Params.Ranges),
					Every:            derefInt(p.Params.Every),
					FilePages:        p.Params.FilePages,
					EncryptUser:      derefString(p.Params.EncryptUser),
					EncryptOwner:     derefString(p.Params.EncryptOwner),
					PermissionsOwner: derefString(p.Params.PermissionsOwner),
					Permissions:      p.Params.Permissions,
					Crop:             pdfops.CropParams{Unit: p.Params.Crop.Unit, Top: p.Params.Crop.Top, Right: p.Params.Crop.Right, Bottom: p.Params.Crop.Bottom, Left: p.Params.Crop.Left, Pages: p.Params.Crop.Pages},
					Resize:           pdfops.ResizeParams{To: p.Params.Resize.To, Fit: p.Params.Resize.Fit, Pages: p.Params.Resize.Pages},
					Layout:           pdfops.LayoutParams{PerSheet: p.Params.Layout.PerSheet, Orientation: p.Params.Layout.Orientation, Margin: p.Params.Layout.Margin, Border: p.Params.Layout.Border},
					Metadata:         pdfops.MetadataParams{Title: p.Params.Metadata.Title, Author: p.Params.Metadata.Author, Subject: p.Params.Metadata.Subject, Keywords: p.Params.Metadata.Keywords, Custom: p.Params.Metadata.Custom},
					PDFA:             p.Params.PDFA,
					Redact:           redactParams(p.Params.Redact),
					Stamp: pdfops.StampParams{
						Kind: p.Params.Stamp.Kind, Text: p.Params.Stamp.Text, Image: p.Params.Stamp.Image,
						Position: p.Params.Stamp.Position, Opacity: p.Params.Stamp.Opacity, Rotation: p.Params.Stamp.Rotation,
						Size: p.Params.Stamp.Size, Color: p.Params.Stamp.Color, Pages: p.Params.Stamp.Pages, OnTop: p.Params.Stamp.OnTop,
						Start: p.Params.Stamp.Start, Format: p.Params.Stamp.Format, Prefix: p.Params.Stamp.Prefix,
						Left: p.Params.Stamp.Left, Centre: p.Params.Stamp.Centre, Right: p.Params.Stamp.Right, Edge: p.Params.Stamp.Edge,
					},
				},
				OutputDir: p.OutputDir,
				Conflict:  protocol.ConflictPolicy(p.Conflict),
				Events:    writeEvent(write),
			})
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

// handleOperations lists the PDF tools that apply to a selection. A selection
// must be uniformly PDFs or uniformly images; a mixed or unrecognised selection
// yields an empty list rather than an error, so a right-click menu can call it
// unconditionally.
func (s *Server) handleOperations(params json.RawMessage) map[string]any {
	var p struct {
		Files []string `json:"files"`
	}
	operations := []protocol.Operation{}
	if json.Unmarshal(params, &p) != nil {
		return map[string]any{"operations": operations}
	}
	if len(p.Files) == 0 {
		return map[string]any{"operations": operations}
	}
	kind, ok := selectionInputKind(p.Files)
	if !ok {
		return map[string]any{"operations": operations}
	}
	for _, op := range pdfops.Catalogue() {
		if op.InputKind() != kind {
			continue
		}
		entry := protocol.Operation{
			ID:      op.ID,
			Label:   op.Label,
			Group:   op.Group,
			Mode:    op.Mode,
			Engine:  op.EngineID(),
			Enabled: true,
		}
		switch reason := op.UnavailableReason(s.deps.PDFToolbox, p.Files); reason {
		case "":
		default:
			entry.Enabled, entry.Reason = false, reason
		}
		operations = append(operations, entry)
	}
	return map[string]any{"operations": operations}
}

// selectionInputKind reports the common input kind of a selection. It fails when
// the files are not all the same recognised kind.
func selectionInputKind(files []string) (pdfops.InputKind, bool) {
	kind := pdfops.InputKind("")
	for _, file := range files {
		var this pdfops.InputKind
		switch {
		case isPDFFile(file):
			this = pdfops.InputPDF
		default:
			format, err := formats.Detect(file)
			if err != nil {
				return "", false
			}
			switch {
			case formats.IsImageFormat(format):
				this = pdfops.InputImage
			case formats.IsOfficeFormat(format):
				this = pdfops.InputOffice
			default:
				return "", false
			}
		}
		if kind == "" {
			kind = this
			continue
		}
		if kind != this {
			return "", false
		}
	}
	return kind, kind != ""
}

// isPDFFile reports whether a path is an existing regular file with a PDF
// header. It never fails a request: an unreadable path is simply not a PDF.
func isPDFFile(path string) bool {
	if err := formats.ValidateInput(path); err != nil {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	header := make([]byte, 5)
	if _, err := io.ReadFull(file, header); err != nil {
		return false
	}
	return string(header) == "%PDF-"
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// editItems converts the wire page list into the operation's own type. A nil
// list stays nil so an edit that omits it is reported as missing rather than as
// an edit of zero pages.
func editItems(items []protocol.PageItem) []pdfops.PageItem {
	if items == nil {
		return nil
	}
	out := make([]pdfops.PageItem, len(items))
	for i, item := range items {
		out[i] = pdfops.PageItem{Src: item.Src, Rotate: item.Rotate}
	}
	return out
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func writeError(write func(any) error, id int, code, msg string) error {
	return write(protocol.Error{ID: id, Error: protocol.ErrorValue{Code: code, Message: msg}})
}
func writeEvent(write func(any) error) func(protocol.Event) {
	return func(event protocol.Event) { _ = write(event) }
}

func (s *Server) EngineStatus() []protocol.EngineStatus {
	var status []protocol.EngineStatus
	if s.deps.Registry != nil {
		status = s.deps.Registry.Status()
	} else {
		status = []protocol.EngineStatus{{ID: "imagemagick", Available: s.deps.Engine != ""}}
	}
	// The PDF engines are separate from the conversion registry, so they are
	// reported alongside it: clients need to know which PDF tools are usable.
	for _, id := range pdfops.EngineIDs() {
		entry := protocol.EngineStatus{ID: id, Available: true}
		if err := s.deps.PDFToolbox.Available(id); err != nil {
			entry.Available, entry.Reason = false, err.Error()
		}
		status = append(status, entry)
	}
	if s.deps.Jobs != nil {
		entry := protocol.EngineStatus{ID: "bgremove", Available: true}
		if available, reason := s.deps.Jobs.BackgroundRemovalStatus(); !available {
			entry.Available, entry.Reason = false, reason
		}
		status = append(status, entry)
	}
	return status
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

// handlePDFInfo answers pdf_info: how many pages a document has and how large
// each one is. The page count is authoritative; per-page sizes are a
// convenience that a missing pdfinfo only removes. Failures a client can act on
// are reported with the same codes every other PDF surface uses, so an editor
// and a menu phrase a locked or damaged file identically.
func (s *Server) handlePDFFind(ctx context.Context, params json.RawMessage) (map[string]any, string, string) {
	var p struct {
		File     string   `json:"file"`
		Terms    []string `json:"terms"`
		Password string   `json:"password,omitempty"`
	}
	if json.Unmarshal(params, &p) != nil || p.File == "" || len(p.Terms) == 0 {
		return nil, "bad_request", "pdf_find needs a file and at least one term"
	}
	if err := formats.ValidateInput(p.File); err != nil {
		return nil, "bad_request", err.Error()
	}
	if !isPDFFile(p.File) {
		return nil, "invalid_pdf", "not a PDF file"
	}
	if s.deps.PDFToolbox.Poppler == nil {
		return nil, "unsupported", "poppler is not installed"
	}
	matches, err := s.deps.PDFToolbox.Poppler.FindPassword(ctx, p.File, p.Terms, p.Password)
	if err != nil {
		return nil, "invalid_pdf", err.Error()
	}
	return map[string]any{"matches": matches}, "", ""
}

func (s *Server) handlePDFInfo(ctx context.Context, params json.RawMessage) (map[string]any, string, string) {
	var p struct {
		File     string  `json:"file"`
		Password *string `json:"password,omitempty"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil, "bad_request", "invalid pdf_info params"
	}
	if p.File == "" {
		return nil, "bad_request", "pdf_info needs a file"
	}
	if err := formats.ValidateInput(p.File); err != nil {
		return nil, "bad_request", err.Error()
	}
	if !isPDFFile(p.File) {
		return nil, "invalid_pdf", "not a PDF file"
	}
	if s.deps.PDFInfo == nil {
		return nil, "internal", "no PDF engine is available"
	}
	password := ""
	if p.Password != nil {
		password = *p.Password
	}
	result, err := s.deps.PDFInfo.Pdf(ctx, p.File, password)
	if err != nil {
		return nil, pdfops.ErrorCode(err), err.Error()
	}
	out := map[string]any{"pages": result.PageCount}
	if len(result.Pages) > 0 {
		sizes := make([]map[string]any, len(result.Pages))
		for i, size := range result.Pages {
			sizes[i] = map[string]any{"w": size.W, "h": size.H, "rot": size.Rotation}
		}
		out["sizes"] = sizes
	}
	return out, "", ""
}

// handlePDFThumbnails answers pdf_thumbnails: a thumbnail per requested page,
// rendered on demand and cached privately. Missing pages are rendered; pages
// already cached are returned immediately.
//
// Requests for a locked or damaged document fail with the same codes every
// other PDF surface uses, so a page grid and a context menu phrase one file
// identically.
func (s *Server) handlePDFThumbnails(ctx context.Context, params json.RawMessage, locked *lockedSession) (map[string]any, string, string) {
	var p struct {
		File     string  `json:"file"`
		Pages    []int   `json:"pages"`
		Width    int     `json:"width,omitempty"`
		Password *string `json:"password,omitempty"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil, "bad_request", "invalid pdf_thumbnails params"
	}
	if err := formats.ValidateInput(p.File); err != nil {
		return nil, "bad_request", err.Error()
	}
	if !isPDFFile(p.File) {
		return nil, "invalid_pdf", "not a PDF file"
	}
	if s.deps.Thumbs == nil {
		return nil, "internal", "no thumbnail renderer is available"
	}
	password := ""
	if p.Password != nil {
		password = *p.Password
	}
	// The page count bounds the request: asking for page 900 of a 12-page
	// document should fail as a request error, not as a render failure.
	if s.deps.PDFInfo != nil {
		info, err := s.deps.PDFInfo.Pdf(ctx, p.File, password)
		if err != nil {
			return nil, pdfops.ErrorCode(err), err.Error()
		}
		for _, page := range p.Pages {
			if page > info.PageCount {
				return nil, "bad_request", fmt.Sprintf("page %d is past the end of a %d page document", page, info.PageCount)
			}
		}
	}
	request := thumbs.Request{File: p.File, Pages: p.Pages, Width: p.Width}
	// A password-protected document is rendered from this connection's private
	// copy; anything else goes through the persistent cache as before.
	pages, handled, err := locked.render(ctx, request, password)
	if err != nil {
		if code := pdfops.ErrorCode(err); code != "internal" {
			return nil, code, err.Error()
		}
		return nil, thumbsCode(err), err.Error()
	}
	if !handled {
		pages, err = s.deps.Thumbs.Render(ctx, request)
	}
	if err != nil {
		return nil, thumbsCode(err), err.Error()
	}
	entries := make([]map[string]any, len(pages))
	for i, page := range pages {
		entries[i] = map[string]any{"page": page.Page, "path": page.Path, "w": page.W, "h": page.H}
	}
	return map[string]any{"pages": entries}, "", ""
}

// thumbsCode maps a thumbnail failure onto a protocol code. The cache's own
// errors describe a bad request; anything else is the renderer's problem.
func thumbsCode(err error) string {
	switch {
	case errors.Is(err, thumbs.ErrBadWidth),
		errors.Is(err, thumbs.ErrTooManyPages),
		errors.Is(err, thumbs.ErrNoPages),
		errors.Is(err, thumbs.ErrBadPage):
		return "bad_request"
	}
	return "internal"
}

// handleCacheClear empties the thumbnail cache. It is the one method that
// removes files the user cannot see, so it reports how many it removed rather
// than failing quietly.
func (s *Server) handleCacheClear() (map[string]any, string, string) {
	if s.deps.Thumbs == nil {
		return map[string]any{"removed": 0}, "", ""
	}
	removed, err := s.deps.Thumbs.Clear()
	if err != nil {
		return nil, "internal", err.Error()
	}
	return map[string]any{"removed": removed}, "", ""
}

// lockedBase is the directory under which a connection's private unlocked
// document goes.
func (s *Server) lockedBase() string {
	if s.deps.RuntimeDir == "" {
		return ""
	}
	return filepath.Join(s.deps.RuntimeDir, "file-converter")
}

// redactParams converts the wire form of a redaction request.
func redactParams(p protocol.RedactParams) pdfops.RedactParams {
	areas := make([]pdfops.RedactArea, len(p.Areas))
	for i, a := range p.Areas {
		areas[i] = pdfops.RedactArea{Page: a.Page, X: a.X, Y: a.Y, W: a.W, H: a.H}
	}
	return pdfops.RedactParams{Areas: areas, Terms: p.Terms, DPI: p.DPI}
}
