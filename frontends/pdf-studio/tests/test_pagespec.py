import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
from pdf_studio.pagespec import PageSpecError, format_pages, parse_pages

# The daemon's Go parser runs against the same file, so the two grammars cannot
# drift apart. It lives in the repo; an installed copy of the Studio has no such
# file, in which case only the cases written out below run.
VECTORS = Path(__file__).parents[3] / "backend" / "internal" / "pdfops" / "testdata" / "pagespec_cases.json"


class SharedVectorTests(unittest.TestCase):
    @unittest.skipUnless(VECTORS.exists(), "shared page-spec vectors are only present in the source tree")
    def test_matches_every_shared_vector(self):
        cases = json.loads(VECTORS.read_text())
        self.assertGreater(len(cases), 20)
        for case in cases:
            with self.subTest(case["name"], spec=case["spec"]):
                if case.get("error"):
                    with self.assertRaises(PageSpecError) as raised:
                        parse_pages(case["spec"], case["page_count"])
                    self.assertIn(case["error"], str(raised.exception))
                else:
                    self.assertEqual(parse_pages(case["spec"], case["page_count"]), case["want"])


class ParseTests(unittest.TestCase):
    def test_keeps_order_and_duplicates(self):
        self.assertEqual(parse_pages("5,1,3,1", 6), [5, 1, 3, 1])

    def test_open_ends(self):
        self.assertEqual(parse_pages("-2,5-", 6), [1, 2, 5, 6])

    def test_error_is_a_value_error(self):
        with self.assertRaises(ValueError):
            parse_pages("nope", 3)

    def test_page_count_zero_skips_the_bound_but_not_the_size_limit(self):
        self.assertEqual(parse_pages("40", 0), [40])
        with self.assertRaises(PageSpecError):
            parse_pages("1-1001", 0)

    def test_z_needs_a_known_page_count(self):
        with self.assertRaises(PageSpecError):
            parse_pages("z", 0)

    def test_open_range_past_the_end_is_an_error_not_an_empty_selection(self):
        # "N-" with N beyond the document names nothing; N is out of range first.
        with self.assertRaises(PageSpecError):
            parse_pages("9-", 3)


class FormatTests(unittest.TestCase):
    def test_collapses_runs_of_three_or_more(self):
        self.assertEqual(format_pages([1, 2, 3, 7, 10, 11, 12]), "1-3,7,10-12")

    def test_short_runs_are_written_out(self):
        self.assertEqual(format_pages([1, 2, 5]), "1,2,5")

    def test_sorts_and_removes_duplicates(self):
        self.assertEqual(format_pages([7, 3, 3, 1, 2]), "1-3,7")

    def test_empty(self):
        self.assertEqual(format_pages([]), "")

    def test_round_trips_through_parse(self):
        for pages in ([1], [1, 2, 3], [2, 4, 6, 7, 8, 9, 20], list(range(1, 50))):
            with self.subTest(pages=pages):
                self.assertEqual(parse_pages(format_pages(pages), 60), sorted(set(pages)))


if __name__ == "__main__":
    unittest.main()
