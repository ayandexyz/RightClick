# SVG Rasterization Design

## Goal

Add secure local SVG input support for the first vector milestone. SVG files can be converted to PNG, JPEG, and WebP through the existing daemon, job lifecycle, atomic output handling, and Nautilus integration.

Raster-to-SVG remains unsupported.

## Scope

Supported presets:

- `svg-to-png`
- `svg-to-jpeg`
- `svg-to-webp`

SVG detection is content-aware and must not rely only on the filename extension. Existing raster image, video, and audio behavior remains unchanged.

## Architecture

Add SVG as a distinct input format rather than classifying it as a raster image. A dedicated SVG engine handles SVG validation and rasterization. The engine uses an installed dedicated SVG renderer when available, preferring the existing system's safe renderer (`rsvg-convert`/librsvg or an equivalent explicitly supported renderer) instead of passing SVG directly to generic ImageMagick delegates.

The renderer produces an intermediate PNG. Existing ImageMagick output conversion and output publication logic then produce JPEG or WebP. PNG output may use the intermediate directly through the same atomic publication path.

The registry resolves SVG sources to SVG targets through the SVG engine and to raster targets through the SVG engine plus the existing image output path. Raster sources never resolve to SVG targets.

## Safety and limits

Before rendering, the SVG engine validates:

- absolute regular input paths;
- bounded input size using the backend's configured file limits;
- valid SVG/XML structure;
- no XML external entities or external DTDs;
- no scripts or event-handler attributes;
- no external file, HTTP, HTTPS, or data references unless explicitly supported by the renderer policy;
- no path traversal through referenced resources.

Renderer commands use argument arrays, never shell interpolation. Temporary intermediates are created in the job's controlled temporary directory and are atomically published only after successful conversion.

Unsupported or unavailable renderer dependencies produce a clear engine availability error and do not hide unrelated ImageMagick targets.

## Protocol and frontend

Add SVG target entries to `SupportedPresets`, `PresetFormat`, target validation, and target discovery. The Nautilus extension adds SVG MIME types to supported input selection. The normal target submenu exposes the three SVG rasterization targets when the renderer is available.

Labels:

- SVG to PNG
- SVG to JPEG
- SVG to WebP

## Testing

Add tests for:

- content-based SVG detection and extension handling;
- valid SVG acceptance;
- rejection of external entities, scripts, external URLs, traversal, malformed XML, and oversized input;
- preset validation and target ordering;
- renderer availability and command argument construction;
- SVG-to-PNG real conversion with output signature verification;
- SVG-to-JPEG and SVG-to-WebP output verification when ImageMagick supports those formats;
- Nautilus SVG menu construction;
- regression coverage proving raster-to-SVG remains unavailable.

## Non-goals

- Raster-to-SVG tracing or vectorization.
- SVG editing, optimization, or SVG-to-SVG normalization.
- Embedded font downloading or network access.
- Cloud conversion or new frontend platforms.
