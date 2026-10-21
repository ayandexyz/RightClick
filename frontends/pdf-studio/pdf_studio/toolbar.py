"""GTK-independent page actions for PDF Studio.

The action catalogue is intentionally plain Python data.  GTK callers can use it
for buttons, menus, and shortcuts without making headless model users import
PyGObject.
"""

from dataclasses import dataclass

from .model import Outcome
from .pagespec import PageSpecError


@dataclass(frozen=True)
class RangeBoxState:
    """The result of applying text from the page range entry."""

    valid: bool
    text: str
    message: str = ""


def _has_selection(model):
    return bool(model.selection)


def _can_delete(model):
    if not model.selection:
        return False
    remaining = [item for item in model.items if item.uid not in model.selection]
    return bool(remaining) and any(not item.blank for item in remaining)


def _can_reverse(model):
    return len(model.items) > 1 and len(model.selection) != 1


def _always(_model):
    return True


ACTION_TABLE = {
    "select-all": {"method": lambda model: model.select_all(), "enabled": _always},
    "select-none": {"method": lambda model: model.select_none(), "enabled": _always},
    "select-odd": {"method": lambda model: model.select_odd(), "enabled": _always},
    "select-even": {"method": lambda model: model.select_even(), "enabled": _always},
    "invert-selection": {"method": lambda model: model.invert_selection(), "enabled": _always},
    "rotate-left": {"method": lambda model: model.rotate(-90), "enabled": _has_selection},
    "rotate-right": {"method": lambda model: model.rotate(90), "enabled": _has_selection},
    "delete": {"method": lambda model: model.delete(), "enabled": _can_delete},
    "duplicate": {"method": lambda model: model.duplicate(), "enabled": _has_selection},
    "insert-blank": {"method": lambda model: model.insert_blank_after(), "enabled": _has_selection},
    "reverse": {"method": lambda model: model.reverse(), "enabled": _can_reverse},
    "undo": {"method": lambda model: model.undo(), "enabled": lambda model: model.can_undo},
    "redo": {"method": lambda model: model.redo(), "enabled": lambda model: model.can_redo},
    "move-left": {"method": lambda model: model.move_left(), "enabled": _has_selection},
    "move-right": {"method": lambda model: model.move_right(), "enabled": _has_selection},
    "move-start": {"method": lambda model: model.move_to_start(), "enabled": _has_selection},
    "move-end": {"method": lambda model: model.move_to_end(), "enabled": _has_selection},
}


def dispatch_action(model, action_id):
    """Run an action by ID and return its model ``Outcome``.

    Selection actions return a successful ``Outcome``-like value from the
    model methods; edit actions return the model's actual outcome.  Disabled
    actions are not dispatched, allowing UI code to use the same predicates.
    """
    action = ACTION_TABLE[action_id]
    method = action["method"]
    result = method(model)
    if result is None:
        return Outcome(True)
    return result


def range_box_state(model, text):
    """Apply a page range, preserving selection when the range is invalid."""
    try:
        outcome = model.select_spec(text)
    except PageSpecError as error:  # compatible with models exposing the parser directly
        return RangeBoxState(False, text, str(error))
    if outcome.message:
        return RangeBoxState(False, text, outcome.message)
    return RangeBoxState(True, text)


__all__ = ["ACTION_TABLE", "RangeBoxState", "dispatch_action", "range_box_state"]
