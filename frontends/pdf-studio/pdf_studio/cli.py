"""Command line, environment checks, and the path rules for opening files.

Everything here is plain Python: nothing imports GTK until ``main`` has checked
that a usable version is installed, so the checks run (and are tested) on a
machine with no display and a missing toolkit.
"""

import argparse
import os
import sys
from dataclasses import dataclass

MIN_GTK = (4, 12)
MIN_ADW = (1, 5)

_INSTALL_HINT = (
    "Install gtk4, libadwaita and python-gobject (Arch), "
    "or gir1.2-gtk-4.0, gir1.2-adw-1 and python3-gi (Debian/Ubuntu)."
)


def _dotted(version):
    return f"{version[0]}.{version[1]}"


def check_versions(gtk, adw):
    """Why the toolkit cannot run the Studio, as one line, or None if it can.

    ``gtk`` and ``adw`` are ``(major, minor)`` tuples, or None when that
    library could not be loaded.
    """
    found = (
        f"GTK {_dotted(gtk) if gtk else 'not installed'}, "
        f"libadwaita {_dotted(adw) if adw else 'not installed'}"
    )
    if gtk and adw and gtk >= MIN_GTK and adw >= MIN_ADW:
        return None
    return (
        f"PDF Studio needs GTK {_dotted(MIN_GTK)} or newer and libadwaita {_dotted(MIN_ADW)} or newer "
        f"(found {found}). {_INSTALL_HINT}"
    )


def parse_args(argv):
    parser = argparse.ArgumentParser(
        prog="fc-pdf-studio",
        description="Edit the pages of a PDF: reorder, rotate, delete, split and merge.",
    )
    parser.add_argument("files", nargs="*", help="PDF files to open; two or more open the merge view")
    return parser.parse_args(argv)


@dataclass(frozen=True)
class PathProblem:
    path: str
    reason: str


def check_paths(paths, cwd=None):
    """Split command-line paths into usable PDFs and reportable problems.

    Paths are made absolute (the daemon requires it) and a file is judged a PDF
    by its header, not its name, the same test the daemon applies. A file named
    twice is opened once. Returns ``(good, problems)``, each in the order given.
    """
    base = cwd if cwd is not None else os.getcwd()
    good, problems, seen = [], [], set()
    for raw in paths:
        path = os.path.normpath(os.path.join(base, os.path.expanduser(raw)))
        if path in seen:
            continue
        seen.add(path)
        reason = _problem_with(path)
        if reason:
            problems.append(PathProblem(path, reason))
        else:
            good.append(path)
    return good, problems


def _problem_with(path):
    if not os.path.exists(path):
        return "does not exist"
    if os.path.isdir(path):
        return "is a folder, not a PDF"
    try:
        with open(path, "rb") as file:
            header = file.read(5)
    except OSError:
        return "could not be read"
    if header != b"%PDF-":
        return "is not a PDF"
    return None


def toolkit_versions():
    """The installed ``(gtk, adw)`` versions, each None if unavailable."""
    gtk = adw = None
    try:
        import gi

        gi.require_version("Gtk", "4.0")
        from gi.repository import Gtk

        gtk = (Gtk.get_major_version(), Gtk.get_minor_version())
    except (ImportError, ValueError):
        pass
    try:
        import gi

        gi.require_version("Adw", "1")
        from gi.repository import Adw

        adw = (Adw.get_major_version(), Adw.get_minor_version())
    except (ImportError, ValueError):
        pass
    return gtk, adw


def main(argv=None):
    args = parse_args(sys.argv[1:] if argv is None else argv)
    problem = check_versions(*toolkit_versions())
    if problem:
        print(problem, file=sys.stderr)
        return 1
    from .app import run

    return run(args.files)
