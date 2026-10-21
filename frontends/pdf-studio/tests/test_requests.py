import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
from pdf_studio.model import EditModel
from pdf_studio.requests import RequestError, edit_request, keep_pages_request, merge_request, split_parts, split_request


def select(model, *positions):
    model.selection = {model.items[p - 1].uid for p in positions}


class EditRequestTests(unittest.TestCase):
    def test_an_edited_document_becomes_the_ordered_item_list(self):
        model = EditModel(4)
        select(model, 2)
        model.rotate(90)
        select(model, 3)
        model.delete()
        select(model, 1)
        model.insert_blank_after()
        op, params = edit_request(model)
        self.assertEqual(op, "pdf-edit")
        self.assertEqual(
            params,
            {"edit": [{"src": 1, "rotate": 0}, {"src": 0, "rotate": 0}, {"src": 2, "rotate": 90}, {"src": 4, "rotate": 0}]},
        )

    def test_the_request_is_plain_json(self):
        model = EditModel(2)
        select(model, 1)
        model.duplicate()
        _, params = edit_request(model)
        self.assertEqual(json.loads(json.dumps(params)), params)

    def test_only_selected_extracts_the_chosen_pages_in_document_order(self):
        model = EditModel(5)
        select(model, 5, 2)
        _, params = edit_request(model, only_selected=True)
        self.assertEqual(params["edit"], [{"src": 2, "rotate": 0}, {"src": 5, "rotate": 0}])

    def test_only_selected_keeps_a_rotation_made_in_the_editor(self):
        model = EditModel(3)
        select(model, 2)
        model.rotate(270)
        _, params = edit_request(model, only_selected=True)
        self.assertEqual(params["edit"], [{"src": 2, "rotate": 270}])

    def test_only_selected_with_no_selection_is_an_error(self):
        with self.assertRaises(RequestError):
            edit_request(EditModel(3), only_selected=True)

    def test_a_selection_of_only_blank_pages_is_an_error(self):
        model = EditModel(2)
        model.insert_blank_at(1)
        select(model, 2)
        with self.assertRaises(RequestError):
            edit_request(model, only_selected=True)

    def test_an_unchanged_document_is_still_a_valid_request(self):
        # Whether to offer Apply for an unchanged document is the window's call.
        op, params = edit_request(EditModel(2))
        self.assertEqual(params["edit"], [{"src": 1, "rotate": 0}, {"src": 2, "rotate": 0}])


class SplitPartsTests(unittest.TestCase):
    def test_ranges_become_one_part_each(self):
        self.assertEqual(split_parts("ranges", "1-2;3-4;5-", 6), [[1, 2], [3, 4], [5, 6]])

    def test_ranges_may_overlap_and_keep_their_own_order(self):
        self.assertEqual(split_parts("ranges", "1-3;2-4", 6), [[1, 2, 3], [2, 3, 4]])
        self.assertEqual(split_parts("ranges", "5,1;2", 6), [[5, 1], [2]])

    def test_every_n_leaves_a_short_last_part(self):
        self.assertEqual(split_parts("every", 4, 10), [[1, 2, 3, 4], [5, 6, 7, 8], [9, 10]])
        self.assertEqual(split_parts("every", 1, 3), [[1], [2], [3]])

    def test_a_split_that_gives_one_file_is_refused(self):
        with self.assertRaises(RequestError) as raised:
            split_parts("every", 6, 6)
        self.assertIn("single", str(raised.exception))
        with self.assertRaises(RequestError):
            split_parts("every", 9, 6)

    def test_every_must_be_a_positive_whole_number(self):
        for bad in (0, -2, 2.5, "3", True, None):
            with self.subTest(bad=bad), self.assertRaises(RequestError):
                split_parts("every", bad, 10)

    def test_bad_ranges_are_refused_before_anything_is_computed(self):
        for bad in ("", "  ", "1-2;;3", "1-2;9", "1-2;x", ";", "3-1", "1-2;"):
            with self.subTest(bad=bad), self.assertRaises(RequestError):
                split_parts("ranges", bad, 6)

    def test_a_single_range_group_is_allowed_like_the_daemon_allows_it(self):
        # One group is one output file, which is just an extract; only the
        # every-N form refuses to produce a single file.
        self.assertEqual(split_parts("ranges", "1-6", 6), [[1, 2, 3, 4, 5, 6]])

    def test_more_than_200_parts_is_refused(self):
        with self.assertRaises(RequestError) as raised:
            split_parts("every", 1, 300)
        self.assertIn("200", str(raised.exception))
        self.assertEqual(len(split_parts("every", 1, 200)), 200)

    def test_unknown_mode(self):
        with self.assertRaises(RequestError):
            split_parts("sideways", 2, 6)


class SplitRequestTests(unittest.TestCase):
    def test_ranges(self):
        self.assertEqual(split_request("ranges", " 1-2 ; 3- ", 6), ("pdf-split-ranges", {"ranges": "1-2;3-"}))

    def test_every(self):
        self.assertEqual(split_request("every", 2, 6), ("pdf-split-every", {"every": 2}))

    def test_it_refuses_what_the_daemon_would_refuse(self):
        with self.assertRaises(RequestError):
            split_request("every", 6, 6)


class KeepPagesRequestTests(unittest.TestCase):
    def test_kept_pages_become_an_extract(self):
        self.assertEqual(keep_pages_request([1, 2, 3, 7]), ("pdf-extract", {"pages": "1-3,7"}))

    def test_order_and_duplicates_do_not_matter(self):
        self.assertEqual(keep_pages_request([3, 1, 2, 2]), ("pdf-extract", {"pages": "1-3"}))

    def test_keeping_nothing_is_refused(self):
        with self.assertRaises(RequestError):
            keep_pages_request([])


class MergeRequestTests(unittest.TestCase):
    def test_no_ranges_sends_no_file_pages(self):
        self.assertEqual(merge_request(["a", "b"], ["", ""]), ("pdf-merge", {}))
        self.assertEqual(merge_request(["a", "b"]), ("pdf-merge", {}))

    def test_ranges_are_aligned_with_the_files(self):
        op, params = merge_request(["a", "b", "c"], ["", "2-3", "  "])
        self.assertEqual((op, params), ("pdf-merge", {"file_pages": ["", "2-3", ""]}))

    def test_needs_two_files(self):
        with self.assertRaises(RequestError):
            merge_request(["only"])

    def test_a_length_mismatch_is_an_error(self):
        with self.assertRaises(RequestError):
            merge_request(["a", "b"], ["1"])

    def test_page_counts_validate_each_range_and_name_the_file(self):
        with self.assertRaises(RequestError) as raised:
            merge_request(["a.pdf", "b.pdf"], ["", "9"], page_counts=[3, 4])
        self.assertIn("b.pdf", str(raised.exception))
        self.assertEqual(
            merge_request(["a.pdf", "b.pdf"], ["z", "2-"], page_counts=[3, 4]),
            ("pdf-merge", {"file_pages": ["z", "2-"]}),
        )


if __name__ == "__main__":
    unittest.main()
