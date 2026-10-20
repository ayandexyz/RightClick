# PDF Tools Design

## Goal

Add iLovePDF-style PDF operations (merge, split, page editing, compress, secure, watermark, OCR, and more, 40 in total) to the existing local daemon, reachable from the file manager right-click menu. Operations that need no input run instantly. Operations that need choices, above all page selection, open a small GTK4 window ("PDF Studio") with a thumbnail grid.

Everything runs locally. No uploads.

## Non-goals (v1)

- Drawing/image signatures and interactive form filling (need a real renderer and annotation editing).
- Editing existing text in place.
- A TUI. Page selection needs thumbnails; Omarchy's default terminal has no image protocol. A text-range CLI (`fcctl pdf ...`) is provided for scripting instead.

## Prior art and reuse

### `pdf-utils-go` (local, `~/Projects/githubProjects/pdf-utils-go`)

Reviewed. It is about 400 lines and a thin wrapper over **pdfcpu** (`api.MergeCreateFile`, `api.SplitFile`, `api.OptimizeFile`) plus `gofpdf` for text/image-to-PDF and `chromedp` for HTML/PDF-to-image.

- Do **not** vendor or copy it. It has no LICENSE file in the checkout, so reuse rights are undefined, and everything useful in it is a call into pdfcpu.
- Do **not** depend on it. Its Chromium requirement (`chromedp`) is unacceptable for a lightweight daemon, its output paths are hard-coded to `dist/<timestamp>`, and its "compress" is only `OptimizeFile` (structural cleanup, it ignores the `percentage` argument).
- It is useful as confirmation of the pdfcpu call shapes. We use pdfcpu directly.

### pdfcpu (`github.com/pdfcpu/pdfcpu`)

The one real candidate: a pure-Go PDF processor with a library API, Apache-2.0 (confirm in the repo before adding the dependency; search results only showed this for older releases). It covers, from its CLI/API surface: merge, split (by span), extract pages/images/fonts/content/metadata, trim/remove/insert pages, rotate, crop/resize/cut/poster, n-up/grid/booklet, stamp and watermark (text/image/PDF), encrypt/decrypt/permissions, optimize, validate, bookmarks, attachments, form export/fill, properties/keywords, and image import.

Caveat: its optimizer does not downsample images, so real **compress** uses Ghostscript instead.

### Others considered

| Library | Verdict |
| --- | --- |
| `unidoc/unipdf` | Commercial license for most uses. Rejected. |
| `ledongthuc/pdf` | Text extraction only. Poppler `pdftotext` is better. Rejected. |
| `jung-kurt/gofpdf` (MIT, archived) | Only needed for text-to-PDF. Generate through Cairo/ImageMagick instead; revisit if needed. |
| `maroto` | Report layout library, irrelevant. |

All license statements other than pdfcpu's need checking against the upstream repos before adoption.

## Engines

Hybrid, preferring in-process Go where it is solid and external binaries where Go has no equal. Each operation declares the engines it needs; the menu greys out operations whose engine is missing (same mechanism as today's format availability).

| Engine | Role |
| --- | --- |
| pdfcpu (Go, linked in) | Structural operations, stamp/watermark, encrypt, nup/booklet, crop, metadata, images to PDF |
| Ghostscript `gs` | Compress presets, PDF/A, grayscale |
| Poppler (`pdftocairo`, `pdftotext`, `pdfimages`, `pdfinfo`) | Thumbnails, rasterize (exists), text and image extraction |
| Tesseract | OCR (rasterize pages, recognize, merge text layer) |
| LibreOffice (optional) | Office to PDF |

Installed on the dev machine: `gs`, `tesseract`, Poppler tools, ImageMagick, `qpdf`. `qpdf` is therefore available as a fallback but is not a dependency. `ocrmypdf`, `mutool`, and `pdftk` are not present and not used.

Dependency note: `backend/go.mod` currently has no third-party modules. pdfcpu pulls in a handful (`golang.org/x/*`, `hhrutter/*`, `pkg/errors`, `yaml.v2`). Accepted; the pdfcpu engine sits behind an interface so it can be swapped for `qpdf` if the dependency becomes a problem.

## Operations (v1 catalogue)

"Instant" = no parameters, runs from the menu. "Studio" = needs the window.

| # | Group | Operation | Engine | Mode |
| --- | --- | --- | --- | --- |
| 1 | Organise | Merge (multi-select) | pdfcpu | instant |
| 2 | Organise | Split by ranges | pdfcpu | Studio |
| 3 | Organise | Split every N pages | pdfcpu | Studio |
| 4 | Organise | Split by max size | pdfcpu | Studio |
| 5 | Organise | Extract pages | pdfcpu | Studio |
| 6 | Organise | Delete pages | pdfcpu | Studio |
| 7 | Organise | Reorder pages (drag) | pdfcpu | Studio |
| 8 | Organise | Reverse | pdfcpu | instant |
| 9 | Organise | Rotate (selected or all; 90/180/270) | pdfcpu | instant for all-pages, else Studio |
| 10 | Organise | Insert blank pages | pdfcpu | Studio |
| 11 | Organise | Duplicate pages | pdfcpu | Studio |
| 12 | Organise | Interleave two PDFs | pdfcpu | instant (2 selected) |
| 13 | Optimise | Compress (low/medium/high) | gs | instant (presets in submenu) |
| 14 | Optimise | Linearize | pdfcpu | instant |
| 15 | Optimise | Repair | pdfcpu | instant |
| 16 | Optimise | Grayscale | gs | instant |
| 17 | Optimise | Flatten forms/annotations | pdfcpu | instant |
| 18 | Optimise | Remove metadata | pdfcpu | instant |
| 19 | From PDF | PDF to images | pdftocairo | exists |
| 20 | From PDF | PDF to text | pdftotext | instant |
| 21 | From PDF | PDF to Markdown/HTML | pdftotext | instant |
| 22 | From PDF | Extract embedded images | pdfimages | instant |
| 23 | From PDF | PDF to PDF/A | gs | instant |
| 24 | To PDF | Images to PDF (multi-select) | pdfcpu | instant |
| 25 | To PDF | Text/Markdown to PDF | Cairo/ImageMagick | instant |
| 26 | To PDF | Office to PDF | LibreOffice | instant |
| 27 | Security | Password-protect | pdfcpu | Studio |
| 28 | Security | Remove password | pdfcpu | Studio (asks password) |
| 29 | Security | Set permissions | pdfcpu | Studio |
| 30 | Security | Redact | pdftocairo + pdfcpu | Studio |
| 31 | Edit | Watermark (text/image) | pdfcpu | Studio |
| 32 | Edit | Page numbers | pdfcpu | Studio |
| 33 | Edit | Header/footer/Bates | pdfcpu | Studio |
| 34 | Edit | Crop/trim margins | pdfcpu | Studio |
| 35 | Edit | Resize to A4/Letter | pdfcpu | Studio |
| 36 | Edit | N-up (2/4/9 per sheet) | pdfcpu | Studio |
| 37 | Edit | Booklet | pdfcpu | Studio |
| 38 | Intelligence | OCR to searchable PDF | tesseract | instant |
| 39 | Intelligence | Compare two PDFs (text diff) | pdftotext | instant (2 selected) |
| 40 | Intelligence | Edit metadata | pdfcpu | Studio |
| 41 | Intelligence | Export bookmarks/TOC | pdfcpu | instant |

## Architecture

### Protocol (v1 additions, backward compatible)

The existing `targets`/`convert` methods stay. PDF operations are a parallel concept because they are not "source format to target format":

- `operations {files}` returns the operations applicable to the selection (count, types, encrypted or not), each with `id`, `label`, `group`, `mode` (`instant` or `studio`), `enabled`, `reason`.
- `run_operation {op, files, params, conflict}` returns a job, using the same job object, events, progress, cancel, atomic publish, conflict policy, and bounded concurrency as conversions.
- `pdf_info {file}` returns page count, page sizes, encrypted flag, and metadata. The Studio uses it.
- `pdf_thumbnails {file, pages?, width}` renders into `$XDG_CACHE_HOME/file-converter/thumbs/<content-hash>/` and returns paths. Thumbnails are cached by content hash so reopening a file is instant.

Params are validated per operation (typed structs, like `ImageOptions`). Page selections use one grammar everywhere: `1-3,7,10-`, plus `z` for the last page.

### Backend layout

```
backend/internal/pdfops/
  op.go          Operation interface: ID, Needs() []engine, Validate(params), Run(ctx, in, params, out)
  registry.go    catalogue + availability
  pagespec.go    page-range grammar, shared by CLI, Studio, and validators
  pdfcpu/        structural ops, one file per group
  gs/            compress, grayscale, PDF/A
  poppler/       thumbnails, text, images
  ocr/           tesseract pipeline
```

Rules carried over from existing engines: argument arrays only (no shell), controlled temp dirs, atomic publish through the job layer, no partial output on failure, context cancellation kills child processes. Encrypted inputs fail with a clear `password_required` error code; passwords travel in `params`, are never logged, and never appear in job state.

### Frontends

**Nautilus extension** (shared by Omarchy and Ubuntu): adds a **PDF Tools** submenu.

- Single PDF: instant ops grouped by category, plus **Customise…**.
- Several PDFs: Merge, Interleave (exactly 2), Compare (exactly 2), and Customise.
- Images selected: **Images to PDF**.

**PDF Studio** (`frontends/pdf-studio/`, Python + GTK4, libadwaita if present; falls back to plain Gtk):

- Left: tool list (the Studio-mode operations). Centre: thumbnail grid, lazy-loaded from `pdf_thumbnails`. Right: parameter form for the chosen tool.
- Selection: click, ctrl/shift-click ranges, rubber-band, select odd/even/all/invert. A text box shows the page spec and stays in two-way sync with the grid.
- Page actions on the grid: delete, rotate left/right, duplicate, insert blank, extract, drag-to-reorder. These build an edit list that is sent as one `run_operation` (no PDF logic in the UI).
- Merge view: file list with drag reorder and per-file page ranges.
- Output: choose "save next to original" (default, conflict policy applies) or "save as…".
- Progress and errors use the existing job events and desktop notifications.

**Omarchy radial plugin:** instant operations only, plus a **Customise…** entry that launches the Studio. It does not try to show 40 items.

**CLI:** `fcctl pdf <op> file.pdf --pages 3,5-7 ...` drives the same `run_operation` method; used for tests and scripting.

## Phasing

1. **Plumbing.** Protocol methods, `pdfops` skeleton, page-spec grammar, pdfcpu engine, with Merge, Extract, Delete, Rotate, Reverse, and the `fcctl pdf` CLI. E2E tests.
2. **Instant actions.** The rest of the instant set (compress, linearize, repair, flatten, metadata strip, text, image extraction, images to PDF, OCR) and the Nautilus PDF Tools submenu.
3. **PDF Studio.** Thumbnails, page grid, range sync, and the page-editing and split tools (2-11).
4. **Form tools.** Watermark, page numbers, header/footer, crop, resize, N-up, booklet, encrypt, permissions, metadata editor.
5. **Heavy and optional.** Redact, compare, PDF/A, Office to PDF.

Each phase is independently shippable and gets its own plan under `docs/superpowers/plans/`.

## Testing

- Unit: page-spec parser (table tests), param validation, op availability.
- Engine: golden fixtures generated at test time (small PDFs built with the engine itself, verified with `pdfinfo`/`pdftotext`), skipped when an engine is missing, as in the existing engine tests.
- Protocol/server: same pattern as `server_test.go` for the new methods.
- Studio: a headless logic layer (edit list to params) unit-tested without GTK; GTK itself smoke-tested manually.
- Property check: for every page-editing op, `pdfinfo` page count equals the expected count.

## Open questions

1. libadwaita required or optional for the Studio? (Default: optional.)
2. Is redaction in scope for v1 given the correctness risk? Naive redaction (a black box over live text) is unsafe; the plan is rasterize-and-rebuild the affected pages, which loses text on those pages.
3. Ship Office to PDF at all, given the LibreOffice dependency?

## Sources

- pdfcpu: <https://github.com/pdfcpu/pdfcpu> (module path as imported by `pdf-utils-go`, v0.12.0)
- Go PDF library overview: <https://unidoc.io/post/golang-pdf-library-guide/> (vendor page, treat comparisons as biased)
- Local review: `~/Projects/githubProjects/pdf-utils-go` (`utils/combiner.go`, `splitter.go`, `compressor.go`)
