# Task 5 Report: Nautilus integration and documentation

## Status

Implemented. Sub-agent dispatch hit a usage limit partway through, so this task was completed directly.

## TDD evidence

- RED: `python3 -m unittest frontends/omarchy/tests/test_extension_menu.py` failed with `IndexError: list index out of range` in `test_pdf_selection_exposes_enabled_pdf_targets`, because `application/pdf` was rejected by `SUPPORTED_MIME_TYPES` and no menu was built.
- GREEN: after adding the PDF MIME types, `python3 -m unittest discover -s frontends/omarchy/tests` reports `Ran 20 tests ... OK`.

## Scope delivered

- `frontends/omarchy/nautilus/file_converter.py`: added `application/pdf` and `application/x-pdf` to `SUPPORTED_MIME_TYPES`.
- `frontends/omarchy/tests/test_extension_menu.py`: added `test_pdf_selection_exposes_enabled_pdf_targets`, asserting the three labels `PDF to PNG`, `PDF to JPEG`, `PDF to WebP` appear and a disabled target is dropped. The labels come from the daemon, so the test pins the frontend side of the contract.
- `README.md`: added a "PDF conversions" section covering Poppler `pdftocairo`, 150 DPI, numbered all-page outputs, the preset table, ImageMagick's WebP-only role, and the absence of page/range options.
- `docs/PROTOCOL.md`: documented the three PDF presets and their requirements next to the SVG and media preset paragraphs. The request shape was not changed.

## Self-review

- Changes are confined to the four files named in the task.
- The new test mirrors the existing SVG test and fails before the MIME change.
- Documentation states 150 DPI, one numbered output per page, `pdftocairo` for all three presets, and ImageMagick only for WebP, matching the engine implementation in `backend/internal/engine/pdf.go`.
- `git diff --check` passed.

## Concerns

- None. The label strings are supplied by the daemon's `targets` response; the frontend does not define them, so this test documents expected daemon labels rather than frontend-generated text.