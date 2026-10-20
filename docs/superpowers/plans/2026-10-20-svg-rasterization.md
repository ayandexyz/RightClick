# SVG Rasterization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add secure local SVG input support with SVG-to-PNG, SVG-to-JPEG, and SVG-to-WebP conversion through the existing daemon and Nautilus integration.

**Architecture:** Treat SVG as a distinct vector input format. Add a dedicated renderer engine backed by an installed `rsvg-convert` executable, with validation performed before execution and argument-array process invocation. The renderer emits PNG; the existing ImageMagick engine converts that intermediate to JPEG/WebP, while the existing job manager handles temporary files and atomic publication.

**Tech Stack:** Go backend, `rsvg-convert`/librsvg, ImageMagick, Unix-socket protocol v1, Python Nautilus extension, Go/Python tests.

## Global Constraints

- Raster-to-SVG remains unsupported.
- SVG detection is content-aware and must not rely only on the extension.
- SVG input must reject XML external entities/DTDs, scripts/event handlers, external file/HTTP/HTTPS/data references, traversal, malformed XML, and oversized files.
- Renderer commands use argument arrays and never shell interpolation.
- No network access or embedded-resource downloading.
- Existing image, video, and audio behavior must remain unchanged.
- Output publication continues through controlled temporary files and atomic rename.

---

### Task 1: Add SVG format detection and protocol presets

**Files:**
- Modify: `backend/internal/formats/images.go`
- Test: `backend/internal/formats/images_test.go`
- Modify: `backend/internal/protocol/validate.go`
- Test: `backend/internal/protocol/validate_test.go`
- Modify: `backend/internal/jobs/manager.go`

**Interfaces:**
- Produces `formats.SVG` and `formats.IsSVGFormat(format formats.Format) bool`.
- Produces `formats.PresetFormat("svg-to-png")`, `formats.PresetFormat("svg-to-jpeg")`, and `formats.PresetFormat("svg-to-webp")`.

- [ ] Add `SVG Format = "svg"` without adding it to raster `IsImageFormat`.
- [ ] Extend `Detect` to read a bounded prefix and identify an SVG root element after optional UTF-8 BOM, XML declaration, comments, and whitespace. Require an SVG namespace or an unambiguous `<svg` root; return `SVG` only for valid XML-shaped input.
- [ ] Add `.svg` to `FormatFromExtension` only as a hint for media probing; content detection remains authoritative.
- [ ] Add `IsSVGFormat` and ensure `IsImageFormat(SVG)` remains false so ImageMagick does not claim SVG directly.
- [ ] Add three protocol targets with labels `SVG to PNG`, `SVG to JPEG`, and `SVG to WebP`, category `image`, and formats `png`, `jpeg`, and `webp`.
- [ ] Update target preset normalization in `manager.go` so user target `png`, `jpeg`, or `webp` is not incorrectly converted into an SVG preset; explicit SVG presets remain accepted.
- [ ] Write failing tests for valid SVG detection, extension mismatch, malformed SVG rejection, preset mappings, and raster-to-SVG absence.
- [ ] Run `go test ./internal/formats ./internal/protocol ./internal/jobs`; expected result is PASS after implementation.
- [ ] Commit: `feat: recognize SVG inputs and presets`.

### Task 2: Implement bounded SVG safety validation

**Files:**
- Create: `backend/internal/engine/svg.go`
- Test: `backend/internal/engine/svg_test.go`
- Modify: `backend/internal/formats/images.go` only if shared validation helpers are required

**Interfaces:**
- `type SVG struct { executable string; runner Runner; maxBytes int64 }`.
- `func NewSVG(runner Runner) *SVG`.
- `func (e *SVG) ID() string` returns `"svg"`.
- `func (e *SVG) Available() (string, error)` locates `rsvg-convert`.
- `func (e *SVG) Supports(source, target formats.Format) bool` accepts only SVG source and PNG/JPEG/WebP targets.
- `func (e *SVG) Convert(ctx context.Context, source string, sourceFormat formats.Format, destination string, target formats.Format, options protocol.ImageOptions) error`.
- `func ValidateSVG(path string, maxBytes int64) error`.

- [ ] Write tests first for absolute regular-file validation, size limits, malformed XML, `DOCTYPE`, `ENTITY`, `<script>`, `onload=`, `href="file:..."`, `href="http..."`, `href="https..."`, `href="data:..."`, and `../` references.
- [ ] Implement bounded file reading using `io.LimitReader`; reject files exceeding the configured maximum before parsing.
- [ ] Parse XML with Go's `encoding/xml` after scanning forbidden constructs. Reject external entities and DTDs before parsing; reject script elements, event-handler attributes, external/data references, and traversal-like resource paths.
- [ ] Keep the validator independent of the renderer executable so unsafe files fail consistently even when `rsvg-convert` is mocked.
- [ ] Run `go test ./internal/engine`; expected result is PASS.
- [ ] Commit: `feat: validate SVG input safely`.

### Task 3: Add dedicated SVG renderer command construction

**Files:**
- Modify: `backend/internal/engine/svg.go`
- Test: `backend/internal/engine/svg_test.go`

**Interfaces:**
- The injected `Runner` receives executable and exact args without shell quoting.
- Expected command shape: `rsvg-convert --format png --output <destination> <source>` for PNG output and for the intermediate stage of JPEG/WebP output.
- The focused ImageMagick helper receives an intermediate PNG path and final target path, and invokes ImageMagick with `sourceFormat=formats.PNG` so SVG is never passed to ImageMagick.

- [ ] Add tests proving `Supports` rejects raster/video/audio sources and unsupported targets, and accepts SVG to PNG/JPEG/WebP.
- [ ] Add tests asserting all conversions invoke `rsvg-convert` with `--format png`, `--output`, destination, and source, regardless of spaces or shell metacharacters in paths.
- [ ] Implement `Convert` to validate source, resolve the executable, choose PNG as the renderer output format, and invoke the runner with argument slices.
- [ ] For JPEG/WebP targets, always render a controlled PNG intermediate with `rsvg-convert`, then invoke ImageMagick on that PNG intermediate through a focused conversion helper. Do not route SVG through generic ImageMagick input handling.
- [ ] Return clear errors for missing renderer, invalid target, validation failure, and renderer failure.
- [ ] Run `go test ./internal/engine`; expected result is PASS.
- [ ] Commit: `feat: render SVG through dedicated engine`.

### Task 4: Integrate SVG engine with registry and image output conversion

**Files:**
- Modify: `backend/cmd/fcd/main.go`
- Modify: `backend/internal/registry/registry.go`
- Modify: `backend/internal/jobs/manager.go` if engine selection needs explicit SVG routing
- Test: `backend/internal/registry/registry_test.go`
- Test: `backend/internal/jobs/manager_test.go`

**Interfaces:**
- Registry resolves SVG sources to SVG engine routes and does not resolve raster sources to SVG targets.
- Existing `Engine` interface remains unchanged.

- [ ] Register the SVG engine alongside ImageMagick and FFmpeg, preserving existing engine ordering and availability behavior.
- [ ] Add SVG to supported-format discovery without adding it to normal raster output candidates.
- [ ] Ensure SVG target discovery returns the three SVG presets only for SVG inputs and does not expose them for raster, video, or audio inputs.
- [ ] Ensure existing raster GIF remains `image-to-gif` and is not confused with SVG-to-raster presets.
- [ ] If JPEG/WebP requires a second engine stage, add a small registry/job conversion path that keeps the SVG intermediate in the controlled temporary directory and publishes only the final output.
- [ ] Add registry and job tests for SVG target discovery, unavailable renderer behavior, mixed-input intersection, and raster-to-SVG rejection.
- [ ] Run `go test ./internal/registry ./internal/jobs ./...`; expected result is PASS.
- [ ] Commit: `feat: integrate SVG conversion into jobs`.

### Task 5: Add Nautilus SVG support and documentation

**Files:**
- Modify: `frontends/omarchy/nautilus/file_converter.py`
- Test: `frontends/omarchy/tests/test_extension_menu.py`
- Modify: `README.md`
- Modify: `docs/PROTOCOL.md`

**Interfaces:**
- Nautilus recognizes `image/svg+xml` and common SVG MIME aliases if present.
- Existing target ordering and disabled-target filtering remain unchanged.

- [ ] Add SVG MIME types to the supported selection set.
- [ ] Add a frontend test that mocks SVG targets and asserts the Convert to menu exposes all enabled SVG targets.
- [ ] Document SVG presets and the dedicated renderer dependency in README and protocol docs.
- [ ] Explicitly document that raster-to-SVG is unsupported and that external SVG resources are rejected.
- [ ] Run `python3 -m unittest discover -s frontends/omarchy/tests`; expected result is PASS.
- [ ] Commit: `docs: expose SVG conversion in Linux integration`.

### Task 6: Verify real conversions and release readiness

**Files:**
- Modify: `backend/e2e_test.go`
- Test fixtures: use temporary SVG files created inside the test; do not add binary fixtures.
- Modify: `README.md` only for verified dependency/setup commands if needed.

- [ ] Add a real SVG fixture containing a rectangle and circle with explicit dimensions.
- [ ] Add an end-to-end SVG-to-PNG test that checks the PNG signature and non-zero output.
- [ ] Add conditional SVG-to-JPEG and SVG-to-WebP checks when ImageMagick write capabilities are available; unavailable optional formats must be skipped with an explicit reason.
- [ ] Add negative end-to-end checks for a malicious SVG with a DOCTYPE and external reference.
- [ ] Run `go test ./...`.
- [ ] Run `go vet ./...`.
- [ ] Run `python3 -m unittest discover -s frontends/omarchy/tests`.
- [ ] Run `git diff --check` and inspect `git diff --stat`.
- [ ] Reinstall locally with `./frontends/omarchy/install.sh` only after all tests pass.
- [ ] Confirm `fcd.service` is active and `fcctl targets /absolute/path/example.svg` lists SVG-to-PNG/JPEG/WebP.
- [ ] Commit: `test: verify SVG conversion end to end`.
