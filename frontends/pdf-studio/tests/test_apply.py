import sys
import threading
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))

from pdf_studio.apply import JobState, build_output_options, friendly_apply_error, reduce_job_event, run_apply


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

    def test_friendly_error_uses_shared_wording(self):
        self.assertIn("passwords are not supported", friendly_apply_error("password_required", "secret"))

    def test_run_apply_reports_states_and_cancels(self):
        cancelled = threading.Event()
        seen = []

        class Client:
            def run_operation(self, paths, op, params=None, **kwargs):
                seen.append((paths, op, params, kwargs))
                yield {"event": "job.updated", "data": {"state": "running", "progress": 0.1}}
                cancelled.set()
                yield {"event": "job.finished", "data": {"state": "cancelled"}}

        run_apply(lambda: Client(), {"files": ["a.pdf"], "op": "pdf-edit", "params": {"edit": []}}, seen.append, cancelled)
        self.assertEqual(seen[0][1], "pdf-edit")
        self.assertEqual(seen[-1].status, "cancelled")


if __name__ == "__main__":
    unittest.main()
