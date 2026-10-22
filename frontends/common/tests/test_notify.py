import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).parents[1]))
from fc_client.notify import reveal


class RevealTests(unittest.TestCase):
    def test_does_nothing_for_an_empty_selection(self):
        with mock.patch("subprocess.run") as run:
            reveal([])
        run.assert_not_called()

    def test_calls_filemanager1_showitems_with_file_uris(self):
        with mock.patch("subprocess.run") as run:
            reveal(["/tmp/a.jpg"])
        run.assert_called_once()
        args = run.call_args.args[0]
        self.assertEqual(args[:5], [
            "gdbus", "call", "--session",
            "--dest", "org.freedesktop.FileManager1",
        ])
        self.assertIn("org.freedesktop.FileManager1.ShowItems", args)
        self.assertIn("[\'file:///tmp/a.jpg\']", args)

    def test_percent_encodes_paths_so_the_gvariant_literal_stays_valid(self):
        with mock.patch("subprocess.run") as run:
            reveal(["/tmp/a b's.jpg"])
        args = run.call_args.args[0]
        self.assertIn("[\'file:///tmp/a%20b%27s.jpg\']", args)

    def test_a_missing_gdbus_does_not_raise(self):
        with mock.patch("subprocess.run", side_effect=FileNotFoundError):
            reveal(["/tmp/a.jpg"])  # must not raise


if __name__ == "__main__":
    unittest.main()
