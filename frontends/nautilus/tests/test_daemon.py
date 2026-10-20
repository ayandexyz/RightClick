import os
import socket
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).parents[1]))
from fc_client import daemon


class DaemonTests(unittest.TestCase):
    def test_socket_path_requires_runtime_directory(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            with self.assertRaises(daemon.DaemonUnavailable):
                daemon.socket_path()

    def test_socket_path_matches_backend(self):
        with mock.patch.dict(os.environ, {"XDG_RUNTIME_DIR": "/run/user/1000"}):
            self.assertEqual(daemon.socket_path(), "/run/user/1000/file-converter/fcd.sock")

    def test_socket_ready_requires_accepting_socket(self):
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "fcd.sock")
            listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            listener.bind(path)
            listener.listen(1)
            try:
                self.assertTrue(daemon._socket_ready(path))
            finally:
                listener.close()

    def test_socket_ready_rejects_stale_path(self):
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "fcd.sock")
            Path(path).touch()
            self.assertFalse(daemon._socket_ready(path))


if __name__ == "__main__":
    unittest.main()
