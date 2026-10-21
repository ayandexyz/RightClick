import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
from fc_client.messages import friendly_error


class FriendlyErrorTests(unittest.TestCase):
    def test_locked_pdf_says_it_needs_a_password(self):
        sentence = friendly_error("password_required", "this file is encrypted", "locked.pdf")
        self.assertIn("locked", sentence)
        self.assertIn("needs a password", sentence)
        self.assertIn("locked.pdf", sentence)

    def test_wrong_password_says_so(self):
        self.assertIn("did not work", friendly_error("password_incorrect", "", "locked.pdf"))

    def test_damaged_pdf_quotes_the_daemon_message(self):
        sentence = friendly_error("invalid_pdf", "no header found", "broken.pdf")
        self.assertIn("could not be read", sentence)
        self.assertIn("no header found", sentence)

    def test_damaged_pdf_without_a_message_still_says_something(self):
        sentence = friendly_error("invalid_pdf", "", "broken.pdf")
        self.assertIn("could not be read", sentence)
        self.assertNotIn(":", sentence.split("could not be read")[1])

    def test_unknown_code_falls_back_to_the_daemon_message(self):
        self.assertEqual(friendly_error("internal", "gs exited 1", "a.pdf"), "a.pdf: gs exited 1")

    def test_unknown_code_with_no_message_still_says_something(self):
        self.assertIn("failed", friendly_error("internal", ""))

    def test_the_file_name_is_optional(self):
        self.assertTrue(friendly_error("internal", "boom"))
        self.assertNotIn(":", friendly_error("internal", "boom"))


if __name__ == "__main__":
    unittest.main()
