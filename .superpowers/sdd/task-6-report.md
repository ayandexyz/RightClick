# Task 6 report: end-to-end coverage and documentation

## Scope

Implemented Task 6 in the relevant E2E/report files. The existing Task 6 documentation remains accurate and was not changed in this follow-up:

- `backend/e2e_test.go`
- `.superpowers/sdd/task-6-report.md`

## E2E coverage

Added `TestE2EFFmpegMediaConversion`, a daemon-level NDJSON E2E test using real FFmpeg and FFprobe.

- The test calls `exec.LookPath` for both `ffmpeg` and `ffprobe` and calls `t.Skipf` with the missing dependency name when either is unavailable.
- Fixture generation checks FFmpeg's encoder list before running and uses the broadly available MPEG-4 Part 2 video and PCM signed 16-bit audio encoders, rather than assuming `libx264`. No ImageMagick dependency is used.
- Conversion helpers capture the convert response job ID before waiting and accept only the matching `job.finished` event with the expected job ID and preset, rather than the first completion.
- Output files must be nonzero; MP4 and MP3 outputs are additionally checked with `ffprobe` for the expected container and media stream.
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
- `cd backend && go test ./... -run TestE2E -v` (PASS; `TestE2EFFmpegMediaConversion` passed with FFmpeg and FFprobe installed)
- `cd backend && go test ./...`
- `cd backend && go vet ./...`
- `git diff --check`
- Self-review of the final diff confirmed only `backend/e2e_test.go` and this report changed in the follow-up; the pre-existing Task 6 documentation files were not modified or deleted.

The real FFmpeg E2E test passed in this environment with both dependencies installed. The follow-up also records the requested nonzero-output, matching-job, encoder-availability, and ffprobe assertions.

## Follow-up evidence (2026-10-20)

- Changed files are limited to `backend/e2e_test.go` and this report. Existing `README.md` and `docs/PROTOCOL.md` artifacts were retained.
- `waitForJob` now records the convert response's job ID for the specific request ID and waits for `job.finished` with both the matching ID and preset.
- Fixture generation no longer assumes `libx264`; it checks encoder availability and skips with a reason when MPEG-4 Part 2 or PCM signed 16-bit encoding is unavailable.
- Both E2E conversion helpers reject zero-byte outputs, and `ffprobe` validates the MP4 container/video stream and MP3 container/audio stream.
- The video target assertion remains four video routes plus all twelve audio routes because the MKV fixture is excluded by same-format rules. The MP3 target assertion remains the eleven other audio routes and no video routes.
- Fresh verification: `gofmt -w e2e_test.go`, `go test ./...`, `go test ./... -run TestE2E -v`, `go vet ./...`, and `git diff --check` all exited successfully. The dedicated FFmpeg E2E test passed.
