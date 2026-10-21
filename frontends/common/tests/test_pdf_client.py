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


class RunOperationPasswordTests(unittest.TestCase):
    """The daemon reads a document's password from params, never the top level."""

    def run_with(self, **kwargs):
        seen = {}

        def handler(file):
            hello(file)
            seen.update(json.loads(file.readline()))
            file.write(b'{"id":2,"result":{"job":"j1"}}\n')
            file.write(b'{"event":"job.finished","data":{"id":"j1","state":"completed"}}\n')
            file.flush()

        server = OneShotServer(handler)
        try:
            list(FcClient(server.path).run_operation(["/tmp/a.pdf"], "pdf-extract", **kwargs))
        finally:
            server.close()
        return seen["params"]

    def test_password_goes_inside_params_beside_the_other_parameters(self):
        sent = self.run_with(params={"pages": "1"}, password="secret")
        self.assertEqual(sent["params"], {"pages": "1", "password": "secret"})
        self.assertNotIn("password", sent)

    def test_a_password_in_params_is_not_overridden(self):
        sent = self.run_with(params={"password": "typed"}, password="opened-with")
        self.assertEqual(sent["params"], {"password": "typed"})

    def test_no_password_means_none_is_sent(self):
        sent = self.run_with(params={"pages": "1"})
        self.assertEqual(sent["params"], {"pages": "1"})


class PdfThumbnailsTests(unittest.TestCase):
    def test_sends_password_only_when_given_for_thumbnails(self):
        seen = {}

        def handler(file):
            hello(file)
            seen.update(json.loads(file.readline()))
            file.write(b'{"id":2,"result":{"pages":[]}}\n')
            file.flush()

        server = OneShotServer(handler)
        try:
            FcClient(server.path).pdf_thumbnails("/tmp/a.pdf", [1], 150, password="secret")
        finally:
            server.close()
        self.assertEqual(seen["params"]["password"], "secret")

    def test_sends_file_pages_and_width_and_returns_the_items(self):
        seen = {}

        def handler(file):
            hello(file)
            seen.update(json.loads(file.readline()))
            file.write(b'{"id":2,"result":{"pages":[{"page":2,"path":"/c/x.png","w":200,"h":259}]}}\n')
            file.flush()

        server = OneShotServer(handler)
        try:
            items = FcClient(server.path).pdf_thumbnails("/tmp/a.pdf", [2], 150)
        finally:
            server.close()
        self.assertEqual(seen["method"], "pdf_thumbnails")
        self.assertEqual(seen["params"], {"file": "/tmp/a.pdf", "pages": [2], "width": 150})
        self.assertEqual(items, [{"page": 2, "path": "/c/x.png", "w": 200, "h": 259}])


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
