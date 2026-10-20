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

## Files changed

- `backend/internal/engine/ffmpeg.go`
- `backend/internal/engine/ffmpeg_test.go`
- `backend/cmd/fcd/main.go`
- `backend/cmd/fcd/main_test.go`
- `.superpowers/sdd/task-2-report.md`

## Concerns / boundaries

- Registry target discovery, media input detection in jobs, output planning, server media behavior, CLI media presets, and end-to-end FFmpeg conversion remain later staged work.
- The daemon now exposes FFmpeg in its runtime registry, but media routes will become fully usable only after those later integration tasks are completed.
- Fixed codec defaults are included only for video containers and WebM. Detailed codec/bitrate/progress options remain out of scope.
