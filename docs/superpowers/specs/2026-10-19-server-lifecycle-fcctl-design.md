# Server Lifecycle and fcctl Design

## Scope

Strengthen the backend's Unix-socket server coverage and make `fcctl` usable for both terminal users and scripts. The wire protocol remains unchanged.

## Server behavior

Add tests for successful cancellation, unknown-job cancellation, client disconnect continuation, malformed JSON, invalid parameters, missing files, unsupported presets, idle shutdown, and stale socket handling. Preserve submitted jobs when a client disconnects; job event writes may fail without cancelling the job.

## fcctl behavior

Keep `hello`, `targets`, `convert`, and `cancel`. Add local argument validation and support `--to png|jpeg|webp` as an alias for the corresponding image preset. Conversion waits for `job.finished`.

Human-readable output is the default. It reports job state and progress, and reports errors to stderr with a non-zero exit status. `--json` prints each raw response or event as JSON for scripts. The flag is accepted for conversion and general command output.

Existing `--preset`, `--output-dir`, and `--on-conflict` options remain supported. No protocol changes, persistent job history, or new conversion engines are included.

## Verification

Use focused tests for server lifecycle and CLI parsing/output, then run `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./cmd/fcd ./cmd/fcctl`, and `git diff --check`.
