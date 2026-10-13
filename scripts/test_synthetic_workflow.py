"""Offline admission tests for the qualification-only workflow; no dispatch."""

import os
import re
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

WORKFLOW = Path(__file__).resolve().parents[1] / ".github/workflows/qualify-synthetic-image.yml"


def python_blocks():
    source = WORKFLOW.read_text()
    return [textwrap.dedent(block) for block in re.findall(r"python3 - <<'PY'\n(.*?)\n\s*PY(?:\n|$)", source, re.S)]


class QualificationAdmissionTests(unittest.TestCase):
    def admission(self, *, branch="refs/heads/main", current=None, changes=None, available=True):
        selected = "1" * 40
        repo = "synthetic-owner/synthetic-intake"
        run = {"head_sha": selected, "head_branch": "main", "event": "push", "conclusion": "success", "repository": {"full_name": repo}}
        run.update(changes or {})
        with tempfile.TemporaryDirectory(prefix="intake-workflow-test-") as directory:
            root = Path(directory)
            output = root / "output"
            output.touch()
            # The actual embedded workflow code calls a local fake gh executable.
            # No authentication, API request or GitHub workflow is initiated.
            fake = root / "gh"
            fake.write_text(f"#!{sys.executable}\n" + textwrap.dedent("""\
                import json, os, sys
                if os.environ['AVAILABLE'] != 'true':
                    print('SYNTHETIC_PRIVATE_SENTINEL', file=sys.stderr)
                    sys.exit(1)
                if sys.argv[1] != 'api':
                    sys.exit(1)
                if sys.argv[-1].endswith('git/ref/heads/main'):
                    print(json.dumps({'object': {'sha': os.environ['CURRENT']}}))
                else:
                    print(os.environ['RUNS'])
                """))
            fake.chmod(0o700)
            import json
            env = {"SOURCE": selected, "SELECTED_REF": branch, "GH_REPO": repo, "GITHUB_OUTPUT": str(output),
                   "PATH": str(root) + os.pathsep + os.defpath, "AVAILABLE": str(available).lower(),
                   "CURRENT": current or selected, "RUNS": json.dumps({"workflow_runs": [run]})}
            result = subprocess.run([sys.executable, "-c", python_blocks()[0]], env=env, capture_output=True, text=True, timeout=10)
            return result.returncode, result.stderr.strip(), output.read_text()

    def test_exact_current_main_success_only(self):
        code, failure, output = self.admission()
        self.assertEqual(code, 0)
        self.assertEqual(failure, "")
        self.assertEqual(output, "source=" + "1" * 40 + "\n")

    def test_stale_fork_pr_failed_or_unavailable_refuse(self):
        cases = [
            {"branch": "refs/tags/v1.0.0"},
            {"current": "2" * 40},
            {"changes": {"head_sha": "2" * 40}},
            {"changes": {"head_branch": "feature"}},
            {"changes": {"event": "pull_request"}},
            {"changes": {"conclusion": "failure"}},
            {"changes": {"repository": {"full_name": "synthetic-fork/intake"}}},
            {"available": False},
        ]
        for case in cases:
            with self.subTest(case=case):
                code, failure, output = self.admission(**case)
                self.assertEqual(code, 1)
                self.assertTrue(failure.startswith("SYNTHETIC_SOURCE_ADMISSION_UNAVAILABLE:"))
                self.assertNotIn("SYNTHETIC_PRIVATE_SENTINEL", failure)
                self.assertEqual(output, "")

    def test_manifest_agreement_is_required(self):
        for other in ["sha256:" + "1" * 64, "sha256:" + "2" * 64, "invalid"]:
            with self.subTest(other=other), tempfile.TemporaryDirectory(prefix="intake-workflow-test-") as directory:
                root = Path(directory)
                for builder, digest in [("a", "sha256:" + "1" * 64), ("b", other)]:
                    (root / builder).mkdir()
                    (root / builder / "digest").write_text(digest + "\n")
                result = subprocess.run([sys.executable, "-c", python_blocks()[2]], cwd=root, capture_output=True, text=True, timeout=10)
                if other == "sha256:" + "1" * 64:
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(result.stderr, "")
                else:
                    self.assertEqual(result.returncode, 1)
                    self.assertTrue(result.stderr.startswith("SYNTHETIC_REPRODUCTION_UNAVAILABLE:"))

    def test_workflow_has_no_mutating_publication_permissions(self):
        source = WORKFLOW.read_text()
        self.assertIn("matrix:\n        builder: [a, b]", source)
        self.assertIn('scripts/image-smoke-build.py', source)
        self.assertIn('--option substitute false --option builders ""', source)
        self.assertIn('scripts/synthetic-image-smoke.py image.tar.gz', source)
        for forbidden in ["contents: write", "packages: write", "id-token: write", "attestations: write", "actions: write", "docker push", "skopeo copy", "container create", "release create", "workflow_run:"]:
            self.assertNotIn(forbidden, source)


if __name__ == "__main__":
    unittest.main()
