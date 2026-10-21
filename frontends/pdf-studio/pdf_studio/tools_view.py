"""The Tools tab: a form drawn from a ``tools.Tool`` description.

Nothing here knows what a watermark or a crop is. A tool says which fields it
has and how to turn them into a request; this module draws one widget per field
kind, keeps what the user typed when they switch tools, and shows a field's
error beneath it.
"""

import os

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Gdk", "4.0")
from gi.repository import Gdk, Gtk  # noqa: E402

from .regions import Region, RegionModel  # noqa: E402
from .tools import RequestError  # noqa: E402


class FieldRow(Gtk.Box):
    """One labelled field: its widget, a help line and an error line."""

    def __init__(self, field, selection_spec):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=4)
        self.field = field
        self._selection = selection_spec
        self._path = ""
        if field.kind != "switch":
            title = Gtk.Label(label=field.label + (" *" if field.required else ""), xalign=0)
            title.add_css_class("heading")
            self.append(title)
        self._build_widget()
        if field.help:
            hint = Gtk.Label(label=field.help, xalign=0, wrap=True)
            hint.add_css_class("dim-label")
            hint.add_css_class("caption")
            self.append(hint)
        self._error = Gtk.Label(xalign=0, wrap=True, visible=False)
        self._error.add_css_class("error")
        self.append(self._error)

    # -- building ------------------------------------------------------------

    def _build_widget(self):
        kind = self.field.kind
        f = self.field
        if kind == "text":
            self._widget = Gtk.Entry(hexpand=True)
        elif kind == "password":
            self._widget = Gtk.PasswordEntry(show_peek_icon=True, hexpand=True)
        elif kind == "number":
            low = f.minimum if f.minimum is not None else -1e9
            high = f.maximum if f.maximum is not None else 1e9
            self._widget = Gtk.SpinButton.new_with_range(low, high, 1 if f.integer else 0.5)
            self._widget.set_digits(0 if f.integer else 2)
            self._widget.set_numeric(True)
        elif kind == "choice":
            self._widget = Gtk.DropDown.new_from_strings([label for _, label in f.choices])
        elif kind == "colour":
            self._widget = Gtk.ColorDialogButton(dialog=Gtk.ColorDialog(), halign=Gtk.Align.START)
        elif kind == "file":
            self._widget = Gtk.Button(label="Choose…", halign=Gtk.Align.START)
            self._widget.connect("clicked", self._choose_file)
        elif kind == "regions":
            # The boxes themselves are drawn on the page canvas, which hands
            # them over through set(); this row keeps the list and says what
            # it holds.
            self._regions = []
            box = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=6)
            self._summary = Gtk.Label(xalign=0, hexpand=True)
            clear = Gtk.Button(label="Clear")
            clear.connect("clicked", lambda _b: self.set([]))
            box.append(self._summary)
            box.append(clear)
            self._widget = box
            self._show_regions()
        elif kind == "terms":
            self._entry = Gtk.TextView(wrap_mode=Gtk.WrapMode.WORD_CHAR, hexpand=True)
            self._entry.set_size_request(-1, 80)
            self._widget = Gtk.ScrolledWindow(min_content_height=80)
            self._widget.set_child(self._entry)
        elif kind == "pages":
            box = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=6)
            self._entry = Gtk.Entry(hexpand=True, placeholder_text="All pages")
            use = Gtk.Button(label="Use selected pages")
            use.connect("clicked", lambda _b: self._entry.set_text(self._selection()))
            box.append(self._entry)
            box.append(use)
            self._widget = box
        elif kind == "switch":
            self._widget = Gtk.CheckButton(label=f.label)
        elif kind == "flags":
            self._widget = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=2)
            self._checks = {}
            for value, label in f.choices:
                check = Gtk.CheckButton(label=label)
                self._checks[value] = check
                self._widget.append(check)
        else:
            raise ValueError(f"unknown field kind {kind!r}")
        self.append(self._widget)

    def _choose_file(self, button):
        images = Gtk.FileFilter()
        images.set_name("PNG and JPEG images")
        images.add_mime_type("image/png")
        images.add_mime_type("image/jpeg")
        dialog = Gtk.FileDialog(title=self.field.label, default_filter=images)

        def done(dialog, result):
            try:
                chosen = dialog.open_finish(result)
            except Exception:  # cancelled
                return
            if chosen is not None and chosen.get_path():
                self.set(chosen.get_path())

        dialog.open(button.get_root(), None, done)

    # -- value ---------------------------------------------------------------

    def get(self):
        kind = self.field.kind
        widget = self._widget
        if kind == "regions":
            return [dict(region) for region in self._regions]
        if kind in ("text", "password"):
            return widget.get_text()
        if kind == "terms":
            buffer = self._entry.get_buffer()
            return buffer.get_text(buffer.get_start_iter(), buffer.get_end_iter(), False).splitlines()
        if kind == "number":
            return widget.get_value()
        if kind == "choice":
            return self.field.choices[widget.get_selected()][0]
        if kind == "colour":
            rgba = widget.get_rgba()
            return "#%02x%02x%02x" % tuple(round(max(0.0, min(1.0, channel)) * 255) for channel in (rgba.red, rgba.green, rgba.blue))
        if kind == "file":
            return self._path
        if kind == "pages":
            return self._entry.get_text()
        if kind == "switch":
            return widget.get_active()
        return [value for value, check in self._checks.items() if check.get_active()]

    def set(self, value):
        kind = self.field.kind
        widget = self._widget
        if kind == "regions":
            self._regions = [dict(region) for region in value or ()]
            self._show_regions()
        elif kind in ("text", "password"):
            widget.set_text(value or "")
        elif kind == "terms":
            self._entry.get_buffer().set_text("\n".join(value or ()))
        elif kind == "number":
            widget.set_value(float(value if value not in (None, "") else 0))
        elif kind == "choice":
            values = [v for v, _ in self.field.choices]
            if value in values:
                widget.set_selected(values.index(value))
        elif kind == "colour":
            rgba = Gdk.RGBA()
            if rgba.parse(value or "#000000"):
                widget.set_rgba(rgba)
        elif kind == "file":
            self._path = value or ""
            widget.set_label(os.path.basename(self._path) or "Choose…")
            widget.set_tooltip_text(self._path or None)
        elif kind == "pages":
            self._entry.set_text(value or "")
        elif kind == "switch":
            widget.set_active(bool(value))
        else:
            chosen = set(value or ())
            for name, check in self._checks.items():
                check.set_active(name in chosen)

    def _show_regions(self):
        model = RegionModel(Region(r["page"], r["x"], r["y"], r["w"], r["h"]) for r in self._regions)
        self._summary.set_label(model.summary())

    def show_error(self, message):
        self._error.set_label(message)
        self._error.set_visible(bool(message))


class ToolsView(Gtk.Box):
    """A tool picker above the form for the chosen tool."""

    def __init__(self, selection_spec=None):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        self._selection = selection_spec or (lambda: "")
        self._tools = []
        self._state = {}  # tool id -> the values last entered
        self._context = {}
        self.rows = {}
        self._shown = None  # id of the tool whose form is on screen
        self._loading = False
        self._picker = Gtk.DropDown.new_from_strings([])
        self._picker.set_margin_start(8)
        self._picker.set_margin_end(8)
        self._picker.connect("notify::selected", self._tool_chosen)
        self.append(self._picker)
        self._summary = Gtk.Label(xalign=0, wrap=True)
        self._summary.add_css_class("dim-label")
        self._summary.set_margin_start(8)
        self._summary.set_margin_end(8)
        self.append(self._summary)
        self._form = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=12)
        for side in ("start", "end", "top", "bottom"):
            getattr(self._form, f"set_margin_{side}")(8)
        scroller = Gtk.ScrolledWindow(vexpand=True, hscrollbar_policy=Gtk.PolicyType.NEVER)
        scroller.set_child(self._form)
        self.append(scroller)

    # -- tools ---------------------------------------------------------------

    def set_tools(self, tools, context=None):
        """Offer ``tools``, keeping the chosen tool and what was typed if they remain."""
        keep = self.current_tool().id if self.current_tool() else None
        self._save()
        self._context = dict(context or self._context)
        self._tools = list(tools)
        self._loading = True
        self._picker.set_model(Gtk.StringList.new([f"{tool.group}: {tool.label}" for tool in self._tools]))
        index = next((i for i, tool in enumerate(self._tools) if tool.id == keep), 0)
        self._picker.set_selected(index)
        self._loading = False
        self._show(self._tools[index] if self._tools else None)

    def current_tool(self):
        index = self._picker.get_selected()
        if 0 <= index < len(self._tools):
            return self._tools[index]
        return None

    def _tool_chosen(self, *_args):
        if self._loading:
            return
        self._save()
        self._show(self.current_tool())

    def _save(self):
        if self.rows and self._shown is not None:
            # Passwords are not kept across tool switches: they stay in the
            # widget they were typed into and nowhere else.
            self._state[self._shown] = {key: value for key, value in self.values().items() if self.rows[key].field.kind != "password"}

    def _show(self, tool):
        child = self._form.get_first_child()
        while child is not None:
            following = child.get_next_sibling()
            self._form.remove(child)
            child = following
        self.rows = {}
        self._shown = tool.id if tool else None
        self._summary.set_label(tool.summary if tool else "")
        if tool is None:
            return
        values = {**tool.defaults(self._context), **self._state.get(tool.id, {})}
        for f in tool.fields:
            row = FieldRow(f, self._selection)
            row.set(values.get(f.key, f.default))
            self.rows[f.key] = row
            self._form.append(row)

    # -- values --------------------------------------------------------------

    def values(self):
        return {key: row.get() for key, row in self.rows.items()}

    def request(self, context=None):
        """The ``(op, params)`` for the form as filled in, or RequestError.

        The error is also shown under the field it names.
        """
        tool = self.current_tool()
        if tool is None:
            raise RequestError("", "There is no tool to apply")
        for row in self.rows.values():
            row.show_error("")
        self._save()
        try:
            return tool.build(self.values(), {**self._context, **(context or {})})
        except RequestError as error:
            row = self.rows.get(error.key)
            if row is not None:
                row.show_error(error.message)
            raise

    def forget(self):
        """Drop what was typed, passwords included. Called when the window closes."""
        self._state.clear()
        for row in self.rows.values():
            if row.field.kind == "password":
                row.set("")
