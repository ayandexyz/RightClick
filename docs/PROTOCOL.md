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

Error codes: `bad_request`, `not_found`, `no_route`, `unsupported`, `busy`, `cancelled`, `internal`.
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
`params: {op, files, params?, output_dir?, on_conflict?}` → `{job: string}`. The job runs through the same lifecycle as conversions: `job.updated` events with progress, `job.finished` with the final state. `params` is `{pages?: string, angle?: int}`. `pages` follows the spec grammar below; `angle` must be 90, 180 or 270.

Operations:

| ID | Engine | Mode | Inputs | Output |
| --- | --- | --- | --- | --- |
| `pdf-merge` | pdfcpu | instant | 2 or more PDFs | `merged.pdf` next to first input or in `output_dir` |
| `pdf-extract` | pdfcpu | studio | 1 or more PDFs | `<stem>-extracted.pdf` |
| `pdf-delete` | pdfcpu | studio | 1 or more PDFs | `<stem>-edited.pdf` |
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
| `pdf-strip-metadata` | pdfcpu | instant | 1 or more PDFs | `<stem>-clean.pdf` |
| `pdf-ocr` | tesseract | studio | 1 or more PDFs | not implemented; always `enabled: false` |
| `pdf-from-images` | pdfcpu | instant | 1 or more JPEG / PNG / TIFF | `images.pdf` in `output_dir` |

`pdf-ocr` is not implemented in this phase: producing a searchable PDF requires drawing an invisible text layer positioned over each page image, which no tool in this toolbox does. It is always returned with `enabled: false` and the reason `making a searchable PDF is not supported by this build`, and `run_operation` returns the same error rather than writing a PDF without a text layer.

Studio operations require either `pages` (page spec, see below) or `angle` (90, 180, 270) in `params`. Instant operations reject those params. Image-only operations (`pdf-from-images`) work on a selection of all-images; the daemon reports the operation disabled with `<format> is not supported for PDF import` for selections containing any unsupported format.

Compress emits `skipped` with notice `already as small as it can get` when Ghostscript produces output that is not smaller than its input, rather than failing. Extract-images reports `skipped` with notice `no embedded images` for PDFs that contain no images.

The page spec is comma-separated items; each is `N`, `N-M`, `N-` (to the last page), `-M` (from page 1), or `z` (the last page). 1-based, whitespace ignored. Duplicates and overlaps are allowed. Out-of-range pages, reversed ranges (`5-3`), garbage, and an empty spec are errors. A selection expanding to more than the daemon's per-document page ceiling (1000) is also rejected. Examples: `1-3,7,10-`, `z`, `-2`.

Error codes on a file job entry:

| Code | Meaning |
| --- | --- |
| `password_required` | the PDF is encrypted; passwords are not accepted in this phase |
| `invalid_pdf` | the file cannot be read as a PDF, or is damaged |

A failure is reported as `invalid_pdf` only when the input itself cannot be opened; a failure on a readable file is the operation's own and stays `internal`.

Merge attributes an unreadable input to that file alone: the unreadable file is `failed` with its own code, and every other input is `skipped` with the notice `not merged: <basename> could not be read`. The job state becomes `failed`.

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
