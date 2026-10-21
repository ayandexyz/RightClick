import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))

from pdf_studio.model import EditModel
from pdf_studio.toolbar import ACTION_TABLE, dispatch_action, range_box_state


def positions(model, *values):
    model.selection = {model.items[value - 1].uid for value in values}


class ActionTableTests(unittest.TestCase):
    def test_table_contains_every_page_action(self):
        expected = {
            "select-all", "select-none", "select-odd", "select-even", "invert-selection",
            "rotate-left", "rotate-right", "delete", "duplicate", "insert-blank", "reverse",
            "undo", "redo", "move-left", "move-right", "move-start", "move-end",
        }
        self.assertEqual(set(ACTION_TABLE), expected)
        for action in ACTION_TABLE.values():
            self.assertTrue(callable(action["method"]))
            self.assertTrue(callable(action["enabled"]))

    def test_dispatches_selection_actions(self):
        model = EditModel(5)
        for action, wanted in (("select-odd", [1, 3, 5]), ("select-even", [2, 4])):
            outcome = dispatch_action(model, action)
            self.assertTrue(outcome.changed)
            self.assertEqual(model.positions(), wanted)
        dispatch_action(model, "invert-selection")
        self.assertEqual(model.positions(), [1, 3, 5])
        dispatch_action(model, "select-none")
        self.assertEqual(model.positions(), [])

    def test_dispatches_edits_and_history(self):
        model = EditModel(3)
        positions(model, 2)
        self.assertTrue(dispatch_action(model, "rotate-left").changed)
        self.assertEqual(model.items[1].rotate, 270)
        self.assertTrue(dispatch_action(model, "duplicate").changed)
        self.assertTrue(dispatch_action(model, "undo").changed)
        self.assertTrue(dispatch_action(model, "redo").changed)

    def test_enabled_predicates_cover_selection_history_and_delete_safety(self):
        model = EditModel(3)
        self.assertFalse(ACTION_TABLE["delete"]["enabled"](model))
        self.assertFalse(ACTION_TABLE["rotate-left"]["enabled"](model))
        self.assertFalse(ACTION_TABLE["undo"]["enabled"](model))
        self.assertTrue(ACTION_TABLE["select-all"]["enabled"](model))
        dispatch_action(model, "select-all")
        self.assertFalse(ACTION_TABLE["delete"]["enabled"](model))
        positions(model, 1)
        self.assertTrue(ACTION_TABLE["delete"]["enabled"](model))
        dispatch_action(model, "delete")
        self.assertTrue(ACTION_TABLE["undo"]["enabled"](model))
        self.assertTrue(ACTION_TABLE["redo"]["enabled"](model) is False)
        dispatch_action(model, "undo")
        self.assertTrue(ACTION_TABLE["redo"]["enabled"](model))

    def test_unknown_action_is_rejected(self):
        with self.assertRaises(KeyError):
            dispatch_action(EditModel(1), "no-such-action")


class RangeBoxTests(unittest.TestCase):
    def test_valid_range_updates_selection_and_round_trips(self):
        model = EditModel(12)
        state = range_box_state(model, "1-3, 7, 10-")
        self.assertTrue(state.valid)
        self.assertEqual(model.selection_spec(), "1-3,7,10-12")
        self.assertEqual(state.text, "1-3, 7, 10-")

    def test_invalid_range_preserves_selection_and_reports_reason(self):
        model = EditModel(4)
        positions(model, 2)
        before = set(model.selection)
        state = range_box_state(model, "1-9")
        self.assertFalse(state.valid)
        self.assertTrue(state.message)
        self.assertEqual(model.selection, before)

    def test_empty_range_clears_selection(self):
        model = EditModel(2)
        positions(model, 1)
        self.assertTrue(range_box_state(model, "").valid)
        self.assertEqual(model.positions(), [])


if __name__ == "__main__":
    unittest.main()
