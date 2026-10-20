# Task 4 Report: Reuse atomic jobs for media outputs

## Outcome

Implemented media outputs through the existing atomic job lifecycle. Media output planning now covers all registered video and audio extensions, media aliases normalize to their canonical presets, and manager conversion can classify media sources through the registry before routing.

## Changes

- Expanded `PlanOutput` coverage tests to all 5 video and 12 audio extensions. Existing generalized output-extension handling and JPEG `.jpg` behavior remain intact.
- Added `MediaSourceRouter` support to job management and exposed registry media classification through `Registry.MediaSource`.
- `Manager.convertFile` now falls back to the route engine's media source classification when regular content detection cannot identify a media file.
- Preserved the same temp-file, conversion, cleanup, and commit lifecycle for media outputs.
- Preserved cancellation cleanup and conflict handling by continuing to use `PlanOutput` and `OutputPlan.Commit` for all routes.
- Added same-format exclusion at conversion time, returning a stable per-file `unsupported` failure without invoking an engine.
- Added coverage for video-to-audio extraction, all media extension aliases, and rejecting audio-to-video submission before queueing.

## Verification

- Focused tests: `go test ./internal/engine ./internal/jobs -run 'Media|Audio|Video|PlanOutput' -v`
- Full backend tests: `go test ./...`
- Race checks: `go test -race ./internal/engine ./internal/jobs`

All passed.

## Concerns

- Media classification still depends on a registered engine implementing the existing `MediaProber` interface and being available. If no media prober is available, the file receives the existing per-file `unsupported` failure.
- The requested implementation files included engine and job files; the registry needed a small public classification bridge so the manager could use the route engine's media classification without duplicating probing logic.
