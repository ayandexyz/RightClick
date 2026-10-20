import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
from pdf_studio.merge_view import merge_preview
from pdf_studio.split_view import split_preview


class SplitPreviewTests(unittest.TestCase):
    def test_ranges_and_every_n_preview_parts(self):
        self.assertEqual(split_preview(6, "ranges", "1-2;3-"), {"parts": [[1, 2], [3, 4, 5, 6]], "error": None})
        self.assertEqual(split_preview(10, "every", 4)["parts"], [[1, 2, 3, 4], [5, 6, 7, 8], [9, 10]])

    def test_invalid_empty_single_and_too_many_parts_return_errors(self):
        for page_count, mode, value, text in (
            (6, "ranges", "1-2;;4", "empty"),
            (6, "every", 6, "single"),
            (300, "every", 1, "200"),
        ):
            with self.subTest(value=value):
                result = split_preview(page_count, mode, value)
                self.assertEqual(result["parts"], [])
                self.assertIn(text, result["error"])


class MergePreviewTests(unittest.TestCase):
    def test_rows_keep_file_order_and_validate_ranges_against_each_count(self):
        result = merge_preview([("first.pdf", 3), ("second.pdf", 4)], ["", "2-"])
        self.assertEqual([row["file"] for row in result["rows"]], ["first.pdf", "second.pdf"])
        self.assertEqual(result["rows"][0]["pages"], "")
        self.assertEqual(result["errors"], [])

        bad = merge_preview([("first.pdf", 3), ("second.pdf", 4)], ["1", "9"])
        self.assertEqual(len(bad["rows"]), 2)
        self.assertIn("second.pdf", bad["errors"][0])
        self.assertEqual(bad["rows"][0]["error"], None)

    def test_alignment_and_minimum_file_errors(self):
        self.assertIn("at least two", merge_preview(["one.pdf"], [""])["errors"][0])
        self.assertIn("Each file needs", merge_preview(["a.pdf", "b.pdf"], [""])["errors"][0])


if __name__ == "__main__":
    unittest.main()
