# Go Image Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a tested Go `fcd` daemon that serves protocol v1 over a Linux Unix socket and converts PNG/JPEG/WebP files asynchronously through ImageMagick.

**Architecture:** Keep wire types and validation in `internal/protocol`, Unix socket lifecycle and NDJSON connection handling in `internal/transport`, format detection and target intersection in `internal/formats`, job sequencing/worker execution/events in `internal/jobs`, and ImageMagick process execution in `internal/engine`. `cmd/fcd` wires these packages together; `cmd/fcctl` provides a development client.

**Tech Stack:** Go, standard library only, Unix domain sockets, newline-delimited JSON, ImageMagick `magick`/`convert` subprocesses, Go `testing` package.

## Global Constraints

- The daemon is a single Go binary with no network dependency.
- Protocol version is `1`, and wire messages follow `docs/PROTOCOL.md`.
- Linux socket path is `$XDG_RUNTIME_DIR/file-converter/fcd.sock`.
- The socket directory is mode `0700`; the socket is mode `0600`.
- Jobs run one at a time in submission order; each job uses a bounded worker pool capped at CPU count.
- Client disconnects do not cancel jobs; events are dropped after disconnect while outputs continue.
- Inputs must be absolute regular files, and PNG/JPEG/WebP are detected from content rather than extension alone.
- ImageMagick commands use argument arrays, never shell strings, and explicit input format prefixes.
- Inputs containing ImageMagick syntax characters `[`, `]`, a leading `@`, or a `:` in the basename are rejected with `bad_request`.
- Every ImageMagick invocation sets memory, map, disk, and time limits.
- Conversion writes a temporary file in the destination directory and atomically renames it only after success.
- `rename` is the default conflict policy and uses ` (1)`, ` (2)`, etc.; `skip` produces no output; `overwrite` replaces only at final rename.
- Transparent PNG/WebP input converted to JPEG is flattened onto white; EXIF orientation is applied and cleared; animated WebP uses the first frame.
- Final state rules: cancellation wins; no failures means `completed`; mixed done/failed means `completed_with_errors`; failures with no done files means `failed`.
- Tests must run without an Omarchy desktop session; ImageMagick integration tests skip cleanly when the tool is unavailable.

---

### Task 1: Initialize the Go module and protocol wire types

**Files:**
- Create: `backend/go.mod`
- Create: `backend/internal/protocol/types.go`
- Create: `backend/internal/protocol/codec.go`
- Create: `backend/internal/protocol/validate.go`
- Test: `backend/internal/protocol/codec_test.go`
- Test: `backend/internal/protocol/validate_test.go`

**Interfaces:**
- Produces `protocol.Request`, `protocol.Response`, `protocol.Event`, `protocol.Error`, `protocol.Target`, `protocol.Job`, `protocol.FileJob`, and `protocol.FileError`.
- Produces `protocol.DecodeRequest([]byte) (Request, error)` and `protocol.EncodeMessage(any) ([]byte, error)`.
- Produces `protocol.ValidateRequest(Request) *Error` and `protocol.SupportedPresets() []Target`.

- [ ] **Step 1: Create the Go module**

Use module path `file-converter/backend` and a current Go language version supported by the installed toolchain:

```bash
cd backend
go mod init file-converter/backend
```

Keep dependencies at zero for this first slice.

- [ ] **Step 2: Write failing codec tests**

Cover request decoding, response encoding, event encoding, malformed JSON, and preservation of unknown fields without requiring them in typed structs:

```go
func TestDecodeRequest(t *testing.T) {
    got, err := DecodeRequest([]byte(`{"id":1,"method":"hello","params":{"client":"fcctl","protocol":1}}`))
    if err != nil { t.Fatal(err) }
    if got.ID != 1 || got.Method != "hello" { t.Fatalf("unexpected request: %+v", got) }
}

func TestEncodeResponseAndEvent(t *testing.T) {
    response, err := EncodeMessage(Response{ID: 1, Result: map[string]any{"ok": true}})
    if err != nil { t.Fatal(err) }
    if string(response) != `{"id":1,"result":{"ok":true}}\n` { t.Fatalf("%q", response) }

    event, err := EncodeMessage(Event{Name: "job.finished", Data: map[string]any{"id": "j1"}})
    if err != nil { t.Fatal(err) }
    if string(event) != `{"event":"job.finished","data":{"id":"j1"}}\n` { t.Fatalf("%q", event) }
}

func TestDecodeMalformedJSON(t *testing.T) {
    if _, err := DecodeRequest([]byte(`{"id":`)); err == nil { t.Fatal("expected error") }
}
```

- [ ] **Step 3: Run protocol tests and verify they fail**

Run:

```bash
cd backend
go test ./internal/protocol
```

Expected: compilation failures because the protocol types and functions do not exist.

- [ ] **Step 4: Implement wire types and codec**

Use JSON tags matching `docs/PROTOCOL.md`. Model request params as `json.RawMessage`; method-specific decoding belongs to the server layer. Encode every message with one trailing newline. Reject empty or non-object request lines.

- [ ] **Step 5: Add validation tests and implementation**

Validate protocol negotiation, known methods, required params, absolute file paths, presets, and conflict policies. Return stable error codes rather than Go error strings. Test that a higher requested protocol produces a `bad_request` error and that invalid conflict policies are rejected.

- [ ] **Step 6: Run tests and commit**

```bash
cd backend
gofmt -w internal/protocol
go test ./internal/protocol
cd ..
git add backend
git commit -m "feat: add backend protocol types"
```

Expected: all protocol tests pass.

---

### Task 2: Add image format detection and target intersection

**Files:**
- Create: `backend/internal/formats/images.go`
- Create: `backend/internal/formats/images_test.go`

**Interfaces:**
- Produces `formats.Detect(path string) (Format, error)`.
- Produces `formats.Targets(paths []string) ([]protocol.Target, error)`.
- Produces `formats.PresetFormat(preset string) (Format, bool)`.

- [ ] **Step 1: Write detection and target tests**

Create temporary files with PNG, JPEG, and WebP signatures, then assert detection is content-based. Include an extension/content mismatch, unsupported content, a nonexistent path, and mixed-file target intersection. Assert fixed order `PNG, JPEG, WebP` and exclusion of the source format:

```go
func TestTargetsUseIntersectionAndFixedOrder(t *testing.T) {
    png := writeFixture(t, "png", pngBytes)
    jpeg := writeFixture(t, "jpg", jpegBytes)
    targets, err := Targets([]string{png, jpeg})
    if err != nil { t.Fatal(err) }
    got := targetFormats(targets)
    want := []string{"webp"}
    if !reflect.DeepEqual(got, want) { t.Fatalf("got %v want %v", got, want) }
}
```

- [ ] **Step 2: Run tests and verify they fail**

```bash
cd backend
go test ./internal/formats
```

Expected: missing package symbols or compilation failures.

- [ ] **Step 3: Implement content sniffing and target calculation**

Read only the minimum header bytes needed for PNG, JPEG, and RIFF/WEBP detection. Require absolute regular files before opening. For multiple inputs, compute the intersection of valid target formats, then emit the fixed order and preset metadata from `protocol.SupportedPresets()`.

- [ ] **Step 4: Run tests and commit**

```bash
cd backend
gofmt -w internal/formats
go test ./internal/formats
cd ..
git add backend/internal/formats
git commit -m "feat: detect image formats and targets"
```

Expected: all format tests pass.

---

### Task 3: Implement secure ImageMagick engine and output planning

**Files:**
- Create: `backend/internal/engine/imagemagick.go`
- Create: `backend/internal/engine/output.go`
- Test: `backend/internal/engine/imagemagick_test.go`
- Test: `backend/internal/engine/output_test.go`

**Interfaces:**
- Produces `engine.NewImageMagick(runner Runner) *ImageMagick`.
- Produces `ImageMagick.Available() (string, error)`.
- Produces `ImageMagick.Convert(ctx context.Context, source, sourceFormat, destination, targetFormat string) error`.
- Produces `engine.PlanOutput(dir, source, targetExt string, conflict protocol.ConflictPolicy) (OutputPlan, error)`.
- Produces `OutputPlan.Commit() (string, error)` and `OutputPlan.RemoveTemp()`.
- `Runner` is injectable and accepts `(context.Context, executable string, args []string) error` for command testing.

- [ ] **Step 1: Write output-plan tests**

Cover default sibling output, explicit output directory, `rename` suffixes, `skip`, `overwrite`, and rejection of paths with ImageMagick syntax characters. Assert the source is never selected as the output and that temporary files are created in the destination directory.

- [ ] **Step 2: Run output tests and verify they fail**

```bash
cd backend
go test ./internal/engine -run 'TestOutput|TestPlan'
```

Expected: compilation failures.

- [ ] **Step 3: Implement output planning**

Validate destination directories, calculate `png`, `jpg`, or `webp` extension, select the first free renamed path for `rename`, and use an exclusive temporary file in the destination directory. For `overwrite`, retain the destination until the successful final rename. Ensure cleanup is safe on every failure path.

- [ ] **Step 4: Write command-construction tests**

Use a fake runner to capture executable and arguments. Assert executable discovery prefers `magick`, falls back to `convert`, never invokes a shell, passes explicit source prefix (`png:...`, `jpeg:...`, `webp:...`), applies `-auto-orient`, uses `-background white -alpha remove -alpha off` for JPEG, selects first WebP frame, writes to the temporary destination, and includes all four resource limits.

- [ ] **Step 5: Implement ImageMagick execution**

Discover `magick` then `convert` with `exec.LookPath`. Build an argument slice only. Use `exec.CommandContext` in the default runner so cancellation terminates the child. Map missing executable to `unsupported`, nonzero command exit to `internal`, and context cancellation to `cancelled`. Use the format prefix on every input and reject unsafe basenames before command construction.

- [ ] **Step 6: Run engine tests and commit**

```bash
cd backend
gofmt -w internal/engine
go test ./internal/engine
cd ..
git add backend/internal/engine
git commit -m "feat: add secure imagemagick image engine"
```

Expected: command and output tests pass; no external ImageMagick installation is required for fake-runner tests.

---

### Task 4: Build the sequential job manager with cancellation and events

**Files:**
- Create: `backend/internal/jobs/manager.go`
- Create: `backend/internal/jobs/manager_test.go`

**Interfaces:**
- Produces `jobs.Manager` with `Submit(ctx SubmitRequest) (string, error)`, `Cancel(id string) error`, and `Close()`.
- `SubmitRequest` contains `Files []string`, `Preset string`, `OutputDir string`, `Conflict protocol.ConflictPolicy`, and `Events func(protocol.Event)`.
- The manager consumes the engine interface from Task 3 and `formats.Detect`.

- [ ] **Step 1: Write manager tests**

Use a fake engine and event collector. Cover queued-to-running transitions, one-job-at-a-time submission order, bounded per-job parallelism capped to CPU count, average progress, output paths, per-file failure isolation, all-skipped completion, mixed done/failed completion, cancellation before and during execution, exactly one `job.finished`, and event dropping by replacing the callback with a no-op after simulated disconnect.

- [ ] **Step 2: Run manager tests and verify they fail**

```bash
cd backend
go test ./internal/jobs
```

Expected: compilation failures.

- [ ] **Step 3: Implement manager state and queue**

Use a single dispatcher goroutine and FIFO job queue. The dispatcher starts only one job at a time. Each job uses a worker pool with `min(len(files), runtime.NumCPU())` workers. Guard mutable job/file state with a mutex or confine updates to the manager goroutine; do not emit events while holding a lock.

- [ ] **Step 4: Implement cancellation and terminal-state rules**

Store a cancel function per active job. On cancel, cancel the context, mark pending files cancelled, allow completed files to remain done, remove temporary outputs, and force final job state `cancelled`. For normal completion, calculate state exactly from the spec. Emit throttled `job.updated` events at no more than 10 per second per job and exactly one terminal `job.finished` event.

- [ ] **Step 5: Run tests and commit**

```bash
cd backend
gofmt -w internal/jobs
go test ./internal/jobs
cd ..
git add backend/internal/jobs
git commit -m "feat: add queued cancellable conversion jobs"
```

Expected: all job lifecycle tests pass.

---

### Task 5: Add Unix socket transport and daemon request handling

**Files:**
- Create: `backend/internal/transport/unix.go`
- Create: `backend/internal/server/server.go`
- Create: `backend/internal/server/server_test.go`

**Interfaces:**
- Produces `transport.Listen(path string) (net.Listener, error)` and `transport.PrepareSocket(path string) (cleanup func(), err error)`.
- Produces `server.New(deps Dependencies) *Server` and `Server.Serve(listener net.Listener) error`.
- `Dependencies` contains the format service, job manager, engine availability/version, and a shutdown callback.

- [ ] **Step 1: Write socket lifecycle tests**

Use temporary `XDG_RUNTIME_DIR` directories. Test directory/socket modes, refusing a live daemon while preserving its socket, replacing a stale socket after connection refusal, and removing the socket during clean shutdown.

- [ ] **Step 2: Write server protocol tests**

Connect with `net.Dial("unix", path)` and send NDJSON lines. Test hello success and higher protocol rejection, targets, convert immediate job response plus events, cancel, unknown method, malformed JSON, invalid params, and a client disconnect followed by successful job completion.

- [ ] **Step 3: Run server tests and verify they fail**

```bash
cd backend
go test ./internal/transport ./internal/server
```

Expected: compilation failures.

- [ ] **Step 4: Implement socket preparation**

Create the parent directory with `0700`; if the socket exists, attempt a short connection. If it succeeds, return an error and do not unlink it. If it fails with connection refused or the path is not a socket, remove the stale entry and listen. Apply `0600` to the socket and return cleanup that closes the listener and unlinks only the path owned by this process.

- [ ] **Step 5: Implement per-connection NDJSON handling**

Use a buffered scanner with a bounded maximum line size. Decode each request, dispatch `hello`, `targets`, `convert`, and `cancel`, and write one response line. For convert jobs, register an event callback synchronized to the connection writer. On write failure, detach the callback without cancelling the job. Return protocol errors for malformed or invalid requests and keep the daemon alive.

- [ ] **Step 6: Run tests and commit**

```bash
cd backend
gofmt -w internal/transport internal/server
go test ./internal/transport ./internal/server
cd ..
git add backend/internal/transport backend/internal/server
git commit -m "feat: serve protocol over unix socket"
```

Expected: all transport and server tests pass.

---

### Task 6: Wire `fcd`, add `fcctl`, and document local usage

**Files:**
- Create: `backend/cmd/fcd/main.go`
- Create: `backend/cmd/fcctl/main.go`
- Modify: `README.md`
- Modify: `docs/PROTOCOL.md` only if implementation clarifies an existing draft detail
- Test: `backend/e2e_test.go`

**Interfaces:**
- `fcd` accepts `serve` and optional `--idle-exit duration`, defaulting to 300 seconds and `0` meaning never.
- `fcctl` supports `hello`, `targets FILE...`, `convert FILE... --preset PRESET [--output-dir DIR] [--on-conflict POLICY]`, and `cancel JOB`.

- [ ] **Step 1: Add daemon wiring**

Construct the ImageMagick engine, format service, job manager, and server. Resolve the socket path from `$XDG_RUNTIME_DIR`, falling back to a safe runtime directory error if unset rather than writing into the project. Handle SIGINT/SIGTERM by stopping intake, waiting for jobs, and cleaning the socket.

- [ ] **Step 2: Add the development client**

Use the same socket resolver and NDJSON codec. Print responses and events as JSON lines so it is useful for manual protocol inspection. Do not add a separate conversion implementation.

- [ ] **Step 3: Write the end-to-end test**

Create real temporary PNG/JPEG/WebP fixtures, start the server on a temporary Unix socket with a fake or installed engine, send `hello`, `targets`, and `convert`, wait for `job.finished`, and assert the output exists and can be detected as the target format. Add a cancellation case with a blocking fake engine and assert no final output or temporary file remains.

- [ ] **Step 4: Run full verification**

```bash
cd backend
gofmt -w cmd e2e_test.go
go test ./...
go vet ./...
go build ./cmd/fcd ./cmd/fcctl
```

Expected: all tests pass, vet is clean, and both binaries build. ImageMagick-dependent tests may report `SKIP` when neither `magick` nor `convert` is installed.

- [ ] **Step 5: Update usage documentation**

Add build/run examples, the required ImageMagick dependency, socket location, `fcctl` examples, supported presets, and the fact that the daemon never uploads files. Preserve the existing roadmap and make clear that only the Linux backend vertical slice is implemented.

- [ ] **Step 6: Commit the integrated slice**

```bash
git add backend README.md docs/PROTOCOL.md
git commit -m "feat: add fcd image conversion daemon"
```

---

## Self-review checklist

- **Spec coverage:** protocol and wire behavior are covered in Tasks 1 and 5; target intersection in Task 2; secure ImageMagick invocation and output atomicity in Task 3; job order, worker cap, progress, cancellation, disconnect behavior, and final states in Task 4; stale/live socket behavior in Task 5; binaries, idle exit, and end-to-end verification in Task 6.
- **Placeholder scan:** no task relies on TBD, TODO, “appropriate handling,” or an unspecified neighboring interface.
- **Type consistency:** `protocol` types are introduced before formats/jobs/server consume them; `engine` interfaces are injected into jobs; server dependencies are wired only after all lower-level contracts exist.
- **Scope:** this plan covers only the approved Linux PNG/JPEG/WebP backend slice. Omarchy, Ubuntu, Windows, documents, media, and packaging remain out of scope.
