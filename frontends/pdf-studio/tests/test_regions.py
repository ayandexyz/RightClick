import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
from pdf_studio.regions import Region, RegionModel, drag_to_region, region_to_pixels


class DragTests(unittest.TestCase):
    def test_a_drag_becomes_fractions_of_the_page_in_any_direction(self):
        forward = drag_to_region(2, (100, 50), (300, 150), (400, 200))
        backward = drag_to_region(2, (300, 150), (100, 50), (400, 200))
        self.assertEqual(forward, backward)
        self.assertEqual(forward, Region(2, 0.25, 0.25, 0.5, 0.5))

    def test_a_drag_is_clamped_to_the_page(self):
        region = drag_to_region(1, (-50, -50), (900, 900), (400, 200))
        self.assertEqual(region, Region(1, 0.0, 0.0, 1.0, 1.0))

    def test_a_stray_click_or_empty_canvas_is_refused(self):
        with self.assertRaises(ValueError):
            drag_to_region(1, (10, 10), (11, 11), (400, 200))
        with self.assertRaises(ValueError):
            drag_to_region(1, (0, 0), (10, 10), (0, 200))

    def test_pixels_round_trip(self):
        region = drag_to_region(1, (40, 20), (120, 100), (400, 200))
        for got, want in zip(region_to_pixels(region, (400, 200)), (40, 20, 120, 100)):
            self.assertAlmostEqual(got, want)


class ModelTests(unittest.TestCase):
    def test_add_remove_undo_and_summary(self):
        model = RegionModel()
        self.assertEqual(model.summary(), "No redaction regions")
        a, b = Region(1, 0, 0, .5, .5), Region(3, .1, .1, .2, .2)
        model.add(a)
        model.add(b)
        self.assertEqual(model.summary(), "2 boxes on pages 1, 3")
        self.assertEqual(model.remove(0), a)
        self.assertEqual(model.summary(), "1 box on pages 3")
        self.assertTrue(model.undo())
        self.assertEqual(model.regions, (a, b))
        with self.assertRaises(IndexError):
            model.remove(5)
        self.assertEqual(model.by_page(), {1: [a], 3: [b]})


if __name__ == "__main__":
    unittest.main()
