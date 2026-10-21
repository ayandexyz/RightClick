# PDF Tools Phase 4 Implementation Plan (Form tools and passwords)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The parameterised PDF tools the spec reserved for phase 4: open, set and remove passwords and permissions; watermark, page numbers, header/footer and Bates stamps; crop, resize, N-up and booklet; and a metadata editor. All of them are driven from PDF Studio through one generic, form-driven **Tools** tab, so adding a tool later is a schema entry, not new GTK code.

**Spec:** `docs/superpowers/specs/2026-10-21-pdf-tools-design.md` (phase 4 and rows 27-29, 31-37, 40)
**Builds on:** phases 1-3 (last commit `1c7e510` plus the drag/merge-reorder commits). Phase 3 left the Studio with an Edit tab, a Split tab, a merge window, the apply bar, and a "this PDF is password-protected" dead end.

**Architecture:** Same shape as phase 3. The Studio holds no PDF logic: it builds a typed request from a form and sends one `run_operation`. The daemon validates typed parameters and runs them through pdfcpu. Two things are new:

1. **An unlock stage.** A password is checked once, in-process, by pdfcpu. If the document is encrypted, the daemon decrypts it to a private temporary copy and every later engine (qpdf, Ghostscript, Poppler, pdfcpu) runs on that plain copy. This keeps the password out of every child process's argument list (readable in `/proc/<pid>/cmdline`) and means no engine needs its own password handling.
2. **A form schema.** Each Studio tool is described declaratively (fields, defaults, validation, how the fields become `Params`). A pure-Python module turns a filled form into a request and is unit-tested without GTK; the GTK layer renders any schema generically.

**Tech Stack:** Go (daemon), pdfcpu v0.12.0 (already linked), qpdf/Ghostscript/Poppler (unchanged); Python 3 + GTK 4 + libadwaita (Studio); Go and `unittest` tests.

## Verified facts this plan relies on

Checked against `pdfcpu@v0.12.0` in the module cache, not recalled from memory:

| Need | API |
| --- | --- |
| Text stamp | `api.AddTextWatermarksFile(in, out, pages, onTop, text, desc, conf)`; `%p` page number, `%p<N>` page number + N (Bates), `%P` page count, `%t` timestamp |
| Image stamp | `api.AddImageWatermarksFile(in, out, pages, onTop, imagePath, desc, conf)` |
| Crop | `api.CropFile(in, out, pages, *model.Box, conf)`, box from `model.ParseBox(s, unit)` |
| Resize | `api.ResizeFile(in, out, pages, *model.Resize, conf)`, config from `pdfcpu.ParseResizeConfig(s, unit)` |
| N-up | `api.NUpFile(inFiles, out, pages, *model.NUp, conf)`; grid from `pdfcpu.ParseNUpGridDefinition(rows, cols, nup)` |
| Booklet | `api.BookletFile(inFiles, out, pages, *model.NUp, conf)` |
| Encrypt / decrypt | `api.EncryptFile`, `api.DecryptFile`, `api.ChangeUserPasswordFile`, `api.ChangeOwnerPasswordFile`; passwords and key length live on `model.Configuration` (`UserPW`, `OwnerPW`, `EncryptUsingAES`, `EncryptKeyLength`) |
| Permissions | `api.SetPermissionsFile`, flags in `Configuration.Permissions` |
| Properties | `api.AddPropertiesFile(in, out, map[string]string, conf)` |
| Bookmarks | `api.ExportBookmarksFile(in, outJSON, conf)` |

Not verified, and therefore spiked in the task that depends on it: whether `AddPropertiesFile` writes the **standard** Info keys (Title, Author, Subject, Keywords) or only custom ones (strip-metadata was dropped in phase 2 because pdfcpu did not touch the standard fields); whether pdfcpu encrypts with only a user password; how booklet treats a page count that is not a multiple of 4; which fonts a text stamp can use for non-Latin text.

## Decisions made in this plan (override before starting if you disagree)

1. **Unlock stage, not per-engine passwords.** One in-process pdfcpu decrypt to a temp copy (directory 0700, file 0600, removed on every exit path including cancellation), then the pipeline is unchanged. The cost is a decrypted copy on disk for the length of the job; it is in a private directory under the job's work dir and is deleted when the job ends.
2. **Thumbnails of a locked document never reach the persistent cache.** The phase 3 cache keeps thumbnails for 7 days, which would defeat the password. A document opened with a password renders its thumbnails into a per-connection directory under `$XDG_RUNTIME_DIR` (tmpfs, so never on disk), removed when the connection closes and swept at daemon start. Unlocked documents use the existing cache unchanged.
3. **The password travels in `params`, in memory only.** It is a new optional `password` field on `pdf_info`, `pdf_thumbnails` and `run_operation`. It must never appear in a `Job`, a job event, a log line, an error message, or a notification. A test greps all of them for a sentinel password. The Studio keeps it in memory for the window's life and never writes it anywhere.
4. **Typed parameters only; the client never sends pdfcpu description strings.** pdfcpu stamps and resizes take a free-form mini-language (`pos:bc, font:..., points:12`). Passing that through would let a client name a font file or any option pdfcpu supports. The daemon accepts typed fields, validates each against an allowlist, and builds the description itself.
5. **Page numbers, header/footer and Bates are presets over one text-stamp operation.** pdfcpu has one stamping engine with `%p`/`%P`/`%p<N>` placeholders; four operations with four code paths would only duplicate it. The Studio shows them as separate tools; the daemon sees `pdf-stamp-text` with a `kind`.
6. **One generic Tools tab in the Studio.** Tool list on the left, a form rendered from a schema in the centre, the existing apply bar below. No per-tool GTK code.
7. **No live preview in v1.** A watermark preview needs a render of the result. Task 12 adds an optional "Preview page 1" using the same operation on a one-page extract; it is allowed to slip.
8. **Single-document tools.** These operations take one PDF. Multi-select in Nautilus keeps opening the merge window; the Tools tab is for the single-document window. N-up and booklet could take several files in pdfcpu but are kept to one input here.

## Global Constraints

- Protocol stays v1 and backward compatible; every new `Params` field and request field is optional, old clients are unaffected.
- Secrets: see decision 3. No password in argv of any child process, ever. Add a test that runs a real encrypted job with a recording `Runner` and asserts the password is in no argument list.
- All new image/file inputs (a watermark image) must be absolute paths to regular files (directories and special files are rejected), image types validated by header, and size-bounded.
- Output naming and conflict policy reuse the existing `Suffix` / OutputPlan machinery; no operation writes beside its input except through the job layer.
- Every new operation honours cancellation and leaves no partial output on failure, like the rest of `pdfops`.
- The 1000-page ceiling (`maxPDFPages`) applies to every new operation.
- Studio code follows the phase 3 rules: GTK only on the main thread; the model/request layers import no GTK; APIs newer than GTK 4.12 / Adw 1.5 are forbidden. The dev machine is much newer, so re-run the "since" audit used at the end of phase 3 (GIR scan of every Gtk/Adw/Gdk class and method called) as part of Task 14.
- Run `go test ./...`, `go vet ./...`, `gofmt -l .` (prints nothing), `python3 -m unittest discover` for every Python test directory, and `git diff --check` before each commit. GUI work has a manual checklist (Task 14) that must be run, not skipped.

## New or changed protocol surface

| Item | Shape |
| --- | --- |
| `password` | optional string on `pdf_info`, `pdf_thumbnails`, `run_operation`; wrong password → error code `password_incorrect`, none given for an encrypted file → `password_required` (unchanged) |
| `Params.stamp` | `{kind: "text"\|"page-numbers"\|"header-footer"\|"bates"\|"image", text, image, position, opacity, rotation, size, color, pages, on_top, start, prefix, left, centre, right}` |
| `Params.crop` | `{unit: "mm"\|"pt"\|"percent", top, right, bottom, left, pages}` |
| `Params.resize` | `{to: "A4"\|"A3"\|"A5"\|"Letter"\|"Legal", fit: "scale"\|"crop"\|"pad", pages}` |
| `Params.layout` | `{per_sheet: 2\|4\|6\|9, orientation: "portrait"\|"landscape", margin, border}` for N-up; `{per_sheet: 2\|4}` for booklet |
| `Params.encrypt` | `{user, owner, permissions: [..]}` |
| `Params.metadata` | `{title, author, subject, keywords, custom: {k: v}}` |
| `pdf_metadata` | `{file, password?}` → `{title, author, subject, keywords, producer, creator, created, modified, custom}` |
| `pdf-decrypt` | studio; password required; `<stem>-unlocked.pdf` |
| `pdf-encrypt` | studio; `<stem>-protected.pdf` |
| `pdf-permissions` | studio; `<stem>-restricted.pdf` |
| `pdf-stamp-text` / `pdf-stamp-image` | studio; `<stem>-stamped.pdf` |
| `pdf-crop` / `pdf-resize` | studio; `-cropped` / `-resized` |
| `pdf-nup` / `pdf-booklet` | studio; `-nup` / `-booklet` |
| `pdf-metadata` | studio; `-edited` is reserved for `pdf-edit`, so this one is `<stem>-metadata.pdf` |

---

# Part A: Daemon

### Task 1: Spikes that decide the open questions

**Files:**
- Create: `backend/internal/pdfops/spike_test.go` (deleted before the commit that closes this task; its findings go in the plan's "Spike results" section below and in code comments where they shape behaviour)

Each spike runs real pdfcpu on a real fixture and records the observed behaviour. No production code in this task.

- [x] **Step 1: Properties.** Call `AddPropertiesFile` with `Title`, `Author`, `Subject`, `Keywords`. Read the result with `pdfinfo` and `qpdf --json`. Record whether the standard Info keys changed, whether the XMP packet (if any) still disagrees, and what `Producer`/`ModDate` do. If standard keys are not set, test the fallback: `qpdf --update-from-json` for the trailer `/Info`, and Ghostscript `pdfmark` DOCINFO. Pick the engine that actually sets all four and also leaves XMP consistent, or record that XMP cannot be kept in sync and the editor warns about it.
- [x] **Step 2: Encryption.** Encrypt with (a) user password only, (b) owner only, (c) both, AES-256. Open each with `pdfinfo`, `qpdf --check` and `pdftotext` using and not using the password. Record what pdfcpu does when only one password is set and what each does for permission flags (`qpdf --show-encryption`).
- [x] **Step 3: Wrong password.** Record the exact pdfcpu error for a wrong password versus no password, so `classifyUnreadable` can tell `password_incorrect` from `password_required` (today both map to `password_required`).
- [x] **Step 4: Stamps.** Text stamp with `%p`, `%P`, `%p100`; a non-Latin string (e.g. Bengali, Devanagari, Cyrillic, an accented Latin string) with the default core font. Record which scripts render and which produce missing glyphs or an error. Record how a stamp behaves on a page with `/Rotate 90`.
- [x] **Step 5: Booklet and N-up.** Booklet with 5, 7 and 8 pages; N-up with a page count that does not fill the last sheet. Record whether pdfcpu pads with blanks or errors.
- [x] **Step 6:** Write the findings into this file under **Spike results**, adjust the later tasks if a finding contradicts them, delete the spike test, commit `docs: record phase 4 spike results`.

### Task 2: Passwords in the daemon (unlock stage)

**Files:**
- Modify: `backend/internal/protocol/types.go`, `validate.go`, `backend/internal/server/server.go`, `backend/internal/jobs/operations.go`, `backend/internal/pdfops/pdfcpu_backend.go`, `info.go`, `backend/internal/thumbs/*`, `docs/PROTOCOL.md`
- Create: `backend/internal/pdfops/unlock.go`, `unlock_test.go`
- Test: `backend/e2e_pdf_password_test.go`

- [x] **Step 1: Write failing tests.** (a) `Unlock(ctx, in, password)` on a plain PDF returns the input path and a no-op cleanup; (b) on an encrypted PDF with the right password returns a private temp path whose content is a readable PDF and whose directory is 0700, file 0600; (c) wrong password → `ErrPasswordIncorrect`; none → `ErrPasswordRequired`; (d) cleanup removes the directory, and is also run when the context is cancelled mid-job; (e) with a recording `Runner`, a full encrypted `pdf-compress-balanced` job puts the password in **no** argument list; (f) a sentinel password appears in no job field, event, log output or error string.
- [x] **Step 2: Implement `Unlock`.** pdfcpu `DecryptFile` with `UserPW` set, into `os.MkdirTemp` 0700. It returns `(path, cleanup, error)`. Map the two pdfcpu failures from Task 1 to `ErrPasswordRequired` / new `ErrPasswordIncorrect` and add `password_incorrect` to `classifyUnreadable` and the code mapping. Where the owner password but not the user password is what is known, record the Task 1 behaviour.
- [x] **Step 3: Thread it through.** `pdf_info`, `pdf_thumbnails` and `Operation.Run` accept an optional password; `Run` unlocks first and hands the unlocked path to the existing code. `Params.Password` is never serialised into a `Job`. Thumbnails of an unlocked-by-password document render into the per-connection tmpfs directory (decision 2); a test asserts nothing is written under the persistent cache for that document and that the directory is gone after the connection closes.
- [x] **Step 4: Validation.** The password is length-bounded (say 127 bytes, the PDF limit), may not be logged by the request logger, and the server's request logging is checked to skip `params.password`. The daemon writes nothing to stdout or stderr during locked jobs (secret audit, Task 14 Step 3), so there is no request log to leak from.
- [x] **Step 5: Docs and commit.** `docs/PROTOCOL.md` documents the field, the two error codes, and the tmpfs thumbnail behaviour. Commit `feat: open password-protected PDFs through an unlock stage`.

### Task 3: Remove, set and restrict passwords

**Files:**
- Modify: `catalogue.go`, `operation.go` (Params), `protocol/*`, `jobs/operations.go`, `pdfcpu_backend.go`, `backend/cmd/fcctl/main.go`, `docs/PROTOCOL.md`
- Create: `backend/internal/pdfops/crypto.go`, `crypto_test.go`

- [x] **Step 1: Failing tests** for `pdf-decrypt` (needs the password; output opens with no password), `pdf-encrypt` (user and/or owner per Task 1; output is AES-256 per `qpdf --show-encryption`; refuses an already-encrypted input with a clear message unless a password was supplied), and `pdf-permissions` (flags map to what `qpdf --show-encryption` reports: print, copy, modify, annotate; the permission set is typed, never a raw integer from the client). Include a real-binary test that skips when `qpdf` is missing.
- [x] **Step 2: Implement** the three operations on `PDFCPU` in `crypto.go`; add catalogue entries (studio mode, `OutputExt` pdf, suffixes above); `pdf-decrypt` is the only operation that sets `NeedsPassword`.
- [x] **Step 3:** `fcctl pdf pdf-decrypt file.pdf --password-file ./pw` reads the password from a file or stdin, **never from a flag** (flags land in shell history and `/proc`). Document it.
- [x] **Step 4:** Run the full Go suite; commit `feat: add pdf-decrypt, pdf-encrypt and pdf-permissions`.

### Task 4: The stamp engine (watermark, page numbers, header/footer, Bates)

**Files:**
- Modify: `catalogue.go`, `operation.go`, `protocol/*`, `jobs/operations.go`, `fcctl`, `docs/PROTOCOL.md`
- Create: `backend/internal/pdfops/stamp.go`, `stamp_test.go`, `stamp_real_test.go`

- [x] **Step 1: Failing tests for the description builder**, a pure function from typed fields to pdfcpu's description string: position presets (`tl tc tr ml mc mr bl bc br`) to `pos:`, opacity 0-100 to `opacity:`, rotation, size, colour as `#rrggbb` only, `on_top`. Table-driven; an unknown position, an opacity outside 0-100, a colour that is not `#rrggbb`, text with a newline or longer than 200 characters, and a font size outside 4-200 are rejected. Assert the builder can never emit an option the allowlist does not name (fuzz the string fields with `,` and `:`).
- [x] **Step 2: Failing tests for the kinds.** `page-numbers` produces `%p` or `%p of %P` at the chosen position and starting number (`start` becomes the `%p<N>` offset); `header-footer` places up to three texts (left, centre, right) in one pass using `%p`/`%P`/`%t` placeholders; `bates` is a prefix plus a zero-padded counter at a start value, in the corner chosen; a page selection limits the pages. Real-binary tests read the output back with `pdftotext -f N -l N` and assert the literal text on the right pages and absence on the others.
- [x] **Step 3: Image stamp.** Validate the image path (absolute, regular file, header is PNG/JPEG, at most 20 MB), then `AddImageWatermarksFile`. Test a missing file, a directory, a text file renamed `.png`, and a huge file.
- [x] **Step 4: Rotated pages.** Using the Task 1 finding, a stamp on a `/Rotate 90` page lands at the visual position the user chose; test against a real rotated fixture.
- [x] **Step 5: Non-Latin text.** Apply the Task 1 finding. The default is to **reject** text with characters the stamp font cannot draw, naming the first offending character, rather than writing a PDF with missing glyphs. If Task 1 shows a safe route to a private font (a TTF installed into a daemon-owned pdfcpu config directory), record it here as a follow-up, not in this task.
- [x] **Step 6:** `fcctl pdf` flags for each kind; run the suite; commit `feat: add text and image stamps, page numbers, header/footer and Bates`.

### Task 5: Crop and resize

**Files:** Modify catalogue, params, protocol, jobs, fcctl, docs. Create `backend/internal/pdfops/geometry.go`, `geometry_test.go`.

- [x] **Step 1: Failing tests.** Crop: margins in `mm`, `pt` or `percent`; negative, non-numeric, or margins that leave no page (top+bottom ≥ height) are rejected with the page named; the output's `pdfinfo -box` MediaBox/CropBox equals the expectation within a point. Resize: `A4`/`A3`/`A5`/`Letter`/`Legal` with `scale`, `crop`, `pad`; the output page size equals the target; content is not distorted (aspect preserved for `scale`).
- [x] **Step 2: Rotation.** Crop and resize on a `/Rotate 90` page behave in visual terms (Task 4's fixture). Document and test the choice.
- [x] **Step 3: Implement** with typed params → `model.ParseBox` / `pdfcpu.ParseResizeConfig` strings built server-side (decision 4). Commit `feat: add pdf-crop and pdf-resize`.

### Task 6: N-up and booklet

**Files:** Modify catalogue, params, protocol, jobs, fcctl, docs. Create `backend/internal/pdfops/layout.go`, `layout_test.go`.

- [x] **Step 1: Failing tests.** `pdf-nup` for 2, 4, 6, 9 per sheet, portrait/landscape, optional margin and border: the output page count is `ceil(pages / per_sheet)`, sheet size unchanged; an input that does not fill the last sheet works (behaviour from Task 1). `pdf-booklet` for 2 and 4 per sheet: output page count and imposition order checked by extracting text from a fixture whose pages are labelled; a page count that is not a multiple of 4 follows the Task 1 finding (pad with blanks, or refuse with a clear message — whichever pdfcpu does reliably).
- [x] **Step 2: Implement** and commit `feat: add pdf-nup and pdf-booklet`.

### Task 7: Metadata read and edit

**Files:** Modify catalogue, params, protocol, server, jobs, fcctl, docs. Create `backend/internal/pdfops/metadata.go`, `metadata_test.go`, `backend/e2e_pdf_metadata_test.go`.

- [x] **Step 1: `pdf_metadata` read.** Title, Author, Subject, Keywords, Producer, Creator, dates and custom keys, from the engine Task 1 selected. Test with a fixture that has all of them and one that has none.
- [x] **Step 2: Failing tests for `pdf-metadata` write**, using the engine and fallback Task 1 chose: after the edit, `pdfinfo` reports all four standard fields, clearing a field clears it, custom keys round-trip, the page content is untouched (byte-for-byte for stream objects is not required; text and page count are). Include the XMP-consistency finding: if the file has an XMP packet that now disagrees, either update it or return a `notice` the Studio shows ("this file also stores metadata in XMP, which was left unchanged").
- [x] **Step 3: Implement** and commit `feat: read and edit PDF metadata`. If Task 1 finds no engine that sets the standard fields reliably, this task is reduced to the read side, the write side is moved to "Out of scope" below, and the plan is updated before continuing.

### Task 8: `operations` catalogue, CLI and docs sweep

**Files:** Modify `README.md`, `docs/PROTOCOL.md`, `backend/cmd/fcctl/main.go` usage text, `catalogue_test.go`.

- [x] **Step 1:** The README operation table lists every phase 4 operation with engine, mode and notes. `catalogue_test.go` asserts each new ID, its suffix, its mode and that none is offered by the Nautilus instant submenu (they are all studio mode and need parameters).
- [x] **Step 2:** Full Go checks; commit `docs: document the phase 4 operations`.

---

# Part B: PDF Studio

### Task 9: Form schema and request builders (no GTK)

**Files:**
- Create: `frontends/pdf-studio/pdf_studio/tools.py`, `frontends/pdf-studio/tests/test_tools.py`

A tool is data: `Tool(id, label, group, op, fields, build)`. A `Field` has `key`, `label`, `kind` (`text`, `choice`, `number`, `colour`, `file`, `pages`, `switch`, `password`), `default`, `required`, `help`, and optional bounds or choices. `build(values, context)` returns `(op, params)` or raises `RequestError` naming the field.

- [x] **Step 1: Failing tests** for every tool in the phase: required fields, bounds (opacity, size, margins), the page-spec field reusing the existing parser, a `colour` field accepting only `#rrggbb`, a `file` field requiring an absolute path, mutually dependent fields (Bates needs a start; header/footer needs at least one of left/centre/right), and the exact `Params` each produces. A conformance test asserts each tool's `Params` keys are the ones documented in `docs/PROTOCOL.md`.
- [x] **Step 2: Implement** `tools.py`: the tool list for stamps (watermark text/image, page numbers, header/footer, Bates), crop, resize, N-up, booklet, encrypt, permissions, decrypt, metadata. Validation messages say what to change.
- [x] **Step 3:** Commit `feat: describe PDF Studio tools as data`.

### Task 10: Password prompt and unlock in the Studio

**Files:** Modify `loading.py`, `window.py`, `thumbnails.py`, `frontends/common/fc_client/client.py`, `messages.py`. Create `pdf_studio/password_dialog.py`. Tests: `test_loading.py`, `test_window_smoke.py`, `common/tests/test_pdf_client.py`.

- [x] **Step 1: Client.** `pdf_info`, `pdf_thumbnails` and `run_operation` take an optional `password`; it is sent only when given. Add `password_incorrect` wording to `friendly_error`. Tests assert the field is absent when no password is set and present when it is.
- [x] **Step 2: Failing tests.** A locked document shows a password prompt instead of the dead-end status page; a wrong password re-prompts with "That password did not work" and keeps the dialog; cancel returns to the status page; a right password loads the editor, and the same password is used by the thumbnail loader and by Apply. Closing the window drops the password (an assertion that no attribute still references it is brittle; instead assert it is cleared on close). Markup in the file name is escaped, as in phase 3.
- [x] **Step 3: Implement** an `Adw.AlertDialog` (Adw 1.5) whose extra child is a `Gtk.PasswordEntry` (GTK 4.4), with Unlock and Cancel responses. The password lives in a single window attribute, is passed to workers, is never put in a log message, and is not stored when the window closes.
- [x] **Step 4:** Real-daemon verification on a private `fcd`: an encrypted fixture opens with the right password and shows thumbnails; the wrong password re-prompts; check that nothing lands in the persistent thumbnail cache directory for that file. Commit `feat: unlock password-protected PDFs in PDF Studio`. Run 2026-10-22 by driving the real `StudioWindow` (display `wayland-1`, never presented) against a private `fcd` with its own runtime and cache directories; only `_make_client` was pointed at the private socket and the password dialog answered in code. Results: a locked document shows the status page and asks for the password; no thumbnails are requested before unlocking; a wrong password re-prompts with "That password did not work." and is not kept; the right password opens it (5 pages) and all 5 thumbnails render, from the connection's private `locked-*` directory; **nothing from the locked document reached the persistent cache** (control: an unlocked document put 5 PNGs there); *Remove password* and *Document properties* run through the window's own request path complete and the output opens without a password; closing removes every `locked-*` directory; the passwords appear in no daemon output, runtime, cache or output file.

### Task 11: The Tools tab

**Files:** Modify `window.py`, `panels.py`. Create `pdf_studio/tools_view.py`. Tests: `test_window_smoke.py`, new `test_tools_view.py`.

- [x] **Step 1: Failing tests.** The Tools tab lists tools grouped by `Tool.group`; choosing one renders its fields from the schema (one widget per `Field.kind`); invalid input shows the field's error under it and keeps Apply from sending; a valid form sends the exact request `Tool.build` produces with the apply bar's output options; switching tools keeps each tool's entered values; a `file` field uses `Gtk.FileDialog` and shows the chosen name. Locked documents show only **Remove password**.
- [x] **Step 2: Implement** `tools_view.py` as a generic renderer over `tools.Tool`. The page-spec field shares the grid's selection: a "Use selected pages" button fills it from the Edit tab's selection.
- [x] **Step 3:** The window's `_request()` returns the Tools tab's request when that tab is visible. The apply bar, progress, Cancel and Show in Files are reused unchanged.
- [x] **Step 4:** Commit `feat: add a Tools tab to PDF Studio`.

### Task 12: Optional preview (allowed to slip)

- [ ] **Step 1:** For stamp, crop, resize, N-up and booklet, a **Preview** button runs the same operation on a one-page extract of the current page into a private temporary directory and shows the result in a `Gtk.Picture` using the existing thumbnail service at a larger width. The preview never writes beside the source and is deleted when the dialog closes. **Dropped (decision 2026-10-22):** not built. Applying a tool already shows a result in the apply bar and *Show in Files*, no later work depends on it, and Step 2 allows dropping it.
- [x] **Step 2:** If this proves flaky or slow, drop it and note it in the manual checklist; nothing else depends on it. Preview is dropped and recorded in the manual checklist below.

### Task 13: Nautilus and installer touch-ups

- [x] **Step 1:** No new Nautilus entries are needed (all phase 4 tools are studio-mode). Confirm `Customise…` opens a locked PDF straight to the password prompt, not an error notification. Verified 2026-10-22: the menu decision never opens the document (`operations` reads only the header, so a locked PDF gets the same menu), **Customise…** spawns `fc-pdf-studio PATH`, and Studio meets a locked file with its status page and password prompt (`test_window_smoke.py`: prompt, wrong password, cancel, close). Against a real daemon, the instant operations that Nautilus also offers for a locked file failed with raw tool text in three cases (`pdf-strip-metadata`, `pdf-to-text`, `pdf-extract-images`: code `internal`); `Operation.Run` now reports `password_required` for any password-less failure on an encrypted input (`TestEveryInstantOperationOnALockedFileAsksForThePassword`). Not exercised: a real Nautilus session.
- [x] **Step 2:** The installer copies the new Studio modules (it copies the whole package already; add a test that every new module imports from the installed layout, the same check used in phase 3).
- [x] **Step 3:** Commit `chore: ship the phase 4 Studio modules`.

### Task 14: Verification

- [x] **Step 1:** `go test ./...`, `go vet ./...`, `gofmt -l .`, every Python suite, `git diff --check`.
- [x] **Step 2: Version audit.** Re-run the GIR scan from phase 3 (every Gtk, Adw and Gdk class and method called, including inherited ones) against GTK 4.12 and libadwaita 1.5. Anything newer is replaced or guarded. Run 2026-10-22 with a scanner that reads the installed GIR `version` (since) attributes (Gtk and Gdk 4.12, Adw 1.5, Gio and GLib 2.76, Pango 1.50) over every Studio module: it checks each `Gtk.`/`Adw.`/`Gdk.` type and enum member named in the code, and each method, property, signal name and constructor keyword against the classes the code uses, their ancestors and one level of return types. Result: nothing newer than the minimums (the one near-miss, `activates_default` on `Gtk.PasswordEntry`, is older than 4.12 on that class). The scanner was self-tested by planting `Adw.Spinner` (1.6), `Gtk.PrintDialog` (4.14) and `Gtk.Widget.get_limit_events` (4.18); all three were flagged. Limits: an API on a class more than one return-type hop from anything named in the code is not checked, and the dev machine still has GTK 4.24 / Adw 1.10, so a run on the real minimum versions remains worthwhile.
- [x] **Step 3: Secret audit.** Run an encrypted job end to end on a private daemon with a recognisable password, then grep the daemon's stdout and stderr, the journal for the unit if running one, the private runtime and cache directories, and `/proc/<pid>/cmdline` of every child process while it runs. The password may appear nowhere. Run on 2026-10-22 against a private `fcd` (private XDG dirs, logging shims on PATH for every Poppler/qpdf call, `fcctl` with password files): info, thumbnails, find, extract, rotate, decrypt, reverse, search-redact, encrypt and permissions. The sentinel passwords appear in no daemon stdout/stderr, runtime or cache file, output, `fcctl` output, user journal, or child-process argument. **Found and fixed:** `pdf_find` and its rotation lookup passed `-upw PASSWORD` to `pdftotext` and `pdfinfo`; they now unlock to a private copy first (`TestFindWithAPasswordNeverPutsItOnACommandLine`). `/proc/PID/cmdline` polling is too slow to catch these short-lived tools, hence the shims.
- [ ] **Step 4: Manual checklist (required).** On real files, record the result of each:
  - Open a password-protected PDF: prompt appears; wrong password re-prompts; right password shows the grid; **Remove password** writes an unlocked copy that opens in a viewer without a prompt.
  - **Password-protect** a PDF, open it in a viewer: the prompt appears; permissions set to "no printing/copying" are honoured by the viewer.
  - Text watermark, image watermark, page numbers (`1 of N`, custom start), header/footer with three zones, Bates numbering: open each output in a viewer and confirm position, opacity, rotation and the pages chosen.
  - A stamp on a rotated page, and a non-Latin string (expect the clear refusal).
  - Crop with margins, resize to A4 and Letter, N-up 4 per sheet, booklet from a page count that is not a multiple of 4.
  - Edit metadata; confirm in a viewer's document-properties dialog and with `pdfinfo`.
  - Every tool cancels cleanly mid-job and leaves no output.
  - Run on Omarchy (Hyprland); if possible on an Ubuntu session at the minimum GTK/Adw versions.
  - A locked PDF's thumbnails are not in `~/.cache/file-converter/thumbs` afterwards.
  - Preview (Task 12) was not built, so there is nothing to check; the tools apply and show their result in the apply bar.
- [ ] **Step 5:** Record results in the final commit message, then commit `feat: complete PDF tools phase 4`.

---

## Out of scope for phase 4

Certificate encryption and digital signatures; drawn or image signatures; annotations and form filling; redaction; comparing PDFs; PDF/A; Office, Markdown, text or HTML conversion; export of bookmarks to the UI (the daemon call exists but has no Studio surface); split by maximum file size; interleave; OCR (still a stub); custom fonts for non-Latin stamps (a follow-up if Task 1 shows a safe route); a live preview if Task 12 slips; the Omarchy radial-menu entry; and any non-Linux frontend.

## Risks

- **Passwords are the highest-stakes part of this phase.** A decrypted copy exists on disk during a job, and a locked document's thumbnails are rendered at all. Decisions 1 and 2 bound both (private directory removed on every exit path; tmpfs thumbnails). The secret audit in Task 14 is the guard; do not skip it. If it turns up a leak, fix it before any other work.
- **pdfcpu's metadata handling already failed once** (strip-metadata, phase 2). Task 1 decides whether Task 7 is feasible with pdfcpu, needs another engine, or shrinks to read-only. Do not start Task 7 before the spike.
- **Non-Latin text in stamps** is a real limitation of pdfcpu's core fonts. The plan refuses clearly instead of producing a PDF with missing glyphs; users with other scripts will want the font follow-up.
- **Stamp, crop and resize on rotated pages** are classic sources of off-by-90-degrees bugs. Real rotated fixtures are required in the tests, not hand-checked once.
- **Form schema versus real needs.** A generic renderer is cheaper than per-tool GTK but can feel flat. If a tool needs a bespoke widget (a position picker), add a `Field.kind` for it rather than a one-off panel.
- **Booklet and N-up semantics** depend on pdfcpu behaviour that Task 1 measures; if it errors on awkward page counts the Studio must pad or explain, not pass the error through.
- **GTK/Adw version drift** (dev machine GTK 4.24, Adw 1.10) applies exactly as in phase 3; the Task 14 audit and an Ubuntu minimum-version run are the controls.

## Spike results

Task 1 was run against pdfcpu v0.12.0 and the installed Poppler/qpdf/Ghostscript toolchain on 2026-10-21 using the existing generated text-PDF fixture.

- **Properties:** `api.AddPropertiesFile` writes the four standard Info keys (`Title`, `Author`, `Subject`, and `Keywords`). `pdfinfo` reports all four values. The output has no metadata stream, so there was no XMP packet to compare in this fixture. `Producer` remains pdfcpu v0.12.0 dev and `CreationDate`/`ModDate` are set to the rewrite time. Task 7 can use pdfcpu for standard fields, with a warning/follow-up for files containing a separate XMP packet.
- **Encryption:** pdfcpu refuses user-password-only encryption with `please provide owner password and optional user password`; owner-only succeeds as AES-256 with an empty user password and opens without a password while restrictions are enforced. Both passwords produce AES-256 encryption and require the user password for Poppler reads. The tested defaults were no print, no copy, no change, and no annotations. The crypto task must require an owner password when calling pdfcpu.
- **Wrong password:** `api.ValidateFile` returns `pdfcpu: please provide the correct password` both when no password is supplied and when a wrong one is supplied; the correct user password validates successfully. The daemon must classify omitted password as `password_required` and supplied-but-invalid password as `password_incorrect`.
- **Stamps:** `%p` expands to the page number, `%P` to the page count, and `%p100` produces a zero-padded counter (`01` through `08` for an eight-page document). pdfcpu accepts Bengali, Devanagari, and Cyrillic input without an error, but core-font output loses unsupported glyphs; accented Latin rendered and extracted successfully. Later stamp validation must reject non-Latin text. A stamp on a page rotated with pdfcpu was rewritten with page rotation 0; visual-position handling needs an explicit rotated-page fixture in Task 4.
- **Booklet/N-up:** With a 1x2 grid, booklet accepts 5, 7, and 8 pages and outputs 4 pages in every case, padding/omitting content according to pdfcpu's booklet imposition. N-up outputs `ceil(pages/2)` pages (3, 4, and 4 respectively), including a partial final sheet. The layout task should assert these page counts and inspect imposition order with labelled pages.
