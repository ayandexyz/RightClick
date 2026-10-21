"""Reading a document's basics from the daemon and saying what happened.

``load_document`` turns every way that can go wrong into a ``LoadResult`` the
window can show, so the GTK code never interprets exceptions. The client and the
daemon starter are passed in, which keeps this testable without a daemon.
"""

import os
from dataclasses import dataclass

from fc_client.client import DaemonUnavailable, FcError, RequestError
from fc_client.messages import friendly_error

DAEMON_HINT = "Start it with: systemctl --user start fcd"


@dataclass(frozen=True)
class LoadResult:
    kind: str  # "ready", "locked", "damaged", "daemon" or "failed"
    pages: int = 0
    sizes: tuple = ()
    title: str = ""
    message: str = ""
    hint: str = ""
    retry: bool = False  # whether trying again could plausibly work
    password_rejected: bool = False  # locked, and the password given did not open it


def load_document(path, make_client, ensure_daemon, password=None):
    """Ask the daemon about ``path``, starting the daemon once if it is down."""
    name = os.path.basename(path)
    started = False
    while True:
        try:
            extra = {} if password is None else {"password": password}
            info = make_client().pdf_info(path, **extra)
        except DaemonUnavailable:
            if started:
                return _daemon_down()
            started = True
            try:
                ensure_daemon()
            except DaemonUnavailable:
                return _daemon_down()
            continue
        except RequestError as error:
            return _from_request_error(error, name)
        except FcError as error:
            return LoadResult("failed", title="Could not open the PDF", message=f"{name}: {error}", retry=True)
        if info.pages < 1:
            return LoadResult("failed", title="Could not open the PDF", message=f"{name} has no pages.")
        return LoadResult("ready", pages=info.pages, sizes=tuple(info.sizes))


def _daemon_down():
    return LoadResult(
        "daemon",
        title="File Converter is not running",
        message="PDF Studio works through the File Converter service, which did not answer.",
        hint=DAEMON_HINT,
        retry=True,
    )


def _from_request_error(error, name):
    if error.code in ("password_required", "password_incorrect"):
        return LoadResult(
            "locked",
            title="This PDF is password-protected",
            message=("That password did not work." if error.code == "password_incorrect" else f"{name} is locked and needs a password."),
            hint="Enter the password to unlock this PDF.",
            password_rejected=error.code == "password_incorrect",
        )
    if error.code == "invalid_pdf":
        return LoadResult(
            "damaged",
            title="This PDF can't be read",
            message=f"{name} looks damaged or is not a valid PDF.",
        )
    return LoadResult(
        "failed",
        title="Could not open the PDF",
        message=friendly_error(error.code, error.message, name),
        retry=error.code != "bad_request",
    )
