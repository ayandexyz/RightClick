# PDF Tools Phase 5 Implementation Plan (Heavy and optional tools)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The four tools the spec kept for last because each carries a dependency or a correctness risk: **Redact** (catalogue 30), **Compare two PDFs** (39), **PDF to PDF/A** (23) and **Office to PDF** (26). After this phase the spec's phasing is complete.

**Spec:** `docs/superpowers/specs/2026-10-21-pdf-tools-design.md` (phase 5, rows 23, 26, 30, 39; open questions 2 and 3)
**Builds on:** phases 1-4 (last commit `5e17905`). Phase 4 left the Studio with Edit, Split and Tools tabs, a generic form renderer (`tools.py` / `tools_view.py`), a password prompt, and a daemon whose connections own a private directory for locked documents.

**Architecture:** Nothing structural is new; each tool slots into an existing shape.

1. **Compare, PDF/A and Office to PDF are instant operations** with no parameters the user must choose, so they appear in the Nautilus PDF Tools submenu (compare when exactly two PDFs are selected, Office to PDF for Office files) and need no Studio code.
2. **Redact is a Studio tool**, the one tool that needs the user to point at the page. It adds one new form field kind, `regions`, drawn by a page canvas widget. Everything else about it (apply bar, cancel, output options, Show in Files) is reused.
3. **A new engine, LibreOffice**, joins the toolbox beside pdfcpu, qpdf, Ghostscript, Poppler and Tesseract. It is optional: when it is missing only Office to PDF is disabled, with a reason, exactly like Tesseract and OCR.

**Tech Stack:** Go (daemon), Ghostscript, Poppler (`pdftocairo`, `pdftotext`), pdfcpu, qpdf, LibreOffice headless (new, optional); Python 3 + GTK 4 + libadwaita (Studio); Go and `unittest` tests.

## Verified facts this plan relies on

Checked on this machine (Arch, 2026-10-21), not recalled:

| Need | Fact |
| --- | --- |
| Ghostscript | 10.08.0 at `/usr/bin/gs`; `PDFA_def.ps` is at `/usr/share/ghostscript/lib/PDFA_def.ps` here (Debian and Ubuntu keep it under a versioned directory, so the path must be discovered, not hard-coded) |
| LibreOffice | `/usr/bin/soffice` and `/usr/bin/libreoffice` present |
| Poppler | `pdftotext`, `pdftocairo`, `pdftoppm` present |
| qpdf | present; `--qdf` writes a fully decompressed file, which the redaction test uses to prove no text survives |
| veraPDF | **not installed**, so PDF/A conformance cannot be machine-validated here (see decision 4) |
| mutool | not installed; nothing in this plan depends on it |
| Existing seams | `Operation` has `Engine`, `Inputs` (`InputPDF` / `InputImage`), `MinInputs` / `MaxInputs`, `Merges`, `Suffix`, `OutputExt`, `Unimplemented`; `Toolbox.Available` decides per-engine availability; `thumbs.Cache.Fork` gives an ephemeral cache; the stamp code already makes a rotated document upright with qpdf (`uprightCopy`) |

Not verified, and therefore spiked in Task 1: which Ghostscript flags give a PDF/A-2b that keeps text selectable and embeds fonts; whether Ghostscript still needs `PDFA_def.ps` and an ICC profile on 10.x; how headless LibreOffice behaves with a second instance, with a user profile that is read-only or missing, with macros and external links in the input, and what it does on a corrupt file (hang, exit code, partial output); the rasterise-and-rebuild fidelity and size at 150, 200 and 300 dpi; whether `pdftotext -bbox-layout` word boxes are reliable enough to find a phrase; and what a rotated or cropped page does to coordinates on the redaction path.

## Decisions made in this plan (override before starting if you disagree)

1. **Redaction is rasterise-and-rebuild, and says so.** Drawing a black rectangle over live text leaves the text in the file (anyone can select it, copy it or read it with `pdftotext`). The only approach this toolbox can make safe is to render each affected page to an image, paint the boxes into the pixels, and replace the page with that image. The cost is explicit and shown to the user before it happens: **every redacted page loses its text layer, its links, its bookmarks' targets and its annotations, and the file grows.** Pages with no redaction are untouched and keep everything.
2. **Redaction is proven, not assumed.** Task 5's test decompresses the output with `qpdf --qdf` and asserts the redacted words appear nowhere in the file, in any stream, on any page that was redacted, and that `pdftotext` returns nothing for those pages. The same test asserts an untouched page still has its text. If a future change breaks this, a test fails rather than a document leaking.
3. **Two ways to choose what to redact, one operation.** Boxes the user draws (`areas`, as fractions of the page as the reader sees it) and search terms (`terms`, matched case-insensitively in the extracted text, each match becoming boxes). Both feed the same rasteriser. Searching is a convenience for finding places; what is painted is what the boxes cover, and the Studio shows the found boxes on the canvas before applying so a miss is visible.
4. **PDF/A is "converted to PDF/A-2b by Ghostscript", not "validated".** Without veraPDF the daemon cannot certify conformance. The output is checked for what can be checked (the `pdfaid` XMP identification, embedded fonts, no encryption, `pdfinfo` opens it) and the documentation says it is a best-effort conversion that should be checked with a validator for archival or legal use. A validator integration is out of scope.
5. **Office to PDF runs LibreOffice in a throwaway profile.** One private profile directory per job (0700, removed on every exit path), so it never contends with the user's own running LibreOffice and never reads or writes their profile. A hard timeout kills the whole process group. Conversion happens in the daemon's own session, with no network access requested; documents that try to fetch external content or run macros are still untrusted input (see Risks).
6. **Compare is a text comparison and says so.** It diffs the extracted text page by page and reports which pages differ and how. It does not compare layout, images or fonts, and a scanned PDF with no text layer reports "no text to compare" instead of "identical". The output is a plain-text report next to the first input.
7. **No new Studio screens beyond the redaction canvas.** Compare, PDF/A and Office to PDF are one-click Nautilus actions.
8. **Locked documents are out of scope for the three instant tools.** They report `password_required` like every other instant operation; the Studio's unlock flow already covers redaction, which runs in the Studio.

## Global Constraints

- Protocol stays v1 and backward compatible; every new field is optional.
- All new operations honour cancellation, leave no partial output on failure, respect the 1000-page ceiling, and reuse the `Suffix` / OutputPlan / conflict machinery.
- Child processes get arguments the daemon built from typed values; no user-supplied string reaches a command line except as a file path, which is always absolute and passed after `--` where the tool supports it.
- Any temporary file holding page content (rasterised pages, unlocked copies, LibreOffice's profile and scratch output) lives in a 0700 directory removed on every exit path including cancellation, and never in the persistent thumbnail cache.
- A redaction request is refused, not silently shortened, when it names a page that does not exist, a box outside the page, or more than 500 boxes.
- Studio code keeps the phase 3 and 4 rules: GTK on the main thread, no GTK in the model/request layers, nothing newer than GTK 4.12 / Adw 1.5 (re-run the GIR "since" audit in Task 12).
- Run `go test -count=1 ./...`, `go vet ./...`, `gofmt -l .` (prints nothing), every Python suite, and `git diff --check` before each commit. Real-binary tests skip when the tool is missing; mutation-check the ones that guard a safety property (redaction, cleanup, timeouts).

## New or changed protocol surface

| Item | Shape |
| --- | --- |
| `pdf-compare` | instant; exactly 2 PDFs; writes `<first-stem>-compare-<second-stem>.txt`; `skipped` with notice `the two documents have identical text` when nothing differs |
| `pdf-to-pdfa` | instant; 1 or more PDFs; `<stem>-pdfa.pdf`; optional `params.pdfa: "1b" \| "2b" \| "3b"`, default `2b` |
| `office-to-pdf` | instant; 1 or more Office files (new `InputOffice`); `<stem>.pdf`; engine `libreoffice` |
| `pdf-redact` | studio; exactly 1 PDF; `<stem>-redacted.pdf`; `params.redact` |
| `Params.redact` | `{areas: [{page, x, y, w, h}], terms: [string], dpi?: 150..300}`; `x y w h` are fractions 0-1 of the page as the reader sees it, origin top-left |
| `pdf_find` | `{file, terms: [string], password?}` → `{matches: [{page, x, y, w, h, term}]}`; read-only, used by the Studio to show what a search would cover before applying |
| engine `libreoffice` | added to `engines` / `hello`, checked like the others |

---

# Part A: Daemon

### Task 1: Spikes that decide the open questions

**Files:**
- Create: `backend/internal/pdfops/spike_test.go` (deleted before the commit that closes this task; findings go in this plan's "Spike results" section and in code comments where they shape behaviour)

Each spike runs the real tool on a real fixture and records what happened. No production code.

- [ ] **Step 1: PDF/A.** Run Ghostscript with `-dPDFA=1|2|3 -dPDFACompatibilityPolicy=1 -sColorConversionStrategy=RGB -sProcessColorModel=DeviceRGB -sDEVICE=pdfwrite`, with and without `PDFA_def.ps`. Record which combinations succeed, whether text stays selectable (`pdftotext`), whether fonts are embedded (`pdffonts`), what `pdfinfo -meta` shows for `pdfaid:part` / `pdfaid:conformance`, and what Ghostscript prints when it cannot comply. Record how to find `PDFA_def.ps` portably (`gs -h` search path, the `GS_LIB` variable, known directories) and whether the ICC profile line needs patching.
- [ ] **Step 2: LibreOffice.** Convert a `.docx`, `.xlsx`, `.pptx`, `.odt` and `.rtf` built by LibreOffice itself with `soffice --headless --norestore --convert-to pdf --outdir DIR -env:UserInstallation=file://PROFILE FILE`. Record the exit code and output name, how long a cold start takes, and what happens when (a) the user's own LibreOffice is running, (b) the profile directory does not exist, (c) the input is corrupt or zero bytes, (d) the input is a `.csv` or `.txt`, (e) the file name has spaces, quotes or a leading dash. Record whether the process leaves children after a kill and whether killing the process group is enough.
- [ ] **Step 3: Hostile Office input.** With a document carrying an external image link and an `AutoOpen` macro, record whether headless conversion fetches the link or runs the macro. If it does, record the setting that stops it and whether it can be passed on the command line or needs a profile file written into the private profile.
- [ ] **Step 4: Redaction path.** Rasterise a page with `pdftocairo -png -r 150|200|300`; record the file size and render time for a text page and a photo page. Rebuild with pdfcpu (`ImportImagesFile` with a page size equal to the original) and record whether the page keeps its exact size and whether a `/Rotate 90` page lands the right way up. Record what `qpdf --qdf` plus `grep` finds for a word that was on the page before and after.
- [ ] **Step 5: Search boxes.** Run `pdftotext -bbox-layout` on a text page, a rotated page and a two-column page; record whether word boxes line up with the rendered words (overlay a rectangle and view it), how a hyphenated or ligature word (`fi`) is reported, and whether a phrase of two words can be found as two adjacent boxes.
- [ ] **Step 6: Diff.** Decide the diff algorithm and its limits: time and memory of a line diff on two 500-page text documents that differ in a few places and in most places. Record where a bound is needed (maximum lines, maximum edit distance) so a pathological pair fails fast with a clear message.
- [ ] **Step 7:** Write the findings under **Spike results**, adjust later tasks where a finding contradicts them, delete the spike test, commit `docs: record phase 5 spike results`.

### Task 2: Compare two PDFs

**Files:**
- Modify: `backend/internal/pdfops/catalogue.go`, `operation.go`, `info.go` or a new `text.go` for extraction, `backend/cmd/fcctl/main.go`, `docs/PROTOCOL.md`
- Create: `backend/internal/pdfops/compare.go`, `compare_test.go`

- [ ] **Step 1: Failing tests for the diff.** A pure function from two lists of pages (each a list of lines) to a report: identical documents → no differences; a changed word → that page listed with the old and new line; a page added or removed → reported as such, not as every following page differing; whitespace-only differences ignored; the bound from Task 1 enforced with a clear error. Table-driven, no external tools.
- [ ] **Step 2: Failing real-binary tests.** Build two fixtures with `buildTextPDF`, differing on one page, and assert the report names that page and quotes the changed line; two identical documents give `skipped` with the notice; a document with no text layer on one side reports `no text to compare` rather than a difference. Skips without `pdftotext`.
- [ ] **Step 3: Implement.** Extract each page with `pdftotext -layout -f N -l N` (one call per document with form-feed splitting is fine), normalise whitespace, diff pages by alignment on page text first and lines within a differing page, and write the report. Catalogue entry: `pdf-compare`, instant, `MinInputs: 2`, `MaxInputs: 2`, engine Poppler, `OutputExt: "txt"`, output name `<a>-compare-<b>`. The job layer needs a way to name an output after two inputs; reuse `Merges` plus an explicit name builder rather than a new concept.
- [ ] **Step 4:** `fcctl pdf compare A B`, docs, full suite, commit `feat: add pdf-compare`.

### Task 3: PDF to PDF/A

**Files:**
- Modify: `backend/internal/pdfops/ghostscript.go`, `catalogue.go`, `operation.go`, `protocol/*`, `fcctl`, `docs/PROTOCOL.md`
- Create: `backend/internal/pdfops/pdfa.go`, `pdfa_test.go`

- [ ] **Step 1: Failing tests.** Argument construction as a pure function (levels `1b`, `2b`, `3b`; an unknown level is rejected; the path of `PDFA_def.ps` and the ICC profile are daemon-discovered and never client-supplied); a real-binary test on a text fixture asserts the output opens, still has selectable text, embeds its fonts (`pdffonts`), carries the matching `pdfaid:part` in its XMP, and is not encrypted; a damaged input reports `invalid_pdf`; a run whose output Ghostscript could not make compliant fails with Ghostscript's own message instead of writing a file that merely claims PDF/A. Skips without `gs`.
- [ ] **Step 2: Implement** `PDFA(ctx, in, out, level)` on the Ghostscript engine using the Task 1 flags. Locating the definition file is one function with the Task 1 rules and a clear error naming where it looked. Output goes through the existing temp-then-rename path so a failure leaves nothing.
- [ ] **Step 3:** Catalogue entry `pdf-to-pdfa` (group `convert`, instant, Ghostscript, `Suffix: "-pdfa"`), optional `params.pdfa`, `fcctl pdf to-pdfa FILE [--level 1b|2b|3b]`, and the documentation wording from decision 4. Commit `feat: add pdf-to-pdfa`.

### Task 4: Office to PDF

**Files:**
- Modify: `backend/internal/pdfops/toolbox.go`, `operation.go`, `catalogue.go`, `backend/internal/formats/*`, `backend/internal/registry/*`, `backend/internal/server/server.go` (engine status), `backend/cmd/fcd/main.go`, `docs/PROTOCOL.md`
- Create: `backend/internal/pdfops/libreoffice.go`, `libreoffice_test.go`

- [ ] **Step 1: Failing tests.** Command construction as a pure function (private profile URL, output directory, `--convert-to pdf`, the input after `--` or, if Task 1 shows `--` is not honoured, a validated absolute path that cannot begin with `-`); timeout and process-group kill with a fake runner that never returns; the profile directory is 0700 and gone after success, failure, timeout and cancellation; a missing engine disables only this operation with the reason `libreoffice is not installed`; a real-binary test converts `.docx`, `.xlsx`, `.pptx`, `.odt` and `.rtf` fixtures (built with LibreOffice itself, so no binary fixtures are committed) and asserts a readable PDF whose `pdftotext` contains the document's words; a zero-byte file and a corrupt file fail with a clear message and no output; two conversions in a row and two at once both succeed. Skips without `soffice`.
- [ ] **Step 2: Hostile input.** Apply the Task 1 finding: a test converts a document with an external link and a macro and asserts neither was followed or run (observed by a local listener for the link, and a marker file for the macro). If LibreOffice cannot be stopped from doing either from the command line, the operation writes the needed settings into the private profile before launching and the test asserts they took effect.
- [ ] **Step 3: Input kinds.** Add `InputOffice` (extensions per Task 1: `docx doc odt rtf txt xlsx xls ods csv pptx ppt odp`). Detection is by content where possible and by extension where the formats package already relies on it; a file whose extension and content disagree is refused. Nautilus and `operations` offer `office-to-pdf` only when every selected file is an Office type.
- [ ] **Step 4: Implement** the engine (`ID`, `Available`, `Convert(ctx, in, outDir)`), the catalogue entry (`office-to-pdf`, group `convert`, instant, `Suffix: ""`, one output per input, conflict policy as usual), engine registration in `fcd`, and `engines` / `hello` reporting. A conversion is capped at 120 seconds by default and cancellable.
- [ ] **Step 5:** `fcctl pdf office-to-pdf FILE...`, docs, full suite, commit `feat: add office-to-pdf`.

### Task 5: Redaction core (rasterise and rebuild)

**Files:**
- Modify: `backend/internal/pdfops/catalogue.go`, `operation.go`, `backend/internal/protocol/*`, `backend/internal/server/server.go`, `fcctl`, `docs/PROTOCOL.md`
- Create: `backend/internal/pdfops/redact.go`, `redact_test.go`, `backend/e2e_pdf_redact_test.go`

- [ ] **Step 1: Failing tests (the safety property first).** Using a fixture whose pages read `PAGE1 SECRETWORD ...`, `PAGE2 ...`, `PAGE3 ...`: redact an area covering `SECRETWORD` on page 2 and assert (a) the output has the same page count and page sizes, (b) `pdftotext` finds no text at all on page 2, (c) `qpdf --qdf --object-streams=disable` output contains the bytes `SECRETWORD` **nowhere**, (d) pages 1 and 3 still extract exactly their original text, (e) rendering page 2 shows black pixels where the box was (`pdftoppm` and a pixel check) and the original ink elsewhere, (f) a `/Rotate 90` page is painted at the visual position the user chose. Mutation-check (c) by removing the rasterise step: the test must fail.
- [ ] **Step 2: Validation tests.** A page that does not exist, a box with `w` or `h` not above 0, a box outside 0-1 on either axis, more than 500 boxes, `dpi` outside 150-300, and no areas and no terms are each refused with the offending value named and nothing written.
- [ ] **Step 3: Implement.** Resolve the distinct pages that carry at least one box; make a rotated document upright first (`uprightCopy`) so coordinates are visual; for each affected page run `pdftocairo -png -r DPI -f N -l N`, paint each box opaque black in the decoded image with Go's `image/draw`, write a PNG, and import it as a one-page PDF of exactly the original page size with pdfcpu; then assemble the output as the original document with those pages replaced, using the existing collect/merge machinery so unaffected pages are copied untouched. All intermediates live in one private work directory removed on every path. Output goes through the standard temp-then-rename path.
- [ ] **Step 4: Residue.** Test what else could leak and fix what does: document-level Info and XMP metadata (title, author, subject, keywords) may repeat redacted words, so `pdf-redact` clears them and says it did in its notice; bookmarks whose titles contain redacted text are removed; attachments and form fields on redacted pages are gone with the page. Assert each with `qpdf --qdf` plus `grep`.
- [ ] **Step 5:** Catalogue entry `pdf-redact` (studio, exactly 1 input, `Suffix: "-redacted"`), the `redact` params and protocol validation, `fcctl pdf redact FILE --area PAGE:X,Y,W,H [--area ...] [--term TEXT ...] [--dpi N]`, an end-to-end test through a real daemon, docs with decision 1's warning in the first paragraph. Commit `feat: add pdf-redact`.

### Task 6: Redaction by search

**Files:**
- Modify: `backend/internal/pdfops/redact.go`, `info.go` or `text.go`, `backend/internal/protocol/*`, `backend/internal/server/server.go`, `docs/PROTOCOL.md`
- Create: `backend/internal/pdfops/find.go`, `find_test.go`

- [ ] **Step 1: Failing tests.** `Find(file, terms)` returns a box per match: a single word, a two-word phrase on one line, a match split across a line break (reported as two boxes), case-insensitive matching, a term that is not found (no boxes, not an error), a hyphenated or ligature word per the Task 1 finding, a rotated page (boxes in visual coordinates), a term longer than 100 characters or empty (refused), and more than 50 terms (refused). Overlay test: every returned box, when painted on a rendered page, covers the word it names.
- [ ] **Step 2: Implement** on `pdftotext -bbox-layout`, converting points to fractions of the visual page. `pdf_find` is read-only, honours a password through the unlock stage, and never caches or logs the terms.
- [ ] **Step 3:** `pdf-redact` with `terms` runs `Find` first and merges the boxes with `areas`; the job result's notice lists how many boxes each term produced, so a term that matched nothing is reported (`no match for "..."`), never silently skipped. Redaction by search over a page with no text layer reports `this page has no text layer; draw a box instead`.
- [ ] **Step 4:** Docs, e2e test of a search redaction through a real daemon, commit `feat: redact PDF text by search`.

### Task 7: Catalogue, CLI and docs sweep

- [ ] **Step 1:** README operation table lists `pdf-compare`, `pdf-to-pdfa`, `office-to-pdf` and `pdf-redact` with engine, mode and notes, and a short section on the redaction trade-off and the PDF/A caveat. `catalogue_test.go` asserts each ID, suffix and mode, that the three instant ones are offered for the right selections only (compare: exactly two PDFs; Office: all Office files; PDF/A: PDFs), and that `pdf-redact` is studio-only.
- [ ] **Step 2:** `fcctl` usage text names every new command and flag (the existing usage test enforces it). Full Go checks; commit `docs: document the phase 5 operations`.

---

# Part B: PDF Studio and Nautilus

### Task 8: Regions field and redact request builder (no GTK)

**Files:**
- Modify: `frontends/pdf-studio/pdf_studio/tools.py`, `tests/test_tools.py`
- Create: `frontends/pdf-studio/pdf_studio/regions.py`, `tests/test_regions.py`

- [ ] **Step 1: Failing tests** for a pure `regions` module: converting a rectangle dragged on a canvas of a given pixel size to fractions of the page (any drag direction, clamped to the page, rejected when smaller than a few pixels), converting back for drawing, adding, removing and moving boxes keeping a stable order, grouping boxes by page for the summary line (`3 boxes on pages 2, 5`), and an undo for the last box. No GTK imports.
- [ ] **Step 2:** A new `Field` kind `regions` (value: a list of `{page, x, y, w, h}`), validated like the daemon: fractions in range, page within the document, at most 500. The `pdf-redact` tool: fields `regions`, `terms` (a multi-line list, one term per line, at most 50, each 1-100 characters), `dpi` choice (150, 200, 300; default 200). A form with no regions and no terms is an error naming both. Conformance test against `docs/PROTOCOL.md` like the other tools.
- [ ] **Step 3:** Commit `feat: describe the redaction tool as data`.

### Task 9: The page canvas

**Files:**
- Modify: `frontends/pdf-studio/pdf_studio/tools_view.py`, `window.py`
- Create: `frontends/pdf-studio/pdf_studio/region_picker.py`, test additions in `tests/test_window_smoke.py`

- [ ] **Step 1: Failing smoke tests** (with the daemon faked as in phase 4): the picker shows the current page (rendered through the existing thumbnail loader at the largest width the daemon allows), previous/next buttons and a page spin move between pages, dragging on the canvas adds a box shown as a translucent black rectangle, a box can be selected and deleted, boxes survive page navigation, `get()` returns what `regions` validated, and found-by-search boxes (from `pdf_find`) appear in a different outline colour and are not part of `get()` until the user keeps them.
- [ ] **Step 2: Implement** `RegionPicker` as a `Gtk.Box` holding a `Gtk.Picture` under a `Gtk.DrawingArea` overlay, a `Gtk.GestureDrag` for drawing, a list of boxes with delete buttons for keyboard and screen-reader users, and a "Find terms" button that calls `pdf_find` on a worker thread and hands results back with `GLib.idle_add`. Nothing newer than GTK 4.12.
- [ ] **Step 3:** `FieldRow` renders `regions` fields with the picker and passes it the document path, page count, password and loader. The Tools tab shows the redaction form scrolled, with the canvas at a sensible height for the tiled window size (about 620x670).
- [ ] **Step 4:** Commit `feat: draw redaction boxes on a page in PDF Studio`.

### Task 10: Confirmation before redacting

- [ ] **Step 1: Failing test.** Applying the redact tool first shows an `Adw.AlertDialog` saying how many pages will be replaced by images and what is lost (text, links, annotations on those pages), with **Redact** (destructive appearance) and **Cancel**; Cancel sends nothing; Redact sends one request. The output file name is `<stem>-redacted.pdf` and the original is never overwritten, even when the conflict policy is *Replace* (the tool forces *Keep both* for this operation, and says so under the policy menu).
- [ ] **Step 2:** The apply bar's completion message for a redaction states the number of boxes and pages and any term that matched nothing (taken from the job's notice).
- [ ] **Step 3:** Commit `feat: confirm before redacting`.

### Task 11: Nautilus and installer

- [ ] **Step 1:** The extension offers **Compare** when exactly two PDFs are selected, **Convert to PDF/A** for PDFs, and **Convert to PDF** for Office files, all driven by the daemon's `operations` answer so the extension holds no list of its own. **Redact…** appears in the Customise… flow only (it needs the Studio).
- [ ] **Step 2:** A short notification when Compare finishes says whether the documents differ and opens the report's folder. An Office conversion shows progress like any other job and reports a LibreOffice failure in the shared wording.
- [ ] **Step 3:** The installer test from phase 4 (every Studio module imports from the installed layout) still passes with the new modules. Commit `feat: offer the phase 5 tools in Nautilus`.

### Task 12: Verification

- [ ] **Step 1:** `go test -count=1 ./...`, `go vet ./...`, `gofmt -l .`, every Python suite, `git diff --check`.
- [ ] **Step 2: Version audit.** Re-run the GIR scan against GTK 4.12 and libadwaita 1.5 for the new widgets (`Gtk.GestureDrag`, `Gtk.DrawingArea`, `Gtk.Picture`, `Adw.AlertDialog`). Anything newer is replaced or guarded.
- [ ] **Step 3: Safety audit for redaction.** On a private daemon, redact a real multi-page document containing a recognisable secret word on several pages and in its title metadata. Then, with nothing from the repo's own helpers: run `pdftotext` on every page, `strings` and `qpdf --qdf` plus `grep` on the whole file, and open it in a PDF viewer and try to select and search for the word on a redacted page. The word must not be findable on any redacted page or in the metadata, and must still be findable on pages that were not redacted.
- [ ] **Step 4: Manual checklist (required).** Record the result of each on real files:
  - Redact by drawing a box, by search term, and by both; a phrase split over two lines; a rotated page; a document with a password; the confirmation dialog's Cancel.
  - Compare two PDFs that differ on one page, on every page, and not at all; a scanned PDF.
  - PDF/A: convert a text PDF and a PDF with images; open the result in a viewer; if a validator is available (veraPDF), run it and record the verdict.
  - Office to PDF: `.docx`, `.xlsx`, `.pptx`, `.odt`, `.rtf`; a file with spaces and a non-ASCII name; while LibreOffice is open on the same machine; a corrupt file; cancel mid-conversion and confirm no `soffice` process is left.
  - Omarchy (Hyprland), and if possible an Ubuntu session at the minimum GTK/Adw versions.
- [ ] **Step 5:** Record results in the final commit message, then commit `feat: complete PDF tools phase 5`.

---

## Out of scope for phase 5

- **PDF/A validation** (veraPDF) and PDF/A-4, PDF/UA and PDF/X.
- **OCR-assisted redaction** (finding text in scanned pages), and redaction of pixels *inside* an existing image beyond painting the box; a scanned page redacted by box works because it is rasterised anyway.
- **Visual comparison** (overlay, pixel diff) and comparison of locked documents.
- **Office formats beyond the Task 1 list**, conversion of Office files *from* PDF, and per-format conversion options (page ranges, paper size, comments).
- **Catalogue items that never belonged to a phase**, which remain open and can form a small **phase 6** if wanted: interleave two PDFs (12), PDF to Markdown/HTML (21), text/Markdown to PDF (25), bookmarks/TOC export in the UI (41), split by maximum file size (4), the OCR text-layer step that would enable `pdf-ocr` (38), the Omarchy radial-menu "Customise…" entry, and the phase 4 optional live preview (Task 12 there).

## Risks

- **Redaction is the one tool where a bug leaks data.** The mitigations are the qdf-grep test, the residue test for metadata and bookmarks, the confirmation dialog, never overwriting the original, and the manual safety audit. If any of them cannot be made to hold, the tool ships disabled rather than approximate.
- **Search is not exhaustive.** Text split by odd kerning, hyphenation, ligatures or text drawn as vector outlines may not match a term. The Studio shows found boxes before applying, and the documentation says a search is an aid and the boxes are the redaction.
- **LibreOffice is a large, slow, untrusted-input parser.** Cold start takes seconds; a corrupt or hostile document can hang or consume memory. The mitigations are the private profile, the hard timeout, process-group kill, and the Task 1 / Task 4 hostile-input tests. Running it under a resource limit (memory cgroup) is a possible follow-up and is not assumed here.
- **PDF/A claims need a validator.** Without one the daemon can only say "converted", which is why decision 4 words it that way.
- **Different distributions place Ghostscript's PDF/A files and LibreOffice's binary differently** (`soffice` vs `libreoffice`, versioned `lib` directories). Discovery is tested with a fake filesystem for the Debian/Ubuntu layouts as well as the Arch one found here.
- **Coordinates are the classic source of silent error** (rotated pages, crop boxes, DPI). Every coordinate path has a rotated-page test, and fractions of the visual page are the only unit that crosses the protocol.

## Spike results

- **PDF/A / Ghostscript 10.08.0:** `gs` produced selectable-text PDF/A-1b, PDF/A-2b, and PDF/A-3b outputs with `-dPDFA=<1|2|3> -dPDFACompatibilityPolicy=1 -sColorConversionStrategy=RGB -sProcessColorModel=DeviceRGB -sDEVICE=pdfwrite`. A proper output intent required `PDFA_def.ps`; the definition file must be discovered rather than hard-coded and patched/configured with an absolute ICC profile such as `/usr/share/ghostscript/iccprofiles/srgb.icc`. Fonts remained embedded and `pdfinfo -meta` exposed the expected `pdfaid:part` and conformance `B`.
- **LibreOffice:** private `-env:UserInstallation=file://...` profiles worked when the profile directory did not yet exist. ODT, RTF, TXT, DOCX, and XLSX fixtures converted successfully; PPTX still needs a real Impress fixture in the implementation tests. Separate private profiles allowed concurrent conversions; sharing the default profile is vulnerable to instance contention. Corrupt and zero-byte OOXML inputs failed or showed format-specific behavior and must never publish partial output.
- **Rasterisation:** Letter pages rendered at 150/200/300 DPI as 1275x1650, 1700x2200, and 2550x3300 PNGs. Runtime and output size increased substantially with DPI. The rasterise-and-rebuild safety design is required because `qpdf --qdf --object-streams=disable` retains original text in an unredacted PDF.
- **Text boxes:** `pdftotext -bbox-layout` returned usable point-coordinate word boxes. Rotated pages showed visual-coordinate axis transformation, so search coordinates must be normalised into the visual page coordinate system before converting to fractions.
- **Diff:** basic line-diff probes handled 500 and 10,000-line documents quickly, but production comparison still needs explicit line/edit-size bounds for pathological inputs.
- **Hostile Office inputs:** external-link and macro probes were not run because they require external listeners or executing hostile document content; the implementation must retain the private profile, timeout, process-group cancellation, and conservative no-network design.
- **Environment:** `gs`, `soffice`, `libreoffice`, `pdftotext`, `pdftocairo`, `pdffonts`, `pdfinfo`, and `qpdf` were present. `veraPDF` was not installed, so PDF/A conformance remains best-effort and must be documented as such.
