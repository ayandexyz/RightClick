import sys
import threading
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))

from pdf_studio.apply import JobState, build_output_options, reduce_job_event, run_apply


class JobReducerTests(unittest.TestCase):
    def test_running_progress_and_completion_are_frozen_states(self):
        state = reduce_job_event(JobState(), {"event": "job.updated", "data": {"state": "running", "progress": 0.4}})
        self.assertEqual((state.status, state.progress), ("running", 0.4))
        self.assertTrue(state.__dataclass_params__.frozen)
        state = reduce_job_event(state, {"event": "job.finished", "data": {"state": "completed", "files": [{"state": "done"}]}})
        self.assertEqual(state.status, "completed")
        self.assertEqual(state.files, ({"state": "done"},))

    def test_finished_errors_and_cancelled_states(self):
        failed = reduce_job_event(JobState(), {"event": "job.finished", "data": {"state": "completed", "files": [{"state": "failed", "error": {"message": "bad"}}]}})
        self.assertEqual(failed.status, "completed-with-errors")
        cancelled = reduce_job_event(JobState(), {"event": "job.finished", "data": {"state": "cancelled"}})
        self.assertEqual(cancelled.status, "cancelled")


class ApplyTests(unittest.TestCase):
    def test_output_options_map_to_daemon_fields(self):
        self.assertEqual(build_output_options("/tmp/out", "rename"), {"output_dir": "/tmp/out", "on_conflict": "rename"})
        self.assertEqual(build_output_options("", "overwrite"), {"on_conflict": "overwrite"})

    def test_a_daemon_error_during_apply_uses_the_shared_wording(self):
        class Locked(Exception):
            code, message = "password_required", "secret"

        class Client:
            def run_operation(self, *args, **kwargs):
                raise Locked()
                yield

        states = []
        run_apply(lambda: Client(), {"files": ["a.pdf"], "op": "pdf-edit"}, states.append, threading.Event())
        self.assertEqual(states[-1].status, "failed")
        self.assertIn("passwords are not supported", states[-1].error)

    def test_run_apply_reports_states_and_cancels(self):
        cancelled = threading.Event()
        seen = []
        states = []

        class Client:
            def run_operation(self, paths, op, params=None, **kwargs):
                seen.append((paths, op, params, kwargs))
                yield {"event": "job.updated", "data": {"id": "job-1", "state": "running", "progress": 0.1}}
                while not self.cancelled:
                    yield {"event": "job.updated", "data": {"id": "job-1", "state": "running", "progress": 0.2}}
                yield {"event": "job.finished", "data": {"id": "job-1", "state": "cancelled"}}

            def __init__(self):
                self.cancelled = False

            def cancel_job(self, job_id):
                seen.append(("cancel", job_id))
                self.cancelled = True

        client = Client()
        def report(state):
            states.append(state)
            if state.status == "running":
                cancelled.set()
        run_apply(lambda: client, {"files": ["a.pdf"], "op": "pdf-edit", "params": {"edit": []}}, report, cancelled)
        self.assertEqual(states[0].status, "running")
        self.assertIn(("cancel", "job-1"), seen)
        self.assertEqual(states[-1].status, "cancelled")


if __name__ == "__main__":
    unittest.main()
