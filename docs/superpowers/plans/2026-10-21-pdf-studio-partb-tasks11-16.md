# PDF Studio Part B Tasks 11-16 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the GTK4 PDF Studio frontend around the existing headless model and daemon protocol, from thumbnail browsing through apply, Nautilus launch, installation, and verification.

**Architecture:** Keep daemon communication and all validation in small GTK-independent modules. The window composes a virtualized grid, toolbar/actions, split/merge views, and apply controller; worker threads perform socket calls and return results through GLib idle callbacks. GTK is optional for headless tests, while installed launches require GTK 4.12+ and libadwaita 1.5+.

**Tech Stack:** Python 3, unittest, GTK 4, libadwaita, PyGObject, existing `frontends/common/fc_client`.

## Global Constraints

- Protocol remains v1 and new `Params` fields remain optional.
- The Studio never parses or writes PDFs; it uses `FcClient` and daemon operations.
- GTK objects are touched only on the main thread; socket calls run on worker threads.
- Thumbnail requests use batches of at most 20 visible-first pages and width 150/200/300.
- Password-protected and damaged files show explicit status/error copy.
- Drag-and-drop is optional; button/keyboard reordering is mandatory and must remain reliable.
- OCR, password editing/opening, annotations, metadata editing, and non-Linux frontends remain out of scope.

---

### Task 11: Thumbnail loader and virtualized grid

**Files:**
- Create: `frontends/pdf-studio/pdf_studio/thumbnails.py`, `frontends/pdf-studio/pdf_studio/grid.py`
- Modify: `frontends/pdf-studio/pdf_studio/window.py`
- Test: `frontends/pdf-studio/tests/test_thumbnails.py`

**Interfaces:**
- `ThumbnailLoader(client_factory, file, page_count, width=200, callback, error_callback)` batches missing pages in visible-first order, de-duplicates requests, retries a failed batch once, and calls `stop()` safely.
- `PageCell` renders one `PageItem` and exposes an accessible `Page N` label.
- `PageGrid(model, loader_factory, on_selection_changed)` owns `Gio.ListStore` and `Gtk.MultiSelection` when GTK is available.

- [x] Write loader tests for 20-page batching, visible-first ordering, in-flight de-duplication, retry-once, callback delivery, and stop.
- [x] Implement the GTK-independent loader using one worker per loader and a dedicated client connection per batch.
- [x] Implement `Gtk.GridView` cell factory with fixed-height placeholders, blank-page cells, source badges, rotation state, and accessible labels.
- [x] Integrate the grid into the ready window path without blocking the GTK main loop.
- [x] Run `python3 -m unittest discover -s frontends/pdf-studio/tests` and commit `feat: add PDF Studio thumbnail grid`.

### Task 12: Actions, range box, undo/redo, shortcuts

**Files:**
- Create: `frontends/pdf-studio/pdf_studio/toolbar.py`
- Modify: `frontends/pdf-studio/pdf_studio/window.py`, `grid.py`
- Test: `frontends/pdf-studio/tests/test_actions.py`

**Interfaces:**
- `ACTION_TABLE` maps action IDs to model methods and enabled predicates.
- `dispatch_action(model, action_id)` returns the model `Outcome`.
- `range_box_state(model, text)` returns valid/invalid status without mutating selection on invalid input.

- [x] Test every action ID, enabled-state rules, invalid range preservation, and range round trips.
- [x] Implement toolbar buttons, status line, debounced range input, undo/redo, zoom, and documented keyboard shortcuts.
- [x] Connect selection changes to the model and preserve selection while updating the range entry.
- [x] Run frontend tests and commit `feat: add PDF Studio page actions and shortcuts`.

### Task 13: Reordering

**Files:**
- Modify: `frontends/pdf-studio/pdf_studio/grid.py`, `toolbar.py`, `window.py`
- Test: extend `test_actions.py` and model tests as needed

- [x] Add mandatory move-left/right/start/end controls and `Alt` shortcuts using existing model block semantics.
- [x] Add drag/drop only if the GTK API is reliable in the available environment; otherwise document button/keyboard fallback in README.
- [x] Run tests and commit `feat: add PDF Studio page reordering`.

### Task 14: Split and merge views

**Files:**
- Create: `frontends/pdf-studio/pdf_studio/split_view.py`, `merge_view.py`
- Modify: `window.py`
- Test: `frontends/pdf-studio/tests/test_split_merge.py`

**Interfaces:**
- `split_preview(page_count, mode, value)` returns `{parts, error}` and mirrors daemon limits.
- `merge_preview(files, file_pages)` validates aligned ranges and returns ordered rows/errors.

- [x] Test bad ranges, empty groups, single-file splits, 200-part ceiling, every-N remainders, aligned merge specs, and per-file errors.
- [x] Implement split segmented controls, live preview, read-only page coloring, merge rows, add/remove/reorder controls, and page-range fields.
- [x] Integrate mode switching into the window for one versus multiple inputs.
- [x] Run tests and commit `feat: add PDF Studio split and merge views`.

### Task 15: Apply flow and error handling

**Files:**
- Create: `frontends/pdf-studio/pdf_studio/apply.py`, `output_panel.py`
- Modify: `window.py`, `requests.py`
- Test: `frontends/pdf-studio/tests/test_apply.py`

**Interfaces:**
- `reduce_job_event(state, event)` produces immutable UI state for running, completed, completed-with-errors, failed, and cancelled jobs.
- `build_output_options(folder, conflict)` returns daemon request fields.
- `friendly_apply_error(code, message)` delegates to `fc_client.messages` wording.
- `run_apply(client_factory, request, on_event, cancel_event)` uses a dedicated connection and sends cancel on request.

- [x] Test event reduction, output option mapping, friendly errors, and cancellation behavior without GTK.
- [x] Implement output destination/conflict popover, worker-thread apply, progress/cancel, retry for daemon-down, success toast, and Show in Files fallback.
- [x] Add unsaved-edits close confirmation and keep source model unchanged after success.
- [x] Run tests and commit `feat: add PDF Studio apply flow`.

### Task 16: Nautilus, installer, desktop entry, verification

**Files:**
- Modify: `frontends/nautilus/file_converter.py`, `frontends/nautilus/install.sh`, `frontends/nautilus/uninstall.sh`, `README.md`
- Create: `frontends/pdf-studio/io.fileconverter.PdfStudio.desktop`
- Test: `frontends/nautilus/tests/test_extension_menu.py`, `test_install.py`

- [x] Test Customise visibility and activation for PDF-only selections, missing launcher, mixed selections, and ordered paths.
- [x] Implement launcher detection and `_spawn_helper` activation as the last PDF Tools entry.
- [x] Implement dependency-gated Studio installation, exact uninstallation, launcher path setup, desktop entry, and update-desktop-database.
- [ ] Run all Go checks and every Python test directory; run the GUI/manual checklist where GTK/display/dependencies permit and record skipped checks honestly. **Open (manual part):** Go and Python suites pass; the GUI checklist was not recorded.
- [x] Commit `feat: open PDF Studio from Nautilus and install it`.

## Final Verification

```bash
go test ./...
go vet ./...
test -z "$(gofmt -l .)"
python3 -m unittest discover -s frontends/common/tests
python3 -m unittest discover -s frontends/nautilus/tests
python3 -m unittest discover -s frontends/omarchy/tests
python3 -m unittest discover -s frontends/pdf-studio/tests

git diff --check
```

GUI smoke checks require PyGObject, GTK 4.12+, libadwaita 1.5+, a display, a running daemon, and real PDF fixtures. Missing prerequisites are reported as skipped, never represented as passing.
