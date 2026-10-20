# Task 5 implementation report

## Scope

Task 5 wires FFmpeg into the daemon-facing engine registry and preserves protocol v1 and ImageMagick behavior.

## Changes

- `backend/cmd/fcd/main.go`
  - Creates and independently probes ImageMagick and FFmpeg.
  - Registers both engines even when either probe is unavailable.
  - An unavailable FFmpeg installation no longer blocks daemon startup or image conversion setup.
  - The registry remains the source of engine availability for `hello` and route selection.

- `backend/internal/jobs/manager.go`
  - Resolves canonical presets through the existing format mapping.
  - Rejects manually submitted audio-to-video conversions synchronously with an `unsupported` error before creating a job.
  - Preserves same-format rejection and the existing worker/atomic output lifecycle.

- `backend/internal/server/server_test.go`
  - Added a registry-backed server fixture with independent fake image and media engines.
  - Verifies `hello` exposes both engine IDs.
  - Verifies audio target discovery exposes all valid audio outputs and excludes the same-format output and all video outputs.
  - Verifies manually requested audio-to-video conversion returns stable `unsupported` behavior.

The existing registry, canonical media preset table, FFmpeg engine, ImageMagick fallback, target filtering, protocol v1 response shape, and validation behavior were already present and remain intact.

## TDD evidence

The new server test was first run before the job-level rejection was added and failed because the request incorrectly created a job. After the minimal manager change, the focused test passed.

## Verification

- `cd backend && go test ./internal/server ./cmd/fcd ./internal/protocol -v` - PASS
- `cd backend && go test ./...` - PASS
- `cd backend && go vet ./...` - PASS
- `git diff --check` - PASS

## Concerns / notes

- FFmpeg availability is probed independently and cached by the engine. The registry still reports availability dynamically through `Available()`.
- Media source probing remains FFprobe-backed and target discovery continues to use the registry, so unavailable FFmpeg produces no media routes while ImageMagick routes remain available.
- No protocol version or wire-format changes were introduced.
