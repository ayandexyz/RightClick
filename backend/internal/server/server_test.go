package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"file-converter/backend/internal/formats"
	"file-converter/backend/internal/jobs"
	"file-converter/backend/internal/protocol"
	"file-converter/backend/internal/registry"
)

type testEngine struct{ block bool }

func (e *testEngine) ID() string                 { return "test" }
func (e *testEngine) Available() (string, error) { return "test", nil }
func (e *testEngine) Supports(source, target formats.Format) bool {
	return source != target && source != "" && target != ""
}
func (e *testEngine) Convert(ctx context.Context, _ string, _ formats.Format, destination string, _ formats.Format) error {
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
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "fcd.sock"))
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	reg.Register(engine)
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
