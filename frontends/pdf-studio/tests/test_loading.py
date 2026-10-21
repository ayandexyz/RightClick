import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
sys.path.insert(0, str(Path(__file__).parents[2] / "common"))
from fc_client.client import DaemonUnavailable, PdfInfo, ProtocolError, RequestError
from pdf_studio.loading import load_document


class FakeClient:
    def __init__(self, *outcomes):
        self.outcomes = list(outcomes)
        self.calls = []

    def pdf_info(self, path, **extra):
        self.calls.append(path)
        self.extras = extra
        outcome = self.outcomes.pop(0)
        if isinstance(outcome, Exception):
            raise outcome
        return outcome


def load(path="/tmp/report.pdf", client=None, ensure=None, password=None):
    started = []

    def ensure_daemon():
        started.append(True)
        if ensure is not None:
            raise ensure

    result = load_document(path, lambda: client, ensure_daemon, password=password)
    return result, started


class LoadingTests(unittest.TestCase):
    def test_a_readable_document_is_ready_with_its_page_count_and_sizes(self):
        sizes = [{"w": 612, "h": 792, "rot": 0}]
        result, started = load(client=FakeClient(PdfInfo(pages=1, sizes=sizes)))
        self.assertEqual(result.kind, "ready")
        self.assertEqual(result.pages, 1)
        self.assertEqual(list(result.sizes), sizes)
        self.assertEqual(started, [])

    def test_a_locked_file_says_so_and_is_not_retryable(self):
        result, _ = load(client=FakeClient(RequestError("password_required", "pdf is encrypted")))
        self.assertEqual(result.kind, "locked")
        self.assertIn("report.pdf", result.message)
        self.assertIn("password", result.message.lower())
        self.assertFalse(result.retry)

    def test_a_locked_file_with_no_password_is_not_a_rejected_password(self):
        result, _ = load(client=FakeClient(RequestError("password_required", "")))
        self.assertFalse(result.password_rejected)

    def test_a_wrong_password_is_reported_as_rejected(self):
        result, _ = load(client=FakeClient(RequestError("password_incorrect", "")), password="nope")
        self.assertEqual(result.kind, "locked")
        self.assertTrue(result.password_rejected)
        self.assertIn("did not work", result.message)

    def test_the_password_is_sent_only_when_there_is_one(self):
        client = FakeClient(PdfInfo(pages=1), PdfInfo(pages=1))
        load(client=client, password="secret")
        self.assertEqual(client.extras, {"password": "secret"})
        load(client=client)
        self.assertEqual(client.extras, {})

    def test_a_damaged_file_says_so_and_is_not_retryable(self):
        result, _ = load(client=FakeClient(RequestError("invalid_pdf", "pdf is damaged or not a valid PDF")))
        self.assertEqual(result.kind, "damaged")
        self.assertIn("report.pdf", result.message)
        self.assertFalse(result.retry)

    def test_a_stopped_daemon_is_started_once_and_the_load_retried(self):
        client = FakeClient(DaemonUnavailable("no socket"), PdfInfo(pages=4))
        result, started = load(client=client)
        self.assertEqual(started, [True])
        self.assertEqual(result.kind, "ready")
        self.assertEqual(client.calls, ["/tmp/report.pdf", "/tmp/report.pdf"])

    def test_a_daemon_that_cannot_be_started_gives_the_recovery_command(self):
        result, started = load(client=FakeClient(DaemonUnavailable("no socket")), ensure=DaemonUnavailable("fcd not found"))
        self.assertEqual(started, [True])
        self.assertEqual(result.kind, "daemon")
        self.assertIn("systemctl --user start fcd", result.hint)
        self.assertTrue(result.retry)

    def test_a_daemon_that_starts_but_still_does_not_answer(self):
        client = FakeClient(DaemonUnavailable("a"), DaemonUnavailable("b"))
        result, started = load(client=client)
        self.assertEqual(result.kind, "daemon")
        self.assertEqual(started, [True])  # tried to start it once, not in a loop
        self.assertEqual(len(client.calls), 2)

    def test_no_runtime_directory_is_a_daemon_problem_not_a_crash(self):
        def factory():
            raise DaemonUnavailable("XDG_RUNTIME_DIR is not set")

        result = load_document("/tmp/a.pdf", factory, lambda: (_ for _ in ()).throw(DaemonUnavailable("x")))
        self.assertEqual(result.kind, "daemon")

    def test_a_bad_request_is_a_failure_that_retrying_cannot_fix(self):
        result, _ = load(client=FakeClient(RequestError("bad_request", "input is not an absolute path")))
        self.assertEqual(result.kind, "failed")
        self.assertFalse(result.retry)
        self.assertIn("absolute path", result.message)

    def test_an_internal_error_is_a_failure_worth_retrying(self):
        result, _ = load(client=FakeClient(RequestError("internal", "something broke")))
        self.assertEqual(result.kind, "failed")
        self.assertTrue(result.retry)

    def test_a_protocol_mismatch_is_a_failure_not_a_daemon_outage(self):
        result, _ = load(client=FakeClient(ProtocolError("unsupported fcd protocol")))
        self.assertEqual(result.kind, "failed")
        self.assertIn("protocol", result.message)

    def test_a_document_with_no_pages_is_a_failure(self):
        result, _ = load(client=FakeClient(PdfInfo(pages=0)))
        self.assertEqual(result.kind, "failed")

    def test_the_file_is_named_by_its_base_name_only(self):
        result, _ = load(path="/home/someone/private/dir/report.pdf", client=FakeClient(RequestError("invalid_pdf", "x")))
        self.assertIn("report.pdf", result.message)
        self.assertNotIn("/home/someone", result.message)


if __name__ == "__main__":
    unittest.main()
