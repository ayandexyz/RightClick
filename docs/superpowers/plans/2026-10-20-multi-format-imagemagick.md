# Multi-format ImageMagick Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add content detection, capability-aware target advertisement, and ImageMagick conversion support for AVIF, GIF, BMP, TIFF, ICO, and HEIC.

**Architecture:** Keep format signatures and preset mappings in `internal/formats`, while `engine.ImageMagick` owns executable discovery, capability probing, format-aware argv policies, and unsupported-read errors. The registry consumes engine capability maps for target enumeration, and the daemon initializes the engine before serving. Existing protocol/server/job plumbing remains generic except for the expanded targets and docs/frontend mappings.

**Tech Stack:** Go, ImageMagick `magick`/`convert`, Python Nautilus frontend, Go tests.

## Global Constraints

- Detection is by content signature and independent of ImageMagick capability.
- ISO-BMFF detection scans the complete bounded `ftyp` box, checks AVIF brands before HEIC brands, and supports the specified HEIC variants.
- Probe `magick -list format` or `convert -list format` once; parse the mode column exactly into read/write maps.
- Probe failure is non-fatal: no binary means no targets and an `imagemagick not found` conversion error; unparseable output falls back to PNG/JPEG/WebP.
- Reject unsafe input basenames containing `[`, `]`, or `:` or beginning with `@` before frame selectors are appended.
- GIF/WebP preserve animation; other targets select `[0]` from multi-frame GIF/WebP/TIFF/ICO sources.
- ICO output always applies shrink-only `-resize 256x256>` and combines with `max_size` using the smaller edge.
- JPEG, BMP, and GIF flatten alpha; alpha-bearing backgrounds are invalid for those targets.
- Quality applies to JPEG, WebP, JXL, AVIF, and HEIC only.

---

### Task 1: Add format constants, signatures, presets, and fixtures

**Files:**
- Modify: `backend/internal/formats/images.go`
- Modify: `backend/internal/formats/images_test.go`
- Modify: `backend/internal/formats/validate_test.go`
- Modify: `backend/internal/protocol/validate.go`
- Modify: `backend/internal/protocol/validate_test.go`
- Modify: `backend/internal/protocol/types.go`

**Interfaces:**
- Produce `formats.AVIF`, `formats.GIF`, `formats.BMP`, `formats.TIFF`, `formats.ICO`, and `formats.HEIC`.
- Extend `formats.PresetFormat` and `protocol.SupportedPresets` with the six `image-to-*` presets.
- Keep `formats.Detect(path) (Format, error)` independent of capability.

- [ ] **Step 1: Write failing signature tests** for GIF, BMP, classic/BigTIFF, ICO, CUR exclusion, AVIF/HEIC brands, AVIF priority over shared `mif1`, and malformed/truncated ISO-BMFF/ICO inputs.
- [ ] **Step 2: Run `go test ./internal/formats ./internal/protocol`** and confirm the new cases fail because constants and signatures are absent.
- [ ] **Step 3: Implement bounded signature readers**: read the 4-byte ISO-BMFF size and type, cap the box, scan major and compatible brands; recognize all required signatures; validate ICO count and each directory entry's nonzero size/offset within file bounds.
- [ ] **Step 4: Add constants and preset mappings** and update protocol target fixtures.
- [ ] **Step 5: Run `gofmt -w internal/formats internal/protocol` and `go test ./internal/formats ./internal/protocol`**, expecting PASS.
- [ ] **Step 6: Commit:** `feat: add image format detection and presets`.

### Task 2: Implement ImageMagick capability probing

**Files:**
- Modify: `backend/internal/engine/imagemagick.go`
- Modify: `backend/internal/engine/imagemagick_test.go`
- Modify: `backend/internal/registry/registry.go`
- Modify: `backend/internal/registry/registry_test.go`
- Modify: `backend/cmd/fcd/main.go`

**Interfaces:**
- Add `Capability(format formats.Format) (read, write bool)` to `ImageMagick`.
- Add `ProbeCapabilities() error` with fallback behavior and cached maps.
- Registry target enumeration consumes writable capabilities, while `Detect` remains signature-only.

- [ ] **Step 1: Write parser tests** using representative `-list format` rows and assert exact format-name matching and `r--`/`rw+` read/write flags.
- [ ] **Step 2: Write tests for missing binaries, malformed output fallback, and writable target filtering.**
- [ ] **Step 3: Run focused engine/registry tests and confirm failure.
- [ ] **Step 4: Implement executable selection and one-time probe** with `magick -list format`, fallback to `convert`, cached capabilities, and baseline fallback `{png,jpeg,webp}` on parse failure.
- [ ] **Step 5: Wire daemon startup to probe before creating the registry; keep startup non-fatal** and ensure no-binary conversion returns `imagemagick not found`.
- [ ] **Step 6: Update registry `Targets`/`TargetsForFiles` to exclude same-format targets and unwritable targets in deterministic protocol order.
- [ ] **Step 7: Run `gofmt`, focused tests, and commit:** `feat: probe imagemagick format capabilities`.

### Task 3: Add target-aware ImageMagick argv behavior

**Files:**
- Modify: `backend/internal/engine/imagemagick.go`
- Modify: `backend/internal/engine/imagemagick_test.go`
- Modify: `backend/internal/engine/process_test.go`
- Modify: `backend/internal/jobs/manager.go`
- Modify: `backend/internal/jobs/manager_test.go`

**Interfaces:**
- Preserve `Convert(ctx, source, sourceFormat, destination, target, options)`.
- Produce deterministic argument ordering: input/frame selector, auto-orient, resize, coalesce/alpha operations, quality, output.

- [ ] **Step 1: Add failing argv tests** for first-frame selection, animation-preserving GIF/WebP output, `-coalesce` before GIF flattening, ICO 256 cap, combined max-size, target-specific alpha flattening, alpha-background rejection, and quality allowlist.
- [ ] **Step 2: Implement format capability sets and frame policy.** Select `[0]` for multi-frame sources when target is not GIF/WebP; reject unsafe paths before selector construction.
- [ ] **Step 3: Implement resize policy** using the minimum of `max_size` and ICO's 256 edge.
- [ ] **Step 4: Implement alpha policy** for JPEG/BMP/GIF and default white backgrounds; preserve alpha for the specified targets; coalesce animated GIF before flattening.
- [ ] **Step 5: Update quality filtering** to include AVIF/HEIC while excluding PNG/GIF/BMP/TIFF/ICO.
- [ ] **Step 6: Run focused engine/jobs tests and commit:** `feat: add format-aware imagemagick conversion`.

### Task 4: Update CLI, Nautilus frontend, documentation, and end-to-end coverage

**Files:**
- Modify: `backend/cmd/fcctl/main.go`
- Modify: `backend/cmd/fcctl/main_test.go`
- Modify: `frontends/omarchy/nautilus/file_converter.py`
- Modify: `frontends/omarchy/nautilus/test_extension_menu.py`
- Modify: `README.md`
- Modify: `docs/PROTOCOL.md`
- Modify: `backend/e2e_test.go`

**Interfaces:**
- CLI target aliases map all new extensions to their presets.
- Nautilus `SUPPORTED_MIME_TYPES` includes the exact MIME additions from the spec.

- [ ] **Step 1: Add failing CLI and frontend tests** for all aliases/MIME types.
- [ ] **Step 2: Implement mappings and update user-facing supported-format documentation**, including ICO's 256×256 cap and capability-dependent targets.
- [ ] **Step 3: Add e2e target assertions without assuming optional local writers; skip capability-dependent conversions.
- [ ] **Step 4: Run Go, Python, and frontend tests, then commit:** `feat: expose additional image formats`.

### Task 5: Full verification and review

**Files:**
- Review all files changed by Tasks 1-4.

- [ ] Run `go test ./...` from `backend/`.
- [ ] Run `go vet ./...` from `backend/`.
- [ ] Run Python tests from `frontends/omarchy/nautilus` using the repository's existing test command.
- [ ] Run `git diff --check` and inspect `git diff` for requirement coverage, unsafe argument handling, and accidental target advertisement.
- [ ] If all checks pass, report the commits and verification results.
