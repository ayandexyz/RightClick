"""GTK redaction page picker.

The picker keeps user-drawn regions separate from search suggestions.  It is
intentionally a small widget: the daemon remains the authority for geometry
and text matching, while this widget only translates pointer coordinates into
page fractions.
"""

import threading

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Gdk", "4.0")
from gi.repository import Gdk, GLib, Gtk  # noqa: E402

from .regions import Region, RegionModel, drag_to_region, region_to_pixels


class RegionPicker(Gtk.Box):
    def __init__(self, path, page_count, client_factory, loader_factory, password=None, on_changed=None):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        self.path = path
        self.page_count = page_count
        self._client_factory = client_factory
        self._loader_factory = loader_factory
        self._password = password
        self._on_changed = on_changed or (lambda: None)
        self._page = 1
        self._model = RegionModel()
        self._found = []
        self._image_size = (1, 1)
        self._picture = Gtk.Picture(content_fit=Gtk.ContentFit.CONTAIN, vexpand=True)
        self._picture.set_size_request(-1, 420)
        self._drawing = Gtk.DrawingArea()
        self._drawing.set_draw_func(self._draw)
        overlay = Gtk.Overlay()
        overlay.set_child(self._picture)
        overlay.add_overlay(self._drawing)
        self.append(overlay)

        controls = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=6)
        self._previous = Gtk.Button(label="Previous")
        self._previous.connect("clicked", lambda _b: self.set_page(self._page - 1))
        self._next = Gtk.Button(label="Next")
        self._next.connect("clicked", lambda _b: self.set_page(self._page + 1))
        self._page_spin = Gtk.SpinButton.new_with_range(1, max(1, page_count), 1)
        self._page_spin.connect("value-changed", lambda b: self.set_page(b.get_value_as_int()))
        self._page_label = Gtk.Label(xalign=0)
        self._find = Gtk.Button(label="Find terms")
        self._find.connect("clicked", self._find_terms)
        controls.append(self._previous)
        controls.append(self._next)
        controls.append(self._page_spin)
        controls.append(self._page_label)
        controls.append(Gtk.Box(hexpand=True))
        controls.append(self._find)
        self.append(controls)

        self._gesture = Gtk.GestureDrag()
        self._gesture.connect("drag-begin", self._drag_begin)
        self._gesture.connect("drag-end", self._drag_end)
        self._drawing.add_controller(self._gesture)
        self.set_page(1)

    def set_page(self, page):
        page = max(1, min(self.page_count, int(page)))
        self._page = page
        self._page_spin.set_value(page)
        self._page_label.set_label(f"of {self.page_count}")
        self._previous.set_sensitive(page > 1)
        self._next.set_sensitive(page < self.page_count)
        loader = self._loader_factory(lambda p, item: GLib.idle_add(self._thumbnail, p, item), lambda *_: None)
        loader.load([page], visible_pages=[page])
        self._loader = loader
        self._drawing.queue_draw()

    def _thumbnail(self, page, item):
        if page != self._page:
            return GLib.SOURCE_REMOVE
        path = item.get("path") if isinstance(item, dict) else getattr(item, "path", None)
        if path:
            try:
                self._picture.set_paintable(Gdk.Texture.new_from_filename(path))
                self._image_size = (float(item.get("w", 1)), float(item.get("h", 1))) if isinstance(item, dict) else (1, 1)
            except Exception:
                pass
        self._drawing.queue_draw()
        return GLib.SOURCE_REMOVE

    def _drag_begin(self, _gesture, x, y):
        self._drag_start = (x, y)

    def _drag_end(self, _gesture, x, y):
        try:
            allocation = self._drawing.get_allocation()
            region = drag_to_region(self._page, self._drag_start, (x, y), (allocation.width, allocation.height))
        except (AttributeError, ValueError):
            return
        self._model.add(region)
        self._on_changed()
        self._drawing.queue_draw()

    def _draw(self, area, cr, width, height):
        for region in self._model.regions:
            if region.page != self._page:
                continue
            x, y, right, bottom = region_to_pixels(region, (width, height))
            cr.set_source_rgba(0, 0, 0, 0.55)
            cr.rectangle(x, y, right - x, bottom - y)
            cr.fill()
        for region in self._found:
            if region.get("page") != self._page:
                continue
            x = region.get("x", 0) * width
            y = region.get("y", 0) * height
            cr.set_source_rgba(0.9, 0.25, 0.1, 0.9)
            cr.set_line_width(2)
            cr.rectangle(x, y, region.get("w", 0) * width, region.get("h", 0) * height)
            cr.stroke()

    def _find_terms(self, _button):
        # Terms are entered in the adjacent generic terms field.  The form
        # calls find_terms() with the current value when it is available.
        self._on_changed()

    def find_terms(self, terms):
        terms = list(terms or ())
        def work():
            try:
                found = self._client_factory().pdf_find(self.path, terms, password=self._password)
            except Exception:
                found = []
            GLib.idle_add(self._set_found, found)
        threading.Thread(target=work, daemon=True).start()

    def _set_found(self, found):
        self._found = list(found or ())
        self._drawing.queue_draw()
        return GLib.SOURCE_REMOVE

    def get(self):
        return [region.as_dict() for region in self._model.regions]

    def set(self, regions):
        self._model = RegionModel(Region(**region) for region in regions or ())
        self._drawing.queue_draw()

    def summary(self):
        return self._model.summary()
