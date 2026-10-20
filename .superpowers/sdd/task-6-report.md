# Task 6 report: end-to-end coverage and documentation

## Scope

Implemented Task 6 in the requested files only:

- `backend/e2e_test.go`
- `README.md`
- `docs/PROTOCOL.md`

## E2E coverage

Added `TestE2EFFmpegMediaConversion`, a daemon-level NDJSON E2E test using real FFmpeg and FFprobe.

- The test calls `exec.LookPath` for both `ffmpeg` and `ffprobe` and calls `t.Skipf` with the missing dependency name when either is unavailable.
- It generates a tiny one-second MKV fixture using FFmpeg's `color` and `sine` lavfi sources, with H.264 video and AAC audio. No ImageMagick dependency is used.
- It requests `targets` for the video fixture and verifies the available video/audio target set. The generated MKV is itself excluded as an identical target, so the expected video targets are MP4, MOV, WebM, and AVI plus all twelve audio outputs.
- It submits `video-to-mp4` and waits for `job.finished`, asserting completed state and an `.mp4` output.
- It submits `audio-to-mp3` from the same video and waits for `job.finished`, asserting completed state and an `.mp3` output. This exercises video-to-audio extraction.
- It requests targets for the generated MP3 and verifies all remaining audio presets are advertised and no `video-to-*` preset is present.
- The helper builds and runs the real daemon in a temporary XDG runtime directory, preserving the existing image E2E conventions.

The all-5/12 target scope is covered where practical: the video fixture exposes all five canonical video routes except its same-format MKV route, and all twelve audio routes; the audio fixture validates all twelve audio routes except same-format MP3 and validates video exclusion. Existing unit tests continue to enumerate all five video and twelve audio formats and routes.

## Documentation

Updated README and protocol documentation to state:

- Exact canonical five video presets and twelve audio presets.
- Both `ffmpeg` and `ffprobe` are required for media targets.
- `ffprobe` classifies sources from stream contents, with video taking precedence for mixed audio/video sources.
- Video-to-audio conversion uses FFmpeg `-vn` extraction.
- Audio sources do not advertise or convert to video targets.
- ImageMagick and FFmpeg availability are independent; missing media tools do not disable image conversion, and missing ImageMagick does not hide available media targets.

## Verification

All requested checks passed:

- `cd backend && gofmt -w e2e_test.go`
- `cd backend && go test ./... -run TestE2E -v`
- `cd backend && go test ./...`
- `cd backend && go vet ./...`
- `git diff --check`
- Self-review of the final diff confirmed only the three requested files changed.

The real FFmpeg E2E test passed in this environment with both dependencies installed.
