# Multi-format ImageMagick support design

## Goal
Add AVIF, GIF, BMP, TIFF, ICO, and HEIC as content-detectable inputs and capability-aware output targets through the existing ImageMagick conversion pipeline.

## Format model and detection
Add formats and presets `image-to-avif`, `image-to-gif`, `image-to-bmp`, `image-to-tiff`, `image-to-ico`, and `image-to-heic`.

Detection is by content signature only and never depends on the local ImageMagick build:

- **ISO-BMFF (AVIF, HEIC):** read the whole `ftyp` box (its size field gives the length, capped at a small maximum) and inspect the major brand plus every compatible brand. If any brand is `avif` or `avis`, the file is AVIF. Otherwise, if any brand is `heic`, `heix`, `heim`, `heis`, `hevc`, `hevx`, or `mif1`/`msf1`, it is HEIC. AVIF is checked first because `mif1` is shared by both.
- **GIF:** `GIF87a` or `GIF89a`.
- **BMP:** `BM`.
- **TIFF:** classic `II*\0` or `MM\0*`, and BigTIFF `II+\0` or `MM\0+`. Camera RAW files that reuse the TIFF header (for example DNG) are not excluded by signature; they are accepted as TIFF and left to ImageMagick to decode or reject.
- **ICO:** `00 00 01 00` with a nonzero image count and plausible directory entries. CUR (`00 00 02 00`) is excluded.

## ImageMagick capability probing
Probe `magick -list format` (or `convert -list format` when only `convert` exists) once during startup, covering every format including PNG, JPEG, WebP, and JXL. Parse the mode column (`r--`, `rw+`, and so on) to build separate read and write capability maps; do not substring-match format names.

- **Target enumeration** exposes only formats the build can write.
- **Detection is independent of capability.** When a file's signature is recognized but the build cannot read it, the conversion fails with a clear error such as "HEIC is not supported by this ImageMagick build", never "unknown format".
- **Probe failure is non-fatal.** If neither `magick` nor `convert` exists, `fcd` still starts, advertises no targets, and conversion requests return "imagemagick not found". If the binary exists but its output cannot be parsed, fall back to the baseline formats (PNG, JPEG, WebP) instead of refusing to start.

## Conversion policies
Continue using the generic ImageMagick engine.

- **Resize:** ICO always applies `-resize 256x256>` and combines with `max_size` by taking the smaller effective edge.
- **Frame selection:** append `[0]` to the input when the source can hold multiple frames (GIF, WebP, TIFF, ICO) and the target is not GIF or WebP. GIF and WebP output preserve animation. Before appending `[0]`, input paths containing `[` or `]` must be rejected or escaped, since ImageMagick parses them as frame or geometry specifiers. When an animated source is written to GIF with alpha flattening, apply `-coalesce` before `-alpha remove`.
- **Alpha:** flatten for JPEG, BMP, and GIF, defaulting the background to white. PNG, WebP, JXL, AVIF, HEIC, TIFF, and ICO preserve alpha where supported. A `background` value carrying alpha (`transparent` or `#rrggbbaa`) is rejected with a validation error when the target is JPEG, BMP, or GIF.
- **Quality:** applies to JPEG, WebP, JXL, AVIF, and HEIC. It is ignored for PNG, GIF, BMP, TIFF, and ICO.
- Existing PNG, WebP, and JXL conversion behavior is otherwise unchanged.

## Integration and tests
Update protocol targets, format mappings, CLI target mapping, registry target enumeration, README/protocol docs, and fixtures.

The Nautilus frontend must be updated as well: `frontends/omarchy/nautilus/file_converter.py` hard-codes `SUPPORTED_MIME_TYPES` (currently `image/png`, `image/jpeg`, `image/webp`). Extend it with `image/jxl`, `image/avif`, `image/gif`, `image/bmp`, `image/x-ms-bmp`, `image/tiff`, `image/vnd.microsoft.icon`, `image/x-icon`, `image/heic`, and `image/heif`, and update `test_extension_menu.py`. Prefer deriving the list from the daemon if the protocol can expose supported inputs.

Tests cover: signatures for every format including AVIF-versus-HEIC brand priority, BigTIFF, and CUR exclusion; detection independent of capability with the clear unsupported-build error; capability-map parsing from sample `-list format` output; target filtering and same-format exclusion; non-fatal probe failure; `[0]` selection and path-bracket handling; `-coalesce` ordering; ICO sizing; alpha flattening and rejection of alpha backgrounds; quality filtering; and local capability-dependent output tests that skip when a writer is unavailable.
