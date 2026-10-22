import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
from pdf_studio.omarchy_theme import accent_hex, readable_on


class AccentHexTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / "colors.toml"

    def _write(self, text):
        self.path.write_text(text)
        return self.path

    def test_reads_the_accent_from_a_real_theme_file(self):
        self._write('mode = "dark"\n\naccent = "#7fbbb3"\nselection = "#3d484d"\n')
        self.assertEqual(accent_hex(self.path), "#7fbbb3")

    def test_missing_file_is_none_not_an_error(self):
        self.assertIsNone(accent_hex(self.path))  # never written

    def test_unparseable_toml_is_none(self):
        self._write("not = [valid toml")
        self.assertIsNone(accent_hex(self.path))

    def test_missing_accent_key_is_none(self):
        self._write('mode = "dark"\n')
        self.assertIsNone(accent_hex(self.path))

    def test_non_hex_accent_is_none(self):
        self._write('accent = "turmeric"\n')
        self.assertIsNone(accent_hex(self.path))

    def test_short_hex_is_none(self):
        self._write('accent = "#fff"\n')
        self.assertIsNone(accent_hex(self.path))


class ReadableOnTests(unittest.TestCase):
    def test_dark_accent_gets_white_text(self):
        self.assertEqual(readable_on("#1c1812"), "#ffffff")

    def test_light_accent_gets_black_text(self):
        self.assertEqual(readable_on("#f0b94d"), "#000000")


if __name__ == "__main__":
    unittest.main()
