# FFmpeg Media Conversion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add FFmpeg-backed conversion for MP4, MOV, WebM, MKV, AVI, MP3, WAV, FLAC, AAC, M4A, OGG, Opus, AIFF, ALAC, AC3, AMR, and WMA while preserving existing ImageMagick image conversion.

**Architecture:** Extend the existing format and preset registries with media categories, add a dedicated FFmpeg engine implementing the existing registry engine interface, and generalize job output planning so both image and media routes use the same atomic lifecycle. FFmpeg probing determines whether each input is audio-only or contains video; target discovery intersects valid targets across all selected files.

**Tech Stack:** Go, standard library `os/exec`, existing NDJSON protocol, existing registry/job manager, FFmpeg and ffprobe when installed.

## Global Constraints

- Keep protocol version 1; additive preset and engine data only.
- Video-to-audio conversion must use `-vn`; audio-to-video targets must never be advertised or accepted.
- Use simple format presets only; do not add codec, bitrate, resolution, sample-rate, or channel request options.
- Pass paths as individual process arguments; never invoke a shell command string.
- Run FFmpeg in a process group and kill the group on cancellation.
- Preserve atomic temporary output, `rename`/`skip`/`overwrite` conflict behavior, and existing image behavior.
- Missing FFmpeg must disable media routes without disabling ImageMagick routes.
- Follow TDD: write focused failing tests before implementation and run `go test ./...` and `go vet ./...` before completion.

---

## File map

- Create `backend/internal/engine/ffmpeg.go`: FFmpeg executable discovery, ffprobe media classification, argument construction, cancellable execution, and media route support.
- Create `backend/internal/engine/ffmpeg_test.go`: unit tests for probe parsing, route support, command construction, availability, and cancellation.
- Modify `backend/internal/formats/images.go`: add media `Format` values, media categories, preset-to-format lookup, and shared input detection that delegates media probing when available through the registry path. The media set includes five video outputs and twelve audio outputs: MP3, WAV, FLAC, AAC, M4A, OGG, Opus, AIFF, ALAC, AC3, AMR, and WMA.
- Modify `backend/internal/formats/images_test.go`: cover media preset mapping and format metadata without weakening existing image tests.
- Modify `backend/internal/protocol/validate.go`: add the twelve media targets to the advertised preset list and accept normalized media presets.
- Modify `backend/internal/protocol/validate_test.go`: verify all media presets, case normalization behavior, and unsupported audio-to-video requests.
- Modify `backend/internal/registry/registry.go`: enumerate image and media targets, preserve engine-specific availability, and compute source/target category routes.
- Modify `backend/internal/registry/registry_test.go`: test audio, video, mixed-file intersections and absent FFmpeg behavior.
- Modify `backend/internal/jobs/manager.go`: normalize media aliases, resolve the target extension, and use generalized output planning while retaining existing job lifecycle.
- Modify `backend/internal/jobs/manager_test.go`: test media submission, target routing, extraction, and invalid audio-to-video requests through fake engines.
- Modify `backend/internal/engine/imagemagick.go` and `backend/internal/engine/imagemagick_test.go`: generalize output extension validation and preserve image-only behavior.
- Modify `backend/internal/server/server.go` and `backend/internal/server/server_test.go`: report FFmpeg in `hello`, dispatch media targets through the registry, and verify server-level conversion errors.
- Modify `backend/cmd/fcd/main.go`: initialize/probe/register FFmpeg independently from ImageMagick.
- Modify `backend/e2e_test.go`: add conditional FFmpeg media conversion tests that skip only when FFmpeg/ffprobe is unavailable.
- Modify `README.md` and `docs/PROTOCOL.md`: document FFmpeg dependency, media presets, extraction behavior, and unavailable-engine behavior.

---

### Task 1: Add media formats and protocol presets

**Files:**
- Modify: `backend/internal/formats/images.go`
- Test: `backend/internal/formats/images_test.go`
- Modify: `backend/internal/protocol/validate.go`
- Test: `backend/internal/protocol/validate_test.go`

**Interfaces:**
- Produce `formats` constants for `MP4`, `MOV`, `WebM`, `MKV`, `AVI`, `MP3`, `WAV`, `FLAC`, `AAC`, `M4A`, `OGG`, and `Opus`.
- Produce `formats.PresetFormat(preset string) (Format, bool)` entries for `video-to-*` and `audio-to-*` presets.
- Produce media classification helpers used by the registry, such as `IsVideoFormat`, `IsAudioFormat`, and `IsImageFormat`.
- `protocol.SupportedPresets()` must return the existing image targets followed by the twelve media targets with categories `video` or `audio`.

- [ ] **Step 1: Write failing format and preset tests**

Add table-driven cases asserting each preset maps to the expected format and category, and that image mappings remain unchanged:

```go
func TestMediaPresetFormats(t *testing.T) {
    cases := map[string]formats.Format{
        "video-to-mp4": formats.MP4, "video-to-mov": formats.MOV,
        "video-to-webm": formats.WebM, "video-to-mkv": formats.MKV,
        "video-to-avi": formats.AVI, "audio-to-mp3": formats.MP3,
        "audio-to-wav": formats.WAV, "audio-to-flac": formats.FLAC,
        "audio-to-aac": formats.AAC, "audio-to-m4a": formats.M4A,
        "audio-to-ogg": formats.OGG, "audio-to-opus": formats.Opus,
    }
    for preset, want := range cases {
        t.Run(preset, func(t *testing.T) {
            got, ok := formats.PresetFormat(preset)
            if !ok || got != want { t.Fatalf("PresetFormat(%q) = %q, %v", preset, got, ok) }
        })
    }
}
```

Add protocol tests that locate every media preset and assert non-empty format/category/enabled fields.

- [ ] **Step 2: Run tests and verify they fail**

Run: `cd backend && go test ./internal/formats ./internal/protocol`
Expected: FAIL because media constants and preset entries do not exist.

- [ ] **Step 3: Implement the minimal format and preset tables**

Add constants and explicit switch cases. Keep the existing image preset strings and ordering unchanged. Add media entries to `supportedPresets`; do not add audio-to-video presets.

- [ ] **Step 4: Run focused tests**

Run: `cd backend && go test ./internal/formats ./internal/protocol`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/formats/images.go backend/internal/formats/images_test.go backend/internal/protocol/validate.go backend/internal/protocol/validate_test.go
git commit -m "feat: add media formats and presets"
```

### Task 2: Implement the FFmpeg engine and probe model

**Files:**
- Create: `backend/internal/engine/ffmpeg.go`
- Create: `backend/internal/engine/ffmpeg_test.go`

**Interfaces:**
- `type MediaKind string` with `MediaAudio` and `MediaVideo`.
- `type Probe func(context.Context, string, []string) ([]byte, error)` for injectable ffprobe tests.
- `type FFmpeg struct` with `NewFFmpeg(runner Runner, probe Probe) *FFmpeg`, `ID() string`, `Available() (string, error)`, `Probe(path string) (MediaKind, error)`, `Supports(source, target formats.Format) bool`, and `Convert(...) error`.
- `FFmpeg` implements `registry.Engine` using the existing `Runner` and `protocol.ImageOptions` signature.

- [ ] **Step 1: Write failing tests for parsing, support, and command construction**

Cover JSON stream classification, audio-only vs video-containing inputs, all output routes, audio-to-video rejection, `-vn` for video-to-audio, and output format selection. Capture runner arguments:

```go
func TestFFmpegConvertExtractsAudio(t *testing.T) {
    var got []string
    f := NewFFmpeg(func(_ context.Context, _ string, args []string) error { got = args; return nil }, nil)
    f.executable = "/usr/bin/ffmpeg"
    err := f.Convert(context.Background(), "/tmp/input.mp4", formats.MP4, "/tmp/out.mp3", formats.MP3, protocol.ImageOptions{})
    if err != nil { t.Fatal(err) }
    if !containsArg(got, "-vn") { t.Fatalf("args = %v, missing -vn", got) }
    if !containsArg(got, "/tmp/out.mp3") { t.Fatalf("args = %v, missing output", got) }
}
```

- [ ] **Step 2: Run focused tests and verify failure**

Run: `cd backend && go test ./internal/engine -run FFmpeg -v`
Expected: FAIL because the FFmpeg type and parser do not exist.

- [ ] **Step 3: Implement probing and safe command construction**

Locate `ffmpeg` and `ffprobe` with `exec.LookPath`. Use `ffprobe -v error -show_entries stream=codec_type -of json -- <path>` and classify any `video` stream as video, otherwise any `audio` stream as audio. Use `--` before paths where supported by the tool and reject unsafe basenames using the existing path rules.

Build arguments with `-y`, input path, optional `-vn` for audio targets, and output path. For target containers requiring explicit defaults, add only fixed preset defaults inside the engine. Do not accept arbitrary user flags. Execute with `exec.Command`, `Setpgid: true`, and kill `-pid` on context cancellation. Injected runners must receive the exact argument slice for deterministic tests.

- [ ] **Step 4: Run focused tests**

Run: `cd backend && go test ./internal/engine -run FFmpeg -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/engine/ffmpeg.go backend/internal/engine/ffmpeg_test.go
git commit -m "feat: add FFmpeg media engine"
```

### Task 3: Generalize registry target discovery and engine routing

**Files:**
- Modify: `backend/internal/registry/registry.go`
- Test: `backend/internal/registry/registry_test.go`
- Modify: `backend/internal/formats/images.go`

**Interfaces:**
- `Registry.TargetsForFiles` must use each registered engine’s `Probe`-backed source support through a media-aware engine path while retaining image detection.
- The registry must intersect targets across all files and never return an audio-to-video target.
- Existing image-only fake engines and tests must continue compiling.

- [ ] **Step 1: Write failing registry tests**

Create minimal fake media files/probe-backed engine fixtures and assert:

```go
func TestTargetsForAudioFileExcludeVideoTargets(t *testing.T) { /* expects twelve audio targets */ }
func TestTargetsForVideoFileIncludeAudioExtraction(t *testing.T) { /* expects five video + twelve audio targets */ }
func TestTargetsForMixedFilesOnlyIncludeAudioTargets(t *testing.T) { /* expects twelve audio targets */ }
```

Assert an unavailable FFmpeg engine contributes no media targets while ImageMagick targets remain present.

- [ ] **Step 2: Run tests and verify failure**

Run: `cd backend && go test ./internal/registry -run 'Media|Audio|Video|Mixed' -v`
Expected: FAIL because target enumeration only knows image formats.

- [ ] **Step 3: Implement media-aware target enumeration**

Add ordered helpers for all image and media formats. Resolve each candidate through registered engines. For media files, use an engine-provided probe/classification interface without changing the existing `Engine` contract for image-only fakes; the registry may use an optional interface assertion such as:

```go
type MediaProber interface { Probe(path string) (engine.MediaKind, error) }
```

Keep `TargetsForFiles` returning protocol targets in `SupportedPresets` order. For a mixed set, retain only candidates supported for every source. Never synthesize video targets for audio sources.

- [ ] **Step 4: Run registry and full package tests**

Run: `cd backend && go test ./internal/registry ./internal/formats ./internal/protocol`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/registry/registry.go backend/internal/registry/registry_test.go backend/internal/formats/images.go
git commit -m "feat: route media targets through registry"
```

### Task 4: Reuse atomic jobs for media outputs

**Files:**
- Modify: `backend/internal/engine/imagemagick.go`
- Test: `backend/internal/engine/imagemagick_test.go`
- Modify: `backend/internal/jobs/manager.go`
- Test: `backend/internal/jobs/manager_test.go`

**Interfaces:**
- `engine.PlanOutput(dir, source, targetExt, conflict)` accepts every registered image/media output extension.
- Job submission normalizes aliases such as `mp4`, `webm`, `mp3`, `wav`, `flac`, `aac`, `m4a`, `ogg`, and `opus` to their canonical media presets.
- `Manager.convertFile` selects a media source classification when the route engine provides it, then uses the same temp/commit/error lifecycle.

- [ ] **Step 1: Write failing output and job tests**

Add output-plan cases for each media extension and assert media submissions reach a fake FFmpeg engine. Add a test that a video source with `audio-to-mp3` succeeds and a test that `audio-to-mp4` returns `unsupported` before a job is queued.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `cd backend && go test ./internal/engine ./internal/jobs -run 'Media|Audio|Video|PlanOutput' -v`
Expected: FAIL because output planning only accepts image extensions and manager normalization only knows image presets.

- [ ] **Step 3: Implement generalized output extension and manager routing**

Replace image-specific extension error text with a generic unsupported target extension error. Keep JPEG’s `.jpg` output behavior. Add explicit media alias normalization. Ensure the route is resolved before conversion and that route errors become per-file stable failures; preserve cancellation and atomic cleanup exactly as existing image jobs do.

- [ ] **Step 4: Run focused and regression tests**

Run: `cd backend && go test ./internal/engine ./internal/jobs -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/engine/imagemagick.go backend/internal/engine/imagemagick_test.go backend/internal/jobs/manager.go backend/internal/jobs/manager_test.go
git commit -m "feat: support media outputs in job lifecycle"
```

### Task 5: Wire FFmpeg into the daemon and protocol behavior

**Files:**
- Modify: `backend/cmd/fcd/main.go`
- Modify: `backend/internal/server/server.go`
- Test: `backend/internal/server/server_test.go`
- Modify: `backend/internal/protocol/validate.go`
- Test: `backend/internal/protocol/validate_test.go`

**Interfaces:**
- `fcd` registers ImageMagick and FFmpeg independently.
- `hello` lists both engines with independent availability values.
- `targets` delegates to the registry and returns media targets only when FFmpeg is available.
- `convert` accepts canonical media presets and returns stable `unsupported` for audio-to-video requests.

- [ ] **Step 1: Write failing server tests**

Add a server dependency fixture with fake ImageMagick and FFmpeg engines. Assert hello includes both IDs, targets include media categories when FFmpeg is available, and a manually requested audio-to-video preset returns an unsupported error.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `cd backend && go test ./internal/server ./cmd/fcd ./internal/protocol -v`
Expected: FAIL because daemon setup reports only ImageMagick and server validation does not know media routes.

- [ ] **Step 3: Wire independent engine availability**

In `cmd/fcd`, probe ImageMagick as before, probe FFmpeg separately, register both engines regardless of availability, and pass registry status to the server. Do not make an unavailable FFmpeg probe prevent startup. Keep the existing version and shutdown behavior unchanged.

- [ ] **Step 4: Run focused tests**

Run: `cd backend && go test ./internal/server ./cmd/fcd ./internal/protocol -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/cmd/fcd/main.go backend/internal/server/server.go backend/internal/server/server_test.go backend/internal/protocol/validate.go backend/internal/protocol/validate_test.go
git commit -m "feat: expose FFmpeg through daemon protocol"
```

### Task 6: Add end-to-end coverage and documentation

**Files:**
- Modify: `backend/e2e_test.go`
- Modify: `README.md`
- Modify: `docs/PROTOCOL.md`

**Interfaces:**
- E2E tests use actual FFmpeg only when both `ffmpeg` and `ffprobe` are available; otherwise they call `t.Skip` with the dependency reason.
- Documentation describes all twelve media outputs, FFmpeg/ffprobe runtime requirements, audio extraction, and independent ImageMagick fallback.

- [ ] **Step 1: Write conditional E2E tests**

Create tiny valid test media with FFmpeg or use a generated fixture, request `targets`, convert a video to MP3 and MP4, wait for `job.finished`, and assert output files exist and have the expected extensions. Assert that audio input targets do not include video presets. Do not make the test depend on ImageMagick.

- [ ] **Step 2: Run E2E tests and verify baseline behavior**

Run: `cd backend && go test ./... -run TestE2E -v`
Expected: media tests PASS when FFmpeg is installed or are explicitly SKIPped when unavailable; existing image E2E behavior remains unchanged.

- [ ] **Step 3: Update README and protocol documentation**

Document the exact canonical presets, the `ffmpeg` and `ffprobe` requirement, source classification, video-to-audio `-vn` extraction, audio-to-video exclusion, and the fact that missing FFmpeg does not disable image conversion.

- [ ] **Step 4: Run all verification**

Run:

```bash
cd backend
gofmt -w cmd/fcd/main.go internal/engine internal/formats internal/jobs internal/protocol internal/registry internal/server
go test ./...
go vet ./...
```

Expected: all tests PASS, no vet findings. If FFmpeg is unavailable, only explicitly dependency-gated E2E tests may skip.

- [ ] **Step 5: Review diff and commit**

```bash
git status --short
git diff --check
git diff --stat
git add backend/e2e_test.go README.md docs/PROTOCOL.md
git commit -m "docs: document FFmpeg media conversion"
```

## Plan self-review

- **Spec coverage:** dedicated FFmpeg engine, twelve outputs, stream-based detection, target intersections, extraction with `-vn`, audio-to-video exclusion, independent availability, cancellation, atomic outputs, protocol v1 compatibility, tests, and docs are covered by Tasks 1-6.
- **Placeholder scan:** no TBD/TODO steps; every task names exact files, commands, expected results, and interfaces.
- **Type consistency:** the plan keeps the existing `registry.Engine.Convert` signature, uses `protocol.ImageOptions` for backward compatibility, and introduces the optional media probe interface only where registry source classification needs it.
- **Scope check:** this is one cohesive backend subsystem with independently testable format, engine, registry, job, server, and E2E layers; no frontend changes are required because the existing target protocol is category-driven.
