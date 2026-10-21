"""The Studio window: loads the documents, then shows the editor or why not.

This is the shell. It reads each file's basics from the daemon on a worker
thread (the UI never blocks on the daemon), and shows one of: a spinner, a
status page explaining a problem, or the editor area.
"""

import os
import threading

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib, Gtk  # noqa: E402
from importlib import import_module

_Gio = import_module("gi.repository.Gio")

from .grid import PageGrid  # noqa: E402
from .model import EditModel  # noqa: E402
from .requests import edit_request  # noqa: E402
from .toolbar import ACTION_TABLE, SHORTCUTS, dispatch_action, range_box_state  # noqa: E402
from .thumbnails import ThumbnailLoader  # noqa: E402

from fc_client.client import FcClient  # noqa: E402
from fc_client.daemon import ensure_daemon, socket_path  # noqa: E402

from .apply import build_output_options, friendly_apply_error, run_apply  # noqa: E402
from .loading import load_document  # noqa: E402

_ICONS = {
    "locked": "dialog-password-symbolic",
    "damaged": "dialog-warning-symbolic",
    "daemon": "network-offline-symbolic",
    "failed": "dialog-error-symbolic",
}


def _make_client():
    return FcClient(socket_path())


def _markup(text):
    return GLib.markup_escape_text(text)


class StudioWindow(Adw.ApplicationWindow):
    def __init__(self, app, paths, problems):
        super().__init__(application=app, default_width=1000, default_height=720)
        self._paths = list(paths)
        self._problems = list(problems)
        self._closed = False
        self._load_token = None
        self.connect("close-request", self._on_close_request)

        self._title = Adw.WindowTitle(title="PDF Studio", subtitle="")
        self.set_title("PDF Studio")
        header = Adw.HeaderBar(title_widget=self._title)

        self._stack = Gtk.Stack(transition_type=Gtk.StackTransitionType.CROSSFADE)
        self._stack.add_named(self._build_loading(), "loading")
        self._status = Adw.StatusPage()
        self._action = Gtk.Button(halign=Gtk.Align.CENTER)
        self._action.add_css_class("pill")
        self._action.connect("clicked", self._on_action)
        self._status.set_child(self._action)
        self._stack.add_named(self._status, "status")
        # The ready page holds the editor. The status page above it only speaks
        # for a merge, which has no grid; a single document's editor gets the
        # whole window.
        self._ready = Adw.StatusPage(icon_name="document-edit-symbolic")
        self._editor = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        self._editor.set_vexpand(True)
        self._editor.set_hexpand(True)
        ready_page = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
        ready_page.append(self._ready)
        ready_page.append(self._editor)
        self._stack.add_named(ready_page, "ready")
        self._model = None
        self._grid = None
        self._range_entry = None
        self._status_line = None
        self._apply_button = None
        self._actions = {}
        self._install_actions()
        self._apply_state = None
        self._cancel_event = None

        view = Adw.ToolbarView()
        view.add_top_bar(header)
        view.set_content(self._stack)
        self.set_content(view)

        if self._paths:
            self._start_loading()
        else:
            self._show_empty()
        if self._problems:
            # After the window is up, so the dialog has something to sit on.
            GLib.idle_add(self._show_problems)

    def _set_title(self, title, subtitle=""):
        """Set the header's title and the window's own, which the task bar and
        window switcher show."""
        self._title.set_title(title)
        self._title.set_subtitle(subtitle)
        self.set_title(title)

    # -- pages ---------------------------------------------------------------

    @staticmethod
    def _build_loading():
        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=12, halign=Gtk.Align.CENTER, valign=Gtk.Align.CENTER)
        spinner = Gtk.Spinner(spinning=True, width_request=32, height_request=32)
        label = Gtk.Label(label="Reading the document…")
        label.add_css_class("dim-label")
        box.append(spinner)
        box.append(label)
        return box

    def _show_status(self, icon, title, description, button=None):
        self._status.set_icon_name(icon)
        self._status.set_title(title)
        self._status.set_description(description)
        self._action.set_visible(button is not None)
        if button is not None:
            self._action.set_label(button)
            self._action.remove_css_class("suggested-action")
            self._action.add_css_class("suggested-action")
        self._stack.set_visible_child_name("status")

    def _show_empty(self):
        self._set_title("PDF Studio")
        self._action_mode = "open"
        self._show_status("document-open-symbolic", "Open a PDF", "Choose the PDF whose pages you want to edit.", "Open PDF…")

    # -- loading -------------------------------------------------------------

    def _start_loading(self):
        self._stack.set_visible_child_name("loading")
        token = self._load_token = object()
        paths = list(self._paths)

        def work():
            results = [load_document(path, _make_client, ensure_daemon) for path in paths]
            GLib.idle_add(self._loaded, token, results)

        threading.Thread(target=work, daemon=True).start()

    def _loaded(self, token, results):
        # A stale answer (the window closed, or a retry started) is ignored.
        if self._closed or token is not self._load_token:
            return GLib.SOURCE_REMOVE
        problem = next(((path, result) for path, result in zip(self._paths, results) if result.kind != "ready"), None)
        if problem is not None:
            path, result = problem
            self._action_mode = "retry"
            description = _markup(result.message)
            if result.hint:
                description += "\n\n" + _markup(result.hint)
            self._set_title(os.path.basename(path))
            self._show_status(_ICONS.get(result.kind, "dialog-error-symbolic"), result.title, description, "Try again" if result.retry else None)
            return GLib.SOURCE_REMOVE
        self._show_ready(results)
        return GLib.SOURCE_REMOVE

    def _install_actions(self):
        for action_id in ACTION_TABLE:
            action = _Gio.SimpleAction.new(action_id, None)
            action.connect("activate", lambda _action, _param, name=action_id: self._run_action(name))
            self.add_action(action)
            self._actions[action_id] = action
            shortcut = SHORTCUTS.get(action_id)
            if shortcut:
                self.get_application().set_accels_for_action(f"win.{action_id}", [shortcut])

    def _run_action(self, action_id):
        if self._model is None or not ACTION_TABLE[action_id]["enabled"](self._model):
            return
        dispatch_action(self._model, action_id)
        self._refresh_editor()

    def _refresh_editor(self):
        if self._grid is not None:
            self._grid.refresh()
        for action_id, action in self._actions.items():
            action.set_enabled(self._model is not None and ACTION_TABLE[action_id]["enabled"](self._model))
        if self._range_entry is not None and self._model is not None:
            self._range_entry.set_text(self._model.selection_spec())
        if self._status_line is not None and self._model is not None:
            self._status_line.set_text(f"{len(self._model.items)} pages · {len(self._model.selection)} selected")

    def _show_ready(self, results):
        if len(results) == 1:
            name = os.path.basename(self._paths[0])
            pages = results[0].pages
            self._set_title(name, f"{pages} page{'s' if pages != 1 else ''}")
            self._ready.set_title(f"{pages} page{'s' if pages != 1 else ''}")
            self._ready.set_description(_markup(name))
            self._ready.set_visible(False)
            self._model = EditModel(pages)
            self._build_editor(self._paths[0])
        else:
            self._set_title(f"Merge {len(results)} PDFs")
            self._ready.set_title(f"{len(results)} PDFs")
            lines = [f"{_markup(os.path.basename(path))} — {result.pages} page{'s' if result.pages != 1 else ''}" for path, result in zip(self._paths, results)]
            self._ready.set_description("\n".join(lines))
            self._build_merge_editor(results)
        self._stack.set_visible_child_name("ready")

    def _build_editor(self, path):
        toolbar = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=6)
        toolbar.set_margin_start(8)
        toolbar.set_margin_end(8)
        for action_id in ("undo", "redo", "rotate-left", "rotate-right", "delete", "duplicate", "insert-blank", "move-left", "move-right", "move-start", "move-end"):
            button = Gtk.Button(label=action_id.replace("-", " ").title())
            button.set_action_name(f"win.{action_id}")
            toolbar.append(button)
        # The row is wider than a tiled window, so it scrolls instead of clipping.
        scroller = Gtk.ScrolledWindow(vscrollbar_policy=Gtk.PolicyType.NEVER, hscrollbar_policy=Gtk.PolicyType.AUTOMATIC)
        scroller.set_child(toolbar)
        self._editor.append(scroller)
        row = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=6)
        row.set_margin_start(8)
        self._range_entry = Gtk.Entry(placeholder_text="Pages, e.g. 1-3,7")
        self._range_entry.connect("activate", self._range_changed)
        row.append(Gtk.Label(label="Selection:"))
        row.append(self._range_entry)
        self._status_line = Gtk.Label(xalign=0)
        row.append(self._status_line)
        self._apply_button = Gtk.Button(label="Apply", css_classes=["suggested-action"])
        self._apply_button.connect("clicked", self._on_apply)
        row.append(self._apply_button)
        self._editor.append(row)
        model = self._model
        if model is None:
            return
        page_count = model.page_count
        self._grid = PageGrid(model, loader_factory=lambda callback, error: ThumbnailLoader(_make_client, path, page_count, callback=callback, error_callback=error), on_selection_changed=self._refresh_editor)
        self._grid.set_vexpand(True)
        self._editor.append(self._grid)
        self._refresh_editor()

    def _build_merge_editor(self, results):
        self._apply_button = Gtk.Button(label="Apply", css_classes=["suggested-action"])
        self._apply_button.connect("clicked", self._on_apply)
        self._editor.append(self._apply_button)

    def _range_changed(self, entry):
        if self._model is None:
            return
        state = range_box_state(self._model, entry.get_text())
        if not state.valid:
            entry.add_css_class("error")
        else:
            entry.remove_css_class("error")
            self._refresh_editor()

    # -- actions -------------------------------------------------------------

    def _on_action(self, _button):
        if getattr(self, "_action_mode", "") == "open":
            self._choose_files()
        else:
            self._start_loading()

    def _choose_files(self):
        pdfs = Gtk.FileFilter()
        pdfs.set_name("PDF documents")
        pdfs.add_mime_type("application/pdf")
        dialog = Gtk.FileDialog(title="Open PDF", default_filter=pdfs)
        dialog.open_multiple(self, None, self._on_files_chosen)

    def _on_files_chosen(self, dialog, result):
        try:
            chosen = dialog.open_multiple_finish(result)
        except GLib.Error:
            return  # cancelled
        paths = [chosen.get_item(i).get_path() for i in range(chosen.get_n_items())]
        paths = [path for path in paths if path]
        if paths:
            self.get_application().open_paths(paths)
            self.close()

    def _on_apply(self, _button):
        if not self._paths:
            return
        if self._apply_button is None:
            return
        self._apply_button.set_sensitive(False)
        self._cancel_event = threading.Event()
        if self._model is not None:
            op, params = edit_request(self._model)
        else:
            op, params = "pdf-merge", {}
        request = {"files": list(self._paths), "op": op, "params": params}
        request.update(build_output_options(os.path.dirname(self._paths[0]), "rename"))

        def report(state):
            self._apply_state = state
            GLib.idle_add(self._apply_state_changed, state)

        def work():
            run_apply(_make_client, request, report, self._cancel_event)

        threading.Thread(target=work, daemon=True).start()

    def _apply_state_changed(self, state):
        if self._model is not None:
            return self._single_apply_state_changed(state)
        if state.status == "completed":
            self._ready.set_title("Apply complete")
        elif state.status == "completed-with-errors":
            self._ready.set_title("Apply completed with errors")
        elif state.status == "failed":
            self._ready.set_title("Apply failed")
            self._ready.set_description(_markup(state.error or "The operation failed"))
        elif state.status == "cancelled":
            self._ready.set_title("Apply cancelled")
        else:
            self._ready.set_description(f"Applying… {round(state.progress * 100)}%")
        if state.status in ("completed", "completed-with-errors", "failed", "cancelled") and self._apply_button is not None:
            self._apply_button.set_sensitive(True)
        return GLib.SOURCE_REMOVE

    def _single_apply_state_changed(self, state):
        """Report an apply in the editor's status line, which stays on screen."""
        done = ("completed", "completed-with-errors", "failed", "cancelled")
        message = {
            "completed": "Saved",
            "completed-with-errors": "Saved with errors",
            "failed": state.error or "The operation failed",
            "cancelled": "Cancelled",
        }.get(state.status, f"Applying… {round(state.progress * 100)}%")
        if self._status_line is not None:
            self._status_line.set_text(message)
        if state.status in done and self._apply_button is not None:
            self._apply_button.set_sensitive(True)
        return GLib.SOURCE_REMOVE

    def _show_problems(self):
        lines = [f"{os.path.basename(problem.path) or problem.path} {problem.reason}" for problem in self._problems]
        dialog = Adw.AlertDialog.new("Some files could not be opened" if self._paths else "That file could not be opened", "\n".join(lines))
        dialog.add_response("ok", "OK")
        dialog.present(self)
        return GLib.SOURCE_REMOVE

    def _on_close_request(self, _window):
        self._closed = True
        return False
