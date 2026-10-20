# Task 3 Report: Generalize Registry Target Discovery and Engine Routing

## Status

Implemented and committed as requested on current master after Tasks 1-2.

## Scope delivered

- Added optional `registry.MediaProber` support so media-aware engines can classify files without changing the existing `registry.Engine` contract.
- `Registry.TargetsForFiles` now preserves existing content-based image detection and falls back to an available registered media prober for audio/video sources.
- Media source formats are mapped from the validated file extension after probe classification.
- Candidate routes continue to be resolved through registered, available engines and intersected across all selected files.
- Audio sources advertise audio targets only. Video sources advertise video targets plus audio extraction targets. Mixed audio/video selections retain only their common audio targets.
- The media preset set contains five video outputs and seven audio outputs before source-format exclusion; an audio source therefore exposes six audio targets because its own MP3 format is omitted.
- Audio-source target discovery now explicitly skips video targets before consulting engine `Supports`, so an overly broad or unavailable FFmpeg-like capability cannot advertise audio-to-video routes.
- Unavailable FFmpeg-like engines contribute no media routes while available ImageMagick image routes remain available.
- Added ordered media format helpers in `formats` for registry source resolution.
- Existing image-only fake engines and image discovery behavior remain compatible.

## TDD evidence

Added registry tests for:

- Audio discovery excluding video targets.
- Video discovery including audio extraction.
- Mixed audio/video intersection.
- Unavailable media engine not hiding ImageMagick image targets.
- Actual audio media classification with an unavailable FFmpeg-like engine, proving no media targets are contributed while image routes remain available.

The focused regression test was first run before the production change and failed with video presets present (`video-to-mp4`, `video-to-mov`, `video-to-webm`, `video-to-mkv`, `video-to-avi`). After the explicit source-classification guard was added, it passed along with the existing audio, video, and mixed-file tests.

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

## Fix evidence

- Corrected the inaccurate “twelve audio targets” wording: there are seven audio output presets before source-format exclusion, and six targets for an MP3 source because `audio-to-mp3` is omitted as a same-format route.
- Added `TestTargetsForFilesClassifiesAudioWithoutUnavailableMediaRoutes`, which uses a real `.mp3` fixture, an unavailable FFmpeg-like engine, an available broad media engine, and an available image engine. It verifies audio classification yields only audio targets and image classification still yields all image routes.
- The regression test was observed failing before the fix and passing after it.
- Final verification commands passed after the fix.
