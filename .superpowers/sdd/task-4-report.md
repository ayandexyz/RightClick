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
- Added coverage for video-to-audio extraction, canonical alias presets, direct media conversion cancellation and temp cleanup, all three media conflict policies, and rejecting audio-to-video submission before queueing.
- Media probing now accepts and receives the Manager job context, so ffprobe cancellation is tied to the job rather than `context.Background()`.
- Route resolution now precedes output planning, preventing unsupported routes from becoming conflict-policy skips when a destination already exists.

## Verification

- Focused tests: `go test ./internal/engine ./internal/jobs -run 'Media|Audio|Video|PlanOutput' -v`
- Full backend tests: `go test ./...`
- Race checks: `go test -race ./internal/engine ./internal/jobs`

All passed.

## Concerns

- Media classification still depends on a registered engine implementing the existing `MediaProber` interface and being available. If no media prober is available, the file receives the existing per-file `unsupported` failure.
- The registry's UI-oriented `TargetsForFiles` path still uses `context.Background()` because it is not a submitted job; submitted conversion paths use the Manager job context.
- The requested implementation files included engine and job files; the registry needed a small public classification bridge so the manager could use the route engine's media classification without duplicating probing logic.

## Task 4 review follow-up

- Fixed `Manager.convertFile` so a `MediaSource` probe error is classified as `cancelled` when `j.ctx.Err()` is non-nil, instead of incorrectly reporting a failed unsupported file.
- Strengthened `TestManagerMediaProbeUsesJobContextOnCancellation` with `job.Files[0].State == "cancelled"`.
- Strengthened `TestManagerMediaConflictPolicies` to assert skip produces no output and does not invoke the engine, rename produces `input (1).mp3` with converted contents, and overwrite replaces `input.mp3` with converted contents.

## Verification evidence

- `cd backend && go test ./internal/jobs -v` -> exit 0; all manager tests passed, including media probe cancellation and all three conflict-policy subtests.
- `cd backend && go test ./...` -> exit 0; all backend packages passed.
- `cd backend && go test -race ./internal/engine ./internal/jobs` -> exit 0; both race-tested packages passed.
- `git diff --check` -> exit 0 with no output.

(End of file - total 46 lines)
