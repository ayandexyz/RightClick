import json
import os
import socket
import time
from dataclasses import dataclass


class FcError(Exception):
    pass


class DaemonUnavailable(FcError):
    pass


class ProtocolError(FcError):
    pass


class RequestError(FcError):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code
        self.message = message


@dataclass
class Target:
    preset: str
    label: str
    format: str
    category: str
    enabled: bool
    reason: str = ""


class FcClient:
    def __init__(self, path, connect_timeout=0.3, request_timeout=0.5, convert_timeout=2.0, silence_timeout=60.0):
        self.path = path
        self.connect_timeout = connect_timeout
        self.request_timeout = request_timeout
        self.convert_timeout = convert_timeout
        self.silence_timeout = silence_timeout
        self._next_id = 1

    def _connect(self):
        if not os.path.exists(self.path):
            raise DaemonUnavailable(f"daemon socket does not exist: {self.path}")
        try:
            stat = os.stat(self.path)
            if stat.st_uid != os.getuid():
                raise DaemonUnavailable("daemon socket is not owned by the current user")
            sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            sock.settimeout(self.connect_timeout)
            sock.connect(self.path)
            return sock
        except DaemonUnavailable:
            raise
        except (OSError, socket.timeout) as exc:
            raise DaemonUnavailable(f"unable to connect to fcd: {exc}") from exc

    def _request(self, sock, file, method, params, timeout=None, deadline=None):
        request_id = self._next_id
        self._next_id += 1
        file.write((json.dumps({"id": request_id, "method": method, "params": params}) + "\n").encode())
        file.flush()
        if deadline is None:
            deadline = time.monotonic() + (timeout if timeout is not None else self.request_timeout)
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise DaemonUnavailable(f"timeout waiting for {method}")
            sock.settimeout(remaining)
            try:
                line = file.readline()
            except socket.timeout as exc:
                raise DaemonUnavailable(f"timeout waiting for {method}") from exc
            if not line:
                raise DaemonUnavailable("fcd closed the connection")
            message = json.loads(line)
            if "id" not in message:
                continue
            if message["id"] != request_id:
                continue
            if "error" in message:
                error = message["error"]
                raise RequestError(error.get("code", "internal"), error.get("message", "request failed"))
            return message.get("result", {})

    def _hello(self, sock, file, deadline=None):
        result = self._request(sock, file, "hello", {"client": "nautilus-extension", "protocol": 1}, deadline=deadline)
        if result.get("server") != "fcd" or result.get("protocol") != 1:
            raise ProtocolError("unsupported fcd protocol")

    def targets(self, paths, menu=False):
        with self._connect() as sock:
            with sock.makefile("rwb") as file:
                # hello and targets share one budget so the menu is never held up longer than request_timeout
                deadline = time.monotonic() + self.request_timeout
                self._hello(sock, file, deadline)
                params = {"files": paths}
                if menu:
                    # the daemon applies the user's menu policy; never filter targets client-side
                    params["menu"] = True
                result = self._request(sock, file, "targets", params, deadline=deadline)
        return [Target(**{key: target.get(key, "") for key in ("preset", "label", "format", "category", "enabled", "reason")}) for target in result.get("targets", [])]

    def convert(self, paths, preset):
        sock = self._connect()
        file = sock.makefile("rwb")
        try:
            self._hello(sock, file)
            result = self._request(sock, file, "convert", {"files": paths, "preset": preset}, self.convert_timeout)
            job_id = result.get("job")
            if not job_id:
                raise ProtocolError("convert response did not include a job")
            while True:
                sock.settimeout(self.silence_timeout)
                line = file.readline()
                if not line:
                    raise DaemonUnavailable("fcd closed the connection while converting")
                message = json.loads(line)
                if message.get("event") in ("job.updated", "job.finished"):
                    yield message
                if message.get("event") == "job.finished" and message.get("data", {}).get("id") == job_id:
                    return
        except socket.timeout as exc:
            raise DaemonUnavailable("no fcd job event for 60 seconds") from exc
        finally:
            file.close()
            sock.close()
