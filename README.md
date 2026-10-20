# file-converter

Convert files from where you are: pick a file, choose a target format in a radial menu, done.
One backend, a native frontend per platform.

## Backend status

The Linux Go backend supports image conversion plus FFmpeg-backed video and audio conversion. Image targets are filtered by the installed ImageMagick build; media targets require both `ffmpeg` and `ffprobe`. These engines are independent: missing FFmpeg/ffprobe does not disable ImageMagick image conversion, and missing ImageMagick does not disable media conversion:

- `fcd` serves protocol v1 over `$XDG_RUNTIME_DIR/file-converter/fcd.sock`.
- `fcctl` is a small NDJSON development client.
- Supported image presets are `image-to-png`, `image-to-jpeg`, `image-to-webp`, `image-to-jxl`, `image-to-avif`, `image-to-gif`, `image-to-bmp`, `image-to-tiff`, `image-to-ico`, and `image-to-heic`.
- Media presets are exactly five video outputs (`video-to-mp4`, `video-to-mov`, `video-to-webm`, `video-to-mkv`, `video-to-avi`) and twelve audio outputs (`audio-to-mp3`, `audio-to-wav`, `audio-to-flac`, `audio-to-aac`, `audio-to-m4a`, `audio-to-ogg`, `audio-to-opus`, `audio-to-aiff`, `audio-to-alac`, `audio-to-ac3`, `audio-to-amr`, `audio-to-wma`). `ffprobe` classifies sources from their stream contents, with video taking precedence when both audio and video streams exist. Video inputs may extract audio using FFmpeg's `-vn` option; audio sources never advertise video targets.
- ICO output is capped at 256×256; GIF/WebP animation is preserved when supported.
- Inputs are absolute regular files and formats are detected from file content.
- ImageMagick performs conversion. The daemon never uploads files or uses a network service.
- Output files are created through temporary files and committed with an atomic rename.

ImageMagick is required at runtime. The backend looks for `magick` first and then `convert`.

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
5. Documents, media, archives, packaging, and additional conversion engines
