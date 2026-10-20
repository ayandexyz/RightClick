package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"file-converter/backend/internal/engine"
	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/jobs"
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
	if !ok || len(engines) != 2 {
		t.Fatalf("hello engines: %#v", result["engines"])
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
	if !ok || len(engines) != 1 {
		t.Fatalf("engines: %#v", result["engines"])
	}
	entry := engines[0].(map[string]any)
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
