# PDF Tools Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the plumbing for PDF operations and five working operations (merge, extract pages, delete pages, rotate, reverse) through the existing daemon, job lifecycle, and `fcctl`, with no GUI yet.

**Spec:** `docs/superpowers/specs/2026-10-21-pdf-tools-design.md`

**Architecture:** A new `pdfops` package holds an `Operation` catalogue and a `Backend` interface; the only real backend wraps pdfcpu, and tests use a fake. Operations are not source-to-target conversions, so they get their own protocol methods (`operations`, `run_operation`) and their own `Manager.SubmitOperation`, which reuses the manager's queue, events, cancel, output planning (`engine.OutputPlan`), reservation set, and atomic publication instead of duplicating them.

**Tech Stack:** Go, `github.com/pdfcpu/pdfcpu`, Unix-socket NDJSON protocol v1, `fcctl`.

## Global Constraints

- Protocol stays v1 and backward compatible: existing methods and presets are untouched.
- All inputs must be absolute regular files with a `%PDF-` header (reuse `formats.ValidateInput` and the signature check in `engine/pdf.go:validatePDF`). Bound page count with the existing `maxPDFPages` (1000).
- Outputs are never written in place: always planned through `engine.OutputPlan`, honouring `on_conflict`, and never overwriting the source.
- No shell, no network. pdfcpu runs in-process, so: call `api.DisableConfigDir()` once at startup (the daemon must not create `~/.config/pdfcpu`), wrap every call in `recover`, and treat context cancellation as abandon-and-rollback (the pdfcpu API is not context-aware).
- Encrypted PDFs fail with error code `password_required`. Passwords are out of scope until phase 4.
- Only the five operations below ship; the catalogue must make adding the rest a one-file change.
- Run `go test ./...`, `go vet ./...`, `gofmt -l .` (must print nothing), and `git diff --check` before completion. Engine tests that need an external binary skip when it is missing, as the existing ones do.

## Operation IDs (fixed)

| ID | Inputs | Params | Output name |
| --- | --- | --- | --- |
| `pdf-merge` | 2 or more | none | `merged.pdf` in the first file's directory (or `output_dir`) |
| `pdf-extract` | 1 or more | `pages` (required) | `<stem>-extracted.pdf` |
| `pdf-delete` | 1 or more | `pages` (required) | `<stem>-edited.pdf` |
| `pdf-rotate` | 1 or more | `angle` (90, 180, 270, required), `pages` (optional, default all) | `<stem>-rotated.pdf` |
| `pdf-reverse` | 1 or more | none | `<stem>-reversed.pdf` |

Merge produces one output from all inputs, in the order given. Every other operation produces one output per input file.

## Page-spec grammar (fixed)

Comma-separated items; each is `N`, `N-M`, `N-` (to the last page), `-M` (from page 1), or `z` (last page), 1-based, whitespace ignored. Examples: `1-3,7,10-`, `z`, `-2`. Duplicates and overlaps are allowed for `Select` (order preserved) and collapsed for `Set`. Out-of-range pages, `0`, reversed ranges (`5-3`), empty items, and garbage are errors.

---

### Task 1: Add the pdfcpu dependency

**Files:**
- Modify: `backend/go.mod`, `backend/go.sum`

- [ ] **Step 1: Check the licence.** Fetch the pinned release's LICENSE from the upstream repository and confirm it is Apache-2.0 (or otherwise compatible). If not, stop and report; the fallback is a `qpdf` backend (see the spec).
- [ ] **Step 2: Add the module.** From `backend/`: `go get github.com/pdfcpu/pdfcpu@v0.12.0` (the version `pdf-utils-go` builds against; use a newer release only if it is verified). Do not import it yet; `go mod tidy` will drop it, so skip tidy until Task 3.
- [ ] **Step 3: Record the pin.** Add a one-line note to the README PDF section: pdfcpu is pinned at the version above.
- [ ] **Step 4: Commit.**

```bash
git add backend/go.mod backend/go.sum
git commit -m "build: add pdfcpu dependency"
```

### Task 2: Page-spec parser

**Files:**
- Create: `backend/internal/pdfops/pagespec.go`
- Test: `backend/internal/pdfops/pagespec_test.go`

**Interfaces:**
- `ParsePages(spec string, pageCount int) ([]int, error)` returns the pages in written order, 1-based, each validated against `pageCount`.
- `PagesSet(pages []int) []int` returns them sorted and de-duplicated.
- `Complement(pages []int, pageCount int) []int` returns every page not in `pages`, sorted.
- `Format(pages []int) string` collapses back to a canonical spec (`1-3,7`), used in error messages and later by the Studio.

- [ ] **Step 1: Write failing table tests.** Cover every example in the grammar section, `z` on a 1-page document, open ends on both sides, overlap (`1-3,2-4`), whitespace, and all error cases (`0`, `5-3`, `abc`, `,`, `1,,2`, `9` with `pageCount` 3, empty string, a spec that expands to more than `maxPDFPages` entries).
- [ ] **Step 2: Run** `go test ./internal/pdfops`; expect compile failure.
- [ ] **Step 3: Implement** the parser as a small hand-written scanner (no regexp backtracking), returning errors that name the offending item.
- [ ] **Step 4: Run tests, then commit.**

```bash
git add backend/internal/pdfops
git commit -m "feat: add PDF page-spec parser"
```

### Task 3: Operation catalogue and backend interface

**Files:**
- Create: `backend/internal/pdfops/operation.go`, `backend/internal/pdfops/backend.go`, `backend/internal/pdfops/catalogue.go`
- Test: `backend/internal/pdfops/catalogue_test.go`, `backend/internal/pdfops/fake_test.go`

**Interfaces:**

```go
type Params struct {
    Pages string // page spec, "" means not provided
    Angle int    // 0 means not provided
}

type Backend interface {
    ID() string
    Available() error
    PageCount(ctx context.Context, path string) (int, error)
    Merge(ctx context.Context, inputs []string, output string) error
    Collect(ctx context.Context, input, output string, pages []int) error // pages in order, duplicates allowed
    Rotate(ctx context.Context, input, output string, pages []int, angle int) error
}

type Operation struct {
    ID, Label, Group string
    Mode             string // "instant" or "studio"
    MinInputs, MaxInputs int // MaxInputs 0 = unlimited
    NeedsPages       bool
    NeedsAngle       bool
    Suffix           string // output name suffix, "" for merge
    Merges           bool   // all inputs produce one output
}

func Catalogue() []Operation
func Lookup(id string) (Operation, bool)
func (o Operation) Validate(files []string, p Params) error
func (o Operation) Run(ctx context.Context, b Backend, inputs []string, output string, p Params) error
```

`Run` resolves the page spec against `PageCount`, then maps to Backend calls: extract = `Collect(pages)`, delete = `Collect(Complement(pages))` (error if nothing would remain), reverse = `Collect(n..1)`, rotate = `Rotate`, merge = `Merge`. Reverse, delete and extract therefore need only `Collect`, which pdfcpu provides as `api.CollectFile`.

- [ ] **Step 1: Write failing tests against a fake `Backend`** that records calls: input-count rules (merge with 1 file fails, extract with 0 fails), missing `pages`/`angle`, invalid angle (45), delete-all-pages is rejected, reverse of a 4-page document calls `Collect` with `[4 3 2 1]`, delete of `2` on 4 pages calls `Collect` with `[1 3 4]`, rotate with empty `pages` rotates all pages.
- [ ] **Step 2: Run, expect failure. Implement. Run, expect pass.**
- [ ] **Step 3: Commit.**

```bash
git add backend/internal/pdfops
git commit -m "feat: add PDF operation catalogue"
```

### Task 4: pdfcpu backend

**Files:**
- Create: `backend/internal/pdfops/pdfcpu_backend.go`
- Test: `backend/internal/pdfops/pdfcpu_backend_test.go`

pdfcpu entry points to use (verify exact names and signatures against the pinned version before coding): `api.PageCountFile`, `api.MergeCreateFile`, `api.CollectFile`, `api.RotateFile`, `api.DisableConfigDir`, `model.NewDefaultConfiguration`.

- [ ] **Step 1: Write failing tests that build their own fixtures.** Generate a small N-page PDF at test time (via the backend or by writing a minimal valid PDF byte string), run each backend method, and verify page counts with `PageCount`. Where `pdftotext` exists, also assert page order for reverse by giving each fixture page distinct text; skip that assertion when the binary is missing.
- [ ] **Step 2: Implement.** Constructor calls `api.DisableConfigDir()` once (guard with `sync.Once`). Every method runs the pdfcpu call in a goroutine with `defer recover()` converting panics to errors, and returns `ctx.Err()` if the context ends first (the goroutine is left to finish writing to a temp file the job layer will delete). Map pdfcpu's encrypted-file error to a sentinel `ErrPasswordRequired`. `Available()` always returns nil (linked in).
- [ ] **Step 3: Run `go mod tidy`, tests, and `go vet`. Commit.**

```bash
git add backend/go.mod backend/go.sum backend/internal/pdfops
git commit -m "feat: add pdfcpu PDF backend"
```

### Task 5: Protocol types and validation

**Files:**
- Modify: `backend/internal/protocol/validate.go`, `backend/internal/protocol/types.go`
- Test: `backend/internal/protocol/validate_test.go`

**Interfaces:**
- New `Operation` wire type: `{id, label, group, mode, enabled, reason?}`.
- `operations` params: `{files: [string]}` (absolute paths, at least one).
- `run_operation` params: `{op: string, files: [string], params: {pages?: string, angle?: int}, output_dir?: string, on_conflict?: "rename"|"skip"|"overwrite"}`.
- `ValidateRequest` accepts both methods. It checks shape only (non-empty `op`, non-empty `files`, absolute paths, valid conflict value, `angle` an integer); per-operation rules stay in `pdfops`.

- [ ] **Step 1: Write failing tests** for valid and invalid shapes of both methods, and assert the existing "unknown method" test still passes for a genuinely unknown name.
- [ ] **Step 2: Implement. Run `go test ./internal/protocol`. Commit.**

```bash
git add backend/internal/protocol
git commit -m "feat: add PDF operation protocol methods"
```

### Task 6: Job manager support for operations

**Files:**
- Modify: `backend/internal/jobs/manager.go`
- Create: `backend/internal/jobs/operations.go`
- Test: `backend/internal/jobs/operations_test.go`

**Interfaces:**

```go
type OperationRequest struct {
    Op        string
    Files     []string
    Params    pdfops.Params
    OutputDir string
    Conflict  protocol.ConflictPolicy
    Events    func(protocol.Event)
}

func (m *Manager) SetPDFBackend(b pdfops.Backend)
func (m *Manager) SubmitOperation(req OperationRequest) (string, error)
```

`job` gains an optional `op *OperationRequest`; `run` dispatches to `runOperation` when it is set. `Job.Preset` on the wire carries the operation ID so existing clients and events keep working.

Behaviour:
- `SubmitOperation` validates synchronously (unknown op, `Operation.Validate`, each file is a regular `%PDF-` file) and returns `bad_request`-class errors via `formats.ErrInvalidInput`.
- Merge is one unit of work whose `Files` list has one entry per input; on success every entry is `done` and the single output path appears on the first entry (others get empty `Outputs`). Failure marks all entries failed with the same error.
- Other operations run one unit per input file through the existing worker lanes (PDF jobs are not video, so they use the normal pool bound).
- Output planning uses a new `engine.PlanNamedOutputExcluding(dir, source, name, ext, conflict, taken)` that generalises `planOutput` (today the name is derived as `stem + suffix`; add a thin wrapper rather than changing existing signatures). The rule that output must not equal source stays.
- Publication, rollback on cancel, `reserved` bookkeeping, and `Finalize` follow `convertFile` exactly. Do not copy-paste it: extract the shared plan, reserve, commit, and pending steps into a helper used by both paths, keeping existing manager tests green.
- Backend errors map to file errors: `ErrPasswordRequired` becomes code `password_required`; everything else is `internal`.

- [ ] **Step 1: Write failing tests with the fake backend:** merge of three files yields one output and `completed`; extract on two files yields two outputs named `<stem>-extracted.pdf`; conflict policies (`rename` produces `(1)`, `skip` skips, `overwrite` replaces and backs up); output never overwrites the source; cancel mid-run leaves no files; one failing file among two gives `completed_with_errors`; encrypted-file error surfaces as `password_required`; two concurrent operation jobs never pick the same destination.
- [ ] **Step 2: Run, expect failure.** Add `PlanNamedOutputExcluding` with its own engine test first, then the manager changes.
- [ ] **Step 3: Refactor the shared publish helper** and run the whole `./internal/jobs` suite to prove conversions are unchanged.
- [ ] **Step 4: Commit.**

```bash
git add backend/internal/engine backend/internal/jobs
git commit -m "feat: run PDF operations through the job manager"
```

### Task 7: Server methods

**Files:**
- Modify: `backend/internal/server/server.go`
- Test: `backend/internal/server/server_test.go`

- [ ] **Step 1: Write failing server tests:**
  - `operations` for one PDF returns the five operations; `pdf-merge` is disabled with reason `needs at least 2 files`; for two PDFs merge is enabled.
  - For a non-PDF selection, `operations` returns an empty list, not an error.
  - `run_operation` returns `{"job": id}` and the subscriber receives `job.updated` and `job.finished`.
  - Invalid params give `bad_request`; an unknown op gives `bad_request`; an encrypted input surfaces `password_required` in the file error.
- [ ] **Step 2: Implement** the two cases in `handle`, following the `convert` case. `Dependencies` gains `PDFBackend pdfops.Backend`; `operations` consults `pdfops.Catalogue()` and `Backend.Available()`.
- [ ] **Step 3: Run `go test ./internal/server`. Commit.**

```bash
git add backend/internal/server
git commit -m "feat: serve PDF operations over the protocol"
```

### Task 8: Daemon wiring

**Files:**
- Modify: `backend/cmd/fcd/main.go`
- Test: `backend/cmd/fcd/main_test.go`

- [ ] **Step 1:** Construct the pdfcpu backend, pass it to `jobs.Manager.SetPDFBackend` and `server.Dependencies.PDFBackend`, and register a `pdfcpu` entry in the engine status list so `fcctl engines` reports it.
- [ ] **Step 2:** Test that `hello` lists the engine and that startup does not create `~/.config/pdfcpu` (set `HOME` and `XDG_CONFIG_HOME` to a temp dir and assert it stays empty).
- [ ] **Step 3: Commit.**

```bash
git add backend/cmd/fcd
git commit -m "feat: wire PDF backend into fcd"
```

### Task 9: `fcctl pdf` command

**Files:**
- Modify: `backend/cmd/fcctl/main.go`
- Test: `backend/cmd/fcctl/main_test.go`

Usage:

```
fcctl pdf merge a.pdf b.pdf [--output-dir DIR] [--on-conflict rename|skip|overwrite]
fcctl pdf extract file.pdf --pages 1-3,7
fcctl pdf delete  file.pdf --pages 2,4
fcctl pdf rotate  file.pdf --angle 90 [--pages 1-3]
fcctl pdf reverse file.pdf
fcctl pdf list    file.pdf        # prints applicable operations
```

- [ ] **Step 1: Write failing parse tests** (flags, positional files, missing `--pages`, bad `--angle`) and request-building tests asserting the exact JSON for `operations` and `run_operation`.
- [ ] **Step 2: Implement** `pdf` as a command that maps to `run_operation` and waits for `job.finished` like `convert` does; `--json` and the human formatter behave identically to `convert`.
- [ ] **Step 3: Commit.**

```bash
git add backend/cmd/fcctl
git commit -m "feat: add fcctl pdf command"
```

### Task 10: End-to-end test and docs

**Files:**
- Modify: `backend/e2e_test.go`, `docs/PROTOCOL.md`, `README.md`
- Create: none

- [ ] **Step 1: E2E:** start a real daemon on a temp socket with the real pdfcpu backend, build a 4-page fixture, then run extract, delete, rotate, reverse, and merge through the socket; assert page counts with `PageCount` and (when `pdftotext` exists) page order and rotation via `pdfinfo`.
- [ ] **Step 2: Docs.** Add `operations` and `run_operation` (params, results, error codes including `password_required`) to `docs/PROTOCOL.md`; add a "PDF tools" section to the README listing the five operations, the page-spec grammar, the output naming table, and the pdfcpu pin. Update the sentence in the PDF conversions section that says page selection is unsupported so it points to extract/delete.
- [ ] **Step 3: Full verification:** `go test ./...`, `go vet ./...`, `gofmt -l .`, `git diff --check`, plus `python3 -m unittest discover -s frontends/nautilus/tests` to prove the frontend is untouched.
- [ ] **Step 4: Commit.**

```bash
git add backend/e2e_test.go docs/PROTOCOL.md README.md
git commit -m "docs: document PDF operations"
```

---

## Out of scope for phase 1 (deliberately)

`pdf_info` and `pdf_thumbnails` (phase 3, they exist for the Studio), the Nautilus submenu (phase 2), passwords, every other operation in the spec catalogue, and any GUI.

## Risks

- **pdfcpu is not context-aware.** Cancel abandons a running call rather than stopping it, so a cancelled large merge can keep burning CPU until it finishes. Acceptable in phase 1; if it matters, move heavy operations to a subprocess (`qpdf`) behind the same `Backend` interface.
- **pdfcpu panics on malformed input** have been reported upstream for some PDFs; the `recover` wrapper and page-count bound are the mitigation, and the e2e suite should include one corrupt-PDF case that must fail cleanly.
- **Refactoring the publish path** in `jobs/manager.go` touches the most concurrency-sensitive code in the project. Task 6 requires the full existing job suite to stay green before and after.
