"""The Studio's editing model: what the page grid shows, as plain data.

The model is an ordered list of page items. Each item is a source page and a
clockwise rotation, or a blank page (``src == 0``). Deleting, duplicating,
reordering, rotating and inserting blanks are all edits of that list; Apply
sends the finished list to the daemon as one ``pdf-edit`` job. Nothing here
imports GTK, so every rule is unit-tested without a display.

Selection is by item identity (``uid``), not by position, so a selection
survives moves, and a duplicated page is a distinct item from its original.
Selecting is not an edit and is never undoable; every other action either
changes the list and can be undone, or is refused with a reason and changes
nothing.
"""

from dataclasses import dataclass

from .pagespec import PageSpecError, format_pages, parse_pages

MAX_ITEMS = 1000
HISTORY_LIMIT = 100


@dataclass(frozen=True)
class PageItem:
    uid: int
    src: int  # 1-based source page; 0 is a blank page
    rotate: int = 0  # clockwise degrees applied on top of the source page's rotation

    @property
    def blank(self):
        return self.src == 0


@dataclass(frozen=True)
class Outcome:
    """What an action did. ``message`` explains a refusal and is safe to show."""

    changed: bool
    message: str = ""


_DONE = Outcome(True)


def _refused(message):
    return Outcome(False, message)


class EditModel:
    def __init__(self, page_count, max_items=MAX_ITEMS):
        if page_count < 1:
            raise ValueError("a document has at least one page")
        self.page_count = page_count
        self.max_items = max_items
        self._next_uid = 1
        self.items = [self._new_item(page) for page in range(1, page_count + 1)]
        self.selection = set()
        self._undo = []
        self._redo = []

    # -- identity and state ------------------------------------------------

    def _new_item(self, src, rotate=0):
        item = PageItem(self._next_uid, src, rotate)
        self._next_uid += 1
        return item

    def is_identity(self):
        """True when applying the list would reproduce the original document."""
        return len(self.items) == self.page_count and all(
            item.src == index + 1 and item.rotate == 0 for index, item in enumerate(self.items)
        )

    @property
    def dirty(self):
        return not self.is_identity()

    @property
    def edit_count(self):
        return len(self._undo)

    @property
    def can_undo(self):
        return bool(self._undo)

    @property
    def can_redo(self):
        return bool(self._redo)

    # -- selection (never an undo step) --------------------------------------

    def positions(self):
        """1-based positions of the selected items, ascending."""
        return [index + 1 for index, item in enumerate(self.items) if item.uid in self.selection]

    def _select_where(self, keep):
        self.selection = {item.uid for index, item in enumerate(self.items) if keep(index)}

    def select_all(self):
        self._select_where(lambda index: True)

    def select_none(self):
        self.selection = set()

    def select_odd(self):
        self._select_where(lambda index: index % 2 == 0)

    def select_even(self):
        self._select_where(lambda index: index % 2 == 1)

    def invert_selection(self):
        self._select_where(lambda index: self.items[index].uid not in self.selection)

    def selection_spec(self):
        return format_pages(self.positions())

    def select_spec(self, text):
        """Select the positions a spec names; blank text clears the selection.

        A bad spec leaves the selection untouched and says why, so a half-typed
        range never throws away what the user had selected.
        """
        if not text.strip():
            changed = bool(self.selection)
            self.selection = set()
            return Outcome(changed)
        try:
            positions = parse_pages(text, len(self.items))
        except PageSpecError as error:
            return _refused(str(error))
        chosen = {self.items[position - 1].uid for position in positions}
        changed = chosen != self.selection
        self.selection = chosen
        return Outcome(changed)

    # -- history -------------------------------------------------------------

    def _snapshot(self):
        return (tuple(self.items), frozenset(self.selection))

    def _restore(self, snapshot):
        items, selection = snapshot
        self.items = list(items)
        self.selection = set(selection)

    def _apply(self, items, selection):
        """Make an edit: record the previous state and install the new one.

        An edit that leaves the list as it was is not an edit, so it adds no
        history and reports that nothing changed.
        """
        if list(items) == self.items:
            return _refused("Nothing to change")
        self._undo.append(self._snapshot())
        del self._undo[:-HISTORY_LIMIT]
        self._redo.clear()
        self.items = list(items)
        self.selection = set(selection)
        return _DONE

    def undo(self):
        if not self._undo:
            return _refused("Nothing to undo")
        self._redo.append(self._snapshot())
        self._restore(self._undo.pop())
        return _DONE

    def redo(self):
        if not self._redo:
            return _refused("Nothing to redo")
        self._undo.append(self._snapshot())
        self._restore(self._redo.pop())
        return _DONE

    # -- edits ---------------------------------------------------------------

    def _need_selection(self):
        return None if self.selection else _refused("Select one or more pages first")

    def _check_room(self, extra):
        if len(self.items) + extra > self.max_items:
            return _refused(f"A document can have at most {self.max_items} pages")
        return None

    def rotate(self, degrees):
        """Turn the selected pages clockwise by a multiple of 90 degrees."""
        if (refusal := self._need_selection()):
            return refusal
        if degrees % 90 != 0:
            return _refused("Pages turn in quarter turns")
        turn = degrees % 360
        if turn == 0:
            return _refused("Nothing to change")
        items = [
            PageItem(item.uid, item.src, (item.rotate + turn) % 360) if item.uid in self.selection else item
            for item in self.items
        ]
        return self._apply(items, self.selection)

    def delete(self):
        if (refusal := self._need_selection()):
            return refusal
        remaining = [item for item in self.items if item.uid not in self.selection]
        if not remaining:
            return _refused("A document needs at least one page")
        if all(item.blank for item in remaining):
            return _refused("At least one original page must remain")
        return self._apply(remaining, set())

    def duplicate(self):
        """Copy each selected page directly after itself and select the copies."""
        if (refusal := self._need_selection()):
            return refusal
        if (refusal := self._check_room(len(self.selection))):
            return refusal
        items, copies = [], set()
        for item in self.items:
            items.append(item)
            if item.uid in self.selection:
                copy = self._new_item(item.src, item.rotate)
                items.append(copy)
                copies.add(copy.uid)
        return self._apply(items, copies)

    def insert_blank_after(self):
        if (refusal := self._need_selection()):
            return refusal
        if (refusal := self._check_room(len(self.selection))):
            return refusal
        items, blanks = [], set()
        for item in self.items:
            items.append(item)
            if item.uid in self.selection:
                blank = self._new_item(0)
                items.append(blank)
                blanks.add(blank.uid)
        return self._apply(items, blanks)

    def insert_blank_at(self, index):
        """Insert a blank page so it lands at ``index`` (0 is the very start)."""
        if not 0 <= index <= len(self.items):
            return _refused("There is no such place to insert a page")
        if (refusal := self._check_room(1)):
            return refusal
        blank = self._new_item(0)
        items = self.items[:index] + [blank] + self.items[index:]
        return self._apply(items, {blank.uid})

    def _move(self, items):
        return self._apply(items, self.selection)

    def move_left(self):
        if (refusal := self._need_selection()):
            return refusal
        items = list(self.items)
        for index in range(1, len(items)):
            if items[index].uid in self.selection and items[index - 1].uid not in self.selection:
                items[index - 1], items[index] = items[index], items[index - 1]
        return self._move(items)

    def move_right(self):
        if (refusal := self._need_selection()):
            return refusal
        items = list(self.items)
        for index in range(len(items) - 2, -1, -1):
            if items[index].uid in self.selection and items[index + 1].uid not in self.selection:
                items[index], items[index + 1] = items[index + 1], items[index]
        return self._move(items)

    def _split_selection(self):
        chosen = [item for item in self.items if item.uid in self.selection]
        rest = [item for item in self.items if item.uid not in self.selection]
        return chosen, rest

    def move_to_start(self):
        if (refusal := self._need_selection()):
            return refusal
        chosen, rest = self._split_selection()
        return self._move(chosen + rest)

    def move_to_end(self):
        if (refusal := self._need_selection()):
            return refusal
        chosen, rest = self._split_selection()
        return self._move(rest + chosen)

    def move_to(self, index):
        """Move the selection, as a block in its current order, so it starts at
        ``index`` among the pages that are not moving."""
        if (refusal := self._need_selection()):
            return refusal
        chosen, rest = self._split_selection()
        if not 0 <= index <= len(rest):
            return _refused("There is no such place to move the pages")
        return self._move(rest[:index] + chosen + rest[index:])

    def reverse(self):
        """Reverse the selected pages within the slots they occupy, or the whole
        document when nothing is selected."""
        if len(self.selection) == 1:
            return _refused("Select at least two pages to reverse them")
        if not self.selection and len(self.items) < 2:
            return _refused("Nothing to change")
        slots = [index for index, item in enumerate(self.items) if not self.selection or item.uid in self.selection]
        reordered = [self.items[index] for index in reversed(slots)]
        items = list(self.items)
        for slot, item in zip(slots, reordered):
            items[slot] = item
        return self._apply(items, self.selection)
