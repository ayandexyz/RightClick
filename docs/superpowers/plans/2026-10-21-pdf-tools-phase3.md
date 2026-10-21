# PDF Tools Phase 3 Implementation Plan (PDF Studio)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A GTK4 "PDF Studio" window with a page-thumbnail grid, so users can pick, delete, rotate, duplicate, insert blank, reorder, extract, split and merge pages visually, reached from **PDF Tools > Customise…** in Nautilus. The daemon gains the operations and the thumbnail service it needs.

**Spec:** `docs/superpowers/specs/2026-10-21-pdf-tools-design.md`
**Builds on:** phase 1 (`dfcf0bf`..`fce7a1a`) and phase 2 (`2a60941`..`c3f4f6d`, plus the OCR stub).

**Architecture:** The Studio holds no PDF logic. It asks the daemon for page counts and thumbnails, keeps an *edit list* in memory (an ordered list of page items, each a source page plus a rotation, or a blank page), and on Apply sends that list to the daemon as one `run_operation` job, so it is atomic, cancellable, and uses the existing conflict and output machinery. The editing model is a plain Python module with no GTK imports, so it is unit-tested headlessly; the GTK layer only renders it. Thumbnails are rendered by the daemon with `pdftocairo` into a private, size-bounded cache keyed by file content.

**Tech Stack:** Go (daemon), pdfcpu, Poppler; Python 3 + GTK 4 + libadwaita (Studio); Go and `unittest` tests.

## Decisions made in this plan (override before starting if you disagree)

1. **libadwaita is required, minimum 1.5** (GTK 4.12). This resolves the spec's open question 1. Nautilus itself already requires libadwaita on both supported desktops, and an optional-Adw fallback would duplicate the UI code. APIs newer than Adw 1.5 and GTK 4.12 must not be used. Dev machine: GTK 4.24, Adw 1.10, so version limits are **not** enforced by the environment; the implementer must check each API's "since" version.
2. **Thumbnails come from the daemon, not from Poppler's GObject bindings.** The `Poppler` typelib is installed here but is a separate package on Ubuntu (`gir1.2-poppler-0.18`), and daemon-side rendering keeps the GUI logic-free and the cache shared.
3. **One new operation, `pdf-edit`, carries the whole grid.** Delete, rotate-selected, duplicate, insert blank and reorder all reduce to "produce this ordered list of pages, each with a clockwise rotation, with optional blanks". That keeps Apply a single atomic job instead of a chain of operations.
4. **Split by maximum file size is out of this phase** (it needs trial writes to measure). Split by ranges and by every-N-pages are in.

## Global Constraints

- Protocol stays v1 and backward compatible; new `Params` fields are optional and old clients are unaffected.
- All paths absolute, inputs regular PDF files with a `%PDF-` header. Page lists are bounded by the existing 1000-page ceiling (`maxPDFPages`).
- The Studio never opens a PDF itself and never writes output itself. It sends `run_operation` and reports the job result.
- The thumbnail cache is private (directory mode 0700, files 0600), because thumbnails of a sensitive document must not outlive their usefulness. It is bounded by total size and age (defaults 256 MB and 7 days) and pruned at daemon start and after each render batch.
- One request in flight per daemon connection: the server handles requests inline, so the Studio opens a dedicated connection for thumbnails and another for jobs and events. A thumbnail request must never block `cancel`.
- GTK objects are touched only on the main thread; blocking daemon calls run on worker threads and hand results back with `GLib.idle_add`.
- Passwords remain out of scope: encrypted files show a clear message and no editor.
- Run `go test ./...`, `go vet ./...`, `gofmt -l .` (prints nothing), `python3 -m unittest discover` for every Python test directory, and `git diff --check` before completion. GUI code cannot be fully automated; Task 16 is a manual checklist and must be run, not skipped.

## New or changed protocol surface

| Item | Shape |
| --- | --- |
| `pdf_info` | `{file}` → `{pages: int, sizes?: [{w, h, rot}]}`; errors: `password_required`, `invalid_pdf`, `bad_request` |
| `pdf_thumbnails` | `{file, pages?: int[], width?: int}` → `{pages: [{page, path, w, h}]}`; `width` 64–800 (default 200), at most 50 pages per request |
| `Params.edit` | `[{src: int, rotate?: 0\|90\|180\|270}]`; `src` is a 1-based source page, **0 means a blank page**; `rotate` is a clockwise delta from the page's current rotation |
| `Params.ranges` | `"1-3;4-6;7-"`: semicolon-separated groups, each a page spec |
| `Params.every` | integer ≥ 1 |
| `Params.file_pages` | for `pdf-merge`: list aligned with `files`; `""` means all pages of that file |
| `pdf-edit` | studio; one PDF in, `<stem>-edited.pdf` out; requires `edit` |
| `pdf-split-ranges` | studio; one PDF in, `<stem>-part-1.pdf`, `-part-2.pdf`, … out; requires `ranges` |
| `pdf-split-every` | studio; one PDF in, same naming; requires `every` |
| `pdf-merge` | now also accepts `file_pages`; file order is the merge order |

---

# Part A: Daemon

### Task 1: Hoist the shared Python client

**Files:**
- Move: `frontends/nautilus/fc_client/` → `frontends/common/fc_client/`
- Modify: `frontends/nautilus/install.sh`, `frontends/nautilus/uninstall.sh`, `frontends/nautilus/file_converter.py` (path setup only), every importer of `fc_client` (find with `grep -rn fc_client frontends`), `frontends/nautilus/tests/*.py`
- Create: `frontends/common/fc_client/messages.py`, `frontends/common/tests/test_messages.py`

The Nautilus extension and the Studio both need the client and the same friendly error wording, so neither may own it.

- [x] **Step 1:** `git mv` the package; update `install.sh` to copy from `frontends/common/fc_client` (installed layout unchanged: `extensions/fc_client/`), update `uninstall.sh` if paths changed, and make tests import from the new location. Do the Omarchy plugin too if it imports the client.
- [x] **Step 2:** Move the error-code-to-sentence mapping out of `file_converter.py` into `messages.py` as `friendly_error(code, message, name) -> str` covering `password_required`, `invalid_pdf`, and a default; keep wording identical so existing notification tests pass unchanged. Add tests for each code.
- [x] **Step 3:** Run `python3 -m unittest discover -s frontends/nautilus/tests`, the Omarchy tests, and the new common tests. Reinstall is not needed to run the tests, but note in the commit that users must rerun `install.sh`.
- [x] **Step 4: Commit.**

```bash
git add -A frontends
git commit -m "refactor: share the daemon client and error wording across frontends"
```

### Task 2: Shared page-spec test vectors

**Files:**
- Create: `backend/internal/pdfops/testdata/pagespec_cases.json`
- Modify: `backend/internal/pdfops/pagespec_test.go`

The Studio needs a Python page-spec parser (Task 9) for the range box. Two parsers will drift unless both are tested against the same cases.

- [x] **Step 1:** Extract the existing Go table cases into the JSON file as `{spec, page_count, want: [..] | null, error: bool}` and make the Go test load it. Add cases the Studio needs: `""` (empty selection is an error for Go, but record it so Python can choose to treat it as "no selection"), whitespace, `z`, `-3`, `2-`, overlaps, and out-of-range.
- [x] **Step 2:** Run `go test ./internal/pdfops`; commit.

```bash
git add backend/internal/pdfops
git commit -m "test: share page-spec cases as JSON"
```

### Task 3: `pdf_info`

**Files:**
- Modify: `backend/internal/pdfops/poppler.go`, `backend/internal/protocol/types.go`, `backend/internal/protocol/validate.go`, `backend/internal/server/server.go`, `backend/cmd/fcctl/main.go`, `docs/PROTOCOL.md`
- Test: matching `_test.go` files

**Behaviour:** page count comes from the pdfcpu backend (its error mapping already yields `password_required` and the daemon already classifies `invalid_pdf`); page sizes and rotations come from `pdfinfo -f 1 -l N` (parse `Page   N size: W x H pts` and `Page   N rot: R`). If `pdfinfo` is missing or its output cannot be parsed, return `pages` only; the Studio does not depend on sizes. Cap parsing at the page ceiling.

- [x] **Step 1: Write failing tests:** fake-runner output parsing (including an unrecognised line), missing `pdfinfo` yields pages only, protocol validation (absolute path, non-PDF rejected), server round trip for a good, encrypted and corrupt file.
- [x] **Step 2: Implement; add `fcctl pdf info FILE`. Run tests; commit.**

```bash
git add backend docs/PROTOCOL.md
git commit -m "feat: add pdf_info"
```

### Task 4: `pdf_thumbnails` and the private cache

**Files:**
- Create: `backend/internal/thumbs/cache.go`, `backend/internal/thumbs/render.go`
- Modify: `backend/internal/pdfops/poppler.go` (or a sibling), `backend/internal/protocol/*`, `backend/internal/server/server.go`, `backend/cmd/fcd/main.go`, `backend/cmd/fcctl/main.go`, `docs/PROTOCOL.md`
- Test: `backend/internal/thumbs/*_test.go`, server tests

**Behaviour:**
- Cache root `$XDG_CACHE_HOME/file-converter/thumbs` (fallback `~/.cache/...`), created 0700. Key directory = SHA-256 of the file's **content** (streamed) plus the width; files are `p<N>.png`, mode 0600. A moved or renamed file reuses its thumbnails; an edited file does not.
- Render missing pages only, with `pdftocairo -png -scale-to W -f N -l N` per contiguous run, into a temp dir inside the cache, then rename into place. Never leave a half-written PNG visible. `pdftocairo` zero-pads output names to the document's digit count, so list and parse the produced files rather than predicting names.
- Read each PNG's dimensions from its header for the response.
- At most 2 renders at once (semaphore), a 60-second ceiling per request, and cancellation kills `pdftocairo` through the shared runner.
- Prune by age and total size (oldest first) at daemon start and after each batch; both limits are injectable for tests. Pruning must never remove files younger than 60 seconds, so a concurrent request cannot lose its thumbnails.
- Reject `width` outside 64–800, more than 50 pages, pages beyond the document, and encrypted or damaged files with the same codes as `pdf_info`.
- Add `fcctl pdf thumbs FILE --pages 1-5 --width 200` (prints paths) and `fcctl cache clear`.

- [x] **Step 1: Write failing tests:** cache hit avoids invoking the renderer (count calls on a fake runner); key is content-based (copy the file elsewhere, same key; change one byte, different key); permissions are 0700/0600; prune by age and by size; young files survive pruning; concurrent identical requests render once; width and page bounds; cancellation leaves no partial file.
- [x] **Step 2: Implement. Add a real-binary test (skipped without `pdftocairo`)** that renders a 3-page fixture and checks three decodable PNGs of the requested width.
- [x] **Step 3:** Document the privacy note in `docs/PROTOCOL.md` and the README (thumbnails are cached for up to 7 days; `fcctl cache clear` removes them).
- [x] **Step 4: Commit.**

```bash
git add backend docs README.md
git commit -m "feat: add pdf_thumbnails with a private bounded cache"
```

### Task 5: `pdf-edit`

**Files:**
- Modify: `backend/internal/pdfops/operation.go`, `backend/internal/pdfops/catalogue.go`, `backend/internal/pdfops/pdfcpu_backend.go`, `backend/internal/protocol/types.go`, `backend/internal/protocol/validate.go`, `backend/internal/server/server.go`
- Test: `backend/internal/pdfops/*_test.go`, `backend/internal/jobs/operations_test.go`, protocol and server tests

**Interfaces:**

```go
type PageItem struct { Src int; Rotate int } // Src 0 = blank page; Rotate in {0,90,180,270}, clockwise delta
// Params gains: Edit []PageItem
// Backend gains:
Assemble(ctx context.Context, input, output string, items []PageItem) error
```

**Assemble algorithm** (verify each pdfcpu call against the pinned version; `api.CollectFile` is already used, and blank insertion is believed to be `api.InsertPagesFile`, which inserts blank pages before or after selected pages with the dimensions of the neighbouring page):
1. `Collect` the non-blank items' `Src` values in order (duplicates allowed) into a temp file. If **no** item is real (all blank), reject: a PDF of only blank pages is not an edit of this document.
2. Insert blank pages at their final positions, processing from the last position to the first so earlier indexes stay valid. Document that a blank page takes its neighbour's size.
3. For each distinct non-zero `Rotate`, call `Rotate` once on the final page indexes that need it. pdfcpu rotation is relative to existing rotation, matching the "clockwise delta" contract.
4. Intermediate files live in a private temp directory removed on every path; cancellation between steps stops the pipeline.

Validation: `edit` non-empty, at most 1000 items, `rotate` one of the four values, `src` in `0..pageCount` (checked at run time against the real count with a precise message such as `page 12 is past the last page (10)`). `pdf-edit` is studio mode and takes no `pages`, `angle`, `ranges` or `every`.

- [x] **Step 1: Write failing tests with the fake backend:** exact `Collect`, insert, and `Rotate` call sequences for: reorder only; delete (omitted pages); duplicate; a blank at the start, middle and end; mixed rotations; rotate on a duplicated page rotates only that copy; all-blank rejected; out-of-range source rejected; 1001 items rejected.
- [x] **Step 2: Real-binary test** (pdfcpu is linked in; use `pdftotext` for order when present): build a 4-page text fixture, assemble `[3, 1+rot90, blank, 3, 4]`, and assert page count 5, text order `PAGE3 PAGE1 (blank) PAGE3 PAGE4`, rotation of page 2 is 90 and others 0 via `pdfinfo`.
- [x] **Step 3:** Wire the protocol `Params.edit` field, validation, and the server; add `fcctl pdf edit FILE --edit '3,1r90,b,3,4'` (a compact form: number = source page, `rN` suffix = rotation, `b` = blank) so the operation is testable without the GUI.
- [x] **Step 4: Commit.**

```bash
git add backend
git commit -m "feat: add pdf-edit for reorder, delete, duplicate, rotate and blank pages"
```

### Task 6: Split operations and general numbered outputs

**Files:**
- Modify: `backend/internal/pdfops/operation.go`, `backend/internal/pdfops/catalogue.go`, `backend/internal/jobs/manager.go` (`runNumberedOperation`), `backend/internal/protocol/*`, `backend/internal/server/server.go`
- Test: `backend/internal/pdfops/*_test.go`, `backend/internal/jobs/operations_test.go`

Phase 2's `runNumberedOperation` is written for image extraction: it globs `*.png` and reports the notice `no embedded images`. Generalise it first, as its own commit, with the existing extract-images tests staying green: glob by the operation's `OutputExtension()`, take the empty-result notice from the operation (new `EmptyNotice` field), and name outputs from the operation's suffix.

**Operations:**
- `pdf-split-ranges` (`ranges`) and `pdf-split-every` (`every`): `Numbered`, `OutputExt: "pdf"`, `Suffix: "-part"`, outputs `<stem>-part-1.pdf`, `-part-2.pdf`, …. Each part is one `Collect` into the staging directory.
- Rules: `ranges` groups are independent and may overlap; an empty group, a bad spec, or a page out of range fails the whole job before any part is written; `every` must be ≥ 1 and less than the page count, otherwise fail with `split would produce a single file`; a split that would yield more than 200 parts is rejected.

- [x] **Step 1: Generalise the numbered path (refactor commit).**

```bash
git add backend
git commit -m "refactor: let numbered operations choose their extension and empty notice"
```

- [x] **Step 2: Write failing tests** for both split operations (counts, names, page contents via the fake, overlapping ranges, every-N remainder part, rejected inputs, atomic publish: if the third part cannot be published the first two are rolled back, rename conflict only renames the colliding part).
- [x] **Step 3: Implement, add `fcctl pdf split-ranges FILE --ranges '1-3;4-6;7-'` and `fcctl pdf split-every FILE --every 5`, add a real-binary e2e test, commit.**

```bash
git add backend
git commit -m "feat: add split by ranges and split every N pages"
```

### Task 7: Merge with order and per-file page ranges

**Files:**
- Modify: `backend/internal/pdfops/operation.go`, `backend/internal/protocol/*`, `backend/internal/server/server.go`, `backend/internal/jobs/operations.go`
- Test: matching tests

`pdf-merge` accepts `file_pages` (aligned with `files`; `""` = all pages). For each file with a non-empty spec, `Collect` the pages into a private temp file, then `Merge` the resulting list in the given order. Order is simply the order of `files`. Validation: length must equal the file count; any bad spec fails the job before output; a spec that selects nothing is an error. The Task 1 phase-2 merge pre-check (unreadable-input attribution) must still run first and still blame the right file.

- [x] **Step 1: Write failing tests:** per-file ranges produce the right `Collect`/`Merge` calls and call order; no specs behaves exactly as before (existing tests unchanged); length mismatch rejected; bad spec attributed to its file in the error message; temp files removed on success, failure and cancel.
- [x] **Step 2: Implement; extend `fcctl pdf merge` with `--pages-for 2=1-3`. Commit.**

```bash
git add backend
git commit -m "feat: merge with per-file page ranges"
```

### Task 8: Daemon docs and end-to-end

**Files:**
- Modify: `docs/PROTOCOL.md`, `README.md`, `backend/e2e_test.go`

- [x] **Step 1:** Document `pdf_info`, `pdf_thumbnails`, the new `Params` fields, the three new operations, merge ranges, and the cache privacy note, with the edit-item semantics (blank = `src 0`, rotation is a clockwise delta, blank takes its neighbour's size).
- [x] **Step 2:** E2E over the socket (engines permitting): info on good, locked and corrupt files; thumbnails twice with the second served from cache; `pdf-edit` with reorder, rotation and a blank; both splits; merge with ranges; assert page counts and text order.
- [x] **Step 3:** Run the full verification commands; commit.

```bash
git add backend docs README.md
git commit -m "docs: document PDF Studio daemon surface and add end-to-end tests"
```

---

# Part B: PDF Studio

All Studio code lives in `frontends/pdf-studio/` as the package `pdf_studio` with tests in `frontends/pdf-studio/tests/`. It imports the client from `frontends/common/fc_client` (path set up by the launcher; see Task 15).

### Task 9: Editing model (no GTK)

**Files:**
- Create: `frontends/pdf-studio/pdf_studio/model.py`, `frontends/pdf-studio/pdf_studio/pagespec.py`, `frontends/pdf-studio/pdf_studio/requests.py`
- Test: `frontends/pdf-studio/tests/test_model.py`, `test_pagespec.py`, `test_requests.py`

**Interfaces:**
- `PageItem(src: int, rotate: int = 0)`; `src == 0` is a blank page. Each item has a stable `uid` so the view can track identity across moves and duplicates.
- `EditModel(page_count)`: `items`, `selection` (set of uids), and operations `select_all/none/odd/even/invert`, `select_spec(text)` and `selection_spec()` (two-way range box), `rotate(delta)`, `delete()`, `duplicate()`, `insert_blank_after()`, `move_left()/move_right()/move_to_start()/move_to_end()`, `move_to(index)`, `reverse()`, `undo()/redo()` (snapshots, bounded to 100 steps), `dirty`, and `is_identity()`.
- `pagespec.py`: parser and formatter with the same grammar as the daemon, tested against `backend/internal/pdfops/testdata/pagespec_cases.json` (Task 2) located relative to the repo; the test is skipped with a clear message if the file is absent (installed layout) but runs in CI/dev.
- `requests.py`: builders returning `(op, params)`: `edit_request(model)` → `pdf-edit` with the edit list (`pdf-extract` with a page spec when the user only extracted, `pdf-delete` is not needed since `pdf-edit` covers it); `split_request(...)`; `merge_request(files, file_pages)`.
- Selection semantics: operations act on the selection; with an empty selection, destructive operations are no-ops that report "nothing selected" rather than acting on everything. Deleting every page is refused. Selecting by spec refers to **current positions**, not source page numbers, and the labels shown on thumbnails are positions, with the source page shown as a smaller badge when it differs.

- [x] **Step 1: Write failing tests** for every model operation, undo/redo chains, duplicate-then-rotate affecting only the selected copy, selection stability across moves, the range-box round trip (`1-3,7` ↔ selection), refusal to delete everything, `is_identity()`, and the three request builders (exact dictionaries).
- [x] **Step 2: Implement. Run `python3 -m unittest discover -s frontends/pdf-studio/tests`. Commit.**

```bash
git add frontends/pdf-studio
git commit -m "feat: add PDF Studio editing model"
```

### Task 10: Application shell

**Files:**
- Create: `frontends/pdf-studio/pdf_studio/app.py`, `window.py`, `__main__.py`, `frontends/pdf-studio/fc-pdf-studio` (launcher script)
- Test: `frontends/pdf-studio/tests/test_cli.py`

- `Adw.Application` with `HANDLES_OPEN` so repeated launches from Nautilus reuse one process and open one window per invocation. Application ID `io.fileconverter.PdfStudio`. Minimum versions checked at startup (`Gtk >= 4.12`, `Adw >= 1.5`): on failure print one clear line naming the missing package (`gir1.2-gtk-4.0`, `gir1.2-adw-1` on Debian; `gtk4`, `libadwaita` on Arch) and exit 1; never a traceback.
- Argument handling: one PDF opens the page editor; two or more open the merge view; non-PDF or missing paths are reported in a dialog; relative paths are made absolute.
- Loading state: the window shows a spinner while `pdf_info` runs on a worker thread; then the editor, or a status page (`Adw.StatusPage`) for: daemon not running (with the `systemctl --user start fcd` hint and an automatic `ensure_daemon()` attempt first), password-protected, damaged, or unreadable file.
- Default size 1000×720; window title is the file name; no state is persisted in this phase.

- [x] **Step 1: Write failing tests** for argument parsing and path normalisation (pure functions in `__main__`), and for the version check message (inject fake versions).
- [x] **Step 2: Implement the shell with an empty content area.**
- [ ] **Step 3:** Manual smoke: `python3 frontends/pdf-studio/fc-pdf-studio some.pdf` shows the window and the three status pages (stop the daemon, use a locked file, use a corrupt file). **Open (manual part):** the implementation and its automated tests exist; the manual check on real files / a display was not recorded.
- [x] **Step 4: Commit.**

```bash
git add frontends/pdf-studio
git commit -m "feat: add PDF Studio application shell"
```

### Task 11: Thumbnail grid

**Files:**
- Create: `frontends/pdf-studio/pdf_studio/grid.py`, `thumbnails.py`
- Modify: `window.py`
- Test: `frontends/pdf-studio/tests/test_thumbnails.py`

- `thumbnails.py` (no GTK): a loader that requests thumbnails from the daemon in batches of up to 20 visible-first pages on a dedicated connection and worker thread, de-duplicates in-flight requests, retries a failed batch once, and delivers `(src_page, path)` results through a callback. Tested with a fake client for ordering, batching, de-duplication and failure handling.
- `grid.py`: `Gtk.GridView` over a `Gio.ListStore` of item objects with a `Gtk.MultiSelection`. Each cell shows the thumbnail (`Gtk.Picture`, fixed-height container so the grid does not jump while loading), a placeholder spinner until loaded, the position number, a rotation applied by rotating the cached pixbuf (`GdkPixbuf` `rotate_simple`; cache rotated results), a source-page badge when it differs, and a blank-page cell drawn natively. Cells for rows near the viewport request thumbnails; far rows do not (use the factory `bind`/`unbind` hooks and a scroll-driven prefetch of two screens).
- Thumbnail width 200 by default with a zoom control (150/200/300) that triggers re-request at the new width.
- Accessibility: every cell has an accessible label `Page N` (with `, rotated 90°`, `, blank`, `, selected` as applicable).

- [x] **Step 1: Tests for the loader. Step 2: Implement the grid with the model from Task 9 driving the store.**
- [ ] **Step 3:** Manual check on a 200-page PDF: scrolling stays smooth, memory does not grow without bound (check by scrolling to the end and back), and a daemon restart mid-scroll recovers. **Open (manual part):** the implementation and its automated tests exist; the manual check on real files / a display was not recorded.
- [x] **Step 4: Commit.**

```bash
git add frontends/pdf-studio
git commit -m "feat: add PDF Studio thumbnail grid"
```

### Task 12: Page actions, range box, undo, shortcuts

**Files:**
- Create: `frontends/pdf-studio/pdf_studio/toolbar.py`
- Modify: `window.py`, `grid.py`
- Test: model coverage already exists; add `tests/test_actions.py` for the action-to-model mapping table

- Header and action bar: Select (all, none, odd, even, invert), Rotate left/right, Delete, Duplicate, Insert blank after, Reverse, Undo, Redo, zoom. Buttons disable when they cannot act (nothing selected, or the action would delete everything), with tooltips.
- Range entry above the grid, two-way with the selection: typing selects (debounced 200 ms; an invalid spec gets the `error` style and a tooltip with the reason, and leaves the selection untouched); selecting updates the text without moving the cursor while the entry has focus.
- Shortcuts: `Ctrl+A`, `Ctrl+Shift+A`, `Delete`, `Ctrl+Z`, `Ctrl+Shift+Z` / `Ctrl+Y`, `[` and `]` rotate, `Ctrl+D` duplicate, `Ctrl+Return` apply, `Escape` clears selection. Register through `Gtk.ShortcutController` so they appear in the shortcuts overlay (`Gtk.ShortcutsWindow`).
- Status line: `12 pages, 3 selected, 2 edits`.

- [x] **Step 1: Test the action table** (each toolbar action id maps to the right model call and enabled-state rule).
- [ ] **Step 2: Implement; manual check that every action works from both toolbar and keyboard and undo restores the exact previous state including selection.** **Open (manual part):** the implementation and its automated tests exist; the manual check on real files / a display was not recorded.
- [x] **Step 3: Commit.**

```bash
git add frontends/pdf-studio
git commit -m "feat: add PDF Studio page actions, range box, and shortcuts"
```

### Task 13: Reordering

**Files:**
- Modify: `grid.py`, `toolbar.py`
- Test: model tests already cover `move_*`

- Buttons and shortcuts first: move left, right, to start, to end (`Alt+Left/Right`, `Alt+Home/End`) acting on the whole selection as a block that preserves relative order. These ship regardless of the drag work.
- Drag and drop: `Gtk.DragSource` on cells (content is the selected uids; the drag icon is a small stack of the dragged thumbnails) and a `Gtk.DropTarget` on the grid that computes the insertion index from the pointer position with a visible insertion marker. Auto-scroll when the pointer nears the top or bottom edge. Dropping inside the selection is a no-op.
- If drag and drop cannot be made reliable with `Gtk.GridView` in the available time-box, ship the buttons, record the limitation in the README, and leave a tracked follow-up; do not ship flaky drag.

- [ ] **Step 1: Implement buttons and shortcuts; manual check. Step 2: Implement DnD; manual check with a 3-page and a 200-page document, including auto-scroll. Step 3: Commit (one commit per step).** **Open (manual part):** the implementation and its automated tests exist; the manual check on real files / a display was not recorded.

```bash
git add frontends/pdf-studio
git commit -m "feat: add page reordering by buttons"
git commit -m "feat: add page reordering by drag and drop"
```

### Task 14: Split and merge views

**Files:**
- Create: `frontends/pdf-studio/pdf_studio/split_view.py`, `merge_view.py`
- Modify: `window.py`
- Test: `frontends/pdf-studio/tests/test_split_merge.py` for the pure parts (preview computation, validation)

- **Mode switch:** `Adw.ViewSwitcher` in the header with **Edit**, **Split**, and (for two or more inputs) **Merge**. A single PDF offers Edit and Split; several offer Merge only, with a list per file that can be opened in Edit.
- **Split view:** two modes selected by a segmented control: *By ranges* (an entry such as `1-3;4-6;7-`, with the grid reused read-only and coloured per part) and *Every N pages* (spin button). A live preview lists the files that will be created (`name-part-1.pdf: pages 1–3`), counts, and any error (bad range, would produce one file, too many parts), computed locally by pure functions that mirror the daemon rules.
- **Merge view:** a reorderable list of files (drag handle plus up/down buttons), each row with a small first-page thumbnail, page count, and a "pages" entry for a per-file range; add-files button (`Gtk.FileDialog`, PDF filter); remove button; output name preview (`merged.pdf`). Files that are locked or damaged show a row-level error and block Apply with a clear message instead of failing the job later.

- [x] **Step 1: Test the preview and validation functions** against the same rules as Task 6 and 7 (including the 200-part ceiling).
- [ ] **Step 2: Implement both views; manual check with real files.** **Open (manual part):** the implementation and its automated tests exist; the manual check on real files / a display was not recorded.
- [x] **Step 3: Commit.**

```bash
git add frontends/pdf-studio
git commit -m "feat: add PDF Studio split and merge views"
```

### Task 15: Apply flow, output options, and error handling

**Files:**
- Create: `frontends/pdf-studio/pdf_studio/apply.py`, `output_panel.py`
- Modify: `window.py`
- Test: `frontends/pdf-studio/tests/test_apply.py`

- **Output options** (a popover on the primary button): destination *Next to original* (default) or *Choose folder…* (`Gtk.FileDialog.select_folder`); conflict policy *Rename* (default), *Skip*, *Overwrite* with a short explanation that overwrite replaces an existing file.
- **Apply** (`Ctrl+Return`): disabled while `is_identity()` for Edit, with the tooltip "No changes to apply". It builds the request via `requests.py`, runs `run_operation` on a worker thread using a connection dedicated to the job, shows an `Adw.ProgressBar`/banner, and offers **Cancel** (sends `cancel`). On success, an `Adw.Toast` names the output(s) with a **Show in Files** action (open the folder and select the file through `org.freedesktop.FileManager1.ShowItems` over D-Bus, falling back to opening the folder with `Gio.AppInfo`); after success the window stays on the *source* document with its edits intact, and the toast is the confirmation, because Apply never modifies the original and the user may want to apply again with different output options.
- **Errors:** a failed job shows an `Adw.AlertDialog` using `friendly_error` from `fc_client/messages.py`; `password_required`, `invalid_pdf`, an unreachable daemon, and a timeout each have specific wording. The daemon-down case offers **Retry**.
- **Unsaved edits:** closing the window with a dirty model asks to *Discard* or *Keep editing*.
- The pure parts (request assembly, job-event reduction to UI state, error mapping) are tested without GTK.

- [x] **Step 1: Write failing tests** for event reduction (running → completed / completed_with_errors / failed / cancelled), output-option to request mapping, and the friendly error table.
- [ ] **Step 2: Implement; manual check of success, cancel mid-job (use a large merge), conflict rename/skip/overwrite, daemon killed mid-job, and the unsaved-edits dialog.** **Open (manual part):** the implementation and its automated tests exist; the manual check on real files / a display was not recorded.
- [x] **Step 3: Commit.**

```bash
git add frontends/pdf-studio
git commit -m "feat: add PDF Studio apply flow and error handling"
```

### Task 16: Nautilus entry, installer, and manual verification

**Files:**
- Modify: `frontends/nautilus/file_converter.py`, `frontends/nautilus/install.sh`, `frontends/nautilus/uninstall.sh`, `README.md`
- Create: `frontends/pdf-studio/io.fileconverter.PdfStudio.desktop`
- Test: `frontends/nautilus/tests/test_extension_menu.py`, `frontends/nautilus/tests/test_install.py` (if the installer has no tests yet, add focused ones for path logic)

**Nautilus:**
- Add **Customise…** as the last item of **PDF Tools** when the selection is all PDFs. It spawns `fc-pdf-studio <paths…>` through the existing `_spawn_helper` pattern (a separate process; never run GTK work inside Nautilus). Hide it when `~/.local/bin/fc-pdf-studio` does not exist, so users who did not install the Studio never see a dead entry.
- Studio-mode operations stay out of the submenu (still true); Customise is their entry point.

**Installer:**
- Copy `pdf_studio/` and `frontends/common/fc_client/` to `~/.local/share/file-converter/studio/`, write the launcher to `~/.local/bin/fc-pdf-studio` (it sets `sys.path` to the installed directory and runs the app), install the `.desktop` file to `~/.local/share/applications/` (no `MimeType`, so it never becomes a default PDF opener), and run `update-desktop-database` if present. Uninstaller removes exactly these.
- Dependency check, never auto-installed: verify `gi` can import `Gtk 4.0` and `Adw 1` at the required versions and print the Arch and Debian package names otherwise. The Nautilus extension install must still succeed when the Studio's dependencies are missing; only the Studio is skipped, with a clear line saying so.

- [x] **Step 1: Write failing extension tests:** Customise appears for PDFs when the launcher exists, is absent when it does not, is absent for images and mixed selections, and activation spawns the helper with all selected paths in order.
- [x] **Step 2: Implement the extension change, installer and uninstaller.** Test the installer's pure path logic with a temporary `HOME`.
- [x] **Step 3: Full verification:** `go test ./...`, `go vet ./...`, `gofmt -l .`, every Python `unittest discover`, `git diff --check`.
- [ ] **Step 4: Manual checklist (required; GUI behaviour cannot be unit-tested).** Reinstall, `nautilus -q`, then on real files confirm each of the following and record the result in the commit message: **Open (manual part):** the implementation and its automated tests exist; the manual check on real files / a display was not recorded.
  - Right-click one PDF > PDF Tools > Customise… opens the Studio on that file; two PDFs open the merge view; an image selection has no Customise.
  - The grid shows thumbnails quickly on a 10-page and a 200-page file; closing and reopening the same file is noticeably faster (cache hit).
  - Select by clicking, shift-click, ctrl-click, rubber-band if supported, and by typing `1-3,7`; the text and selection stay in sync both ways.
  - Delete, rotate, duplicate, insert blank, reverse, reorder by buttons and by drag; undo and redo each; Apply; open the output in a PDF viewer and confirm page order, rotation and blanks.
  - Split by ranges and every-N produce the previewed files; merge with reordered files and per-file ranges matches the preview.
  - A password-protected file and a corrupt file each show the right status page; stopping `fcd` shows the daemon message and Retry works after `systemctl --user start fcd`.
  - Cancel a long job; conflict rename/skip/overwrite behave as labelled; Show in Files selects the output.
  - Run on the Omarchy (Hyprland) session; if possible also on an Ubuntu session with the minimum GTK/Adw versions, since API-version mistakes only show up there.
  - `fcctl cache clear` empties the thumbnail cache; the cache directory is mode 0700.
- [x] **Step 5: Commit.**

```bash
git add frontends README.md
git commit -m "feat: open PDF Studio from Nautilus and install it"
```

---

## Out of scope for phase 3

Split by maximum file size, passwords (opening or setting), drawing or image signatures, annotations, text editing, watermark, page numbers, crop, resize, n-up, booklet, metadata editing, OCR, redaction, comparing PDFs, the Omarchy radial-menu entry for Customise, persistent window state or recent files, and any non-Linux frontend.

## Risks

- **Drag and drop in `Gtk.GridView`** is the least predictable part of GTK4 here. Buttons and shortcuts ship first (Task 13) so the feature is usable without it, and DnD is allowed to slip.
- **GTK/Adw version drift.** The dev machine is far newer than the stated minimums (GTK 4.24 / Adw 1.10 against 4.12 / 1.5), so using a newer API by accident will not fail here. Review every GTK/Adw call's "since" version, and test on a minimum-version system before declaring done.
- **Large documents.** Thumbnail memory and the 1000-page ceiling interact with the grid: a 1000-page document at 200 px is a lot of cells. Virtualisation (GridView) and lazy loading are the mitigation; Task 11's 200-page manual check should be repeated at 1000 pages.
- **Cache privacy.** Thumbnails of confidential documents sit on disk for up to 7 days. The 0700 mode and the age bound reduce the exposure, and `fcctl cache clear` exists, but users should be told (Task 4 docs). If that is unacceptable, shorten the age bound or render to memory only.
- **`pdf-edit` blank-page insertion** depends on pdfcpu behaviour (neighbour sizing, index handling) that is unverified; Task 5's real-binary test is the guard, and the fallback is generating blank pages with an explicit size through pdfcpu's page-creation API.
- **Moving `fc_client` (Task 1)** changes the installed extension's import layout; every importer must be found, and users must rerun the installer. The Omarchy plugin is the easiest one to miss.
- **Two parsers for page specs** (Go and Python) can drift; the shared JSON vectors in Task 2 are the control and must stay in both test suites.
