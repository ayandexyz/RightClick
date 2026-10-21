import sys
import threading
import time
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
from pdf_studio.thumbnails import ThumbnailLoader


class FakeClient:
    def __init__(self, calls, responses=None, failures=0):
        self.calls = calls
        self.responses = responses or {}
        self.failures = failures

    def pdf_thumbnails(self, file, pages, width):
        self.calls.append((file, list(pages), width))
        if self.failures:
            self.failures -= 1
            raise RuntimeError("temporary failure")
        return {"pages": [{"page": page, "path": f"{file}-{page}.png", "w": width, "h": width} for page in pages]}


class ThumbnailLoaderTests(unittest.TestCase):
    def wait_for(self, predicate):
        deadline = time.monotonic() + 2
        while time.monotonic() < deadline:
            if predicate():
                return
            time.sleep(0.01)
        self.fail("timed out waiting for thumbnail loader")

    def test_batches_pages_in_visible_first_order(self):
        calls = []
        received = []
        loader = ThumbnailLoader(
            lambda: FakeClient(calls), "/tmp/report.pdf", 45, width=200,
            callback=lambda page, result: received.append(page),
        )
        try:
            loader.load(range(1, 46), visible_pages=[31, 32, 33])
            self.wait_for(lambda: len(received) == 45)
        finally:
            loader.stop()
        expected = [list(range(31, 34)) + list(range(1, 18)), list(range(18, 31)) + list(range(34, 41)), list(range(41, 46))]
        self.assertEqual([call[1] for call in calls], expected)
        self.assertEqual([len(call[1]) for call in calls], [20, 20, 5])
        self.assertEqual(received, sum(expected, []))

    def test_deduplicates_pages_already_queued_or_loaded(self):
        calls = []
        received = []
        loader = ThumbnailLoader(lambda: FakeClient(calls), "/tmp/a.pdf", 3, callback=lambda page, result: received.append(page))
        try:
            loader.load([1, 2])
            loader.load([2, 3])
            self.wait_for(lambda: len(received) == 3)
            loader.load([1, 2, 3])
            time.sleep(0.05)
        finally:
            loader.stop()
        self.assertEqual(sum((call[1] for call in calls), []), [1, 2, 3])

    def test_retries_a_failed_batch_once_and_reports_success(self):
        calls = []
        received = []
        clients = [FakeClient(calls, failures=1), FakeClient(calls)]
        loader = ThumbnailLoader(lambda: clients.pop(0), "/tmp/a.pdf", 2, callback=lambda page, result: received.append(page))
        try:
            loader.load([1, 2])
            self.wait_for(lambda: len(received) == 2)
        finally:
            loader.stop()
        self.assertEqual(len(calls), 2)

    def test_reports_error_after_one_retry(self):
        calls = []
        errors = []

        class BrokenClient(FakeClient):
            def pdf_thumbnails(self, file, pages, width):
                self.calls.append((file, list(pages), width))
                raise RuntimeError("broken")

        loader = ThumbnailLoader(lambda: BrokenClient(calls), "/tmp/a.pdf", 2, error_callback=lambda error, pages: errors.append((str(error), pages)))
        try:
            loader.load([1, 2])
            self.wait_for(lambda: len(errors) == 1)
        finally:
            loader.stop()
        self.assertEqual(len(calls), 2)
        self.assertEqual(errors, [("broken", [1, 2])])

    def test_stop_is_safe_and_suppresses_late_delivery(self):
        release = threading.Event()
        calls = []

        class BlockingClient(FakeClient):
            def pdf_thumbnails(self, file, pages, width):
                calls.append((file, list(pages), width))
                release.wait(1)
                return {"pages": [{"page": pages[0], "path": "late.png"}]}

        received = []
        loader = ThumbnailLoader(lambda: BlockingClient(calls), "/tmp/a.pdf", 1, callback=lambda page, result: received.append(page))
        loader.load([1])
        self.wait_for(lambda: calls)
        loader.stop()
        release.set()
        self.assertEqual(received, [])

    def test_accepts_a_list_response_and_sends_the_password_only_when_set(self):
        calls = []
        received = []

        class Client:
            def pdf_thumbnails(self, file, pages, width, **extra):
                calls.append((file, pages, width, extra))
                return [{"page": 1, "path": "one.png"}]

        for password, want in ((None, {}), ("secret", {"password": "secret"})):
            calls.clear()
            received.clear()
            loader = ThumbnailLoader(lambda: Client(), "/tmp/a.pdf", 1, callback=lambda page, result: received.append((page, result)), password=password)
            try:
                loader.load([1])
                self.wait_for(lambda: received)
            finally:
                loader.stop()
            self.assertEqual(calls, [("/tmp/a.pdf", [1], 200, want)])
            self.assertEqual(received[0][0], 1)
