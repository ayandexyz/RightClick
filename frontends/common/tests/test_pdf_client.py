import json
import os
import socket
import sys
import tempfile
import threading
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
from fc_client.client import DaemonUnavailable, FcClient, PdfInfo, RequestError


class OneShotServer:
    """A daemon stand-in that answers one connection with a scripted handler."""

    def __init__(self, handler):
        self.directory = tempfile.TemporaryDirectory()
        self.path = os.path.join(self.directory.name, "fcd.sock")
        self.listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.listener.bind(self.path)
        self.listener.listen(1)
        self.handler = handler
        threading.Thread(target=self._run, daemon=True).start()

    def _run(self):
        try:
            conn, _ = self.listener.accept()
        except OSError:
            return
        with conn:
            self.handler(conn.makefile("rwb"))

    def close(self):
        self.listener.close()
        self.directory.cleanup()


def hello(file):
    assert json.loads(file.readline())["method"] == "hello"
    file.write(b'{"id":1,"result":{"server":"fcd","protocol":1}}\n')
    file.flush()


class PdfInfoTests(unittest.TestCase):
    def test_returns_pages_and_sizes(self):
        seen = {}

        def handler(file):
            hello(file)
            request = json.loads(file.readline())
            seen.update(request)
            file.write(b'{"id":2,"result":{"pages":3,"sizes":[{"w":612,"h":792,"rot":0},{"w":612,"h":792,"rot":90},{"w":595,"h":842,"rot":0}]}}\n')
            file.flush()

        server = OneShotServer(handler)
        try:
            info = FcClient(server.path).pdf_info("/tmp/a.pdf")
        finally:
            server.close()
        self.assertEqual(seen["method"], "pdf_info")
        self.assertEqual(seen["params"], {"file": "/tmp/a.pdf"})
        self.assertIsInstance(info, PdfInfo)
        self.assertEqual(info.pages, 3)
        self.assertEqual(info.sizes[1], {"w": 612, "h": 792, "rot": 90})

    def test_sizes_are_optional(self):
        def handler(file):
            hello(file)
            file.readline()
            file.write(b'{"id":2,"result":{"pages":12}}\n')
            file.flush()

        server = OneShotServer(handler)
        try:
            info = FcClient(server.path).pdf_info("/tmp/a.pdf")
        finally:
            server.close()
        self.assertEqual((info.pages, info.sizes), (12, []))

    def test_locked_and_damaged_files_keep_their_error_codes(self):
        for code in ("password_required", "invalid_pdf"):
            with self.subTest(code=code):
                def handler(file, code=code):
                    hello(file)
                    file.readline()
                    file.write(json.dumps({"id": 2, "error": {"code": code, "message": "x"}}).encode() + b"\n")
                    file.flush()

                server = OneShotServer(handler)
                try:
                    with self.assertRaises(RequestError) as raised:
                        FcClient(server.path).pdf_info("/tmp/a.pdf")
                finally:
                    server.close()
                self.assertEqual(raised.exception.code, code)

    def test_it_waits_longer_than_a_menu_query_for_a_slow_document(self):
        def handler(file):
            hello(file)
            file.readline()
            threading.Event().wait(0.8)  # longer than the 0.5 s menu budget
            file.write(b'{"id":2,"result":{"pages":900}}\n')
            file.flush()

        server = OneShotServer(handler)
        try:
            info = FcClient(server.path, request_timeout=0.5).pdf_info("/tmp/big.pdf")
        finally:
            server.close()
        self.assertEqual(info.pages, 900)

    def test_a_missing_socket_is_daemon_unavailable(self):
        with self.assertRaises(DaemonUnavailable):
            FcClient("/nonexistent/fcd.sock").pdf_info("/tmp/a.pdf")


if __name__ == "__main__":
    unittest.main()
