# FFmpeg media conversion design

**Date:** 2026-10-20
**Status:** Approved

## Goal

Add simple FFmpeg-backed video and audio conversion to the existing Go backend. Users select an output format; the backend chooses sensible FFmpeg defaults. This iteration does not expose codec, bitrate, resolution, sample-rate, or channel controls.

## Supported outputs

The backend will advertise one preset for each requested output format:

- Video: `video-to-mp4`, `video-to-mov`, `video-to-webm`, `video-to-mkv`, `video-to-avi`
- Audio: `audio-to-mp3`, `audio-to-wav`, `audio-to-flac`, `audio-to-aac`, `audio-to-m4a`, `audio-to-ogg`, `audio-to-opus`, `audio-to-aiff`, `audio-to-alac`, `audio-to-ac3`, `audio-to-amr`, `audio-to-wma`

The target format in the protocol remains the output extension. Existing image presets and ImageMagick behavior remain unchanged.

## Architecture

Add a dedicated FFmpeg engine behind the existing registry engine interface. The engine is responsible for locating FFmpeg, probing media, constructing argument lists, executing cancellable process groups, and reporting availability. It must not be added to ImageMagick or mixed into job-specific conversion logic.

Extend the shared format and preset registry with media formats and category metadata. The registry resolves routes based on source and target media types and the available engine. Existing job management, conflict handling, temporary files, atomic output commits, cancellation, and event delivery are reused.

## Detection and target discovery

Media inputs are detected by FFprobe/FFmpeg stream metadata, not by filename extension:

- video: contains at least one video stream
- audio: contains at least one audio stream and no video stream
- unsupported: probe fails or contains neither relevant stream

For a group of files, target discovery returns only formats valid for every file:

- video input: video targets and audio targets for audio extraction
- audio input: audio targets only
- mixed audio/video input: audio targets only, because extraction is valid for every input
- audio-to-video targets are never advertised

If FFmpeg is missing or unavailable, media targets are absent and the existing image conversion capability continues to work. The `hello` response reports FFmpeg availability alongside other engines.

## Conversion behavior

FFmpeg uses extension-specific defaults:

- MP4, MOV, AVI, and MKV use broadly compatible video/audio defaults selected by FFmpeg.
- WebM uses WebM-compatible video/audio defaults.
- MP3, WAV, FLAC, AAC, M4A, OGG, and Opus produce audio-only output.
- Video-to-audio conversion uses `-vn` and retains the input audio stream.
- Audio-to-video conversion is rejected as unsupported even if manually requested.

Every input path and output path is passed as an argument, never through a shell command string. FFmpeg runs in its own process group so cancellation terminates the complete conversion.

## Errors and compatibility

- Missing FFmpeg produces a stable unavailable-engine error and no media targets.
- Probe failures and unsupported stream types produce per-file failures.
- Unsupported codec/container combinations surface as stable conversion errors from the FFmpeg engine.
- Existing `rename`, `skip`, and `overwrite` output conflict policies remain unchanged.
- Protocol version remains v1. Adding presets and target data does not change the meaning of existing messages.

Progress initially reports queued, running, and completed states through the existing job events. Detailed FFmpeg progress parsing is intentionally deferred and can be added without changing the protocol.

## Testing

Test-first coverage will include:

1. Format detection and preset mapping for every requested output.
2. Target filtering for video, audio, mixed, and unsupported inputs.
3. FFmpeg command construction, including `-vn` and output extension handling.
4. Missing FFmpeg and failed probe behavior.
5. Cancellation and process-group termination.
6. Job manager integration with atomic output planning and conflict policies.
7. Server-level targets and convert requests.
8. End-to-end conversion tests when FFmpeg is installed, skipped otherwise.

## Acceptance criteria

- With FFmpeg installed, supported media files expose the correct targets and convert successfully through `fcd`.
- Video-to-audio conversion extracts audio; audio-to-video targets are unavailable.
- Image conversion behavior remains unchanged.
- Missing FFmpeg does not prevent the daemon from serving image conversions.
- All existing and new automated tests pass, with coverage maintained at the project’s required level.
