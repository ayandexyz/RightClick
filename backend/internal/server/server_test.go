package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/jobs"
	"file-converter/backend/internal/pdfops"
	"file-converter/backend/internal/protocol"
	"file-converter/backend/internal/registry"
)

type testEngine struct {
	block   bool
	mu      sync.Mutex
	options protocol.ImageOptions
}

type mediaTestEngine struct {
	kind engine.MediaKind
}

func (e *mediaTestEngine) ID() string                 { return "ffmpeg" }
func (e *mediaTestEngine) Available() (string, error) { return "ffmpeg", nil }
func (e *mediaTestEngine) Supports(source, target formats.Format) bool {
	return (formats.IsAudioFormat(source) || formats.IsVideoFormat(source)) &&
		(formats.IsAudioFormat(target) || formats.IsVideoFormat(target)) &&
		(!formats.IsAudioFormat(source) || formats.IsAudioFormat(target))
}
func (e *mediaTestEngine) Convert(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) error {
	return nil
}
func (e *mediaTestEngine) Probe(context.Context, string) (engine.MediaKind, error) {
	return e.kind, nil
}

func (e *testEngine) lastOptions() protocol.ImageOptions {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.options
}

func (e *testEngine) ID() string                 { return "test" }
func (e *testEngine) Available() (string, error) { return "test", nil }
func (e *testEngine) Supports(source, target formats.Format) bool {
	return formats.IsImageFormat(source) && formats.IsImageFormat(target) && source != target
}
func (e *testEngine) Convert(ctx context.Context, _ string, _ formats.Format, destination string, _ formats.Format, options protocol.ImageOptions) error {
	e.mu.Lock()
	e.options = options
	e.mu.Unlock()
	if e.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return os.WriteFile(destination, []byte("converted"), 0o600)
}

func imageFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func startTestServer(t *testing.T, engine *testEngine) (net.Conn, func()) {
	t.Helper()
	reg := registry.New()
	reg.Register(engine)
	return startTestServerWithRegistry(t, reg)
}

func startTestServerWithRegistry(t *testing.T, reg *registry.Registry) (net.Conn, func()) {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "fcd.sock"))
	if err != nil {
		t.Fatal(err)
	}
	manager := jobs.NewManagerWithRegistry(reg)
	srv := New(Dependencies{Jobs: manager, Registry: reg, Version: "test"})
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(listener)
		close(done)
	}()
	conn, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		listener.Close()
		manager.Close()
		t.Fatal(err)
	}
	cleanup := func() {
		_ = conn.Close()
		_ = listener.Close()
		<-done
		manager.Close()
	}
	return conn, cleanup
}

func sendRequest(t *testing.T, conn net.Conn, req protocol.Request) map[string]any {
	t.Helper()
	data, err := protocol.EncodeMessage(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestServerHelloAndRejectsUnknownMethod(t *testing.T) {
	conn, cleanup := startTestServer(t, &testEngine{})
	defer cleanup()

	hello := sendRequest(t, conn, protocol.Request{ID: 1, Method: "hello", Params: json.RawMessage(`{"client":"test","protocol":1}`)})
	if hello["result"] == nil {
		t.Fatalf("hello response: %#v", hello)
	}
	unknown := sendRequest(t, conn, protocol.Request{ID: 2, Method: "unknown", Params: json.RawMessage(`{}`)})
	if unknown["error"] == nil {
		t.Fatalf("unknown response: %#v", unknown)
	}
}

func TestServerHelloTargetsAndRejectsAudioToVideo(t *testing.T) {
	reg := registry.New()
	reg.Register(&testEngine{})
	reg.Register(&mediaTestEngine{kind: engine.MediaAudio})
	conn, cleanup := startTestServerWithRegistry(t, reg)
	defer cleanup()

	hello := sendRequest(t, conn, protocol.Request{ID: 1, Method: "hello", Params: json.RawMessage(`{"client":"test","protocol":1}`)})
	result, ok := hello["result"].(map[string]any)
	if !ok {
		t.Fatalf("hello response: %#v", hello)
	}
	engines, ok := result["engines"].([]any)
	if !ok {
		t.Fatalf("hello engines: %#v", result["engines"])
	}
	if got := countRegistryEngines(engines); got != 2 {
		t.Fatalf("hello engines = %#v, want two conversion engines", engines)
	}

	input := filepath.Join(t.TempDir(), "clip.mp3")
	if err := os.WriteFile(input, []byte("media fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	targets := sendRequest(t, conn, protocol.Request{ID: 2, Method: "targets", Params: mustJSON(map[string]any{"files": []string{input}})})
	targetResult, ok := targets["result"].(map[string]any)
	if !ok || len(targetResult["targets"].([]any)) != 11 {
		t.Fatalf("targets response: %#v", targets)
	}
	convert := sendRequest(t, conn, protocol.Request{ID: 3, Method: "convert", Params: mustJSON(map[string]any{"files": []string{input}, "preset": "video-to-mp4"})})
	if errorValue, ok := convert["error"].(map[string]any); !ok || errorValue["code"] != "unsupported" {
		t.Fatalf("audio-to-video should be unsupported: %#v", convert)
	}
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func TestServerCancelStopsActiveJob(t *testing.T) {
	conn, cleanup := startTestServer(t, &testEngine{block: true})
	defer cleanup()
	input := imageFixture(t)
	outputDir := t.TempDir()
	if err := writeRequest(conn, protocol.Request{ID: 1, Method: "convert", Params: json.RawMessage(`{"files":["` + input + `"],"preset":"image-to-jpeg","output_dir":"` + outputDir + `"}`)}); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(conn)
	jobID := ""
	for {
		if !scanner.Scan() {
			t.Fatal("server closed connection before job started")
		}
		var message map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			t.Fatal(err)
		}
		if result, ok := message["result"].(map[string]any); ok {
			jobID, _ = result["job"].(string)
		}
		if message["event"] == "job.updated" {
			data, _ := message["data"].(map[string]any)
			if data["state"] == "running" {
				break
			}
		}
	}
	if jobID == "" {
		t.Fatal("missing job id")
	}
	if err := writeRequest(conn, protocol.Request{ID: 2, Method: "cancel", Params: json.RawMessage(`{"job":"` + jobID + `"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := writeRequest(conn, protocol.Request{ID: 3, Method: "cancel", Params: json.RawMessage(`{"job":"missing"}`)}); err != nil {
		t.Fatal(err)
	}
	foundCancelled := false
	foundUnknown := false
	deadline := time.After(3 * time.Second)
	for !foundCancelled || !foundUnknown {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for cancelled job")
		default:
		}
		if !scanner.Scan() {
			t.Fatal("server closed connection")
		}
		var message map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			t.Fatal(err)
		}
		if message["error"] != nil {
			foundUnknown = true
		}
		if message["event"] == "job.finished" {
			data, _ := message["data"].(map[string]any)
			foundCancelled = data["state"] == "cancelled"
		}
	}
	if !foundUnknown {
		t.Fatal("expected unknown-job error")
	}
}

func TestServerDisconnectDoesNotCancelJob(t *testing.T) {
	conn, cleanup := startTestServer(t, &testEngine{})
	input := imageFixture(t)
	outputDir := t.TempDir()
	if err := writeRequest(conn, protocol.Request{ID: 1, Method: "convert", Params: json.RawMessage(`{"files":["` + input + `"],"preset":"image-to-jpeg","output_dir":"` + outputDir + `"}`)}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	output := filepath.Join(outputDir, "photo.jpg")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(output); err == nil {
			cleanup()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cleanup()
	t.Fatal("job did not continue after disconnect")
}

func TestServerRejectsMalformedAndInvalidRequests(t *testing.T) {
	conn, cleanup := startTestServer(t, &testEngine{})
	defer cleanup()
	if _, err := conn.Write([]byte("not-json\n")); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var malformed map[string]any
	if err := json.Unmarshal(line, &malformed); err != nil {
		t.Fatal(err)
	}
	if malformed["error"] == nil {
		t.Fatal("expected malformed request error")
	}
	invalid := sendRequest(t, conn, protocol.Request{ID: 2, Method: "convert", Params: json.RawMessage(`{"files":[]}`)})
	if invalid["error"] == nil {
		t.Fatal("expected invalid params error")
	}
	unsupported := sendRequest(t, conn, protocol.Request{ID: 3, Method: "convert", Params: json.RawMessage(`{"files":["/tmp/input.png"],"preset":"document-to-pdf"}`)})
	if unsupported["error"] == nil {
		t.Fatal("expected unsupported preset error")
	}
	missing := sendRequest(t, conn, protocol.Request{ID: 4, Method: "convert", Params: json.RawMessage(`{"files":["/tmp/missing.png"],"preset":"image-to-jpeg"}`)})
	if errorValue, ok := missing["error"].(map[string]any); !ok || errorValue["code"] != "bad_request" {
		t.Fatalf("expected stable bad_request for missing input: %#v", missing)
	}
	unsafe := sendRequest(t, conn, protocol.Request{ID: 5, Method: "convert", Params: json.RawMessage(`{"files":["/tmp/we[0].png"],"preset":"image-to-jpeg"}`)})
	if errorValue, ok := unsafe["error"].(map[string]any); !ok || errorValue["code"] != "bad_request" {
		t.Fatalf("expected stable bad_request for unsafe input: %#v", unsafe)
	}
}

func TestServerIdleShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	reg.Register(&testEngine{})
	manager := jobs.NewManagerWithRegistry(reg)
	defer manager.Close()
	srv := New(Dependencies{Jobs: manager, Registry: reg, Version: "test"})
	done := make(chan error, 1)
	go func() { done <- srv.ServeWithIdle(listener, 50*time.Millisecond) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected listener-close error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down while idle")
	}
}

func writeRequest(conn net.Conn, req protocol.Request) error {
	data, err := protocol.EncodeMessage(req)
	if err != nil {
		return err
	}
	_, err = conn.Write(data)
	return err
}

func TestServerNormalizesBackgroundOption(t *testing.T) {
	engine := &testEngine{}
	conn, cleanup := startTestServer(t, engine)
	defer cleanup()
	input := imageFixture(t)
	outputDir := t.TempDir()
	response := sendRequest(t, conn, protocol.Request{ID: 1, Method: "convert", Params: json.RawMessage(`{"files":["` + input + `"],"preset":"image-to-jpeg","output_dir":"` + outputDir + `","background":"#AbC"}`)})
	if response["error"] != nil {
		t.Fatalf("convert response: %#v", response)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if engine.lastOptions().Background == "#abc" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background was not normalized: %#v", engine.lastOptions())
}

func TestServerConvertReturnsJobAndFinishedEvent(t *testing.T) {
	conn, cleanup := startTestServer(t, &testEngine{})
	defer cleanup()
	input := imageFixture(t)

	data, err := protocol.EncodeMessage(protocol.Request{ID: 1, Method: "convert", Params: json.RawMessage(`{"files":["` + input + `"],"preset":"image-to-jpeg","output_dir":"` + t.TempDir() + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(conn)
	var jobID string
	finished := false
	deadline := time.After(3 * time.Second)
	for !finished {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for conversion")
		default:
		}
		if !scanner.Scan() {
			t.Fatal("server closed connection")
		}
		var message map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			t.Fatal(err)
		}
		if result, ok := message["result"].(map[string]any); ok {
			jobID, _ = result["job"].(string)
		}
		if message["event"] == "job.finished" {
			finished = true
		}
	}
	if jobID == "" {
		t.Fatal("missing job id")
	}
}

func TestServerEnginesReportsStatusWithReason(t *testing.T) {
	reg := registry.New()
	reg.Register(&testEngine{})
	conn, cleanup := startTestServerWithRegistry(t, reg)
	defer cleanup()

	resp := sendRequest(t, conn, protocol.Request{ID: 1, Method: "engines", Params: json.RawMessage(`{}`)})
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("engines response: %#v", resp)
	}
	engines, ok := result["engines"].([]any)
	if !ok {
		t.Fatalf("engines: %#v", result["engines"])
	}
	entry := onlyRegistryEngine(t, engines)
	if _, ok := entry["id"].(string); !ok {
		t.Fatalf("entry missing id: %#v", entry)
	}
	if _, ok := entry["available"].(bool); !ok {
		t.Fatalf("entry missing available: %#v", entry)
	}
	if reason, ok := entry["reason"].(string); !ok || reason != "" {
		t.Fatalf("available engine should report empty reason: %#v", entry)
	}
}

func startMenuServer(t *testing.T, menuPath string) (net.Conn, func()) {
	t.Helper()
	reg := registry.New()
	reg.Register(&testEngine{})
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "fcd.sock"))
	if err != nil {
		t.Fatal(err)
	}
	manager := jobs.NewManagerWithRegistry(reg)
	srv := New(Dependencies{Jobs: manager, Registry: reg, Version: "test", MenuPath: menuPath})
	done := make(chan struct{})
	go func() { _ = srv.Serve(listener); close(done) }()
	conn, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return conn, func() { _ = conn.Close(); _ = listener.Close(); <-done; manager.Close() }
}

func TestServerMenuSetShowResetAndMenuTargets(t *testing.T) {
	menuPath := filepath.Join(t.TempDir(), "cfg", "menu.toml")
	conn, cleanup := startMenuServer(t, menuPath)
	defer cleanup()
	input := imageFixture(t)

	full := sendRequest(t, conn, protocol.Request{ID: 1, Method: "targets", Params: mustJSON(map[string]any{"files": []string{input}})})
	fullCount := len(full["result"].(map[string]any)["targets"].([]any))

	set := sendRequest(t, conn, protocol.Request{ID: 2, Method: "menu", Params: mustJSON(map[string]any{"action": "set", "category": "image", "formats": []string{"webp"}})})
	if set["error"] != nil {
		t.Fatalf("set: %#v", set)
	}
	if _, err := os.Stat(menuPath); err != nil {
		t.Fatalf("policy not saved: %v", err)
	}

	menuTargets := sendRequest(t, conn, protocol.Request{ID: 3, Method: "targets", Params: mustJSON(map[string]any{"files": []string{input}, "menu": true})})
	list := menuTargets["result"].(map[string]any)["targets"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["format"] != "webp" {
		t.Fatalf("menu targets: %#v", list)
	}
	unfiltered := sendRequest(t, conn, protocol.Request{ID: 4, Method: "targets", Params: mustJSON(map[string]any{"files": []string{input}})})
	if got := len(unfiltered["result"].(map[string]any)["targets"].([]any)); got != fullCount {
		t.Fatalf("targets without menu changed: %d != %d", got, fullCount)
	}

	show := sendRequest(t, conn, protocol.Request{ID: 5, Method: "menu", Params: mustJSON(map[string]any{"action": "show"})})
	result := show["result"].(map[string]any)
	if result["path"] != menuPath {
		t.Fatalf("show: %#v", result)
	}
	if got := result["menu"].(map[string]any)["image"].([]any); len(got) != 1 || got[0] != "webp" {
		t.Fatalf("show image: %#v", got)
	}

	reset := sendRequest(t, conn, protocol.Request{ID: 6, Method: "menu", Params: mustJSON(map[string]any{"action": "reset"})})
	if reset["error"] != nil {
		t.Fatalf("reset: %#v", reset)
	}
	after := sendRequest(t, conn, protocol.Request{ID: 7, Method: "menu", Params: mustJSON(map[string]any{"action": "show"})})
	if got := after["result"].(map[string]any)["menu"].(map[string]any)["image"].([]any); len(got) != 3 {
		t.Fatalf("image not back to defaults: %#v", got)
	}
}

func TestServerMenuSetRejectsUnknownFormatAndCorruptFileFallsBack(t *testing.T) {
	menuPath := filepath.Join(t.TempDir(), "menu.toml")
	if err := os.WriteFile(menuPath, []byte("image = nonsense\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	conn, cleanup := startMenuServer(t, menuPath)
	defer cleanup()
	input := imageFixture(t)

	// A corrupt policy must never break the menu: serve defaults.
	resp := sendRequest(t, conn, protocol.Request{ID: 1, Method: "targets", Params: mustJSON(map[string]any{"files": []string{input}, "menu": true})})
	if resp["error"] != nil {
		t.Fatalf("corrupt policy broke targets: %#v", resp)
	}
	bad := sendRequest(t, conn, protocol.Request{ID: 2, Method: "menu", Params: mustJSON(map[string]any{"action": "set", "category": "image", "formats": []string{"hologram"}})})
	if errValue, ok := bad["error"].(map[string]any); !ok || errValue["code"] != "bad_request" {
		t.Fatalf("unknown format: %#v", bad)
	}
}

func TestServerConvertRecursiveFolder(t *testing.T) {
	conn, cleanup := startTestServer(t, &testEngine{})
	defer cleanup()
	root := t.TempDir()
	for _, rel := range []string{"a.png", "sub/b.png"} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := t.TempDir()
	resp := sendRequest(t, conn, protocol.Request{ID: 1, Method: "convert", Params: mustJSON(map[string]any{
		"files": []string{root}, "preset": "image-to-webp", "output_dir": out, "recursive": true,
	})})
	if resp["error"] != nil {
		t.Fatalf("convert: %#v", resp)
	}
	deadline := time.After(3 * time.Second)
	for {
		for _, want := range []string{"a.webp", "sub/b.webp"} {
			if _, err := os.Stat(filepath.Join(out, want)); err != nil {
				goto retry
			}
		}
		return
	retry:
		select {
		case <-deadline:
			t.Fatal("recursive outputs were not written")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestServerConvertEmptyFolderIsBadRequest(t *testing.T) {
	conn, cleanup := startTestServer(t, &testEngine{})
	defer cleanup()
	resp := sendRequest(t, conn, protocol.Request{ID: 1, Method: "convert", Params: mustJSON(map[string]any{
		"files": []string{t.TempDir()}, "preset": "image-to-webp",
	})})
	if errValue, ok := resp["error"].(map[string]any); !ok || errValue["code"] != "bad_request" {
		t.Fatalf("empty folder: %#v", resp)
	}
}

// stubPDFBackend is a pdfops.Backend for server tests: it reports a fixed page
// count and writes a stub file.
type stubPDFBackend struct {
	pages  int
	avail  error
	failed map[string]error
}

func (s *stubPDFBackend) ID() string       { return "stub-pdf" }
func (s *stubPDFBackend) Available() error { return s.avail }
func (s *stubPDFBackend) PageCount(context.Context, string) (int, error) {
	return s.pages, nil
}
func (s *stubPDFBackend) Merge(_ context.Context, inputs []string, output string) error {
	if err := s.failed[inputs[0]]; err != nil {
		return err
	}
	return os.WriteFile(output, []byte("%PDF-1.7\nstub\n"), 0o600)
}
func (s *stubPDFBackend) Collect(_ context.Context, input, output string, _ []int) error {
	if err := s.failed[input]; err != nil {
		return err
	}
	return os.WriteFile(output, []byte("%PDF-1.7\nstub\n"), 0o600)
}
func (s *stubPDFBackend) Rotate(_ context.Context, input, output string, _ []int, _ int) error {
	if err := s.failed[input]; err != nil {
		return err
	}
	return os.WriteFile(output, []byte("%PDF-1.7\nstub\n"), 0o600)
}

func (s *stubPDFBackend) ImportImages(_ context.Context, _ []string, _ string) error { return nil }

func pdfFixture(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("%PDF-1.7\nsource\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// startOperationServer starts a daemon with a PDF backend wired in and returns
// a connection plus a reader that lives for the connection's lifetime.
func startOperationServer(t *testing.T, backend pdfops.Backend) (net.Conn, *bufio.Reader, func()) {
	t.Helper()
	tb := pdfops.Toolbox{}
	if backend != nil {
		tb.PDFCPU = backend
	}
	return startOperationServerWithToolbox(t, tb)
}

// startOperationServerWithToolbox starts a daemon with a full toolbox, for the
// cases that need image support or a deliberately absent engine.
func startOperationServerWithToolbox(t *testing.T, tb pdfops.Toolbox) (net.Conn, *bufio.Reader, func()) {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "fcd.sock"))
	if err != nil {
		t.Fatal(err)
	}
	manager := jobs.NewManagerWithRegistry(registry.New())
	manager.SetPDFToolbox(tb)
	srv := New(Dependencies{Jobs: manager, Registry: registry.New(), Version: "test", PDFToolbox: tb})
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(listener)
		close(done)
	}()
	conn, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		listener.Close()
		manager.Close()
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	cleanup := func() {
		_ = conn.Close()
		_ = listener.Close()
		<-done
		manager.Close()
	}
	return conn, reader, cleanup
}

// exchange writes a request and reads the next single message back.
func exchange(t *testing.T, conn net.Conn, reader *bufio.Reader, req protocol.Request) map[string]any {
	t.Helper()
	data, err := protocol.EncodeMessage(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func operationsFrom(t *testing.T, response map[string]any) []protocol.Operation {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("operations response: %#v", response)
	}
	raw, ok := result["operations"].([]any)
	if !ok {
		t.Fatalf("operations list missing: %#v", result)
	}
	out := make([]protocol.Operation, 0, len(raw))
	for _, item := range raw {
		entry, _ := item.(map[string]any)
		op := protocol.Operation{}
		op.ID, _ = entry["id"].(string)
		op.Label, _ = entry["label"].(string)
		op.Group, _ = entry["group"].(string)
		op.Mode, _ = entry["mode"].(string)
		op.Engine, _ = entry["engine"].(string)
		op.Enabled, _ = entry["enabled"].(bool)
		op.Reason, _ = entry["reason"].(string)
		out = append(out, op)
	}
	return out
}

func TestServerOperationsForOnePDF(t *testing.T) {
	dir := t.TempDir()
	conn, reader, cleanup := startOperationServer(t, &stubPDFBackend{pages: 4})
	defer cleanup()

	response := exchange(t, conn, reader, protocol.Request{ID: 1, Method: "operations", Params: json.RawMessage(`{"files":["` + pdfFixture(t, dir, "one.pdf") + `"]}`)})
	ops := operationsFrom(t, response)
	if len(ops) < 8 {
		t.Fatalf("operations = %+v, want the organise + fix catalogue entries for one PDF", ops)
	}
	for _, op := range ops {
		want := pdfEngineForID(op.ID)
		if op.Engine != want {
			t.Fatalf("%s engine = %q, want %s", op.ID, op.Engine, want)
		}
	}
	byID := map[string]protocol.Operation{}
	for _, op := range ops {
		byID[op.ID] = op
	}
	merge := byID["pdf-merge"]
	if merge.Enabled {
		t.Fatal("merge must be disabled for a single file")
	}
	if merge.Reason != "needs at least 2 files" {
		t.Fatalf("merge reason = %q", merge.Reason)
	}
	for _, id := range []string{"pdf-extract", "pdf-delete", "pdf-rotate", "pdf-reverse"} {
		if !byID[id].Enabled {
			t.Fatalf("%s should be enabled for one PDF: %+v", id, byID[id])
		}
	}
}

func TestServerOperationsEnablesMergeForTwoPDFs(t *testing.T) {
	dir := t.TempDir()
	first := pdfFixture(t, dir, "a.pdf")
	second := pdfFixture(t, dir, "b.pdf")
	conn, reader, cleanup := startOperationServer(t, &stubPDFBackend{pages: 4})
	defer cleanup()

	response := exchange(t, conn, reader, protocol.Request{ID: 1, Method: "operations", Params: json.RawMessage(`{"files":["` + first + `","` + second + `"]}`)})
	for _, op := range operationsFrom(t, response) {
		if op.ID == "pdf-merge" && !op.Enabled {
			t.Fatalf("merge should be enabled for two PDFs: %+v", op)
		}
	}
}

func TestServerOperationsIsEmptyForNonPDFSelection(t *testing.T) {
	dir := t.TempDir()
	notPDF := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notPDF, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	conn, reader, cleanup := startOperationServer(t, &stubPDFBackend{pages: 4})
	defer cleanup()

	response := exchange(t, conn, reader, protocol.Request{ID: 1, Method: "operations", Params: json.RawMessage(`{"files":["` + notPDF + `"]}`)})
	if response["error"] != nil {
		t.Fatalf("a non-PDF selection should not error: %#v", response)
	}
	if ops := operationsFrom(t, response); len(ops) != 0 {
		t.Fatalf("operations = %+v, want none", ops)
	}
}

func TestServerOperationsReportsUnavailableBackend(t *testing.T) {
	dir := t.TempDir()
	conn, reader, cleanup := startOperationServer(t, &stubPDFBackend{pages: 4, avail: errStubMissing})
	defer cleanup()

	response := exchange(t, conn, reader, protocol.Request{ID: 1, Method: "operations", Params: json.RawMessage(`{"files":["` + pdfFixture(t, dir, "a.pdf") + `"]}`)})
	for _, op := range operationsFrom(t, response) {
		if op.Enabled {
			t.Fatalf("%s should be disabled without a backend: %+v", op.ID, op)
		}
	}
}

var errStubMissing = errors.New("missing backend")

func TestServerRunOperationStartsAJob(t *testing.T) {
	dir := t.TempDir()
	source := pdfFixture(t, dir, "doc.pdf")
	conn, reader, cleanup := startOperationServer(t, &stubPDFBackend{pages: 4})
	defer cleanup()

	params, err := json.Marshal(map[string]any{"files": []string{source}, "op": "pdf-rotate", "params": map[string]any{"angle": 90}})
	if err != nil {
		t.Fatal(err)
	}
	response := exchange(t, conn, reader, protocol.Request{ID: 1, Method: "run_operation", Params: params})
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("run_operation response: %#v", response)
	}
	if _, ok := result["job"].(string); !ok {
		t.Fatalf("run_operation result: %#v", result)
	}
}

func TestServerRunOperationRejectsBadParams(t *testing.T) {
	dir := t.TempDir()
	source := pdfFixture(t, dir, "doc.pdf")
	conn, reader, cleanup := startOperationServer(t, &stubPDFBackend{pages: 4})
	defer cleanup()

	tests := []struct {
		name   string
		params string
	}{
		{name: "missing op", params: `{"files":["` + source + `"]}`},
		{name: "unknown op", params: `{"op":"pdf-nope","files":["` + source + `"]}`},
		{name: "missing pages", params: `{"op":"pdf-extract","files":["` + source + `"]}`},
		{name: "bad angle", params: `{"op":"pdf-rotate","files":["` + source + `"],"params":{"angle":45}}`},
		{name: "one file merge", params: `{"op":"pdf-merge","files":["` + source + `"]}`},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response := exchange(t, conn, reader, protocol.Request{ID: i + 1, Method: "run_operation", Params: json.RawMessage(tc.params)})
			errValue, ok := response["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected an error, got %#v", response)
			}
			if errValue["code"] != "bad_request" {
				t.Fatalf("code = %v, want bad_request", errValue["code"])
			}
		})
	}
}

func TestServerRunOperationSurfacesPasswordRequired(t *testing.T) {
	dir := t.TempDir()
	source := pdfFixture(t, dir, "locked.pdf")
	backend := &stubPDFBackend{pages: 4, failed: map[string]error{source: pdfops.ErrPasswordRequired}}
	conn, reader, cleanup := startOperationServer(t, backend)
	defer cleanup()

	params, err := json.Marshal(map[string]any{"files": []string{source}, "op": "pdf-reverse"})
	if err != nil {
		t.Fatal(err)
	}
	response := exchange(t, conn, reader, protocol.Request{ID: 1, Method: "run_operation", Params: params})
	if response["error"] != nil {
		t.Fatalf("run_operation response: %#v", response)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var message map[string]any
		if err := json.Unmarshal(line, &message); err != nil {
			t.Fatal(err)
		}
		if message["event"] != "job.finished" {
			continue
		}
		data, _ := message["data"].(map[string]any)
		files, _ := data["files"].([]any)
		if len(files) == 0 {
			t.Fatalf("job has no files: %#v", data)
		}
		first, _ := files[0].(map[string]any)
		fileError, _ := first["error"].(map[string]any)
		if fileError["code"] != "password_required" {
			t.Fatalf("file error = %#v, want password_required", fileError)
		}
		return
	}
	t.Fatal("timed out waiting for job.finished")
}

// pdfEngineIDs are the engines the daemon reports alongside the conversion
// registry. They are always listed, installed or not, so a client can tell a
// missing binary apart from an older daemon.
var pdfEngineIDs = []string{"pdfcpu", "qpdf", "ghostscript", "poppler", "tesseract"}

// registryEngineIDs are the conversion engines, which are what the conversion
// tests are actually about.
var registryEngineIDs = map[string]bool{"test": true, "ffmpeg": true}

func countRegistryEngines(engines []any) int {
	count := 0
	for _, raw := range engines {
		if row, ok := raw.(map[string]any); ok {
			if id, _ := row["id"].(string); registryEngineIDs[id] {
				count++
			}
		}
	}
	return count
}

func onlyRegistryEngine(t *testing.T, engines []any) map[string]any {
	t.Helper()
	var found map[string]any
	for _, raw := range engines {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := row["id"].(string); registryEngineIDs[id] {
			if found != nil {
				t.Fatalf("more than one registry engine: %#v", engines)
			}
			found = row
		}
	}
	if found == nil {
		t.Fatalf("no registry engine in %#v", engines)
	}
	return found
}

func TestServerOperationsForImagesListsOnlyImageOperations(t *testing.T) {
	dir := t.TempDir()
	conn, reader, cleanup := startOperationServerWithToolbox(t, pdfops.Toolbox{PDFCPU: &stubPDFBackend{pages: 4}})
	defer cleanup()

	response := exchange(t, conn, reader, protocol.Request{ID: 1, Method: "operations", Params: mustJSON(map[string]any{
		"files": []string{pngFixture(t, dir, "a.png"), pngFixture(t, dir, "b.png")},
	})})
	ops := operationsFrom(t, response)
	for _, op := range ops {
		if op.ID == "pdf-merge" || op.ID == "pdf-reverse" {
			t.Fatalf("PDF operation %s offered for an image selection", op.ID)
		}
	}
}

func TestServerOperationsIsEmptyForMixedSelection(t *testing.T) {
	dir := t.TempDir()
	conn, reader, cleanup := startOperationServer(t, &stubPDFBackend{pages: 4})
	defer cleanup()

	response := exchange(t, conn, reader, protocol.Request{ID: 1, Method: "operations", Params: mustJSON(map[string]any{
		"files": []string{pdfFixture(t, dir, "a.pdf"), pngFixture(t, dir, "b.png")},
	})})
	if ops := operationsFrom(t, response); len(ops) != 0 {
		t.Fatalf("operations = %+v, want none for a mixed selection", ops)
	}
}

func TestServerOperationsIsEmptyForUnrecognisedSelection(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	conn, reader, cleanup := startOperationServer(t, &stubPDFBackend{pages: 4})
	defer cleanup()

	response := exchange(t, conn, reader, protocol.Request{ID: 1, Method: "operations", Params: mustJSON(map[string]any{
		"files": []string{other},
	})})
	if ops := operationsFrom(t, response); len(ops) != 0 {
		t.Fatalf("operations = %+v, want none for an unrecognised selection", ops)
	}
}

func TestServerOperationsReportsMissingEngine(t *testing.T) {
	dir := t.TempDir()
	// No PDF engine at all: every operation is disabled with a reason rather
	// than the request failing.
	conn, reader, cleanup := startOperationServerWithToolbox(t, pdfops.Toolbox{})
	defer cleanup()

	response := exchange(t, conn, reader, protocol.Request{ID: 1, Method: "operations", Params: mustJSON(map[string]any{
		"files": []string{pdfFixture(t, dir, "a.pdf")},
	})})
	ops := operationsFrom(t, response)
	if len(ops) == 0 {
		t.Fatal("operations should still list the catalogue when the engine is missing")
	}
	for _, op := range ops {
		if op.Enabled {
			t.Fatalf("%s enabled without a PDF engine", op.ID)
		}
		if op.Reason == "" {
			t.Fatalf("%s disabled with no reason", op.ID)
		}
	}
}

func TestServerEnginesListsEveryPDFEngine(t *testing.T) {
	conn, reader, cleanup := startOperationServer(t, &stubPDFBackend{pages: 4})
	defer cleanup()

	response := exchange(t, conn, reader, protocol.Request{ID: 1, Method: "engines", Params: json.RawMessage(`{}`)})
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("engines response: %#v", response)
	}
	raw, _ := result["engines"].([]any)
	seen := map[string]bool{}
	for _, item := range raw {
		if row, ok := item.(map[string]any); ok {
			if id, _ := row["id"].(string); registryEngineIDs[id] {
				continue
			} else {
				seen[id] = true
			}
		}
	}
	for _, id := range pdfEngineIDs {
		if !seen[id] {
			t.Fatalf("engines = %#v, missing %s", raw, id)
		}
	}
}

// pngFixture writes a real one-pixel PNG, which is what formats.Detect needs to
// classify the selection as images.
func pngFixture(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// pdfEngineForID reads the engine straight from the catalogue, so this assertion
// cannot drift when an operation's engine is reassigned.
func pdfEngineForID(id string) string {
	op, ok := pdfops.Lookup(id)
	if !ok {
		return ""
	}
	return op.EngineID()
}

// TestServerOperationsListsThePhase2Catalogue pins the operations a single PDF
// selection is offered, so a catalogue change shows up as a diff here rather
// than as a surprise in the file manager.
func TestServerOperationsListsThePhase2Catalogue(t *testing.T) {
	dir := t.TempDir()
	conn, reader, cleanup := startOperationServerWithToolbox(t, pdfops.Toolbox{PDFCPU: &stubPDFBackend{pages: 4}})
	defer cleanup()

	response := exchange(t, conn, reader, protocol.Request{ID: 1, Method: "operations", Params: mustJSON(map[string]any{
		"files": []string{pdfFixture(t, dir, "a.pdf")},
	})})
	byID := map[string]protocol.Operation{}
	for _, op := range operationsFrom(t, response) {
		byID[op.ID] = op
	}
	// Engines that are absent still appear, disabled with a reason, so a user
	// can see the tool exists and why it is unavailable.
	for _, want := range []string{
		"pdf-repair", "pdf-linearize", "pdf-flatten",
		"pdf-compress-light", "pdf-compress-balanced", "pdf-compress-strong", "pdf-grayscale",
		"pdf-to-text", "pdf-extract-images", "pdf-ocr",
	} {
		op, ok := byID[want]
		if !ok {
			t.Errorf("operations missing %s; got %v", want, keysOf(byID))
			continue
		}
		if op.Enabled {
			t.Errorf("%s enabled with no external engine in the toolbox", want)
		}
		if op.Reason == "" {
			t.Errorf("%s disabled with no reason", want)
		}
	}
	// pdfcpu is linked in, so its operations are genuinely available.
	for _, want := range []string{"pdf-rotate-cw", "pdf-rotate-180"} {
		if op, ok := byID[want]; !ok || !op.Enabled {
			t.Errorf("%s = %+v, want enabled", want, op)
		}
	}
}

func keysOf(m map[string]protocol.Operation) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
