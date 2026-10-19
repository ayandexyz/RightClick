# File Converter Go Backend: Image Vertical Slice

- **Date:** 2026-10-19
- **Status:** Approved for implementation planning

## Goal

Build the first usable backend slice for `file-converter`: a local Go daemon named `fcd` that discovers PNG/JPEG/WebP targets, converts images asynchronously, reports job progress, and supports cancellation. The Omarchy UI will consume this backend after the protocol is exercised independently.

## Scope

### Included

- Go module and `fcd` executable under `backend/`.
- Linux Unix-domain socket transport at `$XDG_RUNTIME_DIR/file-converter/fcd.sock`.
- NDJSON request/response/event messages described by protocol v1 in `docs/PROTOCOL.md`.
- `hello`, `targets`, `convert`, and `cancel` methods.
- Image input detection for PNG, JPEG, and WebP.
- Target discovery for PNG, JPEG, and WebP in fixed order (PNG, JPEG, WebP), excluding the detected source format. When files of different formats are requested together, a target is offered only if it is valid for every file.
- Asynchronous jobs with queued/running/completed/completed_with_errors/failed/cancelled states.
- Per-file progress and final output paths.
- Cancellation using Go contexts and process termination.
- External ImageMagick execution, with an explicit unsupported/internal error if the required executable is unavailable or conversion fails.
- A small `fcctl` development client or equivalent test harness for exercising the daemon protocol.
- Unit and integration tests for protocol handling, target discovery, output naming, errors, and cancellation where the environment permits.

### Excluded from this slice

- Omarchy radial UI, Hyprland integration, and file-manager actions.
- Ubuntu and Windows frontends.
- Windows named-pipe transport.
- Documents, audio, video, archives, multi-hop conversion, and settings UI.
- Cloud services, telemetry, accounts, and network access.

## Architecture

```text
fcctl / future frontend
          │ NDJSON over Unix socket
          ▼
       fcd daemon
       ├── protocol: decode requests, encode responses/events
       ├── transport: Unix listener and per-client connections
       ├── targets: detect image formats and enumerate targets
       ├── jobs: queue, lifecycle, cancellation, event fan-out
       └── engines: ImageMagick subprocess adapter
```

The protocol package owns stable wire types and validation. The transport package owns socket lifecycle and connection I/O. The job manager owns asynchronous execution and state transitions. The image engine owns executable discovery, command construction, and subprocess execution. No frontend performs conversion logic.

## Data flow

1. A client connects to the socket and sends `hello` with protocol `1`.
2. `targets` validates the requested paths, detects supported image formats, and returns ordered image targets.
3. `convert` validates the preset, output directory, and conflict policy, creates a job, and immediately returns its ID.
4. The job manager runs each file through the image engine, emitting throttled `job.updated` events and exactly one `job.finished` event to the originating connection.
5. `cancel` cancels the active subprocess context and marks every file that has not finished as `cancelled`. Files already `done` stay `done`, and the job ends `cancelled`. The final event reports the file states and job state.
6. On startup the daemon checks the socket path: if a daemon answers on it, the new process exits with an error; if the connection is refused, the stale socket is removed. The socket is removed on clean shutdown.

## Initial presets

The first implementation exposes stable preset IDs for direct image conversion:

- `image-to-png`
- `image-to-jpeg`
- `image-to-webp`

The target response maps these presets to labels, formats, and image category.

## Output files

- Output goes to `output_dir` when given, otherwise next to the source file. The source file is never modified or overwritten.
- The output name is the source basename with the target extension: `png`, `jpg` (for JPEG), `webp`.
- Conflict handling follows the protocol: `rename` is the default, with `skip` and `overwrite` supported. `rename` appends ` (1)`, ` (2)`, and so on before the extension (`photo (1).png`) and uses the first unused name.
- The engine writes to a temporary file in the destination directory and renames it into place only on success. On failure or cancellation the temporary file is deleted, so no partial output is left. `overwrite` replaces the destination only at that final rename.
- A file skipped by `on_conflict: skip` has state `skipped` and no outputs.

## Image conversion rules

- Transparent sources (PNG, WebP) converted to JPEG are flattened onto a white background.
- EXIF orientation is applied to the pixels before output, and the orientation tag is cleared.
- Multi-frame inputs (animated WebP) use the first frame only.
- ImageMagick reports no progress, so a file's progress is `0` while running and `1` when done; job progress is the average over files.

## Security

The engine never builds a shell string; it runs `magick` (or `convert` for ImageMagick 6) with an argument list.

- Inputs are validated as existing regular files with absolute paths, and the format is detected from content, not extension alone.
- Inputs are passed with an explicit format prefix (`png:/abs/path`, `jpeg:/abs/path`, `webp:/abs/path`) so ImageMagick's own sniffing cannot select another coder. Paths containing characters ImageMagick treats as syntax (`[`, `]`, a leading `@`, or a `:` in the basename) are rejected with `bad_request`, or addressed through a safe form that disables that syntax.
- Every invocation sets resource limits (`-limit memory`, `map`, `disk`, `time`) with documented defaults.
- The socket directory is created with mode `0700` and the socket with mode `0600`.

## Concurrency and client lifecycle

- A job's files are converted by a bounded worker pool, capped at the number of CPU cores. Jobs run one at a time in submission order; later jobs stay `queued`.
- If a client disconnects while its job is running, the job continues to completion. Its events are dropped, and the job's outputs are still written.

## Error handling

Wire errors use the existing protocol codes: `bad_request`, `not_found`, `unsupported`, `cancelled`, `internal`, and `busy` where applicable. User-facing messages are generated by the backend, while codes remain stable for frontend behavior. Unsupported formats and unavailable ImageMagick are reported without crashing the daemon. A failed file does not prevent remaining files in the same job from being attempted. The job's final state is decided from the file states:

- `cancelled` when `cancel` was requested before the job finished.
- `completed` when no file failed (every file is `done` or `skipped`, including a job where all files are `skipped`).
- `completed_with_errors` when at least one file is `done` and at least one is `failed`.
- `failed` when at least one file failed and none is `done`.

Paths are treated as absolute native paths as required by protocol v1. The backend does not upload or transmit file contents.

## Testing strategy

- Protocol unit tests: request decoding, response/error encoding, unknown methods, malformed JSON, and hello negotiation.
- Target tests: PNG/JPEG/WebP detection, unsupported extensions/content, and target ordering.
- Job tests: state transitions, event ordering, output naming, conflict policies, per-file failure isolation, and cancellation.
- Engine integration tests: run only when ImageMagick is installed; otherwise skip cleanly and retain command/error coverage through a fake executable or injected runner.
- End-to-end daemon test: start a temporary socket, issue hello/targets/convert, and inspect the produced image and final event.

## Distribution constraints

The daemon must remain a single Go binary with no network dependency. The first Linux implementation may depend on a system ImageMagick installation; engine discovery and the missing-tool error must be explicit so packaging can later choose whether to bundle or declare the dependency. Windows support will introduce a named-pipe adapter without changing protocol or job packages.

## Acceptance criteria

- `fcd` starts and listens on the documented Linux socket.
- A protocol client can negotiate version 1 and receive available engine information.
- PNG/JPEG/WebP files return valid target lists.
- A supported conversion returns a job ID, produces the requested output, and emits progress plus one terminal event.
- Cancellation terminates an active job and reports `cancelled` rather than hanging.
- Invalid paths, unsupported formats, conflicts, and missing conversion tools return stable protocol errors.
- Cancelled or failed conversions leave no partial or temporary output files.
- A second `fcd` started while one is running exits with an error and leaves the first daemon's socket intact; a stale socket from a crashed daemon is replaced.
- Paths ImageMagick would interpret as syntax are rejected, and inputs always reach ImageMagick with an explicit format prefix.
- A job where all files are skipped ends `completed`.
- Tests pass without requiring the Omarchy desktop session.
