"""The GTK panels around the page grid: apply bar, split view, merge view.

Each panel only gathers input and shows state. What a request means, and
whether it is valid, is decided by ``requests`` and the daemon.
"""

import os

import gi

gi.require_version("Gtk", "4.0")
from gi.repository import Gio, GLib, Gtk  # noqa: E402

from .output_panel import CONFLICT_CHOICES, build_output_options  # noqa: E402
from .pagespec import format_pages  # noqa: E402
from .requests import RequestError, merge_request, split_parts, split_request  # noqa: E402

_DONE = ("completed", "completed-with-errors", "failed", "cancelled")


def _margins(widget, side=8, vertical=6):
    widget.set_margin_start(side)
    widget.set_margin_end(side)
    widget.set_margin_top(vertical)
    widget.set_margin_bottom(vertical)


class ApplyBar(Gtk.Box):
    """Output folder, conflict policy, progress, Cancel and Apply."""

    def __init__(self, on_apply, on_cancel):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=4)
        _margins(self)
        self._folder = ""
        self._on_apply, self._on_cancel = on_apply, on_cancel
        self._message = Gtk.Label(xalign=0, wrap=True)
        self._message.add_css_class("dim-label")
        self.append(self._message)
        self._progress = Gtk.ProgressBar(visible=False)
        self.append(self._progress)

        row = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=6)
        row.append(Gtk.Label(label="Save to"))
        self._folder_button = Gtk.Button()
        self._folder_button.connect("clicked", self._choose_folder)
        row.append(self._folder_button)
        self._conflict = Gtk.DropDown.new_from_strings([label for label, _ in CONFLICT_CHOICES])
        self._conflict.set_tooltip_text("If a file with that name already exists")
        row.append(self._conflict)
        spacer = Gtk.Box(hexpand=True)
        row.append(spacer)
        self._outputs = []
        self._reveal = Gtk.Button(label="Show in Files", visible=False)
        self._reveal.connect("clicked", lambda _b: self._show_in_files())
        row.append(self._reveal)
        self._cancel = Gtk.Button(label="Cancel", visible=False)
        self._cancel.connect("clicked", lambda _b: self._on_cancel())
        row.append(self._cancel)
        self._apply = Gtk.Button(label="Apply", css_classes=["suggested-action"])
        self._apply.connect("clicked", lambda _b: self._on_apply())
        row.append(self._apply)
        self.append(row)

    # -- input ---------------------------------------------------------------

    def set_folder(self, folder):
        self._folder = folder
        self._folder_button.set_label(os.path.basename(folder.rstrip("/")) or folder)
        self._folder_button.set_tooltip_text(folder)

    def output_options(self):
        _, conflict = CONFLICT_CHOICES[self._conflict.get_selected()]
        return build_output_options(self._folder, conflict)

    def _choose_folder(self, _button):
        dialog = Gtk.FileDialog(title="Save to folder")
        if self._folder:
            from importlib import import_module
            dialog.set_initial_folder(import_module("gi.repository.Gio").File.new_for_path(self._folder))
        dialog.select_folder(self.get_root(), None, self._folder_chosen)

    def _folder_chosen(self, dialog, result):
        try:
            chosen = dialog.select_folder_finish(result)
        except GLib.Error:
            return  # cancelled
        if chosen is not None and chosen.get_path():
            self.set_folder(chosen.get_path())

    # -- output --------------------------------------------------------------

    def set_message(self, text):
        self._message.set_text(text)

    def _show_in_files(self):
        """Open the file manager with the first output selected."""
        if not self._outputs:
            return
        # Gtk.FileLauncher is GTK 4.10+, under the 4.12 minimum.
        Gtk.FileLauncher.new(Gio.File.new_for_path(self._outputs[0])).open_containing_folder(self.get_root(), None, None, None)

    def set_applying(self, applying):
        if applying:
            self._outputs = []
            self._reveal.set_visible(False)
        self._apply.set_sensitive(not applying)
        self._cancel.set_visible(applying)
        self._cancel.set_sensitive(True)
        self._progress.set_visible(applying)
        if applying:
            self._progress.set_fraction(0.0)

    def show_state(self, state):
        """Reflect a job state; returns True once the job is over."""
        done = state.status in _DONE
        if not done:
            self._progress.set_fraction(state.progress)
            self._message.set_text(f"Applying… {round(state.progress * 100)}%")
            return False
        self.set_applying(False)
        outputs = [path for item in state.files for path in item.get("outputs", ())]
        self._outputs = outputs
        self._reveal.set_visible(bool(outputs))
        self._message.set_text({
            "completed": "Saved " + (", ".join(os.path.basename(p) for p in outputs[:3]) + ("…" if len(outputs) > 3 else "")),
            "completed-with-errors": "Saved with errors",
            "failed": state.error or "The operation failed",
            "cancelled": "Cancelled",
        }[state.status])
        return True

    def cancelling(self):
        self._cancel.set_sensitive(False)
        self._message.set_text("Cancelling…")


class SplitPanel(Gtk.Box):
    """Split by page ranges or every N pages, with a live preview of the files."""

    def __init__(self, page_count, on_change=None):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        _margins(self, 12, 12)
        self._page_count = page_count
        self._on_change = on_change or (lambda: None)
        self._modes = ("ranges", "every")
        self._mode = Gtk.DropDown.new_from_strings(["Page ranges", "Every N pages"])
        self._mode.connect("notify::selected", self._mode_changed)
        self.append(self._mode)
        self._entry = Gtk.Entry()
        self._entry.connect("changed", lambda _e: self._update())
        self.append(self._entry)
        self._preview = Gtk.Label(xalign=0, yalign=0, wrap=True, selectable=True)
        scroller = Gtk.ScrolledWindow(vexpand=True)
        scroller.set_child(self._preview)
        self.append(scroller)
        self._mode_changed()

    def _mode_changed(self, *_args):
        ranges = self._modes[self._mode.get_selected()] == "ranges"
        self._entry.set_placeholder_text("1-3;4-6;7-" if ranges else "Pages per file, e.g. 5")
        self._entry.set_text("")
        self._update()

    def _value(self):
        mode = self._modes[self._mode.get_selected()]
        text = self._entry.get_text()
        if mode == "every":
            text = text.strip()
            return mode, int(text) if text.isdigit() else text
        return mode, text

    def preview_text(self):
        """The preview, or the reason there is none. Also what Apply validates against."""
        mode, value = self._value()
        if value == "":
            return None, "Choose how to split this document"
        try:
            parts = split_parts(mode, value, self._page_count)
        except RequestError as error:
            return None, str(error)
        return parts, "\n".join(f"File {i}: pages {format_pages(part)}" for i, part in enumerate(parts, 1))

    def _update(self):
        parts, text = self.preview_text()
        self._preview.set_text(text)
        # An empty box is not an error yet, only an invitation.
        if parts is None and self._value()[1] != "":
            self._entry.add_css_class("error")
        else:
            self._entry.remove_css_class("error")
        self._on_change()

    def request(self):
        mode, value = self._value()
        return split_request(mode, value, self._page_count)


class MergePanel(Gtk.Box):
    """One row per file, in merge order, each with an optional page range.

    The arrows reorder the files; the order shown is the order merged.
    """

    def __init__(self, paths, page_counts):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        _margins(self, 12, 12)
        # One record per file; the entry keeps its text when the row moves.
        self._rows = []
        for path, count in zip(paths, page_counts):
            entry = Gtk.Entry(placeholder_text="All pages", width_chars=14)
            entry.connect("changed", lambda _e: self._validate())
            self._rows.append({"path": path, "count": count, "entry": entry})
        self._list = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        self.append(self._list)
        self._error = Gtk.Label(xalign=0, wrap=True)
        self._error.add_css_class("error")
        self.append(self._error)
        self._rebuild()

    def _rebuild(self):
        child = self._list.get_first_child()
        while child is not None:
            following = child.get_next_sibling()
            self._list.remove(child)
            child = following
        last = len(self._rows) - 1
        for index, record in enumerate(self._rows):
            row = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=8)
            up = Gtk.Button(icon_name="go-up-symbolic", tooltip_text="Merge earlier", sensitive=index > 0)
            up.connect("clicked", lambda _b, i=index: self.move(i, -1))
            down = Gtk.Button(icon_name="go-down-symbolic", tooltip_text="Merge later", sensitive=index < last)
            down.connect("clicked", lambda _b, i=index: self.move(i, 1))
            row.append(up)
            row.append(down)
            name = Gtk.Label(label=f"{index + 1}. {os.path.basename(record['path'])}", xalign=0, hexpand=True, ellipsize=3)
            name.set_tooltip_text(record["path"])
            row.append(name)
            count = record["count"]
            row.append(Gtk.Label(label=f"{count} page{'s' if count != 1 else ''}", css_classes=["dim-label"]))
            entry = record["entry"]
            if entry.get_parent() is not None:
                entry.unparent()
            row.append(entry)
            self._list.append(row)

    def move(self, index, step):
        target = index + step
        if not 0 <= target < len(self._rows):
            return
        self._rows[index], self._rows[target] = self._rows[target], self._rows[index]
        self._rebuild()
        self._validate()

    def paths(self):
        return [record["path"] for record in self._rows]

    def specs(self):
        return [record["entry"].get_text() for record in self._rows]

    def _validate(self):
        try:
            self.request()
        except RequestError as error:
            self._error.set_text(str(error))
            return
        self._error.set_text("")

    def request(self):
        names = [os.path.basename(path) for path in self.paths()]
        return merge_request(names, self.specs(), [record["count"] for record in self._rows])
