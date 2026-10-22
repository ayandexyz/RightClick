import logging
import subprocess
from urllib.parse import quote

log = logging.getLogger("file_converter")


def send(title, body, icon="image-x-generic"):
    try:
        subprocess.run(
            ["notify-send", "--app-name", "File Converter", "--icon", icon, "--", title, body],
            check=False,
            timeout=3,
        )
    except (FileNotFoundError, OSError, subprocess.TimeoutExpired):
        log.warning("unable to send desktop notification", exc_info=True)


def reveal(paths):
    """Ask the file manager to show and select the given paths.

    A file written by this helper process lands in the directory via a plain
    filesystem syscall, outside any Nautilus-initiated operation, so its own
    directory monitor can take several seconds to notice it on its own
    (sometimes longer than a user waits before hitting F5). The
    freedesktop.org FileManager1 D-Bus interface is implemented by Nautilus
    (and other file managers) specifically to reveal a file immediately,
    so calling it here closes that gap instead of waiting on the monitor.
    """
    if not paths:
        return
    try:
        uris = ",".join(f"'file://{quote(path)}'" for path in paths)
        subprocess.run(
            [
                "gdbus", "call", "--session",
                "--dest", "org.freedesktop.FileManager1",
                "--object-path", "/org/freedesktop/FileManager1",
                "--method", "org.freedesktop.FileManager1.ShowItems",
                f"[{uris}]", "",
            ],
            check=False,
            timeout=3,
        )
    except (FileNotFoundError, OSError, subprocess.TimeoutExpired):
        log.warning("unable to reveal converted file in the file manager", exc_info=True)
