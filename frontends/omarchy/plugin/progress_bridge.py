#!/usr/bin/env python3
import json
import os
import socket
import sys
import time
import threading

runtime = os.environ.get("XDG_RUNTIME_DIR")
if not runtime:
    sys.exit(1)
path = os.path.join(runtime, "file-converter", "fcd.sock")
while True:
    try:
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
            sock.settimeout(5)
            sock.connect(path)
            stream = sock.makefile("rwb")
            def send(message):
                stream.write((json.dumps(message) + "\n").encode())
                stream.flush()
            send({"id": 1, "method": "hello", "params": {"client": "omarchy-progress", "protocol": 1}})
            if not stream.readline():
                raise OSError("fcd closed during hello")
            send({"id": 2, "method": "subscribe", "params": {}})
            if not stream.readline():
                raise OSError("fcd closed during subscribe")
            def read_commands():
                for command in sys.stdin:
                    try:
                        request = json.loads(command)
                        if request.get("method") != "cancel":
                            continue
                        send({"id": request.get("id", 0), "method": "cancel", "params": {"job": request.get("job", request.get("params", {}).get("job", ""))}})
                    except (ValueError, OSError):
                        pass
                os._exit(0)  # parent closed stdin: exit instead of leaking

            threading.Thread(target=read_commands, daemon=True).start()
            for line in stream:
                if line.strip():
                    sys.stdout.write(line.decode())
                    sys.stdout.flush()
    except (OSError, socket.timeout):
        time.sleep(3)
