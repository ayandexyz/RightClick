"""Drives the real GTK window with the daemon faked out.

These need a display, and skip cleanly without one (CI, SSH). The windows are
never presented, so running them does not flash anything on screen.
"""

import os
import sys
import time
import unittest
import warnings
from pathlib import Path

# PyGObject itself trips a deprecation in asyncio on Python 3.14; not ours to fix here.
warnings.filterwarnings("ignore", category=DeprecationWarning, module=r"gi(\.|$)")

sys.path.insert(0, str(Path(__file__).parents[1]))
sys.path.insert(0, str(Path(__file__).parents[2] / "common"))


def _toolkit():
    if not (os.environ.get("WAYLAND_DISPLAY") or os.environ.get("DISPLAY")):
        return None
    try:
        import gi

        gi.require_version("Gtk", "4.0")
        gi.require_version("Adw", "1")
        from gi.repository import Adw, GLib, Gtk

        if not Gtk.init_check():
            return None
        Adw.init()
        return Adw, GLib, Gtk
    except (ImportError, ValueError):
        return None


TOOLKIT = _toolkit()


@unittest.skipUnless(TOOLKIT, "needs a display and GTK 4 with libadwaita")
class WindowSmokeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        from pdf_studio import window as window_module
        from pdf_studio.app import StudioApp
        from pdf_studio.cli import PathProblem
        from pdf_studio.loading import LoadResult

        cls.window_module, cls.LoadResult, cls.PathProblem = window_module, LoadResult, PathProblem
        cls.app = StudioApp()
        cls.app.register()

    def setUp(self):
        self._real = self.window_module.load_document
        self.calls = []
        self.addCleanup(setattr, self.window_module, "load_document", self._real)

    def fake(self, *results):
        queue = list(results)

        def load(path, make_client, ensure_daemon):
            self.calls.append(path)
            return queue.pop(0) if len(queue) > 1 else queue[0]

        self.window_module.load_document = load

    def window(self, paths, problems=()):
        window = self.window_module.StudioWindow(self.app, paths, list(problems))
        self.addCleanup(window.destroy)
        return window

    def pump(self, until, seconds=5.0):
        _, GLib, _ = TOOLKIT
        context = GLib.MainContext.default()
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            while context.pending():
                context.iteration(False)
            if until():
                return True
            time.sleep(0.01)
        return False

    def page(self, window):
        return window._stack.get_visible_child_name()

    def ready(self, pages=12):
        return self.LoadResult("ready", pages=pages)

    def test_a_readable_document_shows_its_name_and_page_count(self):
        self.fake(self.ready(12))
        window = self.window(["/tmp/Quarterly report.pdf"])
        self.assertEqual(self.page(window), "loading")
        self.assertTrue(self.pump(lambda: self.page(window) == "ready"))
        self.assertEqual(window._title.get_title(), "Quarterly report.pdf")
        self.assertEqual(window._title.get_subtitle(), "12 pages")
        # the window's own title is what a task bar and window switcher show
        self.assertEqual(window.get_title(), "Quarterly report.pdf")

    def test_a_single_page_is_not_pluralised(self):
        self.fake(self.ready(1))
        window = self.window(["/tmp/one.pdf"])
        self.assertTrue(self.pump(lambda: self.page(window) == "ready"))
        self.assertEqual(window._title.get_subtitle(), "1 page")

    def test_several_files_make_a_merge_window_listing_each(self):
        self.fake(self.ready(2), self.ready(12))
        window = self.window(["/tmp/a.pdf", "/tmp/b.pdf"])
        self.assertTrue(self.pump(lambda: self.page(window) == "ready"))
        self.assertEqual(window._title.get_title(), "Merge 2 PDFs")
        self.assertEqual(window.get_title(), "Merge 2 PDFs")
        description = window._ready.get_description()
        self.assertIn("a.pdf — 2 pages", description)
        self.assertIn("b.pdf — 12 pages", description)

    def test_a_locked_file_shows_an_explanation_and_no_retry_button(self):
        self.fake(self.LoadResult("locked", title="This PDF is password-protected", message="x.pdf is locked."))
        window = self.window(["/tmp/x.pdf"])
        self.assertTrue(self.pump(lambda: self.page(window) == "status"))
        self.assertEqual(window._status.get_title(), "This PDF is password-protected")
        self.assertFalse(window._action.get_visible())

    def test_try_again_reloads_and_recovers(self):
        down = self.LoadResult("daemon", title="File Converter is not running", message="no answer", hint="Start it with: systemctl --user start fcd", retry=True)
        self.fake(down, self.ready(3))
        window = self.window(["/tmp/a.pdf"])
        self.assertTrue(self.pump(lambda: self.page(window) == "status"))
        self.assertTrue(window._action.get_visible())
        self.assertEqual(window._action.get_label(), "Try again")
        self.assertIn("systemctl --user start fcd", window._status.get_description())
        window._action.emit("clicked")
        self.assertEqual(self.page(window), "loading")
        self.assertTrue(self.pump(lambda: self.page(window) == "ready"))
        self.assertEqual(len(self.calls), 2)

    def test_a_problem_with_any_one_file_is_what_the_window_shows(self):
        bad = self.LoadResult("damaged", title="This PDF can't be read", message="b.pdf looks damaged.")
        self.fake(self.ready(2), bad)
        window = self.window(["/tmp/a.pdf", "/tmp/b.pdf"])
        self.assertTrue(self.pump(lambda: self.page(window) == "status"))
        self.assertEqual(window._status.get_title(), "This PDF can't be read")
        self.assertEqual(window._title.get_title(), "b.pdf")
        self.assertEqual(window.get_title(), "b.pdf")

    def test_markup_characters_in_a_file_name_are_shown_literally(self):
        self.fake(self.ready(2))
        window = self.window(["/tmp/Q&A <draft>.pdf"])
        self.assertTrue(self.pump(lambda: self.page(window) == "ready"))
        self.assertIn("&amp;", window._ready.get_description())
        self.assertIn("&lt;draft&gt;", window._ready.get_description())

    def test_markup_characters_in_an_error_message_are_shown_literally_too(self):
        hint = "Start it with: systemctl --user start fcd & check <status>"
        self.fake(self.LoadResult("daemon", title="x", message="Q&A <draft>.pdf did not load.", hint=hint, retry=True))
        window = self.window(["/tmp/Q&A <draft>.pdf"])
        self.assertTrue(self.pump(lambda: self.page(window) == "status"))
        shown = window._status.get_description()
        self.assertIn("Q&amp;A &lt;draft&gt;.pdf", shown)
        self.assertIn("&amp; check &lt;status&gt;", shown)

    def test_no_files_shows_the_open_prompt_without_calling_the_daemon(self):
        self.fake(self.ready(1))
        window = self.window([])
        self.assertEqual(self.page(window), "status")
        self.assertEqual(window._status.get_title(), "Open a PDF")
        self.assertEqual(window.get_title(), "PDF Studio")
        self.assertEqual(window._action.get_label(), "Open PDF…")
        self.pump(lambda: False, seconds=0.2)
        self.assertEqual(self.calls, [])

    def test_closing_before_the_load_finishes_is_harmless(self):
        self.fake(self.ready(5))
        window = self.window(["/tmp/a.pdf"])
        window.close()
        self.pump(lambda: len(self.calls) > 0)
        self.pump(lambda: False, seconds=0.3)  # let the worker's answer arrive and be ignored

    def test_a_stale_answer_is_ignored_after_a_retry_started(self):
        window = self.window(["/tmp/a.pdf"])
        stale = window._load_token
        window._start_loading()
        self.assertIsNot(window._load_token, stale)
        window._loaded(stale, [self.ready(99)])
        self.assertEqual(self.page(window), "loading")


@unittest.skipUnless(TOOLKIT, "needs a display and GTK 4 with libadwaita")
class PageGridTests(unittest.TestCase):
    def grid(self):
        from pdf_studio.grid import PageGrid
        from pdf_studio.model import EditModel

        class Loader:
            def __init__(self):
                self.requested = []

            def load(self, pages, visible_pages=()):
                self.requested.append(list(pages))

            def stop(self):
                pass

        self.loader = Loader()
        self.model = EditModel(4)
        changes = []
        return PageGrid(self.model, loader_factory=lambda cb, err: self.loader, on_selection_changed=lambda: changes.append(1))

    def test_a_refresh_keeps_the_selection(self):
        grid = self.grid()
        self.model.select_spec("2-3")
        grid.refresh()
        self.assertEqual(self.model.positions(), [2, 3])

    def test_one_loader_serves_every_refresh(self):
        made = []
        grid = self.grid()
        grid._loader_factory = lambda cb, err: made.append(1) or self.loader
        grid._loader = None
        grid.refresh()
        grid.refresh()
        self.assertEqual(len(made), 1)

    def test_a_thumbnail_that_arrives_late_reaches_a_cell_already_on_screen(self):
        from pdf_studio.grid import PageCell

        grid = self.grid()
        cell = PageCell()
        cell.bind(grid._rows[self.model.items[1].uid])
        grid._cells.add(cell)
        self.assertFalse(cell._picture.get_visible())
        path = str(Path(__file__).parent / "thumb.png")
        Path(path).write_bytes(_PNG)
        self.addCleanup(os.unlink, path)
        grid._thumbnail_ready(2, {"page": 2, "path": path})
        self.assertTrue(cell._picture.get_visible())
        # and a later refresh remembers it, so a moved page is not re-requested blank
        grid.refresh()
        self.assertEqual(grid._rows[self.model.items[1].uid].thumbnail, path)


_PNG = bytes.fromhex(
    "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c489"
    "0000000d49444154789c6360000002000001e221bc330000000049454e44ae426082"
)


if __name__ == "__main__":
    unittest.main()
