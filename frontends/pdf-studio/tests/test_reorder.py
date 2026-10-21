import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))

from pdf_studio.model import EditModel
from pdf_studio.reorder import drop_move


def apply_drop(model, dragged, target, after):
    uids = [item.uid for item in model.items]
    move = drop_move(model, uids[dragged - 1], uids[target - 1], after)
    if move is None:
        return None
    model.selection, index = move
    model.move_to(index)
    return [item.src for item in model.items]


class DropMoveTests(unittest.TestCase):
    def test_a_page_dropped_before_another_moves_there(self):
        self.assertEqual(apply_drop(EditModel(5), 4, 2, after=False), [1, 4, 2, 3, 5])

    def test_a_page_dropped_after_another_moves_there(self):
        self.assertEqual(apply_drop(EditModel(5), 1, 3, after=True), [2, 3, 1, 4, 5])

    def test_dropping_after_the_last_page_moves_to_the_end(self):
        self.assertEqual(apply_drop(EditModel(4), 1, 4, after=True), [2, 3, 4, 1])

    def test_dropping_before_the_first_page_moves_to_the_start(self):
        self.assertEqual(apply_drop(EditModel(4), 4, 1, after=False), [4, 1, 2, 3])

    def test_a_drop_that_changes_nothing_is_none(self):
        model = EditModel(4)
        uids = [item.uid for item in model.items]
        self.assertIsNone(drop_move(model, uids[1], uids[2], after=False))  # already just before 3
        self.assertIsNone(drop_move(model, uids[1], uids[0], after=True))   # already just after 1
        self.assertIsNone(drop_move(model, uids[1], uids[1], after=False))  # onto itself

    def test_dragging_a_selected_page_takes_the_whole_selection(self):
        model = EditModel(6)
        model.select_spec("2,3")
        self.assertEqual(apply_drop(model, 2, 6, after=True), [1, 4, 5, 6, 2, 3])

    def test_dropping_onto_a_page_that_is_moving_is_none(self):
        model = EditModel(6)
        model.select_spec("2,3")
        uids = [item.uid for item in model.items]
        self.assertIsNone(drop_move(model, uids[1], uids[2], after=False))

    def test_dragging_an_unselected_page_moves_only_it(self):
        model = EditModel(5)
        model.select_spec("1,2")
        moving, _index = drop_move(model, model.items[4].uid, model.items[0].uid, after=False)
        self.assertEqual(moving, {model.items[4].uid})

    def test_unknown_uids_are_ignored(self):
        model = EditModel(3)
        self.assertIsNone(drop_move(model, 9999, model.items[0].uid, after=False))


if __name__ == "__main__":
    unittest.main()
