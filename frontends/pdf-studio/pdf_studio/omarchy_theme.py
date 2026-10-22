"""The active Omarchy theme's accent color, read straight from its state file.

PDF Studio runs on any GTK4 + libadwaita desktop, not just Omarchy, and GNOME's
own ``accent-color`` setting only offers a handful of named colors — it has no
way to carry an arbitrary theme's exact hex. So on Omarchy this module reads
the real accent past that limitation; everywhere else it returns ``None`` and
the app keeps libadwaita's own accent.

Deliberately plain Python (no GTK import): importable and testable on a
machine with no display.
"""

import tomllib
from pathlib import Path

THEME_FILE = Path.home() / ".local/state/omarchy/current/theme/colors.toml"


def accent_hex(theme_file=THEME_FILE):
    """The current theme's accent as ``"#rrggbb"``, or None if unreadable."""
    try:
        text = Path(theme_file).read_text()
    except OSError:
        return None
    try:
        accent = tomllib.loads(text).get("accent")
    except tomllib.TOMLDecodeError:
        return None
    if not isinstance(accent, str) or not accent.startswith("#") or len(accent) != 7:
        return None
    try:
        int(accent[1:], 16)
    except ValueError:
        return None
    return accent


def readable_on(accent):
    """"#000000" or "#ffffff", whichever reads better on this accent color."""
    r, g, b = (int(accent[i : i + 2], 16) for i in (1, 3, 5))
    brightness = 0.299 * r + 0.587 * g + 0.114 * b
    return "#000000" if brightness > 150 else "#ffffff"
