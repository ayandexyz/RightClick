# file-converter

Convert files from where you are: pick a file, choose a target format in a radial menu, done.
One backend, a native frontend per platform.

## PDF tools

Beyond rasterizing pages, the backend offers structural PDF tools. The Nautilus menu and `fcctl pdf` reach every one through the same `operations` catalogue, with each tool recorded as one of three modes: `instant` (one click, no further input), `studio` (needs a page selection or angle), `import` (assembles a new PDF).

| ID | Engine | Mode | Notes |
| --- | --- | --- | --- |
| `pdf-merge` | pdfcpu | instant | 2 or more files; one `merged.pdf`; `--pages-for 2=1-3` selects pages per file |
| `pdf-extract` | pdfcpu | studio | needs `--pages` |
| `pdf-delete` | pdfcpu | studio | needs `--pages` |
| `pdf-edit` | pdfcpu | studio | `--edit '3,1r90,b,3,4'` |
| `pdf-split-ranges` | pdfcpu | studio | `--ranges '1-3;4-6;7-'` |
| `pdf-split-every` | pdfcpu | studio | `--every 5` |
| `pdf-rotate` | pdfcpu | studio | needs `--angle` (90/180/270), optional `--pages` |
| `pdf-rotate-cw` | pdfcpu | instant | 90° clockwise |
| `pdf-rotate-ccw` | pdfcpu | instant | 90° counter-clockwise |
| `pdf-rotate-180` | pdfcpu | instant | flip 180° |
| `pdf-reverse` | pdfcpu | instant | |
| `pdf-repair` | qpdf | instant | rewrites damaged files |
| `pdf-linearize` | qpdf | instant | web-optimised output |
| `pdf-flatten` | qpdf | instant | flatten form annotations |
| `pdf-compress-light` | ghostscript | instant | `/printer` |
| `pdf-compress-balanced` | ghostscript | instant | `/ebook` (alias: `compress`) |
| `pdf-compress-strong` | ghostscript | instant | `/screen` |
| `pdf-grayscale` | ghostscript | instant | |
| `pdf-to-text` | poppler | instant | extract text to `<stem>-text.txt` |
| `pdf-extract-images` | poppler | instant | numbered PNGs `<stem>-image-N.png` |
| `pdf-from-images` | pdfcpu | instant | JPEG / PNG / TIFF to a single PDF |
| `pdf-decrypt` | pdfcpu | studio | `--password-file FILE` (never a flag value) |
| `pdf-encrypt` | pdfcpu | studio | `--owner-password-file`, optional `--user-password-file` |
| `pdf-permissions` | pdfcpu | studio | `--owner-password-file`, `--permissions LIST` |
| `pdf-stamp-text` | pdfcpu | studio | `--text`, optional `--position --opacity --rotation --size --color --on-top --pages` |
| `pdf-stamp-image` | pdfcpu | studio | `--image FILE` (PNG or JPEG), same options |
| `pdf-page-numbers` | pdfcpu | studio | `--format number\|of-total`, `--start N` |
| `pdf-header-footer` | pdfcpu | studio | `--left --centre --right`, `--edge top\|bottom` |
| `pdf-bates` | pdfcpu | studio | `--start N`, `--prefix TEXT` |
| `pdf-crop` | pdfcpu | studio | `--unit mm\|pt\|percent`, `--top --right --bottom --left` |
| `pdf-resize` | pdfcpu | studio | `--to A4\|A3\|A5\|Letter\|Legal` (fits each page inside, keeping proportions) |
| `pdf-nup` | pdfcpu | studio | `--per-sheet N`, `--orientation`, `--margin`, `--border` |
| `pdf-booklet` | pdfcpu | studio | `--per-sheet 2\|4` |
| `pdf-ocr` | tesseract | studio | not implemented yet; listed but always disabled — see below |

In `--edit`, a number is a source page, `rN` after it rotates that copy clockwise by N degrees on top of any rotation it already has (`3r90`), and `b` inserts a blank page: `--edit '3,1r90,b,3,4'`. Leaving a page out deletes it; naming it twice duplicates it.

`pdf-ocr` is currently **not available**: producing a searchable PDF needs an invisible text layer positioned over each page image, and no tool in this toolbox assembles that. Rather than emit a PDF that looks processed but cannot be searched, the operation is listed with a reason and disabled. `pdftocairo` and `tesseract` are still installed and checked, so the operation starts working as soon as the text-layer step lands.

PDF tools, like conversions, report per-file state (`done`, `failed`, `skipped`) and per-file error codes: `password_required` for encrypted PDFs, `invalid_pdf` for files that cannot be opened as a PDF (which includes bad headers and corrupted structures).

### Errors from PDF operations

Operation-level errors include:

| Code | Meaning |
| --- | --- |
| `password_required` | the PDF is encrypted (no passwords in this version) |
| `invalid_pdf` | the file cannot be read as a PDF; merge attributes the failure to that file and `skipped`s the rest |

Ghostscript compress reports `skipped` with `already as small as it can get` rather than failing when output is not smaller than input.

### Inspecting and thumbnailing PDFs

Two read-only daemon methods expose a document's shape without changing it:

```sh
fcctl pdf info FILE          # page count, and each page's size and rotation
fcctl pdf thumbs FILE --pages 1-5 --width 200   # one PNG per page, paths printed
```

`pdf_thumbnails` renders through `pdftocairo`. Thumbnails are cached so scrolling a
page grid does not re-render: the cache lives under
`$XDG_CACHE_HOME/file-converter/thumbs` (or `~/.cache/file-converter/thumbs`), in a
directory readable only by you, with files readable only by you. It is keyed by the
file's content, so a renamed document keeps its thumbnails and an edited one does
not.

**Thumbnails of a sensitive page stay on disk for up to 7 days**, or until the cache
grows past 256 MB, whichever comes first. To remove them now:

```sh
fcctl cache clear
```

The daemon also prunes the cache at start-up and after every render batch. Without
`pdftocairo` installed the daemon keeps serving every other method and reports
thumbnails as unavailable.

## PDF Studio

Install the Nautilus integration with `./frontends/nautilus/install.sh`. When GTK 4.12+, libadwaita 1.5+, and PyGObject are available, the installer also installs PDF Studio at `~/.local/bin/fc-pdf-studio` and adds **PDF Studio** to the applications menu. In Nautilus, select one or more local PDFs and choose **PDF Tools → Customise…**. The Studio is never registered as the default PDF opener.

If the GTK dependencies are unavailable, the Nautilus extension and daemon still install normally; only the Studio files are skipped. Remove the integration and Studio files with `./frontends/nautilus/uninstall.sh`.

PDF Studio reorders pages by dragging them (the whole selection moves when you drag a selected page; drop on the left or right half of a page to go before or after it) and through toolbar buttons and shortcuts: `Alt+Left`/`Alt+Right` moves the selection one place, while `Alt+Home`/`Alt+End` moves it to the beginning or end. Drag a rectangle over empty space to select several pages. When merging, the arrows on each row set the order the files are merged in.

## Backend status

The Linux Go backend supports image conversion plus FFmpeg-backed video and audio conversion. Image targets are filtered by the installed ImageMagick build; media targets require both `ffmpeg` and `ffprobe`. These engines are independent: missing FFmpeg/ffprobe does not disable ImageMagick image conversion, and missing ImageMagick does not disable media conversion:

- `fcd` serves protocol v1 over `$XDG_RUNTIME_DIR/file-converter/fcd.sock`.
- `fcctl` is a small NDJSON development client.
### Image conversions

Raster image conversions use ImageMagick. The available output targets are:

| Output format | Preset |
| --- | --- |
| PNG | `image-to-png` |
| JPEG | `image-to-jpeg` |
| WebP | `image-to-webp` |
| JPEG XL | `image-to-jxl` |
| AVIF | `image-to-avif` |
| GIF | `image-to-gif` |
| BMP | `image-to-bmp` |
| TIFF | `image-to-tiff` |
| ICO | `image-to-ico` |
| HEIC | `image-to-heic` |

SVG inputs use the dedicated `rsvg-convert` renderer and can be rasterized to PNG, JPEG, or WebP. JPEG and WebP outputs use ImageMagick for the final format conversion:

| Output format | Preset |
| --- | --- |
| PNG | `svg-to-png` |
| JPEG | `svg-to-jpeg` |
| WebP | `svg-to-webp` |

The SVG renderer requires `rsvg-convert`. ImageMagick is additionally required for `svg-to-jpeg` and `svg-to-webp`. Raster-to-SVG conversion is unsupported. SVG input is validated locally and external resources, including file, HTTP/HTTPS, data, and traversal-like references, are rejected.

### PDF conversions

PDF inputs are rasterized with Poppler's `pdftocairo` at 150 DPI, and every page is written as a separate numbered output (`name-1.png`, `name-2.png`, …):

| Output format | Preset |
| --- | --- |
| PNG | `pdf-to-png` |
| JPEG | `pdf-to-jpeg` |
| WebP | `pdf-to-webp` |

`pdftocairo` is required for all PDF conversions. ImageMagick is used only for `pdf-to-webp`, where the rendered PNG pages are converted to WebP; PNG and JPEG outputs come straight from `pdftocairo`. Converting to PDF, video, audio, or SVG is unsupported.

Structural PDF operations (merge, extract, delete, rotate, reverse) are linked in from [pdfcpu](https://github.com/pdfcpu/pdfcpu), pinned at **v0.12.0**, so they need no external binary. They are reachable through the daemon's `operations` and `run_operation` methods and through `fcctl pdf`:

| ID | Inputs | Params | Output name |
| --- | --- | --- | --- |
| `pdf-merge` | 2 or more | none | `merged.pdf` next to the first input or in `--output-dir` |
| `pdf-extract` | 1 or more | `--pages` (required) | `<stem>-extracted.pdf` |
| `pdf-delete` | 1 or more | `--pages` (required) | `<stem>-edited.pdf` |
| `pdf-rotate` | 1 or more | `--angle 90\|180\|270` (required), `--pages` (optional) | `<stem>-rotated.pdf` |
| `pdf-reverse` | 1 or more | none | `<stem>-reversed.pdf` |

Page specifications use comma-separated items: `N`, `N-M`, `N-` (to the last page), `-M` (from page 1), or `z` (the last page). 1-based, whitespace ignored. Duplicates and overlaps are allowed. Encrypted PDFs fail with the file-level `password_required` error code until phase 4.

### Video conversions

Video conversions use FFmpeg. Video inputs can also be converted to audio outputs, but audio inputs never advertise video targets.

| Output format | Preset |
| --- | --- |
| MP4 | `video-to-mp4` |
| MOV | `video-to-mov` |
| WebM | `video-to-webm` |
| MKV | `video-to-mkv` |
| AVI | `video-to-avi` |
| Animated GIF | `video-to-gif` |

### Audio conversions

Audio conversions use FFmpeg. Video inputs may extract audio using FFmpeg's `-vn` option.

| Output format | Preset |
| --- | --- |
| MP3 | `audio-to-mp3` |
| WAV | `audio-to-wav` |
| FLAC | `audio-to-flac` |
| AAC | `audio-to-aac` |
| M4A | `audio-to-m4a` |
| OGG | `audio-to-ogg` |
| Opus | `audio-to-opus` |
| AIFF | `audio-to-aiff` |
| ALAC | `audio-to-alac` |
| AC3 | `audio-to-ac3` |
| AMR | `audio-to-amr` |
| WMA | `audio-to-wma` |

`ffprobe` classifies media sources from their stream contents, with video taking precedence when both audio and video streams exist.
- ICO output is capped at 256×256; GIF/WebP animation is preserved when supported.
- Inputs are absolute regular files and formats are detected from file content.
- ImageMagick performs conversion. The daemon never uploads files or uses a network service.
- Output files are created through temporary files and committed with an atomic rename.

ImageMagick is required at runtime for raster image conversion and SVG JPEG/WebP output. The backend looks for `magick` first and then `convert`. SVG rasterization requires `rsvg-convert`.

## Build and run

```bash
cd backend
go test ./...
go vet ./...
go build -o fcd ./cmd/fcd
go build -o fcctl ./cmd/fcctl

# XDG_RUNTIME_DIR must point to a private runtime directory.
XDG_RUNTIME_DIR=/run/user/$(id -u) ./fcd serve
```

The socket directory is created with mode `0700`; the socket is mode `0600`.

Inspect the protocol manually:

```bash
XDG_RUNTIME_DIR=/run/user/$(id -u) ./fcctl hello
XDG_RUNTIME_DIR=/run/user/$(id -u) ./fcctl targets /absolute/path/photo.png
XDG_RUNTIME_DIR=/run/user/$(id -u) ./fcctl convert /absolute/path/photo.png --preset image-to-jpeg
XDG_RUNTIME_DIR=/run/user/$(id -u) ./fcctl cancel job-1
```

The complete wire contract is documented in [`docs/PROTOCOL.md`](docs/PROTOCOL.md).

## Project layout

```
frontends/nautilus  Nautilus extension, fcd systemd unit, installer (Omarchy and Ubuntu)
frontends/omarchy   Omarchy-only Quattro bar widget and progress panel
frontends/windows   native Windows client
        │  NDJSON over Unix socket / named pipe   (docs/PROTOCOL.md)
backend/            fcd daemon and fcctl development client
```

## Roadmap

1. Complete the Linux backend integration and frontend lifecycle polish
2. Omarchy frontend: `fc-pick <files>` opens the overlay at the cursor
3. Ubuntu frontend
4. Windows frontend
5. PDF, office documents, additional image formats (TGA, PPM, QOI, EXR), archives, packaging, and additional conversion engines

Done: image, video, audio, and SVG rasterization.
