# PDF Rasterization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add secure local PDF-to-image conversion that renders every PDF page at 150 DPI into numbered PNG, JPEG, or WebP outputs through the existing daemon and Nautilus integration.

**Architecture:** Treat PDF as a distinct document input format and register a dedicated Poppler `pdftocairo` engine. Because the existing `Engine.Convert` interface publishes one destination per source while PDFs produce multiple pages, add a focused multi-output engine interface used only by the job manager; it renders into a controlled temporary directory, returns ordered page files, and lets the manager create and atomically commit one output plan per page. Use `pdftocairo` directly for PNG/JPEG and render PNG intermediates followed by the existing ImageMagick conversion for WebP, never passing PDF input to ImageMagick.

**Tech Stack:** Go, Poppler `pdftocairo`, ImageMagick for WebP page conversion, Unix-socket NDJSON protocol v1, Python Nautilus extension, Go/Python tests.

## Global Constraints

- PDF is a distinct input format and must not be added to `IsImageFormat`.
- Supported presets are exactly `pdf-to-png`, `pdf-to-jpeg`, and `pdf-to-webp`.
- Render every page by default at exactly 150 DPI; no page-range or custom-DPI options in this milestone.
- Output names preserve the source stem and append page numbers: `document-1.png`, `document-2.png`, and `document-3.png` for a three-page document.
- Require absolute regular input paths, bounded PDF input, and argument-array subprocess execution with no shell interpolation.
- Require `pdftocairo` for all PDF conversions; require ImageMagick only for PDF-to-WebP.
- Do not route PDF input through ImageMagick or add PDF generation, OCR, editing, optimization, or cloud conversion.
- Preserve existing image, SVG, video, audio, conflict, cancellation, atomic publication, and frontend behavior.
- Run `go test ./...`, `go vet ./...`, `python3 -m unittest discover -s frontends/omarchy/tests`, and `git diff --check` before completion.

---

### Task 1: Add PDF format detection and protocol presets

**Files:**
- Modify: `backend/internal/formats/images.go`
- Test: `backend/internal/formats/images_test.go`
- Modify: `backend/internal/protocol/validate.go`
- Test: `backend/internal/protocol/validate_test.go`
- Modify: `backend/internal/engine/imagemagick.go`
- Test: `backend/internal/jobs/manager_test.go`

**Interfaces:**
- Add `formats.PDF Format = "pdf"`.
- Add `formats.IsPDFFormat(format Format) bool`.
- Extend `Detect` to recognize a file beginning with `%PDF-` as `PDF` after regular-file validation.
- Extend `FormatFromExtension` with `.pdf` as a media-probing hint only.
- Add protocol targets `{pdf-to-png, PDF to PNG, png, image}`, `{pdf-to-jpeg, PDF to JPEG, jpeg, image}`, and `{pdf-to-webp, PDF to WebP, webp, image}`.
- Extend `PresetFormat` with the three explicit PDF presets. Do not make generic `png`, `jpeg`, or `webp` aliases select PDF presets.
- Keep `IsImageFormat(PDF) == false` and extend `supportedOutputExtension` only if the job layer needs to validate PDF-derived output; PDF itself is not an output extension.

- [ ] **Step 1: Write failing format tests.** Add table cases asserting `%PDF-1.7` detects as `PDF`, arbitrary `.pdf` content does not detect as PDF, `.pdf` maps through `FormatFromExtension`, `PDF` is not an image, and `IsPDFFormat(PDF)` is true.
- [ ] **Step 2: Run the focused tests.** Run `go test ./internal/formats ./internal/protocol`; expected failure is missing `PDF`, helper, and presets.
- [ ] **Step 3: Implement minimal detection and presets.** Check the PDF signature before other content fallbacks and add the three protocol entries in stable ordering after SVG entries.
- [ ] **Step 4: Add protocol tests.** Assert all three presets validate, map to the expected target format, and appear in `SupportedPresets` with the exact labels and category.
- [ ] **Step 5: Run focused tests and commit.** Run `go test ./internal/formats ./internal/protocol`; commit:

```bash
git add backend/internal/formats/images.go backend/internal/formats/images_test.go backend/internal/protocol/validate.go backend/internal/protocol/validate_test.go
git commit -m "feat: recognize PDF inputs and presets"
```

### Task 2: Define safe multi-page output planning in the job layer

**Files:**
- Modify: `backend/internal/registry/registry.go`
- Modify: `backend/internal/jobs/manager.go`
- Test: `backend/internal/jobs/manager_test.go`
- Test: `backend/internal/registry/registry_test.go`
- Modify: `backend/internal/engine/imagemagick.go`
- Test: `backend/internal/engine/imagemagick_test.go`

**Interfaces:**
- Add to `backend/internal/registry`:

```go
type MultiOutputEngine interface {
    Engine
    ConvertMany(context.Context, string, formats.Format, string, formats.Format, protocol.ImageOptions) ([]string, error)
}
```

  The destination argument is a controlled temporary directory, and the returned paths are ordered page files owned by the engine.
- Add `engine.PlanOutputPath(dir, source, targetExt, page int, conflict protocol.ConflictPolicy) (engine.OutputPlan, error)` or an equivalent focused allocator that produces `stem-<page>.<ext>` and preserves existing `(1)` conflict suffix behavior.
- Keep `Engine.Convert` unchanged for all existing engines.

- [ ] **Step 1: Write failing allocator and routing tests.** Test that a PDF source and PNG target produce `document-1.png`, `document-2.png`, avoid existing files according to `rename`, skip when requested, reject unsafe stems, and never overwrite the source. Add registry tests asserting PDF targets are available only for PDF inputs and absent for raster, SVG, media, and mixed PDF/non-PDF selections.
- [ ] **Step 2: Run focused tests.** Run `go test ./internal/engine ./internal/jobs ./internal/registry`; expected failure is missing PDF routing and page allocator.
- [ ] **Step 3: Implement the smallest multi-page manager path.** In `convertFile`, detect `MultiOutputEngine` before the existing single-output route. Create a private render directory under the output directory, call `ConvertMany`, validate that every returned page path is inside that directory and has the expected output type, allocate one final output plan per page in returned order, commit pages only after all page renders succeed, and remove the render directory on every success/failure/cancellation path.
- [ ] **Step 4: Preserve job event semantics.** Set one `FileJob.Outputs` slice containing all committed page paths, keep the existing per-input state model, and ensure a failed multi-page conversion removes all temporary files and publishes no partial outputs.
- [ ] **Step 5: Run focused tests and commit.** Run `go test ./internal/engine ./internal/jobs ./internal/registry`; commit:

```bash
git add backend/internal/engine/imagemagick.go backend/internal/engine/imagemagick_test.go backend/internal/jobs/manager.go backend/internal/jobs/manager_test.go backend/internal/registry/registry.go backend/internal/registry/registry_test.go
git commit -m "feat: support multi-page conversion outputs"
```

### Task 3: Implement the dedicated `pdftocairo` PDF engine

**Files:**
- Create: `backend/internal/engine/pdf.go`
- Test: `backend/internal/engine/pdf_test.go`

**Interfaces:**
- Add `func NewPDF(runner Runner) *PDF`.
- Add `func (e *PDF) ID() string` returning `"pdf"`.
- Add `func (e *PDF) Available() (string, error)` locating `pdftocairo`.
- Add `func (e *PDF) Supports(source, target formats.Format) bool` accepting only PDF source and PNG/JPEG/WebP target.
- Add `func (e *PDF) ConvertMany(ctx context.Context, source string, sourceFormat formats.Format, tempDir string, target formats.Format, options protocol.ImageOptions) ([]string, error)`.

- [ ] **Step 1: Write failing engine tests.** Add tests for the support matrix, missing executable, unsafe/non-PDF source rejection, exact argument arrays for paths containing spaces and shell metacharacters, 150 DPI, page prefix handling, and ordered output discovery. Use an injected runner that creates representative output files from the requested prefix.
- [ ] **Step 2: Run engine tests.** Run `go test ./internal/engine -run PDF`; expected failure is missing `PDF` implementation.
- [ ] **Step 3: Implement validation and renderer command construction.** Validate the source with `formats.ValidateInput`, read only a bounded prefix to require `%PDF-`, validate the controlled temp directory, resolve `pdftocairo` unless a runner is injected, and invoke:

```text
pdftocairo -r 150 -png  -- <source> <temp-dir>/page
pdftocairo -r 150 -jpeg -- <source> <temp-dir>/page
```

  The exact argument form may omit the double-dash only if the installed Poppler version rejects it, but source and prefix must remain separate arguments. For WebP, invoke `pdftocairo -r 150 -png -- <source> <temp-dir>/page`, then use `ImageMagick.Convert` once per ordered PNG with `sourceFormat=formats.PNG` and target `formats.WebP`.
- [ ] **Step 4: Collect outputs safely.** Enumerate only files matching the engine-owned prefix and expected renderer suffix, sort by numeric page index, reject an empty result, enforce a practical maximum page count (for example 1,000), and return absolute paths. Remove generated intermediates on error.
- [ ] **Step 5: Run tests and commit.** Run `go test ./internal/engine`; commit:

```bash
git add backend/internal/engine/pdf.go backend/internal/engine/pdf_test.go
git commit -m "feat: render PDF pages with pdftocairo"
```

### Task 4: Register PDF engine and route daemon jobs

**Files:**
- Modify: `backend/cmd/fcd/main.go`
- Test: `backend/cmd/fcd/main_test.go`
- Modify: `backend/internal/registry/registry.go`
- Test: `backend/internal/registry/registry_test.go`
- Modify: `backend/internal/jobs/manager.go`
- Test: `backend/internal/jobs/manager_test.go`

**Interfaces:**
- Register `engine.NewPDF(nil)` alongside ImageMagick, SVG, and FFmpeg.
- Add `formats.PDF` only to document-source handling, not `allSupportedFormats` output candidates.
- Ensure `Registry.Resolve(PDF, PNG/JPEG/WebP)` selects the PDF engine and unrelated engines do not claim PDF input.

- [ ] **Step 1: Write failing integration tests.** Test daemon setup contains a PDF engine, PDF-only target discovery returns exactly three PDF presets when `pdftocairo` is available, unavailable `pdftocairo` disables PDF targets without disabling other engines, and raster-to-PDF/PDF-to-video/PDF-to-SVG are rejected.
- [ ] **Step 2: Run focused tests.** Run `go test ./cmd/fcd ./internal/registry ./internal/jobs`; expected failure is missing registration/routing.
- [ ] **Step 3: Register and filter PDF routes.** Add PDF engine setup and availability probing, add PDF-specific preset filtering for PDF-only selections and mixed selections, and leave existing SVG/image/media filters intact.
- [ ] **Step 4: Verify job outputs.** Add manager tests using a fake `MultiOutputEngine` that returns two page files and assert one input file finishes with two committed numbered outputs; add failure tests asserting no partial outputs.
- [ ] **Step 5: Run tests and commit.** Run `go test ./...` and `go vet ./...`; commit:

```bash
git add backend/cmd/fcd/main.go backend/cmd/fcd/main_test.go backend/internal/registry/registry.go backend/internal/registry/registry_test.go backend/internal/jobs/manager.go backend/internal/jobs/manager_test.go
git commit -m "feat: integrate PDF conversion into jobs"
```

### Task 5: Add Nautilus integration and documentation

**Files:**
- Modify: `frontends/omarchy/nautilus/file_converter.py`
- Test: `frontends/omarchy/tests/test_extension_menu.py`
- Modify: `README.md`
- Modify: `docs/PROTOCOL.md`

**Interfaces:**
- Add common PDF MIME types such as `application/pdf` to `SUPPORTED_MIME_TYPES`.
- Use exact labels `PDF to PNG`, `PDF to JPEG`, and `PDF to WebP`.
- Document Poppler `pdftocairo`, ImageMagick’s WebP-only role, 150 DPI, numbered all-page outputs, and unsupported page/range options.

- [ ] **Step 1: Write the failing Nautilus test.** Mock enabled PDF targets for an `application/pdf` file and assert all three appear while disabled targets are omitted.
- [ ] **Step 2: Run the frontend test.** Run `python3 -m unittest frontends/omarchy/tests/test_extension_menu.py`; expected failure is unsupported PDF MIME filtering.
- [ ] **Step 3: Implement MIME support and docs.** Add `application/pdf`, update README conversion tables and dependency notes, and update `docs/PROTOCOL.md` preset documentation without changing the wire request shape.
- [ ] **Step 4: Run frontend tests and commit.** Run `python3 -m unittest discover -s frontends/omarchy/tests`; commit:

```bash
git add frontends/omarchy/nautilus/file_converter.py frontends/omarchy/tests/test_extension_menu.py README.md docs/PROTOCOL.md
git commit -m "docs: expose PDF conversion in Linux integration"
```

### Task 6: Add real PDF fixtures and end-to-end verification

**Files:**
- Modify: `backend/e2e_test.go`
- Test fixture: generate a temporary PDF in the test using a minimal valid PDF byte fixture, not a checked-in binary.

**Interfaces:**
- Use the existing `startE2EDaemon`, `request`, `waitForJob`, and output assertion helpers.
- Skip clearly when `pdftocairo` is unavailable; skip WebP only when ImageMagick lacks WebP write capability.

- [ ] **Step 1: Add a temporary two-page PDF fixture.** Build the bytes in the test with two simple page objects and write it to `document.pdf`; do not use network access or external files.
- [ ] **Step 2: Add the real PDF-to-PNG test.** Assert targets contain `pdf-to-png`, submit conversion, wait for completion, assert two outputs in order named `document-1.png` and `document-2.png`, and verify both have the PNG signature and non-zero size.
- [ ] **Step 3: Add JPEG/WebP checks.** Assert PDF-to-JPEG produces `.jpg` page outputs with JPEG signatures. Assert PDF-to-WebP produces `.webp` outputs when ImageMagick reports WebP write capability; otherwise skip with an explicit reason.
- [ ] **Step 4: Add negative and mixed-input checks.** Assert PDF targets are absent for raster/SVG selections and that a renderer failure leaves no final page outputs.
- [ ] **Step 5: Run the full verification suite and commit.** Run:

```bash
go test ./...
go vet ./...
python3 -m unittest discover -s frontends/omarchy/tests
git diff --check
```

Commit:

```bash
git add backend/e2e_test.go
git commit -m "test: verify PDF rasterization end to end"
```

## Final Self-Review Checklist

- [ ] Every spec requirement maps to Tasks 1-6: detection, dedicated renderer, 150 DPI, all-page numbered output, PDF-only routing, WebP conversion handoff, cleanup, frontend MIME support, docs, and real conversion tests.
- [ ] No task routes a PDF through ImageMagick; only rendered PNG pages may be passed to ImageMagick for WebP.
- [ ] The existing single-output `Engine.Convert` contract remains unchanged for image, SVG, FFmpeg, and fake test engines.
- [ ] `MultiOutputEngine.ConvertMany` paths are controlled, ordered, bounded, cleaned, and atomically published by the job layer.
- [ ] No placeholder terms such as TBD, TODO, “appropriate handling,” or “handle edge cases” remain in this plan.
- [ ] All commands and expected results are explicit.
