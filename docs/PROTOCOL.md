# Backend protocol (v1, draft)

The backend (`backend/`, working name **fcd**) is a long-running process that owns all conversion work. Frontends
never convert anything themselves: they send requests and render events.

## Transport

- Newline-delimited JSON (NDJSON), UTF-8, one message per line, over a stream socket.
- Linux: Unix socket at `$XDG_RUNTIME_DIR/file-converter/fcd.sock` (mode 0600).
- Windows: named pipe `\\.\pipe\file-converter-fcd` (current user only).
- A frontend that finds no socket starts `fcd serve` and retries; `fcd` exits after `--idle-exit` seconds with no
  clients and no jobs (default 300, 0 = never).
- One connection = one client. Events go only to the connection that started the job unless it sent `subscribe`.

## Messages

Request: `{"id": 1, "method": "convert", "params": {...}}`
Response: `{"id": 1, "result": {...}}` or `{"id": 1, "error": {"code": "no_route", "message": "..."}}`
Event (no `id`): `{"event": "job.updated", "data": {...}}`

Error codes: `bad_request`, `not_found`, `no_route`, `unsupported`, `busy`, `cancelled`, `password_required`, `password_incorrect`, `invalid_pdf`, `internal`. PDF passwords are accepted only in the documented PDF request params, are limited to 127 UTF-8 bytes, and are never returned in jobs, events, errors, logs, or subprocess arguments.
Paths are absolute, UTF-8, native to the backend's OS.

### `hello`
`params: {client: string, protocol: 1}` → `{server: "fcd", version, protocol: 1, engines: [{id, available, reason}]}`

### `engines`
Read-only engine diagnostics. Missing optional engines are reported here, never as startup failures.
`params: {}` → `{engines: [{id, available, reason}]}`; `reason` is empty when available, otherwise why the engine cannot run (e.g. `ffmpeg not found`). `hello` returns the same entries.

The PDF engines `pdfcpu`, `qpdf`, `ghostscript`, `poppler`, and `tesseract` are reported alongside the conversion engines. They are always listed, installed or not, so a client can tell a missing binary apart from an older daemon that has never heard of them. A missing PDF engine disables only the operations that use it.

`fcctl engines [--json]` prints one line per engine.

### `menu`
Reads or edits which targets file-manager menus offer. The policy is stored in `$XDG_CONFIG_HOME/file-converter/menu.toml` (fallback `~/.config/file-converter/menu.toml`), one array of target formats per source category:

```toml
video = ["mp4", "webm"]
```

Categories: `image`, `video`, `audio`, `pdf`, `document`, `presentation`, `spreadsheet` (SVG counts as `image`). Defaults: image `jpeg png webp`, video `mp4 mov webm gif`, audio `mp3 m4a wav`, pdf `png jpeg webp`, document/presentation/spreadsheet `pdf`. An empty list, or one with no reachable target, falls back to the defaults, so a menu never disappears; a corrupt file is treated as all-defaults for `targets`. Unknown categories and formats in the file are ignored.

`params: {action: "show"}` | `{action: "set", category, formats: [string]}` | `{action: "reset", category?}` (no category resets all)
→ `{path, menu: {category: [format]}, custom: [category]}` — `menu` is the effective policy, `custom` the categories that differ from the defaults. `set` rejects unknown categories and formats with `bad_request`.

`fcctl menu show`, `fcctl menu set CATEGORY FORMAT...`, `fcctl menu reset [CATEGORY]`, `fcctl targets FILE... --menu`.

### `targets`
What can these files become? This drives the donut / menu.
`params: {files: [path], menu?: bool}` → `{files_supported: bool, targets: [Target]}`

With `menu: true` the reachable targets are narrowed to the user's menu policy (see `menu`), in policy order; mixed selections offer the union of their categories' lists over the targets reachable for every file. Without it, every reachable target is returned.

```
Target = {
  preset: string,        // id passed back to convert
  label: string,         // already localized
  format: string,        // "png", "pdf", ...
  category: string,      // "image" | "document" | "audio" | "video" | "archive" | "other"
  enabled: bool,
  reason?: string        // why disabled, localized
}
```
Targets are ordered by the user's toolbar profile, so frontends keep the order.

### `convert`
`params: {files: [path], preset: string, output_dir?: string, on_conflict?: "rename"|"skip"|"overwrite", quality?: 1..100, max_size?: positive integer, background?: color, recursive?: bool}`

### Concurrency
Files in a job convert in parallel, up to `fcd serve --max-workers N` at once (default: the CPU count, at most 8). Jobs themselves run one after another in submission order. Video conversions (video source or video target) share a single lane and never overlap, because FFmpeg already uses every core; with more than one worker, the other files use the remaining workers. Destinations are reserved while a conversion runs, so same-named outputs from parallel files still get distinct names (`name.jpg`, `name (1).jpg`). Results keep input order whatever order files finish in. Cancelling a queued job completes it as `cancelled` without running anything; cancelling a running job stops its conversions and rolls back unpublished outputs.

### Folders
`files` may contain folders as well as files. A folder contributes its top-level files, or every file below it when `recursive` is true. Expansion is deterministic: files keep request order and the files a folder contributes are sorted by path. Hidden files and folders and symbolic links found while scanning are skipped, so a scan never leaves the folder. Files named directly are kept even if hidden.

With `output_dir`, a file found under a folder keeps its path relative to that folder (`photos/trip/a.png` with `output_dir: /dest` writes `/dest/trip/a.jpg`); without it, outputs sit next to their sources. Files named directly go straight into `output_dir`.

A file found by scanning that cannot be converted (unknown type, same format as the target, no route, unsafe name) is reported as `skipped` with the reason in the file's `notice`, and does not fail the job. The same problem with a file named directly is a `failed` file. A folder with nothing to convert is rejected with `bad_request`.

`fcctl convert FOLDER --recursive --out-dir DEST --to jpeg`; relative paths are resolved by `fcctl`.

Image presets include `image-to-png`, `image-to-jpeg`, `image-to-webp`, `image-to-jxl`, `image-to-avif`, `image-to-gif`, `image-to-bmp`, `image-to-tiff`, `image-to-ico`, and `image-to-heic`. The daemon advertises only targets writable by the installed ImageMagick build. ICO output is limited to 256×256.

SVG inputs have three dedicated presets: `svg-to-png`, `svg-to-jpeg`, and `svg-to-webp`. They require `rsvg-convert`; JPEG and WebP additionally require ImageMagick for the final conversion. Raster-to-SVG conversion is unsupported. SVG validation rejects external file, HTTP/HTTPS, data, and traversal-like resource references, as well as scripts and DTD/entity declarations.

PDF inputs have three dedicated presets: `pdf-to-png`, `pdf-to-jpeg`, and `pdf-to-webp`. All three require `pdftocairo`, which renders every page at 150 DPI and writes one numbered output per page (`name-1.png`, `name-2.png`, …). Only `pdf-to-webp` additionally requires ImageMagick, used to convert the rendered PNG pages to WebP. PDF output, PDF-to-video, PDF-to-audio, and PDF-to-SVG conversion are unsupported. Rasterizing always produces every page; to choose pages use the `pdf-extract` and `pdf-delete` operations below.

Media presets are exactly five video outputs (`video-to-mp4`, `video-to-mov`, `video-to-webm`, `video-to-mkv`, `video-to-avi`) and twelve audio outputs (`audio-to-mp3`, `audio-to-wav`, `audio-to-flac`, `audio-to-aac`, `audio-to-m4a`, `audio-to-ogg`, `audio-to-opus`, `audio-to-aiff`, `audio-to-alac`, `audio-to-ac3`, `audio-to-amr`, and `audio-to-wma`). They require both the `ffmpeg` and `ffprobe` executables. The registry uses `ffprobe` stream contents for source classification; a source containing video is classified as video even when it also contains audio. Video inputs may be converted to audio with FFmpeg's `-vn` extraction option. Audio sources never advertise video targets, and audio-to-video conversion is rejected. If either media executable is unavailable, ImageMagick image targets remain available independently; missing ImageMagick likewise does not hide available FFmpeg media targets.
→ `{job: string}` (returned immediately; progress arrives as events)

### `cancel`
`params: {job}` → `{}`

### `operations`
`params: {files: string[]}` → `{operations: Operation[]}` where each entry has `id, label, group, mode, engine, enabled, reason?`. Lists the PDF tools applicable to a selection.

The selection must be uniformly one kind: all PDFs, or all images that pdfcpu can import (JPEG, PNG, TIFF; SVG is not an importable image). A mixed or unrecognised selection returns an empty list rather than an error, so a right-click menu can call it unconditionally.

`mode` is `instant` for operations that need no further input and `studio` for ones that need a page selection or an angle. `engine` names the external tool that performs the operation (`pdfcpu`, `qpdf`, `ghostscript`, `poppler`, `tesseract`), so a client can tell which binary an operation needs.

An operation may be present but `enabled: false` with a `reason`: merge with one file reports `needs at least 2 files`, an image operation over an unsupported format reports `<format> is not supported for PDF import`, and an operation whose engine is not installed reports `<engine> is not installed`. A missing engine disables only its own operations.

### `run_operation`
`params: {op, files, params?, output_dir?, on_conflict?}` → `{job: string}`. The job runs through the same lifecycle as conversions: `job.updated` events with progress, `job.finished` with the final state. `params` may contain `{pages?: string, angle?: int, edit?: PageItem[], ranges?: string, every?: int, file_pages?: string[], password?: string, encrypt_user?: string, encrypt_owner?: string, permissions_owner?: string, permissions?: string[], stamp?: Stamp, crop?: Crop, resize?: Resize, layout?: Layout}`. Password fields are read only in memory and are never included in jobs, events, errors, logs, or child-process arguments. `fcctl pdf decrypt` reads `--password-file FILE` (use `-` for stdin); encryption and permissions use `--user-password-file` / `--owner-password-file`, never password values directly on the command line. A password is used only in memory for the unlock stage and is never copied into a job, event, error, log, or subprocess argument. It is limited to 127 UTF-8 bytes. `pages` follows the spec grammar below; `angle` must be 90, 180 or 270. `edit` is the ordered page list for `pdf-edit` (`src: 0` is blank and `rotate` is 0/90/180/270). `ranges` is semicolon-separated page specs for splitting. `every` is the positive page group size. `file_pages` is aligned with merge `files`; an empty entry means all pages.

Operations:

| ID | Engine | Mode | Inputs | Output |
| --- | --- | --- | --- | --- |
| `pdf-merge` | pdfcpu | instant | 2 or more PDFs | `merged.pdf` next to first input or in `output_dir`; optional `file_pages` selects pages per input |
| `pdf-extract` | pdfcpu | studio | 1 or more PDFs | `<stem>-extracted.pdf` |
| `pdf-delete` | pdfcpu | studio | 1 or more PDFs | `<stem>-edited.pdf` |
| `pdf-edit` | pdfcpu | studio | exactly 1 PDF | `<stem>-edited.pdf`; requires `edit` |
| `pdf-split-ranges` | pdfcpu | studio | exactly 1 PDF | `<stem>-part-1.pdf`, ...; requires `ranges` |
| `pdf-split-every` | pdfcpu | studio | exactly 1 PDF | `<stem>-part-1.pdf`, ...; requires `every` |
| `pdf-rotate` | pdfcpu | studio | 1 or more PDFs | `<stem>-rotated.pdf`; `pages` (optional) |
| `pdf-rotate-cw` | pdfcpu | instant | 1 or more PDFs | `<stem>-rotated.pdf` (90°) |
| `pdf-rotate-ccw` | pdfcpu | instant | 1 or more PDFs | `<stem>-rotated.pdf` (-90°) |
| `pdf-rotate-180` | pdfcpu | instant | 1 or more PDFs | `<stem>-rotated.pdf` (180°) |
| `pdf-reverse` | pdfcpu | instant | 1 or more PDFs | `<stem>-reversed.pdf` |
| `pdf-repair` | qpdf | instant | 1 or more PDFs | `<stem>-repaired.pdf` |
| `pdf-linearize` | qpdf | instant | 1 or more PDFs | `<stem>-linearized.pdf` |
| `pdf-flatten` | qpdf | instant | 1 or more PDFs | `<stem>-flattened.pdf` |
| `pdf-compress-light` | ghostscript | instant | 1 or more PDFs | `<stem>-compressed.pdf` (`/printer`) |
| `pdf-compress-balanced` | ghostscript | instant | 1 or more PDFs | `<stem>-compressed.pdf` (`/ebook`); `compress` is an alias |
| `pdf-compress-strong` | ghostscript | instant | 1 or more PDFs | `<stem>-compressed.pdf` (`/screen`) |
| `pdf-grayscale` | ghostscript | instant | 1 or more PDFs | `<stem>-gray.pdf` |
| `pdf-to-text` | poppler | instant | 1 or more PDFs | `<stem>-text.txt` |
| `pdf-extract-images` | poppler | instant | 1 or more PDFs | `<stem>-image-1.png`, `<stem>-image-2.png`, ... |
| `pdf-ocr` | tesseract | studio | 1 or more PDFs | not implemented; always `enabled: false` |
| `pdf-from-images` | pdfcpu | instant | 1 or more JPEG / PNG / TIFF | `images.pdf` in `output_dir` |
| `pdf-decrypt` | pdfcpu | studio | exactly 1 PDF; `password` required | `<stem>-unlocked.pdf` |
| `pdf-encrypt` | pdfcpu | studio | exactly 1 PDF; `encrypt_owner` required, optional `encrypt_user` | `<stem>-protected.pdf`; AES-256; optional `permissions` lists what readers may do (`print`, `copy`, `modify`, `annotate`), default all of them |
| `pdf-permissions` | pdfcpu | studio | exactly 1 PDF; `permissions_owner` required; typed `permissions` | `<stem>-restricted.pdf`; AES-256 |
| `pdf-stamp-text` | pdfcpu | studio | exactly 1 PDF; `stamp.text` required | `<stem>-stamped.pdf` |
| `pdf-stamp-image` | pdfcpu | studio | exactly 1 PDF; `stamp.image` required | `<stem>-stamped.pdf` |
| `pdf-page-numbers` | pdfcpu | studio | exactly 1 PDF | `<stem>-numbered.pdf` |
| `pdf-header-footer` | pdfcpu | studio | exactly 1 PDF; at least one of `stamp.left`, `centre`, `right` | `<stem>-stamped.pdf` |
| `pdf-bates` | pdfcpu | studio | exactly 1 PDF; `stamp.start` required | `<stem>-bates.pdf` |
| `pdf-crop` | pdfcpu | studio | exactly 1 PDF; `crop` required | `<stem>-cropped.pdf` |
| `pdf-resize` | pdfcpu | studio | exactly 1 PDF; `resize` required | `<stem>-resized.pdf` |
| `pdf-nup` | pdfcpu | studio | exactly 1 PDF; `layout.per_sheet` required | `<stem>-nup.pdf` |
| `pdf-booklet` | pdfcpu | studio | exactly 1 PDF; `layout.per_sheet` required | `<stem>-booklet.pdf` |

`pdf-ocr` is not implemented in this phase: producing a searchable PDF requires drawing an invisible text layer positioned over each page image, which no tool in this toolbox does. It is always returned with `enabled: false` and the reason `making a searchable PDF is not supported by this build`, and `run_operation` returns the same error rather than writing a PDF without a text layer.

Studio operations require either `pages` (page spec, see below) or `angle` (90, 180, 270) in `params`. Instant operations reject those params. Image-only operations (`pdf-from-images`) work on a selection of all-images; the daemon reports the operation disabled with `<format> is not supported for PDF import` for selections containing any unsupported format.

Compress emits `skipped` with notice `already as small as it can get` when Ghostscript produces output that is not smaller than its input, rather than failing. Extract-images reports `skipped` with notice `no embedded images` for PDFs that contain no images. Split operations reject empty groups, invalid/out-of-range specs, a single-file split, and more than 200 parts before writing any output. Range groups are independent and may overlap; `every` produces a final remainder part when needed.

The page spec is comma-separated items; each is `N`, `N-M`, `N-` (to the last page), `-M` (from page 1), or `z` (the last page). 1-based, whitespace ignored. Duplicates and overlaps are allowed. Out-of-range pages, reversed ranges (`5-3`), garbage, and an empty spec are errors. A selection expanding to more than the daemon's per-document page ceiling (1000) is also rejected. Examples: `1-3,7,10-`, `z`, `-2`.

#### Stamp parameters

The five stamp operations share one object, `stamp`, and the operation decides which preset it is. A client never sends a pdfcpu description string: every field is typed and checked by the daemon.

| Field | Used by | Meaning |
| --- | --- | --- |
| `text` | `pdf-stamp-text` | one line, at most 200 characters, Latin-1 only (see below). `%p` is the page number, `%P` the page count, `%t` the date; any other `%` is refused |
| `image` | `pdf-stamp-image` | absolute path to a PNG or JPEG of at most 20 MB, checked by its header and not its name |
| `position` | all | `tl tc tr ml mc mr bl bc br`; default `bc`. A stamp is moved 36 points in from the edge it is anchored to |
| `opacity` | all | 0-100; default 100 |
| `rotation` | all | -180 to 180 degrees; default 0 (level) |
| `size` | text kinds | font size 4-200 points; default 12 |
| `color` | text kinds | `#rrggbb`; default `#000000` |
| `on_top` | all | draw over the page content instead of beneath it |
| `pages` | all | page spec; default every page |
| `format`, `start` | `pdf-page-numbers` | `number` (default) or `of-total`; the number on the first page, default 1 |
| `prefix`, `start` | `pdf-bates` | text before the counter (no `%`), and the first number. The counter is zero-padded to 6 digits, or more when the last number needs them |
| `left`, `centre`, `right`, `edge` | `pdf-header-footer` | up to three texts on one line, at the `top` (default) or `bottom` edge |

Text the stamp font cannot draw (anything outside Latin-1: Bengali, Devanagari, Cyrillic, CJK) is rejected with the first offending character named, because pdfcpu would otherwise write a document with those glyphs missing. A document with rotated pages is made upright first (`qpdf --flatten-rotation`), which looks the same, so a stamp lands where the user sees the page's corner; qpdf is required for such a document.

#### Crop, resize and layout parameters

- `crop`: `{unit: "mm"|"pt"|"percent", top, right, bottom, left, pages?}`. Margins are non-negative numbers removed from each edge, measured on the page as the reader sees it (a rotated page is made upright first, which needs qpdf); `unit` is required and at least one margin must be set. A margin that leaves no page is refused naming the first page it fails on. Cropping sets the crop box, so the page content is hidden, not deleted.
- `resize`: `{to: "A4"|"A3"|"A5"|"Letter"|"Legal", fit?: "scale", pages?}`. Each page is fitted inside the target size, centred, with its proportions kept; a landscape page stays landscape. `fit` may be omitted or `scale`: pdfcpu cannot fill-and-crop or pad to a size, so those modes are refused.
- `layout`: `{per_sheet, orientation?: "portrait"|"landscape", margin?, border?}`. `pdf-nup` takes `per_sheet` of 2, 4, 6 or 9. The sheet is the size of the document's first page, landscape for 2- and 6-up by default and when `orientation` is `landscape`; a last sheet that is not full is left partly empty. `pdf-booklet` takes 2 (landscape sheet, folded in the middle) or 4 and ignores the rest; a page count that does not fill the last sheet is padded with blank pages.

#### `pdf-edit`, split and merge parameters

`edit` is the ordered list of pages the output should contain, each item `{src, rotate?}`:

- `src` is a 1-based source page; **`src: 0` inserts a blank page**. The same source page may appear any number of times, which is how duplicating works, and a source page left out of the list is deleted.
- `rotate` is 0, 90, 180 or 270 and is a **clockwise change applied to that item**, not an absolute orientation: a page already rotated 90° with `rotate: 90` ends at 180°. Rotating one copy of a duplicated page leaves its other copies alone.
- A blank page takes the size of the page before it, or of the page after it when it is first.
- The list must contain at least one real page (a document of only blanks is rejected), may not name a page past the end of the source, and holds at most 1000 items.

`ranges` is groups separated by `;`, each group a page spec, for example `1-3;4-6;7-`. Groups are independent, so they may overlap, and each becomes one part. An empty group, a bad spec or a page past the end fails the whole job before any part is written. `every` splits into consecutive parts of that many pages with a shorter last part if the pages do not divide evenly; it must be at least 1 and smaller than the page count, because a split that would produce one file is rejected. Either split is limited to 200 parts. Parts are published together: if one cannot be written, none is.

`file_pages` is aligned with the merge `files` (same length); an empty entry takes every page of that file. The order of `files` is the order of the merge.

Error codes on a file job entry:

| Code | Meaning |
| --- | --- |
| `password_required` | the PDF is encrypted and no password was supplied |
| `password_incorrect` | a password was supplied and it does not open the PDF |
| `invalid_pdf` | the file cannot be read as a PDF, or is damaged |

A failure is reported as `invalid_pdf` only when the input itself cannot be opened; a failure on a readable file is the operation's own and stays `internal`.

Merge attributes an unreadable input to that file alone: the unreadable file is `failed` with its own code, and every other input is `skipped` with the notice `not merged: <basename> could not be read`. The job state becomes `failed`.

### `pdf_info`
`params: {file, password?: string}` → `{pages: int, sizes?: [{w, h, rot}]}`

Reports a document's page count and, when `pdfinfo` is installed, each page's
size in points and its `/Rotate` value. The page count is authoritative and always
present; `sizes` is omitted entirely when it could not be read, so a client must
not treat a missing `sizes` as pages of zero size. Errors: `password_required`,
`invalid_pdf`, `bad_request`.

### `pdf_thumbnails`
`params: {file, pages: [int], width?: int, password?: string}` → `{pages: [{page, path, w, h}]}`

Renders a thumbnail per requested page and returns the path to each PNG. `width`
is the cap on the longest side in pixels, 64–800, default 200. At most 50 pages
per request; page numbers start at 1 and must be within the document. A page
already cached is returned without rendering.

Thumbnails are cached privately: under
`$XDG_CACHE_HOME/file-converter/thumbs` (falling back to
`~/.cache/file-converter/thumbs`), in a directory created mode 0700 with files
mode 0600. The cache key is the SHA-256 of the file's *content* plus the width,
so a renamed or moved document reuses its thumbnails and an edited one does not.
The cache is bounded by total size (256 MB) and age (7 days) and is pruned at
daemon start and after each render batch; files younger than 60 seconds are never
pruned, so a render in flight cannot lose its output.

Errors: `password_required`, `password_incorrect`, `invalid_pdf`, `bad_request`. A password-unlocked document must not be placed in the persistent cache; its thumbnails are temporary and are removed when the client connection closes. A render failure is
`internal`; a request the cache rejects (width out of range, too many pages, a
page below 1) is `bad_request`.

### `cache_clear`
`params: {}` → `{removed: int}`

Removes every cached thumbnail and reports how many files went. Reached from the
CLI as `fcctl cache clear`. It removes everything or nothing, so it is safe to
run at any time; a request in flight may re-render its pages afterwards.

### `jobs`
`params: {}` → `{jobs: [Job]}` (running and recent, for a tray or history view)

### `subscribe`
`params: {}` → `{}`; the connection then receives `job.*` events for every client's jobs.

### `presets` / `formats`
Read-only lists for settings UIs: `presets` → `{presets: [{id, label, format, category}]}`,
`formats` → `{formats: [{id, extensions, category}]}`.

### `shutdown`
`params: {}` → `{}`; then the process exits when jobs finish.

## Events

`job.updated` and `job.finished` carry a `Job`:

```
Job = {
  id: string,
  preset: string, label: string,
  state: "queued"|"running"|"completed"|"completed_with_errors"|"failed"|"cancelled",
  progress: number,                  // 0..1
  files: [{
    source: path,
    state: "pending"|"running"|"done"|"skipped"|"failed"|"cancelled",
    progress: number,
    outputs: [path],
    error?: {code: string, message: string},   // localized message; code is the stable key
    notice?: string                            // localized, e.g. "PDF was not smaller"
  }]
}
```

`job.updated` is throttled to at most 10 per second per job; `job.finished` is always sent once.

## Versioning

`protocol` is an integer. Adding fields or methods keeps the number; removing or changing meaning bumps it.
Frontends ignore unknown fields. The backend rejects a `hello` with a higher protocol than it knows.
