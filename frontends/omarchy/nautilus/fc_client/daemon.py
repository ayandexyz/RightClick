import os
import shutil
import socket
import time

from .client import DaemonUnavailable


def socket_path():
    runtime = os.environ.get("XDG_RUNTIME_DIR")
    if not runtime:
        raise DaemonUnavailable("XDG_RUNTIME_DIR is not set")
    path = os.path.join(runtime, "file-converter", "fcd.sock")
    if len(path.encode()) >= 108:
        raise DaemonUnavailable("fcd socket path is too long for Unix sockets")
    return path


def find_fcd():
    configured = os.environ.get("FC_FCD")
    if configured:
        if os.path.isabs(configured) and os.path.isfile(configured) and os.access(configured, os.X_OK):
            return configured
        return None
    local = os.path.expanduser("~/.local/bin/fcd")
    if os.path.isfile(local) and os.access(local, os.X_OK):
        return local
    return shutil.which("fcd")


def _socket_ready(path):
    if not os.path.exists(path):
        return False
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(0.05)
    try:
        sock.connect(path)
        return True
    except OSError:
        return False
    finally:
        sock.close()


def ensure_daemon():
    """Start fcd detached when possible and wait briefly for its socket."""
    path = socket_path()
    executable = find_fcd()
    if not executable:
        raise DaemonUnavailable("fcd executable was not found")
    import subprocess
    subprocess.Popen([executable, "serve"], start_new_session=True, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for _ in range(30):
        if _socket_ready(path):
            return path
        time.sleep(0.05)
    raise DaemonUnavailable("fcd did not create its socket")
