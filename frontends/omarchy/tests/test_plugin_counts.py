import json
import shutil
import subprocess
import unittest
from pathlib import Path

COUNTS = Path(__file__).parents[1] / "plugin" / "counts.js"


def run_js(expression):
    source = "\n".join(line for line in COUNTS.read_text().splitlines() if not line.startswith(".pragma"))
    result = subprocess.run(
        ["node", "-e", source + f"\nconsole.log(JSON.stringify({expression}))"],
        capture_output=True, text=True, check=True,
    )
    return json.loads(result.stdout)


@unittest.skipUnless(shutil.which("node"), "node is required to run the plugin's JavaScript")
class CountsTests(unittest.TestCase):
    def test_summarize_counts_each_file_state(self):
        files = [{"state": s} for s in ("done", "done", "skipped", "failed", "cancelled", "cancelled", "cancelled", "running", "pending")]
        self.assertEqual(
            run_js(f"summarize({json.dumps(files)})"),
            {"done": 2, "skipped": 1, "failed": 1, "cancelled": 3, "active": 2},
        )

    def test_summarize_handles_missing_files(self):
        self.assertEqual(run_js("summarize(undefined)"), {"done": 0, "skipped": 0, "failed": 0, "cancelled": 0, "active": 0})

    def test_add_sums_counts_across_jobs(self):
        a = {"done": 1, "skipped": 0, "failed": 2, "cancelled": 0, "active": 1}
        b = {"done": 3, "skipped": 1, "failed": 0, "cancelled": 4, "active": 0}
        self.assertEqual(run_js(f"add({json.dumps(a)}, {json.dumps(b)})"), {"done": 4, "skipped": 1, "failed": 2, "cancelled": 4, "active": 1})

    def test_describe_lists_only_non_zero_counts_in_fixed_order(self):
        counts = {"done": 3, "skipped": 0, "failed": 1, "cancelled": 2, "active": 5}
        self.assertEqual(run_js(f"describe({json.dumps(counts)})"), "3 done · 1 failed · 2 cancelled")

    def test_describe_is_empty_when_nothing_finished(self):
        self.assertEqual(run_js('describe({"done":0,"skipped":0,"failed":0,"cancelled":0,"active":2})'), "")


if __name__ == "__main__":
    unittest.main()
