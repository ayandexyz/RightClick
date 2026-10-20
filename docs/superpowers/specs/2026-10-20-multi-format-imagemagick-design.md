# Multi-format ImageMagick support design

## Goal
Add AVIF, GIF, BMP, TIFF, ICO, and HEIC as content-detectable inputs and capability-aware output targets through the existing ImageMagick conversion pipeline.

## Format model and detection
Add formats and presets `image-to-avif`, `image-to-gif`, `image-to-bmp`, `image-to-tiff`, `image-to-ico`, and `image-to-heic`. Detect by content: scan at least the first 32 bytes for ISO-BMFF `ftyp` brands (`avif`/`avis` for AVIF and `heic`/`heix`/`mif1`/`msf1` for HEIC); recognize GIF87a/GIF89a, BMP `BM`, TIFF `II*\\0` or `MM\\0*`, and ICO `00 00 01 00` with nonzero count and plausible directory entries. CUR (`00 00 02 00`) is excluded.

## ImageMagick capability probing
Probe `magick -list format` once during startup and maintain read/write capability maps. Content detection accepts only formats the local build can decode. Target enumeration exposes only formats the build can encode. Startup fails if capability probing cannot complete, preventing unreliable target advertisements.

## Conversion policies
Continue using the generic ImageMagick engine. ICO always applies `-resize 256x256>` and combines with `max_size` using the smaller effective edge. GIF and WebP can preserve animation; append `[0]` for targets that cannot hold animation. Flatten alpha for JPEG, BMP, and GIF, defaulting their background to white. PNG, WebP, JXL, AVIF, HEIC, TIFF, and ICO preserve alpha where supported. Quality applies only to JPEG, AVIF, and HEIC; it is ignored for GIF, BMP, TIFF, and ICO. Existing PNG/WebP/JXL behavior remains unchanged.

## Integration and tests
Update protocol targets, format mappings, CLI target mapping, registry target enumeration, README/protocol docs, and fixtures. Test signatures and capability-aware detection for every format, target filtering, same-format exclusion, animation argument selection, ICO sizing, alpha flattening, quality filtering, and local capability-dependent output tests with skips when a writer is unavailable.
