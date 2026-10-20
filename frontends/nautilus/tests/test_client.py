import json
import os
import socket
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

# The client lives in frontends/common so the Studio can share it.
sys.path.insert(0, str(Path(__file__).parents[2] / "common"))
from fc_client.client import FcClient, DaemonUnavailable, ProtocolError, RequestError


class FakeServer:
    def __init__(self, handler):
        self.directory = tempfile.TemporaryDirectory()
        self.path = os.path.join(self.directory.name, "fcd.sock")
        self.handler = handler
        self.listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.listener.bind(self.path)
        self.listener.listen(1)
        self.thread = threading.Thread(target=self.run, daemon=True)
        self.thread.start()

    def run(self):
        conn, _ = self.listener.accept()
        with conn:
            file = conn.makefile("rwb")
            self.handler(file)

    def close(self):
        self.listener.close()
        self.directory.cleanup()


class ClientTests(unittest.TestCase):
    def test_targets_handshake_and_decodes_targets(self):
        def handler(file):
            self.assertEqual(json.loads(file.readline())["method"], "hello")
            file.write(b'{"id":1,"result":{"server":"fcd","protocol":1}}\n')
            file.flush()
            request = json.loads(file.readline())
            self.assertEqual(request["method"], "targets")
            file.write(b'{"id":2,"result":{"targets":[{"preset":"image-to-jpeg","label":"JPEG","format":"jpeg","enabled":true}]}}\n')
            file.flush()

        server = FakeServer(handler)
        try:
            client = FcClient(server.path, connect_timeout=0.2, request_timeout=0.5)
            targets = client.targets(["/tmp/a.png"])
            self.assertEqual(targets[0].preset, "image-to-jpeg")
        finally:
            server.close()

    def test_targets_requests_menu_policy_only_when_asked(self):
        seen = []

        def handler(file):
            for _ in range(2):
                file.readline()
                file.write(b'{"id":1,"result":{"server":"fcd","protocol":1}}\n')
                file.flush()
                seen.append(json.loads(file.readline())["params"])
                file.write(b'{"id":2,"result":{"targets":[]}}\n')
                file.flush()

        server = FakeServer(handler)
        try:
            FcClient(server.path).targets(["/tmp/a.png"], menu=True)
            self.assertEqual(seen[0], {"files": ["/tmp/a.png"], "menu": True})
        finally:
            server.close()

    def test_targets_default_request_has_no_menu_flag(self):
        seen = []

        def handler(file):
            file.readline()
            file.write(b'{"id":1,"result":{"server":"fcd","protocol":1}}\n')
            file.flush()
            seen.append(json.loads(file.readline())["params"])
            file.write(b'{"id":2,"result":{"targets":[]}}\n')
            file.flush()

        server = FakeServer(handler)
        try:
            FcClient(server.path).targets(["/tmp/a.png"])
            self.assertEqual(seen[0], {"files": ["/tmp/a.png"]})
        finally:
            server.close()

    def test_protocol_mismatch_is_rejected(self):
        def handler(file):
            file.readline()
            file.write(b'{"id":1,"result":{"server":"fcd","protocol":2}}\n')
            file.flush()

        server = FakeServer(handler)
        try:
            with self.assertRaises(ProtocolError):
                FcClient(server.path).targets(["/tmp/a.png"])
        finally:
            server.close()

    def test_convert_yields_finished_event(self):
        def handler(file):
            file.readline()
            file.write(b'{"id":1,"result":{"server":"fcd","protocol":1}}\n')
            file.flush()
            file.readline()
            file.write(b'{"id":2,"result":{"job":"j1"}}\n')
            file.write(b'{"event":"job.updated","data":{"id":"j1","state":"running"}}\n')
            file.write(b'{"event":"job.finished","data":{"id":"j1","state":"completed","files":[]}}\n')
            file.flush()

        server = FakeServer(handler)
        try:
            events = list(FcClient(server.path).convert(["/tmp/a.png"], "image-to-jpeg"))
            self.assertEqual(events[-1]["event"], "job.finished")
            self.assertEqual(events[-1]["data"]["state"], "completed")
        finally:
            server.close()

    def test_request_errors_map(self):
        def handler(file):
            file.readline()
            file.write(b'{"id":1,"result":{"server":"fcd","protocol":1}}\n')
            file.flush()
            file.readline()
            file.write(b'{"id":2,"error":{"code":"unsupported","message":"nope"}}\n')
            file.flush()

        server = FakeServer(handler)
        try:
            with self.assertRaises(RequestError) as raised:
                FcClient(server.path).targets(["/tmp/a.png"])
            self.assertEqual(raised.exception.code, "unsupported")
        finally:
            server.close()

    def test_hello_and_targets_share_one_time_budget(self):
        def handler(file):
            file.readline()
            time.sleep(0.3)
            file.write(b'{"id":1,"result":{"server":"fcd","protocol":1}}\n')
            file.flush()
            file.readline()
            time.sleep(0.3)
            file.write(b'{"id":2,"result":{"targets":[]}}\n')
            file.flush()

        server = FakeServer(handler)
        try:
            started = time.monotonic()
            with self.assertRaises(DaemonUnavailable):
                FcClient(server.path, request_timeout=0.5).targets(["/tmp/a.png"])
            self.assertLess(time.monotonic() - started, 0.58)
        finally:
            server.close()

    def test_missing_runtime_socket_is_unavailable(self):
        with self.assertRaises(DaemonUnavailable):
            FcClient("/definitely/missing/socket").targets(["/tmp/a.png"])


if __name__ == "__main__":
    unittest.main()
