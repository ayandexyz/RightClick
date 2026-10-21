"""Pure geometry and collection model for PDF redaction regions.

The module deliberately has no GTK dependency. Coordinates crossing the daemon
boundary are fractions of the displayed page, with a top-left origin.
"""
from dataclasses import dataclass


@dataclass(frozen=True)
class Region:
    page: int
    x: float
    y: float
    w: float
    h: float

    def as_dict(self):
        return {"page": self.page, "x": self.x, "y": self.y, "w": self.w, "h": self.h}


def drag_to_region(page, start, end, canvas_size, minimum_pixels=3):
    """Convert a pixel drag to a clamped fractional region.

    Raises ValueError for invalid canvas sizes or a drag too small to be an
    intentional redaction. Both drag directions are accepted.
    """
    width, height = canvas_size
    if width <= 0 or height <= 0:
        raise ValueError("canvas size must be positive")
    x0, y0 = start
    x1, y1 = end
    left, right = sorted((x0, x1))
    top, bottom = sorted((y0, y1))
    left = max(0.0, min(float(width), left))
    right = max(0.0, min(float(width), right))
    top = max(0.0, min(float(height), top))
    bottom = max(0.0, min(float(height), bottom))
    if right - left < minimum_pixels or bottom - top < minimum_pixels:
        raise ValueError("redaction region is too small")
    return Region(page, left / width, top / height, (right - left) / width, (bottom - top) / height)


def region_to_pixels(region, canvas_size):
    width, height = canvas_size
    if width <= 0 or height <= 0:
        raise ValueError("canvas size must be positive")
    return (region.x * width, region.y * height,
            (region.x + region.w) * width, (region.y + region.h) * height)


class RegionModel:
    def __init__(self, regions=()):
        self._regions = list(regions)
        self._undo = []

    @property
    def regions(self):
        return tuple(self._regions)

    def _save(self):
        self._undo.append(tuple(self._regions))

    def add(self, region):
        self._save()
        self._regions.append(region)
        return region

    def remove(self, index):
        if not 0 <= index < len(self._regions):
            raise IndexError(index)
        self._save()
        return self._regions.pop(index)

    def move(self, index, region):
        if not 0 <= index < len(self._regions):
            raise IndexError(index)
        self._save()
        self._regions[index] = region

    def undo(self):
        if not self._undo:
            return False
        self._regions = list(self._undo.pop())
        return True

    def by_page(self):
        grouped = {}
        for region in self._regions:
            grouped.setdefault(region.page, []).append(region)
        return grouped

    def summary(self):
        count = len(self._regions)
        if not count:
            return "No redaction regions"
        pages = sorted(self.by_page())
        page_text = ", ".join(str(page) for page in pages)
        noun = "box" if count == 1 else "boxes"
        return f"{count} {noun} on pages {page_text}"
