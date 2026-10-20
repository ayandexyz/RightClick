# Task 2 report: safe multi-page output planning

## Status
Implemented Task 2 only. The PDF renderer itself was not implemented or changed.

## TDD evidence
1. Added allocator tests for page naming, rename conflicts, skip conflicts, unsafe stems, and source protection.
2. Ran the focused engine tests before implementation. They failed with `undefined: PlanOutputPath`, proving the new allocator contract was not already present.
3. Added job-layer tests for ordered multi-page commits and failure without partial outputs.
4. Ran those tests before implementation. They failed because the existing manager used the single-output path and committed `document.png`.
5. Implemented the smallest allocator and multi-output route, then reran the focused tests successfully.

## Implementation
- Added `registry.MultiOutputEngine` with `ConvertMany` while preserving `Engine.Convert`.
- Added `engine.PlanOutputPath`, producing `stem-<page>.<ext>` with existing rename/skip/overwrite behavior.
- Added multi-output manager routing before the existing single-output route.
- Multi-output conversions render into a private output-directory render folder, validate returned paths and extensions, plan all final outputs in engine return order, and only then move/commit pages.
- Render directories are removed with `defer` on success, error, and cancellation paths.
- `FileJob.Outputs` contains committed page paths in order; failed conversions publish no partial outputs.
- PDF target discovery behavior remains restricted to PDF-only inputs and existing registry tests cover non-PDF and mixed selection behavior.

## Verification
- `go test ./internal/engine ./internal/jobs ./internal/registry` passed.
- `go test ./...` passed.
- `git diff --check` passed.

## Concerns / follow-up
- The actual PDF engine implementation remains intentionally deferred to the renderer task.

## Task 2 review fixes
The earlier Task 2 implementation added the registry routing coverage in `backend/internal/registry/registry_test.go`. This approval-fix commit changes only `backend/internal/jobs/manager.go`, `backend/internal/jobs/manager_test.go`, `backend/internal/engine/imagemagick.go`, and this report; `backend/internal/registry/registry.go` and its existing tests are unchanged by the approval fix.

- Use the effective output directory, falling back to the source directory when `OutputDir` is empty, before creating the private render directory.
- Check cancellation immediately after `ConvertMany`, after planning/staging, and before each commit so cancellation does not publish pages.
- Track committed destinations and remove them, clear `FileJob.Outputs`, and remove all temporary plans if a later commit fails.
- Clean every plan temp file when validation or later page planning fails, including failures after earlier plans were created.
- Validate each returned page with `os.Lstat`: it must be a direct child of the render directory, have the expected extension, be regular, and not be a symlink.
- Added focused manager tests for source-directory fallback, cancellation before publication, commit rollback, and symlink/non-regular rejection. Added registry coverage for excluding PDF targets from raster, SVG, media, and mixed selections.

## Review-fix verification
- `gofmt -w backend/internal/jobs/manager.go backend/internal/jobs/manager_test.go` completed successfully.
- `go test ./internal/jobs ./internal/engine ./internal/registry` passed: jobs, engine, and registry packages all passed.
- `go test ./...` passed: backend, command packages, and all internal packages passed.
- `go vet ./...` passed with no diagnostics.
- `git diff --check` passed with no whitespace errors.

## Task 2 approval fixes
- Added a deterministic commit seam and post-commit cancellation check. If cancellation occurs while `Commit` is publishing, every page is rolled back and the file reports `cancelled` with no outputs.
- Added overwrite backups retained by each output plan until the whole batch succeeds. Any later commit failure or cancellation removes new files and restores the originals.
- Added deterministic tests for cancellation during commit and preservation of pre-existing overwrite destinations after a later page commit failure.

## Approval-fix verification
- `go test ./internal/jobs ./internal/engine ./internal/registry` passed.
- `go test ./...` passed.
- `go vet ./...` passed with no diagnostics.
- `gofmt -l internal/jobs/manager.go internal/jobs/manager_test.go internal/engine/imagemagick.go` produced no output; files are formatted.
- `git diff --check` passed with no whitespace errors.
- These commands were executed in `/tmp/file-converter-pdf/backend` (except `git diff --check`, executed at repository root).

## Remaining concerns
- The actual PDF engine implementation remains intentionally deferred to the renderer task; Task 2 only covers safe multi-page job planning, validation, and publication.
