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

## PDF rasterization Task 4

### Outcome

Integrated PDF-only registry routing while retaining the daemon registration from commit `12f6f7f` and the existing multi-page job implementation and tests. The registry now reserves PDF input for the dedicated `pdf` engine, limits it to PNG/JPEG/WebP, and rejects PDF output routes even if an unrelated engine claims arbitrary formats.

### TDD evidence

- RED: `cd backend && go test ./cmd/fcd ./internal/registry ./internal/jobs` -> exit 1. `TestResolveRoutesPDFOnlyThroughPDFEngine` selected `any-format` for PDF-to-PNG, and `TestResolveRejectsRoutesOutsidePDFRasterization` accepted PNG-to-PDF.
- GREEN (focused): `cd backend && go test -count=1 ./cmd/fcd ./internal/registry ./internal/jobs` -> exit 0; all three focused packages passed.
- GREEN (full): `cd backend && go test -count=1 ./...` -> exit 0; all backend packages passed.
- Static checks: `cd backend && go vet ./...` -> exit 0 with no output; `git diff --check` -> exit 0 with no output.

### Coverage and self-review

- Verified daemon setup still includes exactly the previously registered PDF engine; no duplicate registration was added.
- Added explicit route tests proving PDF-to-PNG/JPEG/WebP selects the PDF engine and PNG-to-PDF, PDF-to-video, and PDF-to-SVG are rejected.
- Added availability isolation coverage proving a missing PDF renderer removes PDF targets but leaves image targets available.
- Existing tests already cover exactly three PDF presets, mixed-selection filtering, two ordered numbered outputs for one PDF, and no partial publication on conversion/planning/commit failures; no duplicate manager implementation or tests were added.
- PDF remains a source candidate only and is absent from `allSupportedFormats` output candidates. SVG, raster image, audio, and video routing remains on the existing paths.
- Scope review: production changes are limited to `backend/internal/registry/registry.go`; tests are limited to its paired test file. `main.go`/`main_test.go` and `manager.go`/`manager_test.go` were verified but did not require changes because the required behavior predates this task.

### Concerns

- Dedicated PDF routing relies on the stable engine ID `pdf`, matching `engine.PDF.ID()` and the daemon registration. This prevents broad or future generic engines from claiming PDF input, as required.

(End of file - total 46 lines)
