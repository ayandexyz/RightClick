import logging
import subprocess

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
