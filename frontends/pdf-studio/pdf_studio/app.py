"""The application object: one process, one window per invocation.

Launching the Studio again (for example from another Nautilus selection) hands
its files to the running process, which opens another window, so the daemon
connection and toolkit startup are paid once.
"""

import sys

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gdk, Gio, Gtk  # noqa: E402

from .cli import PathProblem, check_paths  # noqa: E402
from .omarchy_theme import accent_hex, readable_on  # noqa: E402
from .window import StudioWindow  # noqa: E402

APP_ID = "io.fileconverter.PdfStudio"


class StudioApp(Adw.Application):
    def __init__(self):
        super().__init__(application_id=APP_ID, flags=Gio.ApplicationFlags.HANDLES_OPEN)

    def do_startup(self):
        Adw.Application.do_startup(self)
        quit_action = Gio.SimpleAction.new("quit", None)
        quit_action.connect("activate", lambda *_: self.quit())
        self.add_action(quit_action)
        self.set_accels_for_action("app.quit", ["<Primary>q"])
        self.set_accels_for_action("window.close", ["<Primary>w"])
        self._apply_omarchy_accent()

    def _apply_omarchy_accent(self):
        """Retint libadwaita's accent (the Apply button included) to match
        the running Omarchy theme, since GNOME's own accent-color setting
        only has a handful of named colors and can't carry a theme's exact
        hex (see omarchy_theme.py)."""
        accent = accent_hex()
        if accent is None:
            return
        provider = Gtk.CssProvider()
        provider.load_from_string(
            "@define-color accent_color {0}; "
            "@define-color accent_bg_color {0}; "
            "@define-color accent_fg_color {1};".format(accent, readable_on(accent))
        )
        Gtk.StyleContext.add_provider_for_display(
            Gdk.Display.get_default(), provider, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION
        )

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
        StudioWindow(self, good, problems + remote).present()


def run(files):
    # GLib resolves relative names against the caller's directory and forwards
    # them to the running instance, so the raw arguments go straight through.
    return StudioApp().run([sys.argv[0], *files])
