# PDF Rasterization Design

## Goal

Add local PDF input support for the first document milestone. PDF files can be rendered into one output image per page through the existing daemon, job lifecycle, atomic output handling, and Nautilus integration.

The initial output formats are PNG, JPEG, and WebP. Every page is rendered by default at 150 DPI. Page-range selection, custom resolution, PDF generation, and document editing remain out of scope.

## Scope

Supported presets:

- `pdf-to-png`
- `pdf-to-jpeg`
- `pdf-to-webp`

PDF detection is content-aware and must recognize the PDF signature rather than relying only on the filename extension. Existing raster image, SVG, video, and audio behavior remains unchanged.

Each input PDF produces numbered outputs in page order, for example `document-1.png`, `document-2.png`, and `document-3.png`. Output naming must remain collision-safe and use the existing conflict policy. Multi-page output is part of one submitted job, and a failed page render must not publish a partial page output for that page.

## Architecture

Add PDF as a distinct document input format rather than classifying it as a raster image. A dedicated PDF engine uses the installed Poppler `pdftocairo` executable. The engine must not pass PDFs through ImageMagick or rely on ImageMagick PDF delegates.

The engine invokes `pdftocairo` with argument arrays, a fixed 150 DPI setting, and an output prefix in a controlled temporary directory. PNG, JPEG, and WebP output modes are selected explicitly. Because `pdftocairo` emits one file per page, the engine reports or returns the ordered page paths to the job layer, which atomically publishes each final output with the existing job machinery.

The registry resolves PDF sources only to the three PDF presets. Raster, SVG, video, and audio sources never resolve to PDF targets, and PDF sources never resolve to unrelated document or media targets in this milestone.

## Safety and limits

Before rendering, the PDF engine validates:

- absolute regular input paths;
- bounded input size using the backend's configured file limits;
- the PDF header/signature and readable input;
- a controlled output directory and generated filename prefix.

Renderer commands use argument arrays, never shell interpolation. The input path and generated output paths must remain separate arguments even when they contain spaces or shell metacharacters. Temporary page outputs are created in the job's controlled temporary directory and are atomically published only after successful conversion.

The engine must enforce a practical page/output limit to avoid unbounded resource use. The initial implementation should use the existing job or protocol error path for renderer failures and must clean up temporary page files on failure. Unsupported or unavailable `pdftocairo` produces a clear engine availability error and does not hide unrelated image, SVG, video, or audio targets.

PDF content is rendered locally. No network access, uploads, or external resource fetching is introduced by this feature.

## Protocol and frontend

Add PDF target entries to `SupportedPresets`, preset validation, target discovery, and output extension handling. The Nautilus extension adds common PDF MIME types to supported input selection. The target submenu exposes the three PDF rasterization targets when `pdftocairo` is available.

Labels:

- PDF to PNG
- PDF to JPEG
- PDF to WebP

The existing request shape remains unchanged. A conversion request still contains one or more input paths, a preset, and an output directory. Page outputs are reported in the job completion event as multiple output paths.

## Testing

Add tests for:

- content-based PDF detection and `.pdf` extension handling;
- PDF preset validation and target ordering;
- PDF engine availability and support matrix;
- exact `pdftocairo` argument construction, including paths with spaces and shell metacharacters;
- all-pages output naming and ordered output collection;
- renderer failure cleanup and no partial publication;
- registry discovery for PDF-only and mixed selections;
- Nautilus PDF menu construction;
- real PDF-to-PNG conversion with page-count and PNG-signature verification;
- conditional PDF-to-JPEG and PDF-to-WebP verification when `pdftocairo` supports those output modes;
- regression coverage proving raster, SVG, video, and audio sources do not advertise PDF targets.

## Non-goals

- PDF generation or image-to-PDF conversion.
- Page-range, page-selection, crop, rotation, or custom DPI options.
- PDF text extraction, OCR, editing, compression, or optimization.
- Password-protected PDF handling beyond a clear renderer failure.
- ImageMagick PDF delegates or cloud conversion.
- New frontend platforms beyond the existing Nautilus integration.
