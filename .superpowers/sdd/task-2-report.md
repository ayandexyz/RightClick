# Task 2 Implementation Report: FFmpeg Engine and Probe Model

## Scope

Implemented the dedicated FFmpeg engine and probe model requested for Task 2, plus the minimum daemon registration required to make the engine reachable from the real application path. Existing Task 1 format/protocol work and later media job/target integration were otherwise left unchanged.

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
- Safe path checks using absolute paths and unsafe basename rules.
- Deterministic command construction using individual arguments, `-y`, `-f <target>`, fixed video container defaults, `-vn` for audio outputs, and `--` before the destination path.
- Process-group execution with `Setpgid: true` and process-group termination on context cancellation.

Updated `backend/cmd/fcd/main.go` so the daemon registers both ImageMagick and FFmpeg independently. FFmpeg availability remains independently reported through the registry status path, so a missing FFmpeg binary does not prevent image engine registration.

Added a daemon regression test in `backend/cmd/fcd/main_test.go` proving the runtime engine registry includes FFmpeg.

## TDD evidence

1. Wrote the focused FFmpeg tests first.
2. Ran `go test ./internal/engine -run FFmpeg -v` before implementation and observed the expected missing-type/parser build failure.
3. Implemented the minimal engine and parser, then verified focused tests passed.
4. Added all output-route coverage and verified it passed.
5. After the reachability review identified missing daemon registration, wrote `TestEngineRegistryIncludesFFmpeg` first and observed the expected `undefined: newEngineRegistry` build failure.
6. Added the smallest registry setup helper and FFmpeg registration, then verified the new test passed.

## Verification

Focused engine and daemon tests:

```text
go test ./cmd/fcd ./internal/engine
ok
```

Focused vet:

```text
go vet ./cmd/fcd ./internal/engine
passed
```

Full backend suite:

```text
go test ./...
ok for all backend packages
```

`git diff --check` passed.

## Files changed for the complete Task 2 implementation

- `backend/internal/engine/ffmpeg.go`
- `backend/internal/engine/ffmpeg_test.go`
- `backend/internal/formats/images.go`
- `backend/cmd/fcd/main.go`
- `backend/cmd/fcd/main_test.go`
- `.superpowers/sdd/task-2-report.md`

The daemon files were changed in the earlier registration commit (`3498ac9`), not in the review-fix commit (`23a8752`). The review-fix commit changed `ffmpeg.go`, `ffmpeg_test.go`, `formats/images.go`, and this report. This distinction is intentional so the report does not attribute daemon changes to the later fix commit.

## Concerns / boundaries

- Registry target discovery, media input detection in jobs, output planning, server media behavior, CLI media presets, and end-to-end FFmpeg conversion remain later staged work.
- The daemon now exposes FFmpeg in its runtime registry, but media routes will become fully usable only after those later integration tasks are completed.
- Fixed codec defaults are included only for video containers and WebM. Detailed codec/bitrate/progress options remain out of scope.

## Review Fix Report

Addressed the Task 2 review findings as follows:

1. **Context-bound, cancellable probing.** `Probe` now accepts the caller's `context.Context`; injected probes receive that exact context instead of `context.Background()`. Real ffprobe execution uses the same process-group cancellation model as ffmpeg, including `Setpgid` and group termination when the context is canceled.
2. **Canonical path validation.** Added `formats.ValidatePath`, which reuses the existing absolute-path and unsafe-basename rules without requiring the path to exist. FFmpeg probing and conversion now call this shared helper rather than maintaining a divergent unsafe-path implementation.
3. **Usable availability.** `FFmpeg.Available` now requires both `ffmpeg` and `ffprobe`, so registry status and resolution do not advertise an engine that cannot probe media. Tests cover missing ffprobe after a successful ffmpeg lookup. Existing daemon registration remains because it is required for runtime reachability, and the daemon test still verifies registration.
4. **Expanded conversion coverage.** Tests now cover every video and audio target route, all media/image rejection combinations, exact argument ordering, `--` placement, fixed video defaults, `-vn` for audio targets, and the absence of video defaults during audio extraction.
5. **Verification.** `go test ./internal/engine ./cmd/fcd`, `go test ./internal/engine -run FFmpeg -v`, and the full backend `go test ./...` suite all pass. `git diff --check` also passes.
