# Omarchy Frontend, Phase 1: Nautilus "Convert to" Submenu

- **Date:** 2026-10-20
- **Status:** Draft for review
- **Depends on:** the Go backend slice (`fcd`, protocol v1 in `docs/PROTOCOL.md`)

## Goal

Right-click one or more image files in Nautilus on Omarchy, choose **Convert to ▸ JPEG / PNG / WebP**, and get the converted files next to the originals with a desktop notification when the job ends. All conversion work happens in `fcd`; the frontend only asks which targets exist, starts the job, and reports the result.

## Why this shape

Hyprland is Wayland, where an application cannot observe global pointer state or another application's drag. A context-menu entry inside the file manager avoids that limit. Nautilus is the default file manager here (Nautilus 51.1, `nautilus-python` 4.2, `Nautilus-4.1` typelib), so the entry is a `nautilus-python` extension.

## Extension language

Nautilus itself is written in C, and native extensions are C shared objects built against `libnautilus-extension`. This phase uses `nautilus-python` (itself such a C extension, which embeds Python) instead of a native C extension:

- A native extension runs inside the Nautilus process with no isolation, so a memory bug crashes the file manager. A Python extension that only does socket I/O has a much smaller failure surface.
- The extension ABI is tied to the installed Nautilus version, so a C extension needs rebuilding for each Nautilus release. The Python API (`Nautilus-4.1` typelib) does not.
- The menu logic is small and the heavy work is in `fcd`, so Python's speed is not a concern.
- It needs no compiler toolchain at install time.

The cost is a runtime dependency on `nautilus-python`. If that dependency becomes a problem (for example, packaging for distributions that don't ship it), a C extension that implements the same behavior against the same protocol is a possible later replacement. Nothing in `fc_client` or the protocol would change.

## Scope

### Included

- A Nautilus `MenuProvider` extension that adds one top-level item, **Convert to**, with a submenu of targets.
- A pure-Python protocol client (`fc_client`) that talks NDJSON to `fcd` over the Unix socket. It has no GTK or Nautilus imports, so it can be tested on its own.
- Starting `fcd` automatically: a systemd user unit installed by the installer, plus an on-demand spawn from the extension when the socket is missing.
- A completion notification through `notify-send`.
- An installer and uninstaller script.
- Automated tests for the client, and a stub-based test for the extension's menu logic.

### Excluded (later phases)

- Progress bar, toast window, and a cancel button (phase 2).
- Radial menu overlay (phase 3).
- Folders, recursive conversion, and background-click items.
- Non-local files (anything whose URI scheme is not `file`).
- Other file managers.
- Per-conversion options (quality, size, output directory); `convert` uses the protocol defaults (output next to the source, `on_conflict: rename`).
- Packaging for distribution (AUR, Omarchy plugin).

## Layout

```text
frontends/omarchy/
├── nautilus/
│   ├── file_converter.py        extension entry point (thin; only Nautilus glue)
│   └── fc_client/
│       ├── __init__.py
│       ├── client.py            socket, NDJSON framing, request/response, events
│       ├── daemon.py            socket path, fcd discovery, spawn-on-demand
│       └── notify.py            notify-send wrapper
├── systemd/fcd.service          user unit
├── install.sh
├── uninstall.sh
└── tests/
    ├── test_client.py
    ├── test_daemon.py
    └── test_extension_menu.py   stubbed Nautilus module
```

`nautilus-python` loads each `.py` file at the top level of an extensions directory as an extension, so helper code lives in the `fc_client/` package directory, which the loader ignores. The loader removes empty entries from `sys.path` and does not add the extension's own directory, so `file_converter.py` must insert `os.path.dirname(__file__)` into `sys.path` before importing `fc_client`.

## Installation locations

| Item | Path |
|---|---|
| Extension and package | `~/.local/share/nautilus-python/extensions/` (`file_converter.py`, `fc_client/`) |
| `fcd` binary | `~/.local/bin/fcd` |
| systemd unit | `~/.config/systemd/user/fcd.service` |

`install.sh` builds `fcd` from `backend/` with `go build`, copies the files, runs `systemctl --user daemon-reload`, enables and starts the unit, and then runs `nautilus -q` so the file manager reloads extensions. `uninstall.sh` reverses each step and leaves user files untouched. Both scripts are idempotent and print what they changed.

### systemd unit

`fcd.service` runs `%h/.local/bin/fcd serve --idle-exit 0` with `Restart=on-failure`, and is wanted by `default.target`. `--idle-exit 0` keeps it running so the first right-click after login is fast. If the unit is not running (the user disabled it, or it was never installed), the extension's spawn-on-demand path still works with the daemon's default idle exit.

## Protocol client (`fc_client`)

### Socket discovery

The path is `$XDG_RUNTIME_DIR/file-converter/fcd.sock`, as the daemon computes it. If `XDG_RUNTIME_DIR` is unset, the client raises `DaemonUnavailable` and does not guess a path. A path of 108 bytes or more raises a clear `DaemonUnavailable` message instead of the raw OS error.

### Connection and framing

- One connection per operation; no connection pooling and no long-lived state in the Nautilus process.
- Messages are UTF-8 JSON, one per line. Requests carry an integer `id`; the client matches responses by `id` and treats messages without `id` as events.
- Every socket operation has a timeout. Defaults: connect 0.3 s, `hello` and `targets` round trip 0.5 s total, `convert` submit 2 s. A job's event stream has no overall timeout but the client treats 60 s of silence as a lost daemon.
- The first message on each connection is `hello` with `{"client": "nautilus-extension", "protocol": 1}`. A `protocol` mismatch or an unknown `server` raises `ProtocolError`.

### API

```python
class FcClient:
    def targets(self, paths: list[str]) -> list[Target]            # menu-building call, short timeouts
    def convert(self, paths: list[str], preset: str) -> Job        # submits, then yields events until finished
```

`Target` and `Job` mirror the wire types (`preset`, `label`, `format`, `category`, `enabled`, `reason`; job `id`, `state`, `files[]` with `state`, `outputs`, `error`). Unknown fields are ignored. Errors map to exceptions: `DaemonUnavailable`, `ProtocolError`, and `RequestError(code, message)` for protocol error responses such as `bad_request` and `unsupported`.

### Daemon startup

`ensure_daemon()` is called when connecting fails with "no such file" or "connection refused":

1. Look for `fcd` in this order: `$FC_FCD`, `~/.local/bin/fcd`, then `PATH`.
2. If `fcd` is found, start it detached with `fcd serve` (own session, stdin, stdout and stderr to `/dev/null`) and poll the socket every 50 ms for up to 1.5 s.
3. If the socket answers, continue; otherwise raise `DaemonUnavailable`.

The extension never blocks the file manager on this path. When it builds a menu and the daemon is down, it starts the spawn in the background, omits the item for this one menu open, and logs the reason. The next right-click, a second or two later, finds the daemon running. This only occurs if the systemd unit is not active.

## Extension behavior

### Class

`FileConverterMenuProvider(GObject.GObject, Nautilus.MenuProvider)`, with `get_file_items(self, files)` as in nautilus-python 4.x (no window argument). `get_background_items` returns an empty list.

### Selection filtering (no socket involved)

An item is offered only if every selected `Nautilus.FileInfo` satisfies all of:

- `get_uri_scheme() == "file"` and `get_location().get_path()` is not `None` (a local path).
- `not is_directory()`.
- `get_mime_type()` is one of `image/png`, `image/jpeg`, `image/webp`.

If any file fails a check, `get_file_items` returns `[]` immediately. This keeps the menu fast for the common non-image case and never opens a socket for it. The MIME list is only a cheap pre-filter; `fcd` remains the authority on what is convertible.

### Building the menu

1. Collect the absolute paths of the selected files.
2. Call `FcClient.targets(paths)` with the short timeouts above.
3. Keep targets with `enabled == true`, in the order returned (the backend's fixed order). If none remain, return `[]`.
4. Create the top-level item `FileConverter::ConvertTo`, label **Convert to**, with a `Nautilus.Menu` submenu. Add one `Nautilus.MenuItem` per target, named `FileConverter::<preset>`, labelled with `target.label`.
5. On any `FcClient` exception, return `[]`, log it, and, for `DaemonUnavailable`, start the background spawn described above.

`get_file_items` runs on Nautilus's main thread each time the context menu opens. The total time it may spend is bounded by the 0.5 s `targets` timeout plus the connection time; there is no retry inside the call.

### Activation

Each submenu item's `activate` handler captures `(paths, preset)` and starts a worker thread. The handler itself does no I/O.

The worker:

1. Calls `FcClient.convert(paths, preset)` and reads events until `job.finished`.
2. Sends a notification built from the final job (see below).
3. On `RequestError` or `DaemonUnavailable` sends an error notification.

No GTK calls happen on the worker thread; notifications are subprocesses, so the worker never touches Nautilus state.

### Notifications

Sent with `notify-send --app-name "File Converter" --icon <name>` through `subprocess.run` with an argument list and a 3 s timeout.

| Final job | Title | Body |
|---|---|---|
| `completed`, all files done | Conversion complete | `3 files converted` (or `photo.png → photo.jpg` for one file) |
| `completed`, some files skipped | Conversion complete | `2 converted, 1 skipped` |
| `completed_with_errors` | Conversion finished with errors | `2 converted, 1 failed: <first error message>` |
| `failed` | Conversion failed | first error message |
| `cancelled` | Conversion cancelled | (empty) |
| daemon unavailable | File Converter is not running | `Start it with: systemctl --user start fcd` |

Error messages come from the backend's `error.message`. File names in notifications are base names only.

## Error handling

| Situation | Behavior |
|---|---|
| `fcd` not running when the menu opens | No item this time; background spawn; item appears on the next right-click. |
| `fcd` hangs or answers slowly | The timeout expires; no item; logged. Nautilus is never blocked more than about 0.5 s. |
| Protocol version mismatch | No item; logged once per Nautilus session. |
| `convert` is rejected (`bad_request`, `not_found`, `unsupported`) | Error notification with the backend message. |
| Daemon dies mid-job | The 60 s silence limit expires, then an error notification. The partial-output rules in the backend spec still hold (no partial files). |
| `notify-send` missing | Skip notification, log a warning; the conversion result is still on disk. |

Logging uses the `logging` module on the `file_converter` logger, written to stderr (Nautilus's stderr goes to the user journal under the session). `FC_DEBUG=1` sets the level to DEBUG.

## Security

- The client sends only absolute paths from `Nautilus.FileInfo` and never constructs a shell command from file names. `notify-send` and `fcd` are started with argument lists.
- The socket directory and socket are already mode `0700` and `0600` (backend spec). The client checks that the socket is owned by the current user before connecting and refuses otherwise.
- `$FC_FCD` is honored only if it is an absolute path to an executable file; it exists for development.
- File names appear in notifications as text; `notify-send` is invoked with `--` before the title so a name starting with `-` cannot be read as an option.

## Testing

Automated:

- `test_client.py`: framing, matching responses by `id`, event handling, each timeout, `protocol` mismatch, and each error-code mapping, against a fake in-process Unix-socket server. One additional test runs against the real `fcd` binary and a generated PNG, and is skipped if `go` or `magick` is unavailable.
- `test_daemon.py`: socket path rules (unset runtime dir, too-long path), `fcd` discovery order, spawn-on-demand with a stub executable, and the 1.5 s give-up.
- `test_extension_menu.py`: injects a stub `Nautilus` module and fake `FileInfo` objects. It checks that directories, non-local files and unsupported MIME types give no items; that a mixed selection gives no items; that disabled targets are dropped; that the order and labels match the response; and that a client exception gives `[]`.

Manual checklist (documented in the plan, run on the real desktop):

1. `install.sh` completes; `systemctl --user status fcd` is active; `nautilus -q` was run.
2. Right-click one PNG: **Convert to** offers JPEG and WebP. Choosing JPEG creates `name.jpg` next to the source and shows a notification.
3. Select a PNG and a JPEG together: the submenu contains only targets valid for both.
4. Right-click a folder, a text file, and a file on a mounted network share: no **Convert to** item.
5. Stop `fcd`, right-click an image: no item the first time, an item the next time.
6. Convert a file that already has a `.jpg` sibling: result is `name (1).jpg`.
7. `uninstall.sh` removes everything the installer added.

## Acceptance criteria

- With `fcd` running, a right-click on selected PNG, JPEG or WebP files shows **Convert to** with only valid, enabled targets, in the backend's order.
- Right-clicking anything else shows no item and opens no socket.
- Choosing a target converts every selected file next to its source, never modifies the source, and shows exactly one notification at the end.
- The context menu is never delayed by more than about half a second, including when `fcd` is stopped or unresponsive.
- Nautilus does not freeze or crash in any failure row of the table above.
- `install.sh` and `uninstall.sh` can each be run twice in a row without errors.
- All automated tests pass without a desktop session, and the real-`fcd` test passes when its tools are installed.

## Open questions to settle during planning

1. Whether to keep an in-extension cache of the last `targets` result (keyed by paths and modification times) to avoid a socket round trip on repeated right-clicks. The spec assumes no cache; measure the real latency first.
2. Whether `install.sh` should build `fcd` itself or accept a prebuilt binary path, for machines without Go.
3. The icon name for notifications (a themed icon such as `image-x-generic`, or a project icon later).
