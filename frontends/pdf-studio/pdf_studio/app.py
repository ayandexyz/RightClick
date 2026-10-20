"""The application object: one process, one window per invocation.

Launching the Studio again (for example from another Nautilus selection) hands
its files to the running process, which opens another window, so the daemon
connection and toolkit startup are paid once.
"""

import sys

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gio  # noqa: E402

from .cli import PathProblem, check_paths  # noqa: E402
from .merge_view import merge_preview  # noqa: E402
from .split_view import split_preview  # noqa: E402
from .window import StudioWindow  # noqa: E402

APP_ID = "io.fileconverter.PdfStudio"


class StudioApp(Adw.Application):
    def __init__(self):
        super().__init__(application_id=APP_ID, flags=Gio.ApplicationFlags.HANDLES_OPEN)

    @staticmethod
    def split_preview(page_count, mode, value):
        """Expose the headless split preview to the GTK application layer."""
        return split_preview(page_count, mode, value)

    @staticmethod
    def merge_preview(files, file_pages):
        """Expose the headless merge preview to the GTK application layer."""
        return merge_preview(files, file_pages)

    def do_startup(self):
        Adw.Application.do_startup(self)
        quit_action = Gio.SimpleAction.new("quit", None)
        quit_action.connect("activate", lambda *_: self.quit())
        self.add_action(quit_action)
        self.set_accels_for_action("app.quit", ["<Primary>q"])
        self.set_accels_for_action("window.close", ["<Primary>w"])

    def do_activate(self):
        # Started with no files: show the empty window with an Open button.
        StudioWindow(self, [], []).present()

    def do_open(self, files, n_files, hint):
        self.open_paths([file.get_path() or file.get_uri() for file in files])

    def open_paths(self, paths):
        """Open one window for these files (two or more mean the merge view)."""
        local = [path for path in paths if path.startswith("/")]
        remote = [PathProblem(path, "is not a local file") for path in paths if not path.startswith("/")]
        good, problems = check_paths(local)
        # Build the initial headless previews before handing documents to GTK.
        # The loaded window can refresh these with actual page counts later.
        self._split_preview = split_preview(0, "ranges", "1") if len(good) == 1 else None
        self._merge_preview = merge_preview(good, [""] * len(good)) if len(good) >= 2 else None
        StudioWindow(self, good, problems + remote).present()


def run(files):
    # GLib resolves relative names against the caller's directory and forwards
    # them to the running instance, so the raw arguments go straight through.
    return StudioApp().run([sys.argv[0], *files])
