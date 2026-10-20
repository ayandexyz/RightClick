# Task 3 Report: Generalize Registry Target Discovery and Engine Routing

## Status

Implemented and committed as requested on current master after Tasks 1-2.

## Scope delivered

- Added optional `registry.MediaProber` support so media-aware engines can classify files without changing the existing `registry.Engine` contract.
- `Registry.TargetsForFiles` now preserves existing content-based image detection and falls back to an available registered media prober for audio/video sources.
- Media source formats are mapped from the validated file extension after probe classification.
- Candidate routes continue to be resolved through registered, available engines and intersected across all selected files.
- Audio sources advertise audio targets only. Video sources advertise video targets plus audio extraction targets. Mixed audio/video selections retain only their common audio targets.
- Unavailable FFmpeg-like engines contribute no media routes while available ImageMagick image routes remain available.
- Added ordered media format helpers in `formats` for registry source resolution.
- Existing image-only fake engines and image discovery behavior remain compatible.

## TDD evidence

Added registry tests for:

- Audio discovery excluding video targets.
- Video discovery including audio extraction.
- Mixed audio/video intersection.
- Unavailable media engine not hiding ImageMagick image targets.

The focused media test command was run before implementation and failed because discovery only used image detection. After implementation, the focused tests passed.

## Verification

Passed:

```text
cd backend && go test ./internal/registry ./internal/formats ./internal/protocol
go test ./...
go vet ./...
git diff --check
```

## Files changed

- `backend/internal/registry/registry.go`
- `backend/internal/registry/registry_test.go`
- `backend/internal/formats/images.go`

## Concerns / follow-up

- Media classification currently uses the media engine probe for kind and the source extension for the concrete media format. This matches the staged Task 3 scope and avoids changing the existing engine contract.
- No unrelated job, server, protocol, or FFmpeg changes were made.
