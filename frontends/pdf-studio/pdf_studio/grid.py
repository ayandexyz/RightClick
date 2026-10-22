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

    Gdk = import_module("gi.repository.Gdk")
    Gio = import_module("gi.repository.Gio")
    GLib = import_module("gi.repository.GLib")
    GObject = import_module("gi.repository.GObject")
    Gtk = import_module("gi.repository.Gtk")
except (ImportError, ValueError):  # pragma: no cover - depends on the host
    Gdk = Gio = GLib = GObject = Gtk = None

# PyGObject's dynamic modules are intentionally typed as Any.  Keeping the
# aliases here also prevents static analyzers from treating the unavailable
# branch as an object with GTK attributes.
_Gdk: Any = Gdk
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
            self.row = None
            self.item = None

        def bind(self, row):
            self.row = row
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
            self.row = None
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

        def __init__(self, model, loader_factory=None, on_selection_changed=None, on_reorder=None, width=_THUMBNAIL_WIDTH):
            super().__init__(orientation=_Gtk.Orientation.VERTICAL)
            self.model = model
            self._loader_factory: Any = loader_factory
            self._on_selection_changed = on_selection_changed or (lambda: None)
            # Called as on_reorder(dragged_uid, target_uid, after) when a page is
            # dropped on another; the owner decides what that does to the model.
            self._on_reorder = on_reorder or (lambda _dragged, _target, _after: None)
            self._rows = {}
            self._width = width
            self._loader = None
            # Thumbnails by source page outlive a refresh: an edit moves pages
            # around but never changes what a source page looks like.
            self._thumbs = {}
            self._cells = set()
            self._refreshing = False

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
            self.view.set_enable_rubberband(True)
            scroller = _Gtk.ScrolledWindow(hscrollbar_policy=_Gtk.PolicyType.NEVER)
            scroller.set_vexpand(True)
            scroller.set_child(self.view)
            self.append(scroller)
            self.refresh()

        def refresh(self):
            # Rebuilding the store makes the selection model report an empty
            # selection; that must not overwrite the model's own.
            self._refreshing = True
            try:
                self.store.remove_all()
                self._rows.clear()
                for position, item in enumerate(self.model.items, 1):
                    row = _GtkRow(item, position)
                    row.thumbnail = self._thumbs.get(item.src) if not item.blank else None
                    self._rows[item.uid] = row
                    self.store.append(row)
                for index, item in enumerate(self.model.items):
                    if item.uid in self.model.selection:
                        self.selection.select_item(index, False)
            finally:
                self._refreshing = False
            if self._loader_factory:
                if self._loader is None:
                    self._loader = self._make_loader()
                if self._loader is not None:
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
            if not path:
                return _GLib.SOURCE_REMOVE
            self._thumbs[page] = path
            for row in self._rows.values():
                if row.item.src == page:
                    row.thumbnail = path
            # A cell already on screen keeps what it was bound with, so bind it again.
            for cell in list(self._cells):
                if cell.row is not None and cell.row.item.src == page:
                    cell.bind(cell.row)
            return _GLib.SOURCE_REMOVE

        def _setup_cell(self, _factory, list_item):
            cell = PageCell()
            list_item.set_child(cell)
            source = _Gtk.DragSource(actions=_Gdk.DragAction.MOVE)
            source.connect("prepare", self._drag_prepare, cell)
            source.connect("drag-begin", self._drag_begin, cell)
            cell.add_controller(source)
            target = _Gtk.DropTarget.new(_GObject.TYPE_STRING, _Gdk.DragAction.MOVE)
            target.connect("drop", self._drop, cell)
            cell.add_controller(target)

        @staticmethod
        def _drag_payload(cell):
            """What a drag from this cell carries: the page's uid as text."""
            return None if cell.row is None else str(cell.row.item.uid)

        def _drag_prepare(self, _source, _x, _y, cell):
            payload = self._drag_payload(cell)
            return None if payload is None else _Gdk.ContentProvider.new_for_value(payload)

        @staticmethod
        def _drag_begin(source, _drag, cell):
            source.set_icon(_Gtk.WidgetPaintable.new(cell), 0, 0)

        def _drop(self, _target, value, x, _y, cell):
            if cell.row is None:
                return False
            try:
                dragged = int(value)
            except (TypeError, ValueError):
                return False
            # The right half of a cell is "after" it, so the very end is reachable.
            self._on_reorder(dragged, cell.row.item.uid, x > cell.get_width() / 2)
            return True

        def _bind_cell(self, _factory, list_item):
            cell = list_item.get_child()
            cell.bind(list_item.get_item())
            self._cells.add(cell)

        def _unbind_cell(self, _factory, list_item):
            cell = list_item.get_child()
            self._cells.discard(cell)
            cell.unbind()

        def _selection_changed(self, selection, _position, _n_items):
            if self._refreshing:
                return
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


    _keep_css_installed = False

    def _install_keep_css():
        """A translucent accent tint for kept pages, echoing the Edit tab's
        selected-page highlight (which comes from GridView's own ":selected"
        styling that this NoSelection grid doesn't get for free)."""
        global _keep_css_installed
        if _keep_css_installed:
            return
        provider = _Gtk.CssProvider()
        provider.load_from_string(
            ".keep-page-kept { background-color: alpha(@accent_bg_color, 0.35); border-radius: 12px; }"
        )
        _Gtk.StyleContext.add_provider_for_display(
            _Gdk.Display.get_default(), provider, _Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION
        )
        _keep_css_installed = True


    class _KeepRow(_GObject.Object):
        def __init__(self, position):
            super().__init__()
            self.position = position
            self.thumbnail = None
            self.kept = True


    class _KeepPageCell(_Gtk.Box):
        """A thumbnail with a "keep this page" checkbox at its top-left corner."""

        def __init__(self):
            super().__init__(orientation=_Gtk.Orientation.VERTICAL, spacing=4)
            _install_keep_css()
            self.set_margin_start(6)
            self.set_margin_end(6)
            self.set_margin_top(6)
            self.set_margin_bottom(6)
            self.set_size_request(_THUMBNAIL_WIDTH + 12, _THUMBNAIL_HEIGHT + 40)

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

            self._check = _Gtk.CheckButton()
            self._check.set_halign(_Gtk.Align.START)
            self._check.set_valign(_Gtk.Align.START)
            self._check.set_margin_start(6)
            self._check.set_margin_top(6)
            self._check.add_css_class("osd")
            self._check.set_tooltip_text("Keep this page")
            # The checkbox is a display-only indicator; the click gesture below
            # toggles it from anywhere on the thumbnail, the corner included.
            self._check.set_can_target(False)
            self._toggle_handler = self._check.connect("toggled", self._on_toggled)
            self._frame.add_overlay(self._check)

            click = _Gtk.GestureClick.new()
            click.connect("released", self._on_frame_clicked)
            self._frame.add_controller(click)

            self._caption = _Gtk.Label()
            self.append(self._caption)
            self.row = None
            self._on_toggle = None

        def bind(self, row, on_toggle):
            self.row = row
            self._on_toggle = on_toggle
            self._caption.set_label(f"Page {row.position}")
            with self._check.handler_block(self._toggle_handler):
                self._check.set_active(row.kept)
            self._update_kept_style(row.kept)
            self._set_thumbnail(row.thumbnail)

        def _on_frame_clicked(self, _gesture, _n_press, _x, _y):
            self._check.set_active(not self._check.get_active())

        def _update_kept_style(self, kept):
            if kept:
                self.add_css_class("keep-page-kept")
            else:
                self.remove_css_class("keep-page-kept")

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

        def _on_toggled(self, _check):
            if self.row is None:
                return
            self.row.kept = self._check.get_active()
            self._update_kept_style(self.row.kept)
            if self._on_toggle:
                self._on_toggle()

        def unbind(self):
            self.row = None
            self._on_toggle = None
            self._picture.set_visible(False)
            self._placeholder.stop()


    class _GtkKeepPagesGrid(_Gtk.Box):
        """A grid of page thumbnails, each with a keep/discard checkbox.

        Every page starts ticked (kept); unticking excludes it.
        ``kept_positions()`` returns the 1-based positions still ticked, in
        page order.
        """

        def __init__(self, page_count, loader_factory=None, on_change=None):
            super().__init__(orientation=_Gtk.Orientation.VERTICAL)
            self._page_count = page_count
            self._loader_factory: Any = loader_factory
            self._on_change = on_change or (lambda: None)
            self._loader = None
            self._thumbs = {}
            self._cells = set()

            self.store = _Gio.ListStore.new(_KeepRow)
            for position in range(1, page_count + 1):
                self.store.append(_KeepRow(position))
            no_selection = _Gtk.NoSelection.new(self.store)
            factory = _Gtk.SignalListItemFactory()
            factory.connect("setup", self._setup_cell)
            factory.connect("bind", self._bind_cell)
            factory.connect("unbind", self._unbind_cell)
            self.view = _Gtk.GridView(model=no_selection, factory=factory)
            self.view.set_min_columns(1)
            self.view.set_max_columns(12)
            scroller = _Gtk.ScrolledWindow(hscrollbar_policy=_Gtk.PolicyType.NEVER)
            scroller.set_vexpand(True)
            scroller.set_child(self.view)
            self.append(scroller)

        def ensure_loaded(self):
            """Start fetching thumbnails, the first time this is shown.

            Thumbnails are not requested until the grid is actually shown, so
            switching to "Keep pages" does not double the daemon's thumbnail
            work when the Edit tab's own grid already loaded them.
            """
            if self._loader is not None or not self._loader_factory:
                return
            self._loader = self._make_loader()
            self._loader.load(list(range(1, self._page_count + 1)))

        def _make_loader(self):
            callback = lambda page, result: _GLib.idle_add(self._thumbnail_ready, page, result)
            error = lambda _error, _pages: None
            try:
                return self._loader_factory(callback, error)
            except TypeError:
                return self._loader_factory(callback=callback, error_callback=error)

        def _thumbnail_ready(self, page, result):
            path = result.get("path") if isinstance(result, dict) else getattr(result, "path", None)
            if not path:
                return _GLib.SOURCE_REMOVE
            self._thumbs[page] = path
            for row in self._rows():
                if row.position == page:
                    row.thumbnail = path
            for cell in list(self._cells):
                if cell.row is not None and cell.row.position == page:
                    cell.bind(cell.row, self._on_change)
            return _GLib.SOURCE_REMOVE

        def _rows(self):
            for index in range(self.store.get_n_items()):
                yield self.store.get_item(index)

        def _setup_cell(self, _factory, list_item):
            list_item.set_child(_KeepPageCell())

        def _bind_cell(self, _factory, list_item):
            cell = list_item.get_child()
            row = list_item.get_item()
            row.thumbnail = self._thumbs.get(row.position)
            cell.bind(row, self._on_change)
            self._cells.add(cell)

        def _unbind_cell(self, _factory, list_item):
            cell = list_item.get_child()
            self._cells.discard(cell)
            cell.unbind()

        def kept_positions(self):
            return [row.position for row in self._rows() if row.kept]

        def set_all(self, kept):
            for row in self._rows():
                row.kept = kept
            for cell in list(self._cells):
                if cell.row is not None:
                    with cell._check.handler_block(cell._toggle_handler):
                        cell._check.set_active(cell.row.kept)
                    cell._update_kept_style(cell.row.kept)
            self._on_change()

        def close(self):
            if self._loader:
                self._loader.stop()
                self._loader = None


    KeepPageCell = _KeepPageCell
    KeepPagesGrid = _GtkKeepPagesGrid

else:

    class PageCell:
        """Import-safe placeholder for environments without GTK."""

        def __init__(self, *args, **kwargs):
            raise RuntimeError("PageCell requires GTK 4")


    class PageGrid:
        """Import-safe placeholder for environments without GTK."""

        def __init__(self, *args, **kwargs):
            raise RuntimeError("PageGrid requires GTK 4")


    class KeepPageCell:
        """Import-safe placeholder for environments without GTK."""

        def __init__(self, *args, **kwargs):
            raise RuntimeError("KeepPageCell requires GTK 4")


    class KeepPagesGrid:
        """Import-safe placeholder for environments without GTK."""

        def __init__(self, *args, **kwargs):
            raise RuntimeError("KeepPagesGrid requires GTK 4")


__all__ = ["GTK_AVAILABLE", "PageCell", "PageGrid", "KeepPageCell", "KeepPagesGrid"]
