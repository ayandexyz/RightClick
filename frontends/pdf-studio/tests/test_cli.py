import contextlib
import io
import os
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
from pdf_studio.cli import MIN_ADW, MIN_GTK, check_paths, check_versions, parse_args


class VersionCheckTests(unittest.TestCase):
    def test_current_enough_is_fine(self):
        self.assertIsNone(check_versions((4, 12), (1, 5)))
        self.assertIsNone(check_versions((4, 24), (1, 10)))

    def test_the_minimums_are_the_documented_ones(self):
        self.assertEqual(MIN_GTK, (4, 12))
        self.assertEqual(MIN_ADW, (1, 5))

    def test_old_gtk_is_named_with_both_versions(self):
        message = check_versions((4, 10), (1, 5))
        self.assertIn("GTK 4.12", message)
        self.assertIn("4.10", message)

    def test_old_libadwaita(self):
        message = check_versions((4, 14), (1, 2))
        self.assertIn("libadwaita 1.5", message)
        self.assertIn("1.2", message)

    def test_a_newer_major_always_passes(self):
        self.assertIsNone(check_versions((5, 0), (2, 0)))

    def test_missing_toolkits_are_said_plainly(self):
        self.assertIn("not installed", check_versions(None, (1, 5)))
        self.assertIn("not installed", check_versions((4, 12), None))

    def test_the_message_names_the_packages_for_both_supported_distros(self):
        message = check_versions(None, None)
        for package in ("gtk4", "libadwaita", "gir1.2-gtk-4.0", "gir1.2-adw-1"):
            self.assertIn(package, message)

    def test_it_is_one_line(self):
        self.assertNotIn("\n", check_versions((4, 0), (1, 0)))


class ArgumentTests(unittest.TestCase):
    def test_no_files_opens_the_empty_window(self):
        self.assertEqual(parse_args([]).files, [])

    def test_files_keep_their_order(self):
        self.assertEqual(parse_args(["b.pdf", "a.pdf"]).files, ["b.pdf", "a.pdf"])

    def test_unknown_options_are_an_error_not_a_file(self):
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            parse_args(["--nope"])


class PathCheckTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)

    def pdf(self, name="a.pdf", body=b"%PDF-1.7\nstuff\n"):
        path = self.dir / name
        path.write_bytes(body)
        return str(path)

    def test_a_pdf_is_accepted_as_an_absolute_path(self):
        good, problems = check_paths([self.pdf()])
        self.assertEqual(good, [str(self.dir / "a.pdf")])
        self.assertEqual(problems, [])

    def test_relative_paths_are_made_absolute_against_the_given_directory(self):
        self.pdf("rel.pdf")
        good, _ = check_paths(["rel.pdf"], cwd=str(self.dir))
        self.assertEqual(good, [str(self.dir / "rel.pdf")])

    def test_dot_segments_are_collapsed(self):
        self.pdf("a.pdf")
        good, _ = check_paths([str(self.dir / "sub" / ".." / "a.pdf")])
        self.assertEqual(good, [str(self.dir / "a.pdf")])

    def test_a_missing_file_is_a_problem_naming_the_file(self):
        good, problems = check_paths([str(self.dir / "gone.pdf")])
        self.assertEqual(good, [])
        self.assertEqual(problems[0].path, str(self.dir / "gone.pdf"))
        self.assertIn("exist", problems[0].reason)

    def test_a_folder_is_a_problem(self):
        _, problems = check_paths([str(self.dir)])
        self.assertIn("folder", problems[0].reason)

    def test_a_file_that_is_not_a_pdf_is_a_problem_whatever_its_name(self):
        _, problems = check_paths([self.pdf("fake.pdf", b"hello")])
        self.assertIn("PDF", problems[0].reason)

    def test_a_pdf_without_the_extension_is_still_a_pdf(self):
        good, problems = check_paths([self.pdf("noext")])
        self.assertEqual((len(good), problems), (1, []))

    def test_an_empty_file_is_a_problem(self):
        _, problems = check_paths([self.pdf("empty.pdf", b"")])
        self.assertEqual(len(problems), 1)

    def test_good_files_are_kept_alongside_bad_ones_in_order(self):
        a, b = self.pdf("a.pdf"), self.pdf("b.pdf")
        good, problems = check_paths([a, str(self.dir / "gone.pdf"), b])
        self.assertEqual(good, [a, b])
        self.assertEqual(len(problems), 1)

    def test_naming_a_file_twice_opens_it_once(self):
        a = self.pdf("a.pdf")
        good, problems = check_paths([a, a])
        self.assertEqual((good, problems), ([a], []))

    @unittest.skipIf(os.geteuid() == 0, "root can read anything")
    def test_an_unreadable_file_is_a_problem(self):
        path = self.pdf("secret.pdf")
        os.chmod(path, 0)
        self.addCleanup(os.chmod, path, 0o600)
        _, problems = check_paths([path])
        self.assertIn("read", problems[0].reason)


if __name__ == "__main__":
    unittest.main()
