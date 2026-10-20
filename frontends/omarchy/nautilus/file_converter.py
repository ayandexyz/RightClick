import logging
import os
import sys

_here = os.path.dirname(__file__)
if _here not in sys.path:
    sys.path.insert(0, _here)

from fc_client.client import FcClient, FcError, DaemonUnavailable
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

        try:
            targets = [target for target in FcClient(_socket_path()).targets(paths) if target.enabled]
        except DaemonUnavailable as exc:
            log.debug("fcd unavailable while building menu: %s", exc)
            _spawn_helper("--warm")
            return []
        except FcError:
            log.exception("failed to build file converter menu")
            return []
        if not targets:
            return []

        submenu = Nautilus.Menu()
        for target in targets:
            item = Nautilus.MenuItem(name=f"FileConverter::{target.preset}", label=target.label)
            item.connect("activate", self._activate, paths, target.preset)
            submenu.append_item(item)
        top = Nautilus.MenuItem(name="FileConverter::ConvertTo", label="Convert to")
        top.set_submenu(submenu)
        return [top]

    def _activate(self, _item, paths, preset):
        _spawn_helper("--convert", preset, *paths)

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


if __name__ == "__main__":
    if sys.argv[1:2] == ["--warm"]:
        _warm_daemon()
    elif sys.argv[1:2] == ["--convert"] and len(sys.argv) > 3:
        FileConverterMenuProvider._convert(sys.argv[3:], sys.argv[2])
