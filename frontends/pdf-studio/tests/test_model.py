import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
from pdf_studio.model import EditModel


def srcs(model):
    return [item.src for item in model.items]


def rots(model):
    return [item.rotate for item in model.items]


def select_positions(model, *positions):
    """Select by 1-based position, the way a user reads the grid."""
    model.select_none()
    model.selection = {model.items[p - 1].uid for p in positions}


class StartingStateTests(unittest.TestCase):
    def test_starts_as_the_untouched_document(self):
        model = EditModel(4)
        self.assertEqual(srcs(model), [1, 2, 3, 4])
        self.assertEqual(rots(model), [0, 0, 0, 0])
        self.assertTrue(model.is_identity())
        self.assertFalse(model.dirty)
        self.assertEqual(model.selection, set())
        self.assertEqual(model.edit_count, 0)

    def test_every_item_has_a_distinct_uid(self):
        model = EditModel(5)
        self.assertEqual(len({item.uid for item in model.items}), 5)

    def test_needs_at_least_one_page(self):
        with self.assertRaises(ValueError):
            EditModel(0)


class SelectionTests(unittest.TestCase):
    def test_select_all_none_invert(self):
        model = EditModel(4)
        model.select_all()
        self.assertEqual(model.positions(), [1, 2, 3, 4])
        model.select_none()
        self.assertEqual(model.positions(), [])
        select_positions(model, 1, 4)
        model.invert_selection()
        self.assertEqual(model.positions(), [2, 3])

    def test_odd_and_even_are_by_position(self):
        model = EditModel(5)
        model.select_odd()
        self.assertEqual(model.positions(), [1, 3, 5])
        model.select_even()
        self.assertEqual(model.positions(), [2, 4])

    def test_odd_and_even_follow_current_positions_not_source_pages(self):
        model = EditModel(4)
        select_positions(model, 1)
        model.delete()  # now [2, 3, 4]
        model.select_odd()
        self.assertEqual([model.items[p - 1].src for p in model.positions()], [2, 4])

    def test_select_by_spec_and_back(self):
        model = EditModel(12)
        outcome = model.select_spec("1-3,7,10-")
        self.assertTrue(outcome.changed, outcome.message)
        self.assertEqual(model.positions(), [1, 2, 3, 7, 10, 11, 12])
        self.assertEqual(model.selection_spec(), "1-3,7,10-12")

    def test_blank_spec_clears_the_selection(self):
        model = EditModel(4)
        model.select_all()
        self.assertTrue(model.select_spec("  ").changed)
        self.assertEqual(model.selection, set())

    def test_bad_spec_leaves_the_selection_alone_and_says_why(self):
        model = EditModel(4)
        select_positions(model, 2)
        outcome = model.select_spec("9")
        self.assertFalse(outcome.changed)
        self.assertIn("9", outcome.message)
        self.assertEqual(model.positions(), [2])

    def test_selecting_does_not_create_an_undo_step(self):
        model = EditModel(4)
        model.select_all()
        model.select_none()
        self.assertEqual(model.edit_count, 0)
        self.assertFalse(model.can_undo)

    def test_selection_survives_a_move_by_identity(self):
        model = EditModel(4)
        select_positions(model, 2)
        chosen = next(iter(model.selection))
        model.move_to_end()
        self.assertEqual(model.items[-1].uid, chosen)
        self.assertEqual(model.positions(), [4])


class RotateTests(unittest.TestCase):
    def test_rotates_only_the_selection_clockwise(self):
        model = EditModel(3)
        select_positions(model, 2)
        self.assertTrue(model.rotate(90).changed)
        self.assertEqual(rots(model), [0, 90, 0])

    def test_rotation_accumulates_and_wraps(self):
        model = EditModel(2)
        select_positions(model, 1)
        for _ in range(3):
            model.rotate(90)
        self.assertEqual(rots(model), [270, 0])
        model.rotate(90)
        self.assertEqual(rots(model), [0, 0])

    def test_negative_turns_are_anticlockwise(self):
        model = EditModel(1)
        select_positions(model, 1)
        model.rotate(-90)
        self.assertEqual(rots(model), [270])

    def test_only_quarter_turns_are_accepted(self):
        model = EditModel(1)
        select_positions(model, 1)
        self.assertFalse(model.rotate(45).changed)
        self.assertEqual(rots(model), [0])

    def test_nothing_selected_is_a_reported_no_op(self):
        model = EditModel(3)
        outcome = model.rotate(90)
        self.assertFalse(outcome.changed)
        self.assertIn("select", outcome.message.lower())
        self.assertEqual(model.edit_count, 0)

    def test_rotating_a_full_turn_back_to_zero_is_the_identity_again(self):
        model = EditModel(2)
        select_positions(model, 1)
        model.rotate(180)
        self.assertTrue(model.dirty)
        model.rotate(180)
        self.assertFalse(model.dirty)


class DeleteTests(unittest.TestCase):
    def test_deletes_the_selection(self):
        model = EditModel(5)
        select_positions(model, 2, 4)
        self.assertTrue(model.delete().changed)
        self.assertEqual(srcs(model), [1, 3, 5])
        self.assertEqual(model.selection, set())

    def test_refuses_to_delete_everything(self):
        model = EditModel(3)
        model.select_all()
        outcome = model.delete()
        self.assertFalse(outcome.changed)
        self.assertEqual(srcs(model), [1, 2, 3])

    def test_refuses_to_leave_only_blank_pages(self):
        model = EditModel(2)
        select_positions(model, 1)
        model.insert_blank_after()  # [1, blank, 2]
        select_positions(model, 1, 3)
        outcome = model.delete()
        self.assertFalse(outcome.changed)
        self.assertIn("original page", outcome.message)

    def test_nothing_selected_is_a_reported_no_op(self):
        model = EditModel(3)
        self.assertFalse(model.delete().changed)
        self.assertEqual(srcs(model), [1, 2, 3])


class DuplicateAndBlankTests(unittest.TestCase):
    def test_duplicate_puts_each_copy_right_after_its_original(self):
        model = EditModel(3)
        select_positions(model, 1, 3)
        self.assertTrue(model.duplicate().changed)
        self.assertEqual(srcs(model), [1, 1, 2, 3, 3])

    def test_duplicate_selects_the_copies_with_new_identities(self):
        model = EditModel(2)
        select_positions(model, 1)
        original = next(iter(model.selection))
        model.duplicate()
        self.assertEqual(model.positions(), [2])
        self.assertNotEqual(model.items[1].uid, original)
        self.assertEqual(len({item.uid for item in model.items}), 3)

    def test_rotating_a_copy_leaves_the_original_alone(self):
        model = EditModel(2)
        select_positions(model, 1)
        model.duplicate()
        model.rotate(90)  # the selected copy
        self.assertEqual(srcs(model), [1, 1, 2])
        self.assertEqual(rots(model), [0, 90, 0])

    def test_a_copy_keeps_the_rotation_of_its_original(self):
        model = EditModel(1)
        select_positions(model, 1)
        model.rotate(90)
        model.duplicate()
        self.assertEqual(rots(model), [90, 90])

    def test_insert_blank_after_each_selected_page(self):
        model = EditModel(3)
        select_positions(model, 1, 2)
        self.assertTrue(model.insert_blank_after().changed)
        self.assertEqual(srcs(model), [1, 0, 2, 0, 3])
        self.assertEqual(model.positions(), [2, 4])

    def test_insert_blank_at_the_start(self):
        model = EditModel(2)
        self.assertTrue(model.insert_blank_at(0).changed)
        self.assertEqual(srcs(model), [0, 1, 2])

    def test_insert_blank_at_rejects_positions_outside_the_document(self):
        model = EditModel(2)
        self.assertFalse(model.insert_blank_at(3).changed)
        self.assertFalse(model.insert_blank_at(-1).changed)

    def test_the_item_ceiling_is_enforced(self):
        model = EditModel(3, max_items=4)
        model.select_all()
        outcome = model.duplicate()
        self.assertFalse(outcome.changed)
        self.assertIn("4", outcome.message)
        self.assertEqual(srcs(model), [1, 2, 3])
        select_positions(model, 1)
        self.assertTrue(model.duplicate().changed)
        select_positions(model, 1)
        self.assertFalse(model.insert_blank_after().changed)


class MoveTests(unittest.TestCase):
    def test_move_right_and_left_by_one(self):
        model = EditModel(4)
        select_positions(model, 2)
        model.move_right()
        self.assertEqual(srcs(model), [1, 3, 2, 4])
        model.move_left()
        model.move_left()
        self.assertEqual(srcs(model), [2, 1, 3, 4])

    def test_a_block_moves_together_and_stops_at_the_edge(self):
        model = EditModel(5)
        select_positions(model, 4, 5)
        self.assertFalse(model.move_right().changed)
        select_positions(model, 2, 3)
        model.move_right()
        self.assertEqual(srcs(model), [1, 4, 2, 3, 5])
        model.move_left()
        model.move_left()
        self.assertEqual(srcs(model), [2, 3, 1, 4, 5])
        self.assertFalse(model.move_left().changed)

    def test_non_adjacent_selection_keeps_its_relative_order(self):
        model = EditModel(5)
        select_positions(model, 2, 4)
        model.move_to_start()
        self.assertEqual(srcs(model), [2, 4, 1, 3, 5])
        model.move_to_end()
        self.assertEqual(srcs(model), [1, 3, 5, 2, 4])

    def test_move_to_an_index(self):
        model = EditModel(6)
        select_positions(model, 1, 2)
        # index counts among the pages that are not moving: [3,4,5,6] -> before 5
        self.assertTrue(model.move_to(2).changed)
        self.assertEqual(srcs(model), [3, 4, 1, 2, 5, 6])

    def test_move_to_the_end_index(self):
        model = EditModel(4)
        select_positions(model, 1)
        model.move_to(3)
        self.assertEqual(srcs(model), [2, 3, 4, 1])

    def test_move_to_where_it_already_is_changes_nothing(self):
        model = EditModel(4)
        select_positions(model, 2)
        outcome = model.move_to(1)
        self.assertFalse(outcome.changed)
        self.assertEqual(model.edit_count, 0)

    def test_move_to_rejects_a_bad_index(self):
        model = EditModel(4)
        select_positions(model, 1)
        self.assertFalse(model.move_to(4).changed)
        self.assertFalse(model.move_to(-1).changed)

    def test_nothing_selected_moves_nothing(self):
        model = EditModel(3)
        for action in (model.move_left, model.move_right, model.move_to_start, model.move_to_end):
            self.assertFalse(action().changed)
        self.assertEqual(srcs(model), [1, 2, 3])


class ReverseTests(unittest.TestCase):
    def test_reverses_the_whole_document_when_nothing_is_selected(self):
        model = EditModel(4)
        self.assertTrue(model.reverse().changed)
        self.assertEqual(srcs(model), [4, 3, 2, 1])

    def test_reverses_only_the_selection_within_its_own_slots(self):
        model = EditModel(5)
        select_positions(model, 1, 3, 5)
        model.reverse()
        self.assertEqual(srcs(model), [5, 2, 3, 4, 1])

    def test_a_single_selected_page_cannot_be_reversed(self):
        model = EditModel(3)
        select_positions(model, 2)
        self.assertFalse(model.reverse().changed)

    def test_a_one_page_document_cannot_be_reversed(self):
        self.assertFalse(EditModel(1).reverse().changed)


class UndoRedoTests(unittest.TestCase):
    def test_undo_restores_items_and_the_selection_at_that_moment(self):
        model = EditModel(4)
        select_positions(model, 2, 3)
        model.delete()
        self.assertEqual(srcs(model), [1, 4])
        self.assertTrue(model.undo().changed)
        self.assertEqual(srcs(model), [1, 2, 3, 4])
        self.assertEqual(model.positions(), [2, 3])

    def test_redo_reapplies(self):
        model = EditModel(3)
        select_positions(model, 1)
        model.rotate(90)
        model.undo()
        self.assertEqual(rots(model), [0, 0, 0])
        self.assertTrue(model.redo().changed)
        self.assertEqual(rots(model), [90, 0, 0])

    def test_a_new_edit_discards_the_redo_history(self):
        model = EditModel(3)
        select_positions(model, 1)
        model.rotate(90)
        model.undo()
        model.rotate(180)
        self.assertFalse(model.can_redo)

    def test_a_chain_unwinds_in_order(self):
        model = EditModel(4)
        select_positions(model, 1)
        model.rotate(90)
        model.duplicate()
        model.move_to_end()
        for _ in range(3):
            self.assertTrue(model.undo().changed)
        self.assertEqual(srcs(model), [1, 2, 3, 4])
        self.assertEqual(rots(model), [0, 0, 0, 0])
        self.assertFalse(model.undo().changed)

    def test_undoing_everything_makes_the_model_clean_again(self):
        model = EditModel(3)
        select_positions(model, 2)
        model.delete()
        self.assertTrue(model.dirty)
        model.undo()
        self.assertFalse(model.dirty)

    def test_history_is_bounded(self):
        model = EditModel(2)
        select_positions(model, 1)
        for _ in range(150):
            model.rotate(90)
        self.assertEqual(model.edit_count, 100)
        undone = 0
        while model.undo().changed:
            undone += 1
        self.assertEqual(undone, 100)

    def test_a_refused_action_adds_no_history(self):
        model = EditModel(2)
        model.select_all()
        model.delete()
        self.assertEqual(model.edit_count, 0)

    def test_redo_after_undo_restores_the_post_edit_selection(self):
        model = EditModel(3)
        select_positions(model, 1)
        model.duplicate()
        after = model.positions()
        model.undo()
        model.redo()
        self.assertEqual(model.positions(), after)


class IdentityTests(unittest.TestCase):
    def test_reordering_back_is_the_identity(self):
        model = EditModel(3)
        select_positions(model, 1)
        model.move_to_end()
        self.assertFalse(model.is_identity())
        select_positions(model, 3)
        model.move_to_start()
        self.assertTrue(model.is_identity())

    def test_a_deletion_is_not_the_identity(self):
        model = EditModel(3)
        select_positions(model, 3)
        model.delete()
        self.assertFalse(model.is_identity())

    def test_a_blank_page_is_not_the_identity(self):
        model = EditModel(2)
        model.insert_blank_at(0)
        self.assertFalse(model.is_identity())


if __name__ == "__main__":
    unittest.main()
