# Task 1 implementation report

## Scope

Implemented only Task 1 from the approved PDF rasterization plan: PDF input detection, PDF format classification, protocol PDF rasterization presets, and target filtering so the new presets are advertised only for PDF inputs.

## Files changed

- `backend/internal/formats/images.go`
  - Added `formats.PDF`.
  - Added `IsPDFFormat`.
  - Detects a `%PDF-` prefix after regular-file validation.
  - Added `.pdf` to `FormatFromExtension` as an extension hint only.
  - Added `pdf-to-png`, `pdf-to-jpeg`, and `pdf-to-webp` mappings to `PresetFormat`.
  - Filters `pdf-to-*` targets to PDF inputs in `Targets`.
- `backend/internal/formats/images_test.go`
  - Added PDF signature and arbitrary `.pdf` content fixtures/cases.
  - Verifies extension mapping, PDF/image classification, and all three preset mappings.
- `backend/internal/protocol/validate.go`
  - Added the three PDF targets after the SVG targets with exact labels, formats, image category, and enabled state.
- `backend/internal/protocol/validate_test.go`
  - Added exact `SupportedPresets` assertions for all three PDF targets.
- `backend/internal/registry/registry.go`
  - Ensures registry target discovery does not advertise PDF presets for non-PDF inputs, preserving existing image/media target behavior.

The requested `engine/imagemagick.go` and `jobs/manager_test.go` did not require changes for Task 1: PDF-derived output uses existing raster output extensions, and existing generic `png`, `jpeg`, and `webp` aliases remain mapped to the generic image presets.

## Test-first evidence

- Added the format tests before implementation.
- Focused test run initially failed at compile time because `PDF` and `IsPDFFormat` were not yet defined.
- After implementation, a focused run exposed existing target-order expectations that needed PDF target filtering; the filtering was added and the focused tests passed.

## Verification

Commands run from `backend/`:

- `go test ./internal/formats ./internal/protocol` -> PASS
- `go test ./...` -> PASS
- `go vet ./...` -> PASS
- `git diff --check` -> PASS

## Concerns

- PDF conversion execution itself is intentionally not implemented in Task 1. The new presets are protocol/format metadata only; the later rasterization task must add the PDF-capable engine route.
- `.pdf` extension handling remains only a media-probing hint; malformed or arbitrary `.pdf` content is rejected by content detection.

## Task 1 review fix

- Updated `backend/internal/registry/registry.go` so `allSupportedFormats()` includes `formats.PDF` as a source candidate while retaining PDF as output-ineligible (`IsImageFormat(PDF)` remains false).
- PDF target discovery now resolves PDF -> PNG/JPEG/WebP through the registry and only advertises `pdf-to-*` presets when every selected input is PDF.
- Added focused registry regressions covering PDF-only, mixed PDF/raster, raster-only discovery, PDF route candidates, stable PDF preset placement immediately after SVG presets, and the existing generic `png`/`jpeg`/`webp` alias behavior remains unchanged.

## Review-fix verification

Commands run from `backend/`:

- `go test ./internal/registry` -> PASS
- `go test ./...` -> PASS
- `go vet ./...` -> PASS
- `git diff --check` -> PASS
