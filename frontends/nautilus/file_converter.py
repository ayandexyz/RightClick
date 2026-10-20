import logging
import os
import sys

_here = os.path.dirname(__file__)
if _here not in sys.path:
    sys.path.insert(0, _here)

from fc_client.client import FcClient, FcError, DaemonUnavailable, RequestError
from fc_client.daemon import ensure_daemon
from fc_client.notify import send

log = logging.getLogger("file_converter")
if os.environ.get("FC_DEBUG") == "1":
    logging.basicConfig(level=logging.DEBUG)

SUPPORTED_MIME_TYPES = {
    "image/png", "image/jpeg", "image/webp", "image/jxl", "image/avif",
    "image/svg+xml", "image/svg", "image/svg-xml", "image/x-svg+xml",
    "image/vnd.adobe.svg+xml",
    "image/gif", "image/bmp", "image/x-ms-bmp", "image/tiff",
    "image/vnd.microsoft.icon", "image/x-icon", "image/heic", "image/heif",
    "video/mp4", "video/quicktime", "video/webm", "video/x-matroska",
    "video/x-msvideo", "video/mpeg", "video/ogg",
    "audio/mpeg", "audio/mp3", "audio/wav", "audio/x-wav", "audio/flac",
    "audio/aac", "audio/mp4", "audio/x-m4a", "audio/ogg", "audio/opus",
    "audio/aiff", "audio/x-aiff", "audio/ac3", "audio/amr", "audio/x-ms-wma",
    "application/pdf", "application/x-pdf",
}

try:
    if __name__ == "__main__":
        raise ImportError  # helper process: no Nautilus bindings needed
    import gi
    gi.require_version("GObject", "2.0")
    gi.require_version("Nautilus", "4.1")
    from gi.repository import GObject, Nautilus
except ImportError:
    GObject = None
    Nautilus = None


def _path(file_info):
    if file_info.get_uri_scheme() != "file" or file_info.is_directory():
        return None
    location = file_info.get_location()
    path = location.get_path() if location is not None else None
    return path if path and os.path.isabs(path) else None


class FileConverterMenuProvider(*((GObject.GObject, Nautilus.MenuProvider) if Nautilus else (object,))):
    def get_background_items(self, files):
        return []

    def get_file_items(self, files):
        if not files:
            return []
        paths = []
        for file_info in files:
            path = _path(file_info)
            if path is None or file_info.get_mime_type() not in SUPPORTED_MIME_TYPES:
                return []
            paths.append(path)

        client = FcClient(_socket_path())
        try:
            targets = [target for target in client.targets(paths, menu=True) if target.enabled]
        except DaemonUnavailable as exc:
            log.debug("fcd unavailable while building menu: %s", exc)
            _spawn_helper("--warm")
            return []
        except FcError:
            log.exception("failed to build file converter menu")
            return []

        items = []
        if targets:
            submenu = Nautilus.Menu()
            for target in targets:
                item = Nautilus.MenuItem(name=f"FileConverter::{target.preset}", label=target.label)
                item.connect("activate", self._activate, paths, target.preset)
                submenu.append_item(item)
            top = Nautilus.MenuItem(name="FileConverter::ConvertTo", label="Convert to")
            top.set_submenu(submenu)
            items.append(top)

        pdf_tools = self._pdf_tools_menu(client, paths)
        if pdf_tools is not None:
            items.append(pdf_tools)
        return items

    def _pdf_tools_menu(self, client, paths):
        """Build the PDF Tools submenu, or return None when it does not apply.

        Studio operations need a page selection or an angle that the Nautilus
        menu cannot collect, so they are left for the CLI; offering them here
        would fail on activation.
        """
        try:
            operations = client.operations(paths)
        except DaemonUnavailable as exc:
            log.debug("fcd unavailable while listing operations: %s", exc)
            return None
        except RequestError as exc:
            # A daemon older than the operations method answers unknown_method.
            log.debug("daemon does not offer PDF operations: %s", exc)
            return None
        except FcError:
            log.exception("failed to list PDF operations")
            return None

        instant = [op for op in operations if op.enabled and op.mode == "instant"]
        if not instant:
            return None
        submenu = Nautilus.Menu()
        for op in instant:
            item = Nautilus.MenuItem(name=f"FileConverter::pdf::{op.id}", label=op.label)
            item.connect("activate", self._activate_operation, paths, op.id)
            submenu.append_item(item)
        top = Nautilus.MenuItem(name="FileConverter::PDFTools", label="PDF Tools")
        top.set_submenu(submenu)
        return top

    def _activate(self, _item, paths, preset):
        _spawn_helper("--convert", preset, *paths)

    def _activate_operation(self, _item, paths, op):
        _spawn_helper("--operation", op, *paths)

    @staticmethod
    def _convert(paths, preset):
        try:
            final = None
            for event in FcClient(_socket_path()).convert(paths, preset):
                if event.get("event") == "job.finished":
                    final = event.get("data", {})
            _notify_job(final or {})
        except FcError as exc:
            if isinstance(exc, DaemonUnavailable):
                send("File Converter is not running", "Start it with: systemctl --user start fcd")
            else:
                send("Conversion failed", str(exc))

    @staticmethod
    def _run_operation(paths, op):
        try:
            final = None
            for event in FcClient(_socket_path()).run_operation(paths, op):
                if event.get("event") == "job.finished":
                    final = event.get("data", {})
            _notify_operation(final or {}, op)
        except FcError as exc:
            if isinstance(exc, DaemonUnavailable):
                send("File Converter is not running", "Start it with: systemctl --user start fcd")
            else:
                send("PDF operation failed", str(exc))


def _spawn_helper(*args):
    # Python threads inside Nautilus only run while its main loop is busy (they stalled until F5),
    # so the blocking work runs in a separate process. sys.executable is nautilus itself here.
    from gi.repository import Gio, GLib
    try:
        Gio.Subprocess.new(
            ["/usr/bin/env", "python3", os.path.abspath(__file__), *args],
            Gio.SubprocessFlags.STDOUT_SILENCE | Gio.SubprocessFlags.STDERR_SILENCE,
        )
    except GLib.Error:
        log.exception("unable to start file converter helper")


def _socket_path():
    from fc_client.daemon import socket_path
    return socket_path()


def _warm_daemon():
    try:
        ensure_daemon()
    except DaemonUnavailable as exc:
        log.debug("unable to start fcd: %s", exc)


def _notify_job(job):
    state = job.get("state")
    files = job.get("files", [])
    done = [item for item in files if item.get("state") == "done"]
    failed = [item for item in files if item.get("state") == "failed"]
    skipped = [item for item in files if item.get("state") == "skipped"]
    if state == "completed" and not skipped:
        body = f"{len(done)} files converted" if len(done) != 1 else "Conversion complete"
        if len(done) == 1 and done[0].get("outputs"):
            body = f"{os.path.basename(done[0]['source'])} → {os.path.basename(done[0]['outputs'][0])}"
        send("Conversion complete", body)
    elif state == "completed_with_errors":
        message = failed[0].get("error", {}).get("message", "") if failed else ""
        send("Conversion finished with errors", f"{len(done)} converted, {len(failed)} failed" + (f": {message}" if message else ""))
    elif state == "completed":
        send("Conversion complete", f"{len(done)} converted, {len(skipped)} skipped")
    elif state == "cancelled":
        send("Conversion cancelled", "")
    else:
        message = failed[0].get("error", {}).get("message", "Conversion failed") if failed else "Conversion failed"
        send("Conversion failed", message)


def _notify_operation(job, op):
    """Notification text for a PDF operation completion, tailored per outcome."""
    state = job.get("state")
    files = job.get("files", [])
    done = [item for item in files if item.get("state") == "done"]
    failed = [item for item in files if item.get("state") == "failed"]
    skipped = [item for item in files if item.get("state") == "skipped"]
    title = OPERATION_TITLES.get(op, "PDF operation complete")
    if state == "completed" and not failed:
        body = " ".join(_sizes(done)) or f"{len(done)} done, {len(skipped)} skipped"
        if len(done) and len(failed) == 0:
            send(title, body)
        else:
            send(title, f"{len(done)} done, {len(skipped)} skipped")
    elif state == "failed":
        first = failed[0].get("error", {})
        code = first.get("code", "")
        message = first.get("message", "")
        if code == "invalid_pdf":
            send(title, f"PDF could not be read: {message}")
        elif code == "password_required":
            send(title, "PDF is locked — passwords are not supported yet")
        elif message:
            send(f"{title} failed", message)
        else:
            send(f"{title} failed", "Operation failed")
    elif state == "cancelled":
        send(f"{title} cancelled", "")
    else:
        send(title, f"{len(done)} done, {len(skipped)} skipped, {len(failed)} failed")


def _sizes(done):
    """Render the input→output size for completed compress jobs, else return empty body."""
    messages = []
    for item in done:
        source = item.get("source")
        outputs = item.get("outputs") or []
        if not source or not outputs:
            continue
        try:
            before = os.path.getsize(source)
        except OSError:
            continue
        try:
            after = os.path.getsize(outputs[0])
        except OSError:
            continue
        messages.append(f"{os.path.basename(source)}: {_format_size(before)} → {_format_size(after)}")
    return messages


def _format_size(n):
    for unit in ("B", "KB", "MB", "GB"):
        if n < 1024:
            return f"{n:.0f}{unit}" if unit == "B" else f"{n:.1f}{unit}"
        n /= 1024
    return f"{n:.1f}TB"


# Friendly titles for the operation IDs the menu exposes. Falls back to a generic
# "PDF operation complete" inside _notify_operation.
OPERATION_TITLES = {
    "pdf-merge": "PDFs merged",
    "pdf-repair": "PDF repaired",
    "pdf-linearize": "PDF linearized",
    "pdf-flatten": "PDF flattened",
    "pdf-compress-light": "PDF compressed (light)",
    "pdf-compress-balanced": "PDF compressed",
    "pdf-compress-strong": "PDF compressed (strong)",
    "pdf-grayscale": "PDF converted to grayscale",
    "pdf-rotate": "PDF rotated",
    "pdf-rotate-cw": "PDF rotated 90°",
    "pdf-rotate-ccw": "PDF rotated 90°",
    "pdf-rotate-180": "PDF rotated 180°",
    "pdf-reverse": "PDF reversed",
    "pdf-extract": "PDF pages extracted",
    "pdf-delete": "PDF pages deleted",
    "pdf-to-text": "Text extracted",
    "pdf-extract-images": "Images extracted",
    "pdf-ocr": "PDF OCR'd",
    "pdf-from-images": "Images combined into PDF",
}


if __name__ == "__main__":
    if sys.argv[1:2] == ["--warm"]:
        _warm_daemon()
    elif sys.argv[1:2] == ["--convert"] and len(sys.argv) > 3:
        FileConverterMenuProvider._convert(sys.argv[3:], sys.argv[2])
    elif sys.argv[1:2] == ["--operation"] and len(sys.argv) > 3:
        FileConverterMenuProvider._run_operation(sys.argv[3:], sys.argv[2])
