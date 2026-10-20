# PDF Tools Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make PDF tools usable from the file manager: a **PDF Tools** submenu in Nautilus offering every operation that needs no input, backed by new engines (qpdf, Ghostscript, Poppler, Tesseract) on top of phase 1's pdfcpu backend.

**Spec:** `docs/superpowers/specs/2026-10-21-pdf-tools-design.md`
**Builds on:** `docs/superpowers/plans/2026-10-21-pdf-tools-phase1.md` (done; commits `dfcf0bf`..`fce7a1a`)

**Architecture:** Phase 1 has one `Backend` (pdfcpu) and every operation produces one `.pdf`. Phase 2 generalises both. A `Toolbox` holds one object per external engine, each built on the existing `engine.Runner` seam so tests assert argument lists without running binaries. Each `Operation` declares its engine, input kind (PDF or image), and output kind (single file of any extension, or numbered files). The job layer's operation path grows to cover those, reusing `reserveOutput`/`publishPlan` and the numbered-output publishing already used by PDF-to-image. The Nautilus extension asks the daemon for `targets` and `operations` over one connection and renders both.

**Tech Stack:** Go, pdfcpu, qpdf, Ghostscript, Poppler (`pdftotext`, `pdfimages`, `pdftocairo`), Tesseract, Python Nautilus extension, Go/Python tests.

## Global Constraints

- Protocol stays v1 and backward compatible. `operations` for a PDF-only selection must still return the phase 1 operations with the same IDs.
- Subprocesses use argument arrays only (no shell), the shared `engine.Runner` (process-group kill on cancel), controlled temp files from `OutputPlan`, and hold no passwords.
- Nothing is written in place. Outputs go through `engine.OutputPlan` and honour `on_conflict`.
- A missing engine disables only its own operations. It never disables other engines or conversions.
- Only operations with `Mode: "instant"` appear in the Nautilus submenu. Page-based tools (`pdf-extract`, `pdf-delete`, custom `pdf-rotate`) wait for the phase 3 Studio and become `Mode: "studio"` here.
- Every claim about a tool's flags or API below is from memory and **must be checked against the installed version** (`gs --help`, `qpdf --help=all`, `pdfimages -h`, `tesseract --help`, and the pinned pdfcpu source) before coding. Where a check fails, the task says what to do instead.
- Run `go test ./...`, `go vet ./...`, `gofmt -l .` (prints nothing), `python3 -m unittest discover -s frontends/nautilus/tests`, and `git diff --check` before completion. Tests needing a real binary skip when it is missing.

## Operation catalogue added or changed

| ID | Engine | Input | Output | Notes |
| --- | --- | --- | --- | --- |
| `pdf-rotate-cw` / `pdf-rotate-ccw` / `pdf-rotate-180` | pdfcpu | PDF | `<stem>-rotated.pdf` | fixed angles 90 / 270 / 180, all pages, `instant` |
| `pdf-rotate` | pdfcpu | PDF | `<stem>-rotated.pdf` | phase 1 op, now `studio` (needs an angle) |
| `pdf-extract`, `pdf-delete` | pdfcpu | PDF | as phase 1 | already `studio`; no change |
| `pdf-repair` | qpdf | PDF | `<stem>-repaired.pdf` | |
| `pdf-linearize` | qpdf | PDF | `<stem>-linearized.pdf` | pdfcpu cannot write linearized files, hence qpdf |
| `pdf-flatten` | qpdf | PDF | `<stem>-flattened.pdf` | forms and annotations |
| `pdf-strip-metadata` | pdfcpu | PDF | `<stem>-clean.pdf` | Info dictionary and XMP, see Task 5 |
| `pdf-compress-light` / `-balanced` / `-strong` | Ghostscript | PDF | `<stem>-compressed.pdf` | `/printer`, `/ebook`, `/screen` |
| `pdf-grayscale` | Ghostscript | PDF | `<stem>-gray.pdf` | |
| `pdf-to-text` | Poppler | PDF | `<stem>.txt` | `pdftotext -layout` |
| `pdf-extract-images` | Poppler | PDF | `<stem>-image-1.png`, `-2.png`, … | numbered outputs |
| `pdf-from-images` | pdfcpu | images | `<first-stem>.pdf` | all inputs in order, one PDF |
| `pdf-ocr` | Tesseract | PDF | `<stem>-ocr.pdf` | searchable PDF; Task 9, ships last |

---

### Task 1: Error model — `invalid_pdf` and merge attribution

**Files:**
- Modify: `backend/internal/pdfops/backend.go` or `operation.go`, `backend/internal/pdfops/pdfcpu_backend.go`, `backend/internal/jobs/manager.go` (`failOperationFile`), `backend/internal/jobs/operations.go`
- Test: `backend/internal/jobs/operations_test.go`, `backend/internal/pdfops/pdfcpu_backend_test.go`

**Interfaces:**
- `var ErrInvalidPDF = errors.New("pdf is damaged or not a valid PDF")` next to `ErrPasswordRequired`.
- `type InputError struct { Path string; Err error }` with `Error()` and `Unwrap()`.
- Error code `invalid_pdf` joins `password_required`; `operationErrorCode` maps `ErrInvalidPDF`.

Behaviour:
- Classification is engine-agnostic: when an operation fails with an error that is not `ErrPasswordRequired` and not a cancellation, probe the input with `PageCount`. If the probe also fails (and is not a password error), report `invalid_pdf` and keep the original message in the daemon log only. If the probe succeeds, the failure is the operation's own and stays `internal`.
- `Merge` (job layer, before calling the backend) probes each input with `PageCount` and returns an `InputError` naming the first unreadable file. `mergeOperation` then marks that file `failed` with its code (`password_required`, `invalid_pdf`) and every other entry `skipped` with notice `not merged: <basename> could not be read`. Job state becomes `failed`.

- [ ] **Step 1: Write failing tests** with the fake backend: corrupt input yields `invalid_pdf`; a genuine operation failure on a readable file stays `internal`; merge of good + locked marks only the locked file failed and the good one skipped with the notice; merge of good + corrupt likewise with `invalid_pdf`; cancellation is never classified as invalid.
- [ ] **Step 2: Run, expect failure. Implement. Run `go test ./internal/jobs ./internal/pdfops`.**
- [ ] **Step 3: Extend the e2e corrupt-file case** in `backend/e2e_test.go` to assert the `invalid_pdf` code over the socket.
- [ ] **Step 4: Commit.**

```bash
git add backend
git commit -m "feat: report damaged PDFs and attribute merge failures"
```

### Task 2: Operation model, toolbox, and rotate variants

**Files:**
- Modify: `backend/internal/pdfops/operation.go`, `backend/internal/pdfops/catalogue.go`, `backend/internal/jobs/operations.go`, `backend/internal/jobs/manager.go`, `backend/internal/server/server.go`, `backend/cmd/fcd/main.go`, `backend/internal/protocol/types.go`, `docs/PROTOCOL.md`
- Test: matching `_test.go` files

**Interfaces:**

```go
type InputKind string
const ( InputPDF InputKind = "pdf"; InputImage InputKind = "image" )

// Operation gains:
Engine      string    // "pdfcpu" | "qpdf" | "ghostscript" | "poppler" | "tesseract"
Inputs      InputKind // default InputPDF
OutputExt   string    // default "pdf"
Numbered    bool      // one input produces N numbered outputs
FixedAngle  int       // rotate variants: used when Params.Angle is 0

// Toolbox replaces the single Backend argument.
type Toolbox struct {
    PDFCPU  Backend
    QPDF    *QPDF
    GS      *Ghostscript
    Poppler *Poppler
    OCR     *OCR
}
func (t Toolbox) Available(engine string) error // nil, or the reason it cannot run
func (o Operation) Run(ctx context.Context, tb Toolbox, inputs []string, output string, p Params) error
```

`Manager.SetPDFBackend` becomes `SetPDFToolbox`; `server.Dependencies.PDFBackend` becomes `PDFToolbox`. `Validate` treats `FixedAngle` as satisfying `NeedsAngle` and rejects an explicit `Angle` on fixed variants.

Protocol `operations`:
- A selection of all PDFs returns operations with `Inputs == InputPDF`; a selection of all images (by `formats.Detect`, using `formats.IsImageFormat`, SVG excluded) returns operations with `Inputs == InputImage`; anything mixed or unrecognised returns an empty list.
- `enabled` is false with a `reason` when the engine is unavailable (`Toolbox.Available`), the file count is out of range, or, for image input, any file's format is not supported by the operation.
- Entries gain an `engine` field. Unknown fields are already ignored by clients.
- `engines` status lists `pdfcpu`, `qpdf`, `ghostscript`, `poppler`, `tesseract` with availability and reason.

Catalogue changes: add `pdf-rotate-cw` (90), `pdf-rotate-ccw` (270), `pdf-rotate-180`, all `instant`; change `pdf-rotate` to `studio`. Update the phase 1 tests and `docs/PROTOCOL.md` accordingly.

- [ ] **Step 1: Write failing tests** for `Validate` with fixed angles, `Toolbox.Available` with absent engines, `operations` for PDF-only, image-only, mixed, and empty selections, and for an engine reported unavailable.
- [ ] **Step 2: Refactor** the call sites mechanically first (signature changes only) and confirm the whole phase 1 suite is green before adding behaviour.
- [ ] **Step 3: Implement the new fields and `operations` logic. Run all backend tests.**
- [ ] **Step 4: Commit.**

```bash
git add backend docs/PROTOCOL.md
git commit -m "refactor: generalise PDF operations over engines and input kinds"
```

### Task 3: Subprocess helper and qpdf engine

**Files:**
- Create: `backend/internal/pdfops/tool.go`, `backend/internal/pdfops/qpdf.go`
- Test: `backend/internal/pdfops/qpdf_test.go`

**Interfaces:**
- `tool.go`: a small `lookTool(name) (string, error)` and `runTool(ctx, runner engine.Runner, path string, args ...string) error`, wrapping the existing `engine.Runner` so cancellation kills the process group. Include stderr (trimmed to a bounded length) in returned errors.
- `type QPDF struct{ runner engine.Runner }`, `NewQPDF(runner)`, `Available() error`, `Repair(ctx, in, out)`, `Linearize(ctx, in, out)`, `Flatten(ctx, in, out)`.
- Check against `qpdf --help=all`: repair is a plain rewrite (`qpdf in out`; qpdf exits 3 on recoverable warnings and still writes output — treat exit 3 as success and surface the warning as a notice only if easy, otherwise ignore); linearize is `--linearize`; flatten is `--flatten-annotations=all` combined with `--generate-appearances`. Encrypted input (exit with a password message) maps to `ErrPasswordRequired`.

- [ ] **Step 1: Write failing tests with a fake runner** asserting exact argument lists, exit-3 handling, password mapping, and that missing `qpdf` makes `Available()` return a descriptive error.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Add a real-binary test** (skipped without qpdf) that repairs a truncated-xref fixture generated at test time, linearizes a fixture and checks `qpdf --check` reports linearization, and flattens a fixture with an annotation.
- [ ] **Step 4: Commit.**

```bash
git add backend/internal/pdfops
git commit -m "feat: add qpdf engine for repair, linearize, and flatten"
```

### Task 4: Ghostscript engine

**Files:**
- Create: `backend/internal/pdfops/ghostscript.go`
- Test: `backend/internal/pdfops/ghostscript_test.go`

**Interfaces:** `type Ghostscript struct{ runner engine.Runner }`, `Compress(ctx, in, out, level)` with `level` one of `light`, `balanced`, `strong` mapped to `-dPDFSETTINGS=/printer`, `/ebook`, `/screen`, and `Grayscale(ctx, in, out)` using `-sColorConversionStrategy=Gray -dProcessColorModel=/DeviceGray`. Always pass `-dSAFER -dBATCH -dNOPAUSE -dQUIET -sDEVICE=pdfwrite` and `-sOutputFile=` as separate checked arguments. Never pass the input before `--`/`-f` such that a filename beginning with `-` is treated as a flag.

Behaviour:
- **No-gain rule:** if a compress output is not smaller than its input, the file is reported `skipped` with notice `already as small as it can get` and the output is discarded. Implement by having `Run` return a typed `ErrNoGain` that `runOneOperation` maps to `skipped`.
- Encrypted input maps to `ErrPasswordRequired`.

- [ ] **Step 1: Write failing fake-runner tests:** exact args for each level and for grayscale, filename-beginning-with-`-` safety, password mapping, `ErrNoGain` produces a skipped file and removes the temp output.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Real-binary test** (skipped without `gs`): compress a fixture containing a large generated image and assert the output is smaller; grayscale a coloured fixture and verify with `pdfimages -list` or `gs -sDEVICE=inkcov` that no colour remains.
- [ ] **Step 4: Commit.**

```bash
git add backend/internal/pdfops
git commit -m "feat: add Ghostscript engine for compress and grayscale"
```

### Task 5: pdfcpu additions — strip metadata and images to PDF

**Files:**
- Modify: `backend/internal/pdfops/backend.go`, `backend/internal/pdfops/pdfcpu_backend.go`, `backend/internal/pdfops/fake_test.go`
- Test: `backend/internal/pdfops/pdfcpu_backend_test.go`

**Interfaces:** add `StripMetadata(ctx, in, out)` and `ImagesToPDF(ctx, inputs []string, out string)` to `Backend`.

pdfcpu entry points to check in the pinned source before coding: `api.ImportImagesFile` for images to PDF, and for metadata the properties/keywords removal functions (`api.RemovePropertiesFile`, `api.RemoveKeywordsFile`).

- **Images to PDF:** confirm which image types pdfcpu imports. Accept only those in `operations` and `Validate`; for any other type the entry is disabled with reason `<format> is not supported for PDF import`. Do not add an ImageMagick pre-conversion in this phase. Page size: use the image's own size (no margins) unless pdfcpu requires an import configuration.
- **Strip metadata acceptance test:** after the operation, none of `Title`, `Author`, `Subject`, `Keywords`, `Creator`, `Producer` may be non-empty in `pdfinfo`, and the catalog must have no `/Metadata` stream. **If pdfcpu removes the Info dictionary but not the XMP stream**, remove XMP by the means pdfcpu offers or, if it offers none, drop `pdf-strip-metadata` from phase 2 and record that in the spec rather than shipping a half-clean result.
- Add `pdf-strip-metadata`, `pdf-rotate-*` wiring and `pdf-from-images` (`Inputs: InputImage`, `Merges: true`, name from first input's stem) to the catalogue.

- [ ] **Step 1: Write failing tests** with fixtures that have all metadata fields set, and with PNG and JPEG inputs (generated at test time).
- [ ] **Step 2: Implement. Run `go test ./internal/pdfops`.**
- [ ] **Step 3: Commit.**

```bash
git add backend/internal/pdfops
git commit -m "feat: add metadata stripping and images-to-PDF"
```

### Task 6: Poppler engine — text and image extraction, with numbered outputs

**Files:**
- Create: `backend/internal/pdfops/poppler.go`
- Modify: `backend/internal/jobs/operations.go`, `backend/internal/engine/imagemagick.go` (only if a missing extension blocks planning)
- Test: `backend/internal/pdfops/poppler_test.go`, `backend/internal/jobs/operations_test.go`

**Interfaces:**
- `type Poppler struct{ runner engine.Runner }`, `Text(ctx, in, out)` running `pdftotext -layout in out`, and `Images(ctx, in, dir)` running `pdfimages -png in dir/img` and returning ordered file paths (confirm `-png` versus `-all` against `pdfimages -h`; `-png` is chosen so every output has one predictable extension).
- `supportedOutputExtension` must accept `txt`; check and extend it if not. Text output is a single file, so it uses the normal single-output path with `OutputExt: "txt"`.
- Numbered operations: a PDF with no embedded images yields job state `completed` with the file `skipped` and notice `no embedded images`. Never publish an empty set.

Job layer: `runOneOperation` branches on `op.Numbered`. Factor the publish-many logic out of `convertMany` (`backend/internal/jobs/manager.go`) into a helper used by both, rather than copying it, and keep the existing PDF-to-image tests green. Names: `<stem>-image-1.png`, `<stem>-image-2.png`, … via `PlanNamedOutputExcluding` with the base `<stem>-image-N`.

- [ ] **Step 1: Write failing tests:** fake-runner argument lists; text output extension and conflict handling; numbered outputs published atomically and in order; zero images gives skipped with the notice; cancellation mid-way leaves no partial set; a name collision on `-image-2` renames only that file.
- [ ] **Step 2: Implement; run `go test ./internal/jobs ./internal/pdfops ./internal/engine`.**
- [ ] **Step 3: Real-binary test** (skipped without Poppler): extract text from a fixture and compare; extract images from a fixture containing two images and assert two PNGs.
- [ ] **Step 4: Commit.**

```bash
git add backend
git commit -m "feat: add PDF text and image extraction"
```

### Task 7: Daemon wiring and `fcctl pdf`

**Files:**
- Modify: `backend/cmd/fcd/main.go`, `backend/cmd/fcctl/main.go`, `docs/PROTOCOL.md`, `README.md`
- Test: `backend/cmd/fcd/main_test.go`, `backend/cmd/fcctl/main_test.go`, `backend/e2e_test.go`

- [ ] **Step 1:** Build the `Toolbox` in `fcd` with real runners and pass it to the manager and server. `fcctl engines` must list all five engines with reasons for any that are missing.
- [ ] **Step 2:** `fcctl pdf` becomes table-driven from `pdfops.Catalogue()` for instant operations: `fcctl pdf <name> file.pdf…` where `<name>` is the ID without the `pdf-` prefix (`compress-balanced`, `grayscale`, `repair`, `linearize`, `flatten`, `strip-metadata`, `to-text`, `extract-images`, `rotate-cw`, `rotate-ccw`, `rotate-180`, `ocr`, `from-images`). Phase 1 subcommands keep working unchanged. `fcctl pdf list` shows engine and enabled state with reason. Unknown names list the valid ones.
- [ ] **Step 3: Tests:** argument parsing and exact request JSON for several operations; the e2e suite gains one test per engine (skipped without the binary) running through the socket and asserting output names, page counts, and the `no gain` and `no images` skips.
- [ ] **Step 4: Docs:** add the new operations, output names, error codes (`invalid_pdf`), and the `engine` field to `docs/PROTOCOL.md` and the README PDF section, including which system packages each operation needs.
- [ ] **Step 5: Commit.**

```bash
git add backend docs README.md
git commit -m "feat: wire PDF engines into fcd and fcctl"
```

### Task 8: Nautilus PDF Tools submenu

**Files:**
- Modify: `frontends/nautilus/fc_client/client.py`, `frontends/nautilus/file_converter.py`
- Test: `frontends/nautilus/tests/test_client.py`, `frontends/nautilus/tests/test_extension_menu.py`

**Client:**
- `Operation` dataclass: `id, label, group, mode, engine, enabled, reason`.
- `menu_items(paths)` opens one connection, sends `hello`, then `targets` (with `menu: True`) and `operations` against a **single shared 0.5 s deadline**, and returns `(targets, operations)`. An old daemon that rejects `operations` as an unknown method must degrade to `(targets, [])`, not fail the menu.
- `run_operation(paths, op)` is a generator like `convert`; factor the shared event-streaming loop into one private method used by both.

**Extension:**
- `get_file_items` builds the existing **Convert to** submenu and, independently, a **PDF Tools** submenu from `operations` filtered to `enabled` and `mode == "instant"`. Either may be absent; return whichever exist. A selection with neither returns `[]`.
- **PDF Tools** contains one nested submenu per group, in this order: Organise, Optimise, Convert from PDF, Convert to PDF, Text & OCR. Skip empty groups. Display labels: map group IDs to those names in one table.
- Item activation spawns the helper with `--op <id> <paths…>`, mirroring `--convert`. The helper calls `run_operation` and notifies.
- Disabled operations are dropped, matching how disabled conversion targets are handled today. `fcctl pdf list` is where reasons are shown.
- MIME filter: unchanged (PDF and images are already allowed).

**Notifications:** generalise `_notify_job` with the job's `preset` (the operation ID):
- Titles per operation, e.g. `PDFs merged`, `PDF compressed`, `Text extracted`, falling back to `PDF operation complete`.
- Single-file success body: `source → output`. Compress success includes the sizes (`1.8 MB → 640 KB`) computed from file sizes in the helper, not the daemon.
- Skips show the daemon notice (`already as small as it can get`, `no embedded images`).
- Error codes get friendly text: `password_required` → `<name> is password-protected (passwords are not supported yet)`; `invalid_pdf` → `<name> is damaged or not a valid PDF`; anything else shows the daemon message.

- [ ] **Step 1: Write failing tests** in the existing style (fake `Nautilus` module): single PDF builds Convert-to plus PDF Tools with the expected group order; two PDFs include Merge; images build a PDF Tools submenu containing only Convert-to-PDF entries; mixed PDF+image selection has no PDF Tools; a studio-mode or disabled operation is never shown; an old daemon without `operations` still yields the Convert-to menu; item activation passes `--op` with the right arguments; notification text for each error code and for the compress sizes.
- [ ] **Step 2: Implement client changes, then the extension.**
- [ ] **Step 3: Run** `python3 -m unittest discover -s frontends/nautilus/tests`.
- [ ] **Step 4: Commit.**

```bash
git add frontends/nautilus
git commit -m "feat: add PDF Tools submenu to the Nautilus extension"
```

### Task 9: OCR (ship last; may slip to its own PR)

**Files:**
- Create: `backend/internal/pdfops/ocr.go`
- Test: `backend/internal/pdfops/ocr_test.go`

Pipeline: rasterize pages with `pdftocairo -png -r 300` into a private temp directory, write an image list file, run `tesseract <list> <outbase> -l <lang> pdf` (confirm that Tesseract accepts an image-list file and writes one multi-page PDF; if it does not, OCR each page to its own PDF and merge with the pdfcpu backend), then publish the result. All temp files live in one `os.MkdirTemp` directory removed on every exit path.

Rules:
- Language is `eng` in this phase. `Available()` verifies `tesseract --list-langs` contains `eng` and reports a precise reason if not. An environment override `FC_OCR_LANG` (validated against `--list-langs`, restricted to `[a-z_+]+`) is allowed; no per-request language yet.
- Page bound: reuse `maxPDFPages`, and additionally refuse documents over 200 pages with a clear error (OCR cost grows quickly; revisit with progress reporting in a later phase).
- Cancellation must stop both the rasterizer and Tesseract (process-group kill via the shared runner) and remove the temp directory.
- A PDF that already has a text layer is still processed; do not try to detect it in this phase.

- [ ] **Step 1: Write failing fake-runner tests:** exact command sequence, temp directory cleanup on success, failure, and cancel, page-limit rejection, language validation.
- [ ] **Step 2: Implement. Add a real-binary test** (skipped without `tesseract` and `pdftocairo`): build a one-page fixture containing rendered text, OCR it, and assert `pdftotext` finds the words.
- [ ] **Step 3: Add `pdf-ocr` to the catalogue and the Nautilus group table. Commit.**

```bash
git add backend frontends/nautilus
git commit -m "feat: add OCR to searchable PDF"
```

### Task 10: Installer notice, docs, and final verification

**Files:**
- Modify: `frontends/nautilus/install.sh`, `README.md`

- [ ] **Step 1:** After installing the extension, `install.sh` runs `fcctl engines` (if the daemon is reachable) or checks `command -v` for `qpdf gs pdftotext pdfimages tesseract`, and prints one line per missing engine naming the operations it disables and the package to install (`pacman -S qpdf ghostscript poppler tesseract tesseract-data-eng`; `apt-get install qpdf ghostscript poppler-utils tesseract-ocr`). It never installs them automatically. Add a shell test or `--dry-run` check if the existing script has one.
- [ ] **Step 2:** README lists each PDF operation with its engine and output name.
- [ ] **Step 3: Full verification:** the commands in Global Constraints.
- [ ] **Step 4: Manual check in Nautilus** (cannot be automated here): reinstall the extension, run `nautilus -q`, then confirm on real files that a single PDF shows PDF Tools with the expected groups, two PDFs add Merge, selected PNGs show Convert to PDF, a PDF plus a PNG shows no PDF Tools, compress shows sizes in the notification, a locked PDF shows the friendly message, and uninstalling a binary (rename `qpdf` on PATH for the daemon) hides only its operations.
- [ ] **Step 5: Commit.**

```bash
git add frontends/nautilus/install.sh README.md
git commit -m "docs: document PDF tool dependencies and install check"
```

---

## Out of scope for phase 2

Anything that needs page selection or a form (extract, delete, custom rotate, split, reorder, watermark, page numbers, crop, encrypt, metadata editor), thumbnails (`pdf_info`, `pdf_thumbnails`), passwords, per-request OCR language, OCR progress, PDF to Markdown/HTML, PDF/A, Office to PDF, and the Omarchy radial plugin.

## Risks

- **API drift from memory.** Flags for qpdf, Ghostscript, `pdfimages` and Tesseract, and pdfcpu's metadata and image-import functions, are unverified. Each task starts with a check and names its fallback; two operations (`pdf-strip-metadata`, and OCR via per-page merge) may change shape.
- **Menu latency.** Nautilus builds the menu synchronously. Two daemon calls share one 0.5 s budget; if `operations` is slow it must be dropped rather than delay the Convert-to menu. Test this with a fake slow server.
- **Refactor surface.** Task 2 changes signatures used by the manager, server and fcd at once. Do it as a pure mechanical commit first with the suite green, then add behaviour.
- **Compress can inflate files.** The no-gain rule handles it; without it users would silently get bigger PDFs.
- **OCR cost.** Up to 200 pages at 300 DPI is slow and memory-hungry, and has no progress events yet. It ships last so the rest of the phase is not blocked on it.
