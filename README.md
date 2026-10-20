# file-converter

Convert files from where you are: pick a file, choose a target format in a radial menu, done.
One backend, a native frontend per platform.

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
frontends/omarchy   layer-shell overlay + Hyprland keybind + file-manager action
frontends/ubuntu    GTK4/libadwaita + Nautilus action
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
