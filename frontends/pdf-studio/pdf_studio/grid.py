"""PDF page thumbnail grid.

The module keeps GTK optional so the editing model and headless tests can import
it on machines without Py_GObject.  When GTK is present, :class:`PageGrid`
provides a virtualized ``_Gtk.GridView`` backed by a ``_Gio.ListStore``.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from .model import PageItem

try:  # GTK is deliberately an optional import for headless callers.
    import gi

    gi.require_version("Gtk", "4.0")
    from importlib import import_module

    Gio = import_module("gi.repository.Gio")
    GLib = import_module("gi.repository.GLib")
    GObject = import_module("gi.repository.GObject")
    Gtk = import_module("gi.repository.Gtk")
except (ImportError, ValueError):  # pragma: no cover - depends on the host
    Gio = GLib = GObject = Gtk = None

# PyGObject's dynamic modules are intentionally typed as Any.  Keeping the
# aliases here also prevents static analyzers from treating the unavailable
# branch as an object with GTK attributes.
_Gio: Any = Gio
_GLib: Any = GLib
_GObject: Any = GObject
_Gtk: Any = Gtk
GTK_AVAILABLE = _Gtk is not None
_THUMBNAIL_WIDTH = 200
_THUMBNAIL_HEIGHT = 260


@dataclass
class _Row:
    item: PageItem
    position: int
    thumbnail: object = None


if GTK_AVAILABLE:

    class _GtkRow(_GObject.Object):
        def __init__(self, item, position):
            super().__init__()
            self.item = item
            self.position = position
            self.thumbnail = None


    class _GtkPageCell(_Gtk.Box):
        """One page item rendered as a fixed-size, accessible grid cell."""

        def __init__(self):
            super().__init__(orientation=_Gtk.Orientation.VERTICAL, spacing=4)
            self.set_margin_start(6)
            self.set_margin_end(6)
            self.set_margin_top(6)
            self.set_margin_bottom(6)
            self.set_size_request(_THUMBNAIL_WIDTH + 12, _THUMBNAIL_HEIGHT + 54)

            self._frame = _Gtk.Overlay()
            self._frame.set_size_request(_THUMBNAIL_WIDTH, _THUMBNAIL_HEIGHT)
            self._frame.add_css_class("card")
            self.append(self._frame)
            self._picture = _Gtk.Picture(content_fit=_Gtk.ContentFit.CONTAIN)
            self._picture.set_size_request(_THUMBNAIL_WIDTH, _THUMBNAIL_HEIGHT)
            self._picture.set_visible(False)
            self._frame.set_child(self._picture)
            self._placeholder = _Gtk.Spinner()
            self._placeholder.set_size_request(_THUMBNAIL_WIDTH, _THUMBNAIL_HEIGHT)
            self._placeholder.set_halign(_Gtk.Align.CENTER)
            self._placeholder.set_valign(_Gtk.Align.CENTER)
            self._frame.add_overlay(self._placeholder)
            self._blank = _Gtk.Label(label="Blank page")
            self._blank.set_size_request(_THUMBNAIL_WIDTH, _THUMBNAIL_HEIGHT)
            self._blank.set_halign(_Gtk.Align.CENTER)
            self._blank.set_valign(_Gtk.Align.CENTER)
            self._blank.add_css_class("dim-label")
            self._frame.add_overlay(self._blank)

            self._badge = _Gtk.Label()
            self._badge.add_css_class("badge")
            self._badge.set_halign(_Gtk.Align.START)
            self._badge.set_valign(_Gtk.Align.START)
            self._badge.set_margin_start(8)
            self._badge.set_margin_top(8)
            self._frame.add_overlay(self._badge)
            self._rotation = _Gtk.Label()
            self._rotation.add_css_class("badge")
            self._rotation.set_halign(_Gtk.Align.END)
            self._rotation.set_valign(_Gtk.Align.START)
            self._rotation.set_margin_end(8)
            self._rotation.set_margin_top(8)
            self._frame.add_overlay(self._rotation)

            self._caption = _Gtk.Label()
            self._caption.set_ellipsize(3)  # PANGO_ELLIPSIZE_END
            self.append(self._caption)
            self.item = None

        def bind(self, row):
            self.item = row.item
            self._caption.set_label(f"Page {row.position}")
            details = [f"Page {row.position}"]
            if row.item.blank:
                self._blank.set_visible(True)
                self._picture.set_visible(False)
                self._placeholder.set_visible(False)
                self._badge.set_label("Blank")
                self._badge.set_visible(True)
                details.append("blank")
            else:
                self._blank.set_visible(False)
                self._badge.set_label(f"Source {row.item.src}")
                self._badge.set_visible(row.item.src != row.position)
                self._set_thumbnail(row.thumbnail)
                if row.item.src != row.position:
                    details.append(f"source {row.item.src}")
            if row.item.rotate:
                self._rotation.set_label(f"↻ {row.item.rotate}°")
                self._rotation.set_visible(True)
                details.append(f"rotated {row.item.rotate}°")
            else:
                self._rotation.set_visible(False)
            self._caption.set_tooltip_text(", ".join(details))

        def _set_thumbnail(self, thumbnail):
            if thumbnail:
                try:
                    self._picture.set_filename(thumbnail)
                    self._picture.set_visible(True)
                    self._placeholder.set_visible(False)
                    return
                except (_GLib.Error, TypeError):
                    pass
            self._picture.set_visible(False)
            self._placeholder.set_visible(True)
            self._placeholder.start()

        def unbind(self):
            self.item = None
            self._picture.set_visible(False)
            self._placeholder.stop()


    class _GtkPageGrid(_Gtk.Box):
        """A ``_Gtk.GridView`` mirroring an :class:`~pdf_studio.model.EditModel`.

        ``loader_factory`` is called as ``factory(callback, error_callback)``.
        It may return ``None`` for callers that want to supply thumbnails later.
        The callback accepts ``(source_page, result)`` where result is either a
        daemon thumbnail mapping or an object with ``path``.
        """

        def __init__(self, model, loader_factory=None, on_selection_changed=None, width=_THUMBNAIL_WIDTH):
            super().__init__(orientation=_Gtk.Orientation.VERTICAL)
            self.model = model
            self._loader_factory: Any = loader_factory
            self._on_selection_changed = on_selection_changed or (lambda: None)
            self._rows = {}
            self._width = width
            self._loader = None

            self.store = _Gio.ListStore.new(_GtkRow)
            self.selection = _Gtk.MultiSelection.new(self.store)
            self.selection.connect("selection-changed", self._selection_changed)
            factory = _Gtk.SignalListItemFactory()
            factory.connect("setup", self._setup_cell)
            factory.connect("bind", self._bind_cell)
            factory.connect("unbind", self._unbind_cell)
            self.view = _Gtk.GridView(model=self.selection, factory=factory)
            self.view.set_min_columns(1)
            self.view.set_max_columns(12)
            self.view.set_single_click_activate(False)
            self.append(self.view)
            self.refresh()

        def refresh(self):
            self.store.remove_all()
            self._rows.clear()
            for position, item in enumerate(self.model.items, 1):
                row = _GtkRow(item, position)
                self._rows[item.uid] = row
                self.store.append(row)
            for index, item in enumerate(self.model.items):
                if item.uid in self.model.selection:
                    self.selection.select_item(index, False)
            if self._loader:
                self._loader.stop()
            if self._loader_factory:
                self._loader = self._make_loader()
                self._loader.load([item.src for item in self.model.items if not item.blank])

        def _make_loader(self):
            callback = lambda page, result: _GLib.idle_add(self._thumbnail_ready, page, result)
            error = lambda _error, _pages: None
            try:
                return self._loader_factory(callback, error)
            except TypeError:
                return self._loader_factory(callback=callback, error_callback=error)

        def _thumbnail_ready(self, page, result):
            path = result.get("path") if isinstance(result, dict) else getattr(result, "path", None)
            for row in self._rows.values():
                if row.item.src == page:
                    row.thumbnail = path
            self.view.queue_allocate()
            return _GLib.SOURCE_REMOVE

        def _setup_cell(self, _factory, list_item):
            list_item.set_child(PageCell())

        def _bind_cell(self, _factory, list_item):
            list_item.get_child().bind(list_item.get_item())

        def _unbind_cell(self, _factory, list_item):
            list_item.get_child().unbind()

        def _selection_changed(self, selection, _position, _n_items):
            self.model.selection = {
                self.store.get_item(index).item.uid
                for index in range(self.store.get_n_items())
                if selection.is_selected(index)
            }
            self._on_selection_changed()

        def close(self):
            if self._loader:
                self._loader.stop()
                self._loader = None


    PageCell = _GtkPageCell
    PageGrid = _GtkPageGrid

else:

    class PageCell:
        """Import-safe placeholder for environments without GTK."""

        def __init__(self, *args, **kwargs):
            raise RuntimeError("PageCell requires GTK 4")


    class PageGrid:
        """Import-safe placeholder for environments without GTK."""

        def __init__(self, *args, **kwargs):
            raise RuntimeError("PageGrid requires GTK 4")


__all__ = ["GTK_AVAILABLE", "PageCell", "PageGrid"]
