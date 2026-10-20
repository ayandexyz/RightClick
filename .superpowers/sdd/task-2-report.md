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

## Follow-up Review Fix Report

### Changes made

- Removed the standalone `--` argument before the FFmpeg destination in `backend/internal/engine/ffmpeg.go`. Source and destination paths continue to pass through `formats.ValidatePath`, so canonical absolute-path and unsafe-basename validation is unchanged.
- Updated exact FFmpeg argument assertions to require the destination as the final ordinary argument rather than `--`, and expanded command-construction coverage from only MP4/MP3 sources to every supported video and audio source format.
- Added `TestFFmpegCancellationKillsProcessGroupAndReturnsContextCanceled` in `backend/internal/engine/process_test.go`. It runs a deterministic fake FFmpeg shell process with a sleeping child, waits for a readiness marker, cancels the context, asserts `errors.Is(err, context.Canceled)`, and checks that the child never writes its completion marker.

### TDD evidence

- First updated the exact-argument expectation and expanded route matrix, then ran the focused tests. They failed because production still emitted `--` before each destination.
- Applied the minimal production change removing only that argument.
- Re-ran the focused route and cancellation tests; both passed.

### Verification commands and output

All commands were run from `backend` unless noted:

```text
$ go test ./internal/engine -run 'TestFFmpegConvertBuildsEveryRequiredRoute|TestFFmpegCancellationKillsProcessGroupAndReturnsContextCanceled' -count=1
aok   file-converter/backend/internal/engine  0.114s

$ go test ./internal/engine ./cmd/fcd
ok   file-converter/backend/internal/engine  0.225s
ok   file-converter/backend/cmd/fcd          (cached)

$ go test ./...
ok   file-converter/backend                         (cached)
ok   file-converter/backend/cmd/fcctl                (cached)
ok   file-converter/backend/cmd/fcd                  (cached)
ok   file-converter/backend/internal/engine          (cached)
ok   file-converter/backend/internal/formats         (cached)
ok   file-converter/backend/internal/jobs             (cached)
ok   file-converter/backend/internal/protocol         (cached)
ok   file-converter/backend/internal/registry         (cached)
ok   file-converter/backend/internal/server            (cached)
ok   file-converter/backend/internal/transport         (cached)

$ git diff --check
passed with no output
```

### Concerns / boundaries

- The cancellation regression test uses the existing Linux process-group execution style and shell utilities, matching the repository's current ImageMagick process test. It is intentionally scoped to the FFmpeg runner's cancellation contract.
- No speculative route or source behavior was added: coverage now exercises the already-declared five video and seven audio formats, while audio-to-video routes remain rejected.

## Cancellation Test Hardening Evidence

Strengthened `backend/internal/engine/process_test.go` so the FFmpeg cancellation test starts a sleeping child, records its PID, and asserts that the recorded child PID no longer exists after cancellation. The completion-marker assertion remains, so the test no longer passes merely because only the parent exits.

Exact verification evidence:

```text
$ cd /home/ayan-de/Projects/omarchy-projects/file-converter/backend && go test ./internal/engine -run Cancellation -v -count=1
=== RUN   TestFFmpegCancellationKillsProcessGroupAndReturnsContextCanceled
--- PASS: TestFFmpegCancellationKillsProcessGroupAndReturnsContextCanceled (0.11s)
=== RUN   TestImageMagickCancellationKillsProcessGroup
--- PASS: TestImageMagickCancellationKillsProcessGroup (0.11s)
PASS
ok  	file-converter/backend/internal/engine	0.223s

$ cd /home/ayan-de/Projects/omarchy-projects/file-converter/backend && go test ./...
ok  	file-converter/backend	(cached)
ok  	file-converter/backend/cmd/fcctl	(cached)
ok  	file-converter/backend/cmd/fcd	(cached)
ok  	file-converter/backend/internal/engine	0.225s
ok  	file-converter/backend/internal/formats	(cached)
ok  	file-converter/backend/internal/jobs	(cached)
ok  	file-converter/backend/internal/protocol	(cached)
ok  	file-converter/backend/internal/registry	(cached)
ok  	file-converter/backend/internal/server	(cached)
ok  	file-converter/backend/internal/transport	(cached)

$ cd /home/ayan-de/Projects/omarchy-projects/file-converter && git diff --check
(no output; exit status 0)
```

## Final Task 2 Review Fix: Injected Availability Seams

### Changes made

- `FFmpeg.Probe` now invokes an injected `Probe` callback before any `ffprobe` lookup. Real ffprobe execution still performs the `ffprobe` availability lookup when no callback is injected.
- `FFmpeg.Convert` now invokes an injected `Runner` without `ffmpeg` or `ffprobe` availability lookup, preserving command-construction tests with no installed binaries. Real conversion still performs the normal `Available` check when no runner is injected.
- Added explicit tests for both injected seams and for missing `ffprobe`/`ffmpeg` behavior without injection.

### TDD evidence

- Added failing seam tests that install a lookup function which fails the test if called; both initially failed because `Probe` and `Convert` performed availability lookup before invoking their injected callbacks.
- Applied the minimal ordering changes, then reran the focused tests successfully.

### Verification evidence

```text
$ cd /home/ayan-de/Projects/omarchy-projects/file-converter/backend && go test ./internal/engine ./cmd/fcd
ok   file-converter/backend/internal/engine
ok   file-converter/backend/cmd/fcd

$ go test ./...
ok   file-converter/backend and all packages

$ cd /home/ayan-de/Projects/omarchy-projects/file-converter && git diff --check
passed with no output
```
