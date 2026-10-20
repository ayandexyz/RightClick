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

`fcctl engines [--json]` prints one line per engine.

### `targets`
What can these files become? This drives the donut / menu.
`params: {files: [path]}` → `{files_supported: bool, targets: [Target]}`

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
`params: {files: [path], preset: string, output_dir?: string, on_conflict?: "rename"|"skip"|"overwrite", quality?: 1..100, max_size?: positive integer, background?: color}`

Image presets include `image-to-png`, `image-to-jpeg`, `image-to-webp`, `image-to-jxl`, `image-to-avif`, `image-to-gif`, `image-to-bmp`, `image-to-tiff`, `image-to-ico`, and `image-to-heic`. The daemon advertises only targets writable by the installed ImageMagick build. ICO output is limited to 256×256.

SVG inputs have three dedicated presets: `svg-to-png`, `svg-to-jpeg`, and `svg-to-webp`. They require `rsvg-convert`; JPEG and WebP additionally require ImageMagick for the final conversion. Raster-to-SVG conversion is unsupported. SVG validation rejects external file, HTTP/HTTPS, data, and traversal-like resource references, as well as scripts and DTD/entity declarations.

PDF inputs have three dedicated presets: `pdf-to-png`, `pdf-to-jpeg`, and `pdf-to-webp`. All three require `pdftocairo`, which renders every page at 150 DPI and writes one numbered output per page (`name-1.png`, `name-2.png`, …). Only `pdf-to-webp` additionally requires ImageMagick, used to convert the rendered PNG pages to WebP. PDF output, PDF-to-video, PDF-to-audio, and PDF-to-SVG conversion are unsupported, and page or page-range selection is not supported.

Media presets are exactly five video outputs (`video-to-mp4`, `video-to-mov`, `video-to-webm`, `video-to-mkv`, `video-to-avi`) and twelve audio outputs (`audio-to-mp3`, `audio-to-wav`, `audio-to-flac`, `audio-to-aac`, `audio-to-m4a`, `audio-to-ogg`, `audio-to-opus`, `audio-to-aiff`, `audio-to-alac`, `audio-to-ac3`, `audio-to-amr`, and `audio-to-wma`). They require both the `ffmpeg` and `ffprobe` executables. The registry uses `ffprobe` stream contents for source classification; a source containing video is classified as video even when it also contains audio. Video inputs may be converted to audio with FFmpeg's `-vn` extraction option. Audio sources never advertise video targets, and audio-to-video conversion is rejected. If either media executable is unavailable, ImageMagick image targets remain available independently; missing ImageMagick likewise does not hide available FFmpeg media targets.
→ `{job: string}` (returned immediately; progress arrives as events)

### `cancel`
`params: {job}` → `{}`

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
