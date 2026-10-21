"""GTK-independent apply job state and controller for PDF Studio."""

from dataclasses import dataclass, field
import queue
import threading
from types import MappingProxyType

from fc_client.messages import friendly_error

from .output_panel import build_output_options


@dataclass(frozen=True)
class JobState:
    """Immutable state exposed to the UI while an apply job runs."""

    status: str = "pending"
    progress: float = 0.0
    files: tuple = field(default_factory=tuple)
    error: str = ""
    job_id: str = ""


def _freeze_files(files):
    return tuple(MappingProxyType(dict(item)) for item in (files or ()))


def reduce_job_event(state, event):
    """Reduce one daemon job event into a new immutable UI state."""
    data = event.get("data") or {}
    event_name = event.get("event", "")
    status = data.get("state", state.status)
    progress = data.get("progress", state.progress)
    try:
        progress = max(0.0, min(1.0, float(progress)))
    except (TypeError, ValueError):
        progress = state.progress
    files = state.files
    if "files" in data:
        files = _freeze_files(data["files"])
    if event_name == "job.finished":
        if status == "cancelled":
            status = "cancelled"
        elif status == "failed":
            status = "failed"
        elif any(item.get("state") in ("failed", "skipped") for item in files):
            status = "completed-with-errors"
        else:
            status = "completed"
    return JobState(
        status=status,
        progress=progress,
        files=files,
        error=data.get("error", state.error) or state.error,
        job_id=str(data.get("id", state.job_id) or state.job_id),
    )


def build_output_options(folder, conflict):
    """Return optional output fields accepted by the daemon."""
    options = {}
    if folder:
        options["output_dir"] = folder
    if conflict:
        options["on_conflict"] = conflict
    return options


def friendly_apply_error(code, message):
    return friendly_error(code, message)


def _cancel(client, job_id):
    for name in ("cancel_job", "cancel"):
        method = getattr(client, name, None)
        if method is not None:
            method(job_id)
            return


def run_apply(client_factory, request, on_event, cancel_event):
    """Run an apply request and report immutable states.

    The operation connection is owned by a worker thread.  Cancellation uses a
    second client connection when the client provides ``cancel_job``/``cancel``;
    the operation stream is still drained until the daemon reports its result.
    """
    client = client_factory()
    events = queue.Queue()

    def produce():
        try:
            for event in client.run_operation(
                request.get("files", []), request["op"], request.get("params"),
                output_dir=request.get("output_dir"), on_conflict=request.get("on_conflict"),
            ):
                events.put(("event", event))
        except Exception as error:  # surfaced as UI state, not a worker traceback
            events.put(("error", error))
        finally:
            events.put(("done", None))

    threading.Thread(target=produce, daemon=True).start()
    state = JobState()
    cancel_sent = False
    while True:
        if cancel_event.is_set() and not cancel_sent and state.job_id:
            try:
                _cancel(client_factory(), state.job_id)
            except Exception:
                pass
            cancel_sent = True
        try:
            kind, value = events.get(timeout=0.05)
        except queue.Empty:
            continue
        if kind == "event":
            state = reduce_job_event(state, value)
            on_event(state)
            if state.status in ("completed", "completed-with-errors", "failed", "cancelled"):
                return state
        elif kind == "error":
            code = getattr(value, "code", "")
            message = getattr(value, "message", str(value))
            state = JobState("failed", state.progress, state.files, friendly_apply_error(code, message), state.job_id)
            on_event(state)
            return state
        elif kind == "done":
            if state.status not in ("completed", "completed-with-errors", "failed", "cancelled"):
                state = JobState("failed", state.progress, state.files, "The apply job ended unexpectedly", state.job_id)
                on_event(state)
            return state
