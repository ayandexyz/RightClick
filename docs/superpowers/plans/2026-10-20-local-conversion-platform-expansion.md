# Local Conversion Platform Expansion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Evolve the local file-conversion service from image/media conversion into a reliable, configurable, batch-capable Linux conversion platform with strong Nautilus and Omarchy integration.

**Architecture:** Keep the Go daemon as the single local conversion authority and preserve the NDJSON protocol over the Unix socket. Extend the existing format registry, engine interfaces, job manager, and frontend clients rather than introducing a second conversion path. Add capabilities in independently testable phases: safe job execution, menu policy, batch inputs, engine expansion, then additional file-manager integrations.

**Tech Stack:** Go, FFmpeg/ffprobe, ImageMagick, optional LibreOffice, optional PDF rendering engine, Unix-domain sockets, NDJSON, Python Nautilus extension, QML Omarchy plugin, systemd user service.

## Global Constraints

- Conversions remain local; files must never be uploaded or sent to a network service.
- The daemon remains the source of truth for format detection, target discovery, job state, cancellation, and output publication.
- Existing protocol clients must continue to work unless a protocol change is explicitly versioned and tested.
- Existing image, video, and audio behavior must remain compatible with current presets.
- Missing optional engines disable only the formats they provide; they must not prevent the daemon from starting.
- Inputs must be absolute regular files unless a later batch-input task explicitly expands directories.
- Outputs must never silently overwrite an existing file.
- Every behavior change requires focused unit tests and a real conversion or integration check where the required executable is available.
- No cloud account, billing, licensing, browser-extension, or web-dashboard work is included in this roadmap.

---

## Current Architecture and File Map

The implementation must follow the existing boundaries instead of creating parallel abstractions.

| Area | Existing location | Responsibility |
|---|---|---|
| Protocol types and validation | `backend/internal/protocol/` | Requests, responses, events, target/job structures, validation, supported presets |
| Format detection | `backend/internal/formats/` | Format identifiers, MIME/category helpers, extension/content detection, input validation |
| Engine execution | `backend/internal/engine/` | ImageMagick, FFmpeg, subprocess lifecycle, media probing |
| Target routing | `backend/internal/registry/registry.go` | Engine registration, availability, source detection, target calculation |
| Job lifecycle | `backend/internal/jobs/manager.go` | Queueing, cancellation, progress events, per-file state |
| Socket server | `backend/internal/server/` and `backend/internal/transport/` | Unix socket serving and protocol dispatch |
| CLI | `backend/cmd/fcctl/` | Development and diagnostic client |
| Nautilus | `frontends/omarchy/nautilus/` | Dynamic context-menu target discovery and conversion activation |
| Omarchy plugin | `frontends/omarchy/plugin/` | Progress display, job state, panel/bar interaction |
| Installation | `frontends/omarchy/install.sh` | Backend, systemd service, Nautilus files, plugin installation |
| Tests | `backend/**/*_test.go`, `backend/e2e_test.go`, `frontends/omarchy/tests/` | Unit, protocol, engine, integration, and frontend behavior |

---

## Phase 1: Reliability and Diagnostics

### Task 1: Define a stable conversion/output policy

**Files:**
- Modify: `backend/internal/protocol/types.go`
- Modify: `backend/internal/protocol/validate.go`
- Modify: `backend/internal/jobs/manager.go`
- Modify: `backend/internal/engine/imagemagick.go`
- Modify: `backend/internal/engine/ffmpeg.go`
- Test: `backend/internal/protocol/validate_test.go`
- Test: `backend/internal/jobs/manager_test.go`
- Test: `backend/internal/engine/imagemagick_test.go`
- Test: `backend/internal/engine/ffmpeg_test.go`

**Interfaces:**
- Add or preserve `protocol.ConflictPolicy` values `rename`, `skip`, and `overwrite`.
- Extend submit parameters with an optional output directory and conflict policy while retaining current defaults.
- Keep the default policy `rename`; existing callers must not overwrite files.

- [ ] Write failing tests proving that the default policy creates a numbered sibling when the intended output exists.
- [ ] Write failing tests proving `skip` produces a completed file entry with `State == "skipped"` and no conversion process.
- [ ] Write failing tests proving `overwrite` is explicit and does not happen under the default policy.
- [ ] Run:

```bash
cd backend
go test ./internal/jobs ./internal/protocol ./internal/engine
```

Expected: the new policy tests fail before implementation.
- [ ] Implement one output-path allocator shared by ImageMagick and FFmpeg jobs. It must use the target extension, preserve the source stem, and choose `name-1.ext`, `name-2.ext`, and so on when needed.
- [ ] Validate that an output path cannot equal an input path and that output directories are created before conversion.
- [ ] Run the focused tests and then:

```bash
cd backend
go test ./...
go vet ./...
```

Expected: all tests pass.
- [ ] Perform a real image and video conversion into a directory containing a colliding output and verify that the original file remains unchanged.

### Task 2: Add staging and atomic publication for every engine

**Files:**
- Modify: `backend/internal/engine/process.go`
- Modify: `backend/internal/engine/imagemagick.go`
- Modify: `backend/internal/engine/ffmpeg.go`
- Modify: `backend/internal/jobs/manager.go`
- Test: `backend/internal/engine/process_test.go`
- Test: `backend/internal/jobs/manager_test.go`
- Test: `backend/e2e_test.go`

**Interfaces:**
- Engines continue to write to a destination supplied by the manager.
- The manager owns temporary staging directories and final publication.

- [ ] Add a test where a converter writes a partial output and returns an error; assert that no final output remains.
- [ ] Add a test where cancellation occurs during execution; assert that temporary files are removed and the file state is `cancelled`.
- [ ] Run the focused tests and observe failure.
- [ ] Create a temporary staging directory on the same filesystem as the final destination.
- [ ] Run each engine against a staging path, validate that the expected output exists and is a regular file, then publish with an atomic rename.
- [ ] Remove staging with `defer` on success, error, and cancellation.
- [ ] Preserve output metadata in the protocol event only after publication succeeds.
- [ ] Run:

```bash
cd backend
go test ./...
go vet ./...
```

- [ ] Run a real conversion and inspect both the final output and the destination directory for leftover temporary files.

### Task 3: Add engine availability diagnostics

**Files:**
- Modify: `backend/internal/protocol/types.go`
- Modify: `backend/internal/protocol/validate.go`
- Modify: `backend/internal/registry/registry.go`
- Modify: `backend/cmd/fcctl/main.go`
- Modify: `docs/PROTOCOL.md`
- Test: `backend/internal/registry/registry_test.go`
- Test: `backend/cmd/fcctl/main_test.go`

**Interfaces:**
- Add a read-only `engines` request returning entries shaped like:

```json
{
  "id": "ffmpeg",
  "available": true,
  "reason": ""
}
```

- Preserve the existing `hello`, `targets`, `convert`, and `cancel` methods.

- [ ] Add registry tests for available and unavailable engines with stable IDs and reasons.
- [ ] Add protocol tests for the `engines` response.
- [ ] Run the focused tests and verify the new tests fail.
- [ ] Implement `Registry.Status()` so each registered engine reports its availability reason without preventing other engines from registering.
- [ ] Add `fcctl engines` and document its output.
- [ ] Run:

```bash
cd backend
go test ./...
go vet ./...
go build -o /tmp/fcd ./cmd/fcd
go build -o /tmp/fcctl ./cmd/fcctl
```

- [ ] Run `/tmp/fcctl engines` against the installed daemon and verify `ffmpeg`, `ffprobe`, and `magick` status.

---

## Phase 2: Configurable Context Menus

### Task 4: Add menu policy to the daemon

**Files:**
- Create: `backend/internal/menu/menu.go`
- Create: `backend/internal/menu/menu_test.go`
- Modify: `backend/internal/protocol/types.go`
- Modify: `backend/internal/registry/registry.go`
- Modify: `backend/internal/server/server.go`
- Modify: `backend/internal/jobs/manager.go`
- Modify: `docs/PROTOCOL.md`

**Interfaces:**
- Store menu policy in `$XDG_CONFIG_HOME/file-converter/menu.toml`, falling back to `$HOME/.config/file-converter/menu.toml`.
- Use category keys `image`, `video`, `audio`, `pdf`, `document`, `presentation`, and `spreadsheet`.
- Expose a target-discovery option that returns menu targets rather than every reachable target.
- Default menu targets:

```toml
image = ["jpeg", "png", "webp"]
video = ["mp4", "mov", "webm", "gif"]
audio = ["mp3", "m4a", "wav"]
```

- [ ] Write parser tests for valid lists, unknown categories, unknown formats, duplicate formats, malformed values, missing files, and atomic saves.
- [ ] Run:

```bash
cd backend
go test ./internal/menu
```

Expected: parser tests fail before the package exists.
- [ ] Implement strict parsing for known categories and format IDs; ignore unknown future IDs while rejecting malformed TOML values.
- [ ] Implement `Load`, `Save`, `Set`, `Reset`, `Effective`, and `TargetsForSource` functions.
- [ ] Ensure an empty or unreachable customized list falls back to the category defaults so menus never disappear unexpectedly.
- [ ] Add protocol methods for reading and updating menu choices, or expose the policy through `fcctl menu show`, `fcctl menu set`, and `fcctl menu reset`.
- [ ] Run all backend tests and verify a configured video menu through `fcctl targets --menu`.

### Task 5: Wire menu policy into Nautilus and Omarchy

**Files:**
- Modify: `backend/internal/registry/registry.go`
- Modify: `frontends/omarchy/nautilus/fc_client/client.py`
- Modify: `frontends/omarchy/nautilus/file_converter.py`
- Modify: `frontends/omarchy/plugin/Service.qml`
- Modify: `frontends/omarchy/tests/test_client.py`
- Modify: `frontends/omarchy/tests/test_extension_menu.py`
- Test: `frontends/omarchy/tests/test_plugin_contract.py` if a plugin contract test is added

**Interfaces:**
- Nautilus continues to request targets for selected files, but the daemon returns only policy-approved reachable targets.
- Mixed selections use the intersection of reachable menu targets.

- [ ] Add a Python test proving that a video selection displays only the configured video targets.
- [ ] Add a Python test proving a mixed image/audio selection does not display incompatible targets.
- [ ] Run:

```bash
python3 -m unittest discover -s frontends/omarchy/tests -v
```

Expected: new tests fail until the client and daemon contract is wired.
- [ ] Update the client only if the protocol adds a distinct menu-target method; do not duplicate target filtering in Python.
- [ ] Keep MIME filtering broad enough for all daemon-supported categories and add a regression test for each newly supported category.
- [ ] Ensure the Omarchy UI displays the target label returned by the daemon and does not maintain its own hard-coded format list.
- [ ] Reinstall with:

```bash
./frontends/omarchy/install.sh
nautilus -q
```

- [ ] Run all Go and Python tests and verify a configured video right-click menu manually.

---

## Phase 3: Batch and Folder Conversion

### Task 6: Add folder expansion and recursive input policy

**Files:**
- Create: `backend/internal/batch/batch.go`
- Create: `backend/internal/batch/batch_test.go`
- Modify: `backend/internal/protocol/types.go`
- Modify: `backend/internal/protocol/validate.go`
- Modify: `backend/internal/jobs/manager.go`
- Modify: `backend/cmd/fcctl/main.go`
- Modify: `docs/PROTOCOL.md`

**Interfaces:**
- Add submit options:

```go
type BatchOptions struct {
    Recursive bool `json:"recursive"`
}
```

- Folder expansion returns sorted `BatchItem` values containing the input path, whether it was explicit, and its folder root.
- Hidden files and hidden directories are skipped; symlinked directories are not followed.
- `--out-dir` preserves each input folder’s relative path.

- [ ] Write tests for top-level folder expansion, recursive expansion, hidden entries, symlink directories, stable ordering, explicit files, and relative output directories.
- [ ] Run the focused tests and observe failure.
- [ ] Implement `ExpandInputs(paths []string, recursive bool) ([]BatchItem, error)` with deterministic lexical ordering.
- [ ] Update submit validation so explicit files report errors while unsupported files found in folders become skipped file entries with a reason.
- [ ] Add `fcctl convert folder/ --recursive --out-dir destination`.
- [ ] Run:

```bash
cd backend
go test ./...
go vet ./...
```

- [ ] Perform a real recursive image batch and verify that output subdirectories mirror the input tree.

### Task 7: Add bounded batch concurrency and video serialization

**Files:**
- Modify: `backend/internal/jobs/manager.go`
- Modify: `backend/internal/protocol/types.go`
- Modify: `frontends/omarchy/plugin/Service.qml`
- Modify: `frontends/omarchy/plugin/Panel.qml`
- Modify: `frontends/omarchy/plugin/BarWidget.qml`
- Test: `backend/internal/jobs/manager_test.go`
- Test: `backend/internal/jobs/cancel_test.go`

**Interfaces:**
- Add a bounded concurrency setting with a safe default based on CPU count.
- Video jobs use a single serialization lane; image/audio jobs may use the remaining workers.
- Events retain per-file state and aggregate job progress.

- [ ] Add tests proving that no more than the configured number of jobs run concurrently.
- [ ] Add tests proving that two video conversions never overlap.
- [ ] Add tests proving queued jobs become `cancelled` after cancellation and running jobs receive context cancellation.
- [ ] Run the focused tests and observe failure.
- [ ] Implement worker scheduling with a semaphore and a dedicated video mutex/channel.
- [ ] Preserve input order in the final result list even when workers finish out of order.
- [ ] Add Omarchy display fields for completed, skipped, failed, and cancelled counts.
- [ ] Add a cancel action that sends the existing `cancel` protocol request for the active job.
- [ ] Run all backend and frontend tests, then perform a real batch with at least one intentionally unsupported file.

---

## Phase 4: Conversion Options and Media Quality

### Task 8: Expand conversion options beyond image options

**Files:**
- Modify: `backend/internal/protocol/types.go`
- Modify: `backend/internal/protocol/validate.go`
- Modify: `backend/internal/engine/ffmpeg.go`
- Modify: `backend/internal/jobs/manager.go`
- Modify: `backend/cmd/fcctl/main.go`
- Test: `backend/internal/protocol/validate_test.go`
- Test: `backend/internal/engine/ffmpeg_test.go`
- Test: `backend/internal/jobs/manager_test.go`
- Modify: `docs/PROTOCOL.md`

**Interfaces:**
- Extend conversion options with:

```go
type MediaOptions struct {
    Quality      *int   `json:"quality,omitempty"`
    VideoHeight  *int   `json:"video_height,omitempty"`
    VideoCodec   string `json:"video_codec,omitempty"`
    AudioBitrate *int   `json:"audio_bitrate,omitempty"`
    StripAudio   bool   `json:"strip_audio,omitempty"`
}
```

- Accept video codecs `h264` and `hevc` only.
- Accept video heights greater than zero and audio bitrates within the documented safe range.
- Keep current preset defaults when options are omitted.

- [ ] Add validation tests for accepted and rejected quality, height, codec, and bitrate values.
- [ ] Add FFmpeg command-construction tests for MP4, WebM, GIF, audio extraction, and `strip_audio`.
- [ ] Run focused tests and verify failures.
- [ ] Implement option validation before a job enters the queue.
- [ ] Map quality to codec-specific CRF values, use even dimensions for video scaling, enable fast-start for MP4/MOV, and use palette generation for GIF.
- [ ] Use ffprobe duration when available for progress; fall back to indeterminate progress when duration cannot be read.
- [ ] Convert an input with no audio stream to an audio target into a clear `no audio track` error.
- [ ] Run:

```bash
cd backend
go test ./...
go vet ./...
```

- [ ] Perform real conversions at two resolutions and verify output streams with `ffprobe`.

---

## Phase 5: Additional Engines and Formats

### Task 9: Add PDF conversion

**Files:**
- Modify: `backend/internal/formats/`
- Create or modify: `backend/internal/engine/pdf.go`
- Modify: `backend/internal/registry/registry.go`
- Modify: `backend/internal/protocol/`
- Test: `backend/internal/engine/pdf_test.go`
- Test: `backend/internal/registry/registry_test.go`
- Modify: `docs/PROTOCOL.md`

**Interfaces:**
- Add PDF as its own category.
- Support PDF-to-PNG/JPEG for selected pages and image-to-PDF where the installed engine supports it.
- Treat the PDF renderer as optional and report its absence through `engines`.

- [ ] Add format and target tests before implementation.
- [ ] Add page-selection options with a documented syntax such as `1-3` and reject malformed or out-of-range values.
- [ ] Use a subprocess or dynamically loaded renderer behind an engine interface; never make PDF support a requirement for image/media startup.
- [ ] Stage all page outputs before publication and preserve page order.
- [ ] Run engine tests with a skip when the optional executable/library is unavailable, then run a real conversion when available.

### Task 10: Add office/document conversion through an optional engine

**Files:**
- Create or modify: `backend/internal/engine/office.go`
- Modify: `backend/internal/formats/`
- Modify: `backend/internal/registry/registry.go`
- Modify: `backend/internal/protocol/`
- Test: `backend/internal/engine/office_test.go`
- Test: `backend/internal/registry/registry_test.go`
- Modify: `docs/PROTOCOL.md`

**Interfaces:**
- Add document, presentation, and spreadsheet categories.
- Support conversions exposed by the installed headless office engine, initially document/presentation/spreadsheet to PDF.
- Report `libreoffice unavailable` rather than failing daemon startup.

- [ ] Add format identifiers and MIME mappings for DOCX, DOC, ODT, RTF, TXT, HTML, PPTX, PPT, ODP, XLSX, XLS, ODS, and CSV.
- [ ] Add registry tests proving targets appear only when the engine is available.
- [ ] Run tests and verify failure before implementation.
- [ ] Implement isolated subprocess execution with a temporary profile directory, explicit output directory, timeout, cancellation, and cleanup.
- [ ] Prevent the office process from writing outside the staging directory.
- [ ] Run all tests and a real conversion for each supported document category when the engine exists.

### Task 11: Expand image and vector formats

> **Status:** SVG input (SVG to PNG/JPEG/WebP via `rsvg-convert`) is done; see `2026-10-20-svg-rasterization.md`. Still open: SVG to PDF, TGA, PPM/PGM/PBM, QOI, EXR.

**Files:**
- Modify: `backend/internal/formats/`
- Modify: `backend/internal/engine/imagemagick.go`
- Create or modify: `backend/internal/engine/svg.go`
- Modify: `backend/internal/registry/registry.go`
- Test: `backend/internal/formats/*_test.go`
- Test: `backend/internal/engine/imagemagick_test.go`
- Test: `backend/internal/engine/svg_test.go`

**Interfaces:**
- Add SVG, TGA, PPM/PGM/PBM, QOI, and EXR only when the installed engine can actually perform the route.
- Keep raster-to-video/audio routes unavailable unless an explicit direct engine supports them.

- [ ] Add format lookup and MIME tests.
- [ ] Add target tests for SVG to PNG/JPEG/PDF and additional raster formats.
- [ ] Run tests before implementation.
- [ ] Implement capability checks from the installed ImageMagick build instead of assuming every delegate is present.
- [ ] Add a dedicated SVG engine if rasterization requires a renderer not provided by ImageMagick.
- [ ] Run a real conversion matrix for every available new format and verify target filtering does not advertise unsupported routes.

---

## Phase 6: Additional Linux File-Manager Integrations

### Task 12: Extract shared static-menu generation

**Files:**
- Create: `frontends/linux/menu_generator.py`
- Create: `frontends/linux/test_menu_generator.py`
- Modify: `frontends/omarchy/install.sh`
- Create: `frontends/linux/install.py`
- Create: `frontends/linux/uninstall.py`

**Interfaces:**
- The generator consumes daemon format/target JSON and produces only user-owned files.
- Every generated file contains a versioned ownership marker.
- Uninstall removes only files carrying the ownership marker and preserves unrelated configuration.

- [ ] Write tests for target grouping, MIME grouping, shell quoting, path-with-spaces handling, generated markers, and preservation of unrelated actions.
- [ ] Run:

```bash
python3 -m unittest frontends/linux/test_menu_generator.py -v
```

Expected: tests fail before the generator exists.
- [ ] Implement shared command construction using `fcctl`/daemon target data, never a duplicated hard-coded format list.
- [ ] Implement safe atomic writes and mode `0700`/`0644` according to each file-manager requirement.
- [ ] Run focused tests and inspect generated files manually.

### Task 13: Add Thunar, Nemo, and Dolphin integrations

**Files:**
- Modify: `frontends/linux/install.py`
- Modify: `frontends/linux/uninstall.py`
- Create: `frontends/linux/tests/test_install.py`
- Modify: `README.md`

**Interfaces:**
- Support explicit flags `--nautilus`, `--thunar`, `--nemo`, and `--dolphin`.
- With no flags, install only integrations for detected file managers.
- All commands must pass selected paths safely and support spaces, quotes, and non-ASCII names.

- [ ] Add tests for generated Thunar XML, Nemo action files, and Dolphin service menus.
- [ ] Add tests proving uninstall preserves unrelated user actions.
- [ ] Run tests before implementation.
- [ ] Implement one generated action per target and a final “More options” action for the full conversion UI.
- [ ] Make static integrations invoke the application/CLI with selected files and a target, while Nautilus continues using the dynamic extension.
- [ ] Update installation output with exact restart instructions for each detected file manager.
- [ ] Run:

```bash
python3 -m unittest discover -s frontends/linux/tests -v
```

- [ ] Verify each generated integration in its file manager when installed; report unavailable desktop environments without treating them as code failures.

---

## Phase 7: Packaging and Completion Gates

### Task 14: Make installation reproducible and observable

**Files:**
- Modify: `frontends/omarchy/install.sh`
- Modify: `frontends/omarchy/uninstall.sh`
- Modify: `frontends/omarchy/systemd/fcd.service`
- Modify: `README.md`
- Create: `docs/INSTALL.md`
- Create: `docs/VERIFICATION.md`

**Interfaces:**
- Installation must verify required binaries, install the daemon and clients, install the systemd user unit, and report optional engine omissions.
- Uninstallation must stop/disable the user service and remove only project-owned files.

- [ ] Add shell checks for `go` build artifacts, `ffmpeg`, `ffprobe`, and `magick`/`convert`.
- [ ] Add a dry-run or verification mode that does not modify user state.
- [ ] Document service status, socket location, Nautilus restart, plugin validation, and manual `fcctl` checks.
- [ ] Run installation in a temporary HOME where possible, then run uninstall and verify unrelated files remain.
- [ ] Run the complete verification suite:

```bash
cd backend
go test ./...
go vet ./...
go build ./cmd/fcd ./cmd/fcctl
cd ..
python3 -m unittest discover -s frontends/omarchy/tests -v
./frontends/omarchy/install.sh
systemctl --user is-active fcd.service
```

- [ ] Perform real image, video, audio, batch, collision, cancellation, and configured-menu checks before calling the phase complete.

---

## Recommended Delivery Order

Implement and verify in this order:

1. Safe output naming and atomic publication
2. Engine diagnostics
3. Configurable menu policy
4. Folder and recursive batch conversion
5. Bounded concurrency and cancellation UI
6. Media quality/options
7. PDF support
8. Office/document support
9. Additional image/vector formats
10. Thunar/Nemo/Dolphin integrations
11. Installation, verification, and packaging polish

Each task should be reviewed and committed independently. Do not begin the next phase until the preceding phase's focused tests and real-run checks pass.

## Explicitly Out of Scope

The following are intentionally excluded from this implementation plan:

- Remote conversion APIs or file uploads
- User accounts, authentication, subscriptions, billing, or license enforcement
- Browser extensions
- Web dashboards or hosted job storage
- Cross-platform desktop application rewrites
- Automatic installation of system packages without user consent
- Destructive cleanup of unrelated file-manager configuration
- Silent replacement of the current Go daemon with another runtime
