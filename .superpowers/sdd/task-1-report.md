# Task 1 Implementation Report: Media Formats and Protocol Presets

## Scope

Implemented Task 1 only: added media format constants, preset mappings, media classification helpers, and protocol target presets. No conversion engine or unrelated backend behavior was changed.

## Requirements addressed

- Added `formats.Format` constants for:
  - Video: `MP4`, `MOV`, `WebM`, `MKV`, `AVI`
  - Audio: `MP3`, `WAV`, `FLAC`, `AAC`, `M4A`, `OGG`, `Opus`
- Extended `formats.PresetFormat` with all required `video-to-*` and `audio-to-*` presets.
- Added `IsVideoFormat`, `IsAudioFormat`, and `IsImageFormat` helpers.
- Extended `protocol.SupportedPresets()` with the twelve media targets after the existing image targets.
- Preserved existing image preset strings and ordering.
- Did not add audio-to-video presets.
- Added tests for every media preset, category/enabled/format fields, classification helpers, and the updated target ordering.

## TDD evidence

1. Added format and protocol tests before production implementation.
2. Ran the focused tests and observed the expected failure:
   - Media constants were undefined.
   - Media presets were missing from `SupportedPresets()`.
3. Added the minimal constants, classification switches, preset switch cases, and protocol table entries.
4. Updated the existing `Targets` ordering assertions to include the newly required media targets.
5. Re-ran the focused tests successfully.

## Verification

Focused command:

```text
cd backend && go test ./internal/formats ./internal/protocol
```

Result:

```text
ok   file-converter/backend/internal/formats
ok   file-converter/backend/internal/protocol
```

Full backend test suite:

```text
cd backend && go test ./...
```

Result:

```text
ok   file-converter/backend
ok   file-converter/backend/cmd/fcctl
ok   file-converter/backend/cmd/fcd
ok   file-converter/backend/internal/engine
ok   file-converter/backend/internal/formats
ok   file-converter/backend/internal/jobs
ok   file-converter/backend/internal/protocol
ok   file-converter/backend/internal/registry
ok   file-converter/backend/internal/server
ok   file-converter/backend/internal/transport
```

`git diff --check` also completed without whitespace errors.

## Files changed

- `.superpowers/sdd/task-1-report.md`
- `backend/internal/formats/images.go`
- `backend/internal/formats/images_test.go`
- `backend/internal/protocol/validate.go`
- `backend/internal/protocol/validate_test.go`

## Concerns / follow-up

- Media detection and conversion engine support remain outside Task 1. The new constants and presets provide the protocol and registry-facing format vocabulary for later staged tasks.
- Registry target enumeration now includes all registered image and media formats, and uses the shared classification helpers when building its candidate list. Actual media engine availability and probing remain part of later staged tasks.
- The registry regression test uses a capability-complete fake engine to prove media targets are reachable through the registry-backed targets path without changing the existing engine contract.

## Follow-up correction

A verifier identified that the initial implementation left the registry's candidate list image-only. This was corrected in the follow-up commit by replacing the hard-coded image list with an ordered all-format list filtered through `IsImageFormat`, `IsVideoFormat`, and `IsAudioFormat`, plus a regression test for registry-backed media target discovery.

## Task 1 review findings fixed

- Registry source discovery now considers PDF separately from output target candidates. PDF is checked as an input source, but is never returned by `Registry.Targets` as an output target.
- PDF-only target discovery exposes exactly `pdf-to-png`, `pdf-to-jpeg`, and `pdf-to-webp`; mixed PDF/non-PDF selections do not expose PDF presets.
- `formats.Targets` applies the same PDF-only filtering while preserving existing SVG and raster/media target behavior.
- Added regressions proving PDF cannot be an output target and PDF target listings contain exactly the three explicit PDF presets.

## Review-fix verification

Commands run from the repository root unless noted:

- `cd backend && go test ./...` -> PASS
- `cd backend && go vet ./...` -> PASS
- `git diff --check` -> PASS
