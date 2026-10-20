# Task 2 Implementation Report: FFmpeg Engine and Probe Model

## Scope

Implemented only the dedicated FFmpeg engine and probe model requested for Task 2. Existing Task 1 format and protocol work was left unchanged, and later registry/daemon/job wiring was not added.

## Implementation

Created `backend/internal/engine/ffmpeg.go` with:

- `MediaKind`, `MediaAudio`, and `MediaVideo`.
- Injectable `Probe` callback matching the required ffprobe seam.
- `FFmpeg` implementing the existing `registry.Engine` contract and `NewFFmpeg` constructor.
- Independent `ffmpeg` availability lookup with a stable missing-binary error.
- ffprobe execution using `-v error -show_entries stream=codec_type -of json -- <path>`.
- JSON stream classification where any video stream takes precedence, otherwise audio streams classify the input as audio.
- Media-only route support for all Task 1 video/audio formats.
- Rejection of audio-to-video and image/media routes.
- Safe path checks using absolute paths and the existing unsafe basename rules.
- Deterministic command construction using individual arguments, `-y`, `-f <target>`, fixed video container defaults, `-vn` for audio outputs, and `--` before the destination path.
- Process-group execution with `Setpgid: true` and process-group termination on context cancellation.

Created `backend/internal/engine/ffmpeg_test.go` with TDD coverage for:

- Audio-only, video-only, and mixed stream JSON classification.
- Invalid and unsupported probe responses.
- Exact ffprobe argument construction through the injected probe.
- Supported and rejected conversion routes.
- All twelve media output routes and target format selection.
- `-vn` audio extraction behavior.
- Unsafe path and audio-to-video rejection.
- Missing FFmpeg availability reporting.

## TDD evidence

1. Wrote the focused FFmpeg tests first.
2. Ran `go test ./internal/engine -run FFmpeg -v` before implementation and observed the expected build failure because the FFmpeg types and parser did not exist.
3. Implemented the minimal engine and parser.
4. Re-ran the focused tests successfully.
5. Added the complete output-route table coverage and re-ran the focused tests successfully.

## Verification

Focused engine tests:

```text
go test ./internal/engine -run FFmpeg -v
PASS
```

Full engine package:

```text
go test ./internal/engine -v
PASS
```

Full backend suite:

```text
go test ./...
```

Most packages passed, but the pre-existing `internal/server.TestServerCancelStopsActiveJob` timed out after 10 minutes. The stack trace shows it blocked in the existing test engine/job manager cancellation path, not in the new FFmpeg tests. The full suite therefore did not complete successfully.

`git diff --check` passed.

`go vet ./...` was started as part of verification, but the full verification command sequence was interrupted by the 10-minute full-suite timeout before a usable vet result was available.

## Files changed

- `backend/internal/engine/ffmpeg.go`
- `backend/internal/engine/ffmpeg_test.go`
- `.superpowers/sdd/task-2-report.md`

## Concerns

- This task intentionally does not register FFmpeg with the daemon or modify registry target discovery, job output planning, or server behavior. Those belong to later staged tasks.
- The full backend suite has an existing cancellation timeout in `internal/server`; this remains unresolved and prevents claiming a fully green repository-wide test run.
- Fixed codec defaults are included only for video containers and WebM, as required by the dedicated engine scope. Detailed codec/bitrate/progress options remain out of scope.
