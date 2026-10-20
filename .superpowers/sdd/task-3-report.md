# Task 3 Report: Dedicated `pdftocairo` PDF Engine

## Status

Implemented the dedicated PDF rasterization engine and focused tests.

## TDD evidence

- RED: `go test ./internal/engine -run PDF` failed to build with `undefined: NewPDF`, confirming the implementation was missing.
- GREEN: `go test ./internal/engine -run PDF` passed after implementation.
- Focused suite: `go test ./internal/engine` passed.

## Scope delivered

- Added PDF-to-PNG/JPEG/WebP support through `pdftocairo` at 150 DPI.
- Validates absolute safe regular-file inputs, the `%PDF-` header, and the controlled temporary directory.
- Passes source and output prefix as separate arguments, preserving spaces and shell metacharacters safely.
- Discovers only `page-<number>` renderer outputs, orders them numerically, rejects empty or excessive output sets, and returns absolute paths.
- Converts ordered PNG intermediates to WebP through `ImageMagick.Convert` and cleans generated intermediates or partial outputs on errors.

## Self-review

- Changes are confined to the requested engine implementation and tests.
- Command arguments, format support, missing executable behavior, validation, numeric ordering, WebP conversion, empty output handling, and error cleanup are covered.
- `git diff --check` passed; no unrelated behavior was changed.

## Concerns

None.

## Important findings fix evidence

- RED: `go test ./internal/engine -run PDF -count=1` failed because a non-empty `.file-converter-render-*` directory was accepted and the injected renderer ran. A separate focused RED check failed because a mode `0755` render directory was accepted.
- Temp-dir validation now accepts only absolute manager-shaped `.file-converter-render-*` directories that are real directories (not symlinks), mode `0700`, and empty before rendering. This establishes invocation ownership before output discovery or cleanup and preserves pre-existing files by rejecting the directory before invoking the renderer.
- Removed mutable PDF caches: `Available` performs a local lookup, and each WebP conversion invocation uses a local ImageMagick instance. Concurrent conversions therefore do not mutate shared `PDF` state.
- Added regression coverage for uncontrolled, symlink, non-private, and non-empty render directories; preservation of a pre-existing matching page file; the 1,000-page cap and cleanup; mid-sequence WebP cleanup; and concurrent WebP conversions under the race detector. Removed dead runner setup from the existing cleanup test.
- GREEN verification:
  - `go test ./internal/engine -run PDF -count=1` -> `ok file-converter/backend/internal/engine 0.009s`
  - `go test -race ./internal/engine -run PDF -count=1` -> `ok file-converter/backend/internal/engine 1.033s`
  - `go test ./internal/engine -count=1` -> `ok file-converter/backend/internal/engine 0.235s`
  - `git diff --check` -> passed with no output.

## Reachability follow-up

- Independent review found the PDF engine was not registered by the daemon, so production requests could not reach `PDF.ConvertMany` despite the engine-level fixes.
- RED: `go test ./cmd/fcd -run TestEngineRegistryIncludesPDF -count=1` failed with `daemon engine registry does not include pdf`.
- Registered `engine.NewPDF(nil)` in `setupEngines` and added an application-registry regression test. This required a narrow exception to the original engine-file scope because the defect existed at the application composition root.
- GREEN verification:
  - `go test ./cmd/fcd -count=1` -> `ok file-converter/backend/cmd/fcd 0.112s`
  - `go test ./internal/engine -run PDF -count=1` -> `ok file-converter/backend/internal/engine 0.009s`
  - `go test -race ./internal/engine -run PDF -count=1` -> `ok file-converter/backend/internal/engine 1.035s`
  - `go test ./... -count=1` -> all backend packages passed.
  - `git diff --check` -> passed with no output.
