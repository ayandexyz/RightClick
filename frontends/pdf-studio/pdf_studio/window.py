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
        self._ready = Adw.StatusPage(icon_name="document-edit-symbolic")
        self._apply_button = Gtk.Button(label="Apply", halign=Gtk.Align.CENTER)
        self._apply_button.add_css_class("suggested-action")
        self._apply_button.connect("clicked", self._on_apply)
        self._ready.set_child(self._apply_button)
        self._stack.add_named(self._ready, "ready")
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

    def _show_ready(self, results):
        if len(results) == 1:
            name = os.path.basename(self._paths[0])
            pages = results[0].pages
            self._set_title(name, f"{pages} page{'s' if pages != 1 else ''}")
            self._ready.set_title(f"{pages} page{'s' if pages != 1 else ''}")
            self._ready.set_description(_markup(name))
        else:
            self._set_title(f"Merge {len(results)} PDFs")
            self._ready.set_title(f"{len(results)} PDFs")
            lines = [f"{_markup(os.path.basename(path))} — {result.pages} page{'s' if result.pages != 1 else ''}" for path, result in zip(self._paths, results)]
            self._ready.set_description("\n".join(lines))
        self._stack.set_visible_child_name("ready")

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
        self._apply_button.set_sensitive(False)
        self._cancel_event = threading.Event()
        request = {
            "files": list(self._paths),
            "op": "pdf-merge" if len(self._paths) > 1 else "pdf-edit",
            "params": {},
        }
        request.update(build_output_options(os.path.dirname(self._paths[0]), "rename"))

        def report(state):
            self._apply_state = state
            GLib.idle_add(self._apply_state_changed, state)

        def work():
            run_apply(_make_client, request, report, self._cancel_event)

        threading.Thread(target=work, daemon=True).start()

    def _apply_state_changed(self, state):
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
        if state.status in ("completed", "completed-with-errors", "failed", "cancelled"):
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
