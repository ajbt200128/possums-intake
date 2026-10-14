"""Offline synthetic first-publication tests: no network, registry or dispatch."""
import copy
import hashlib
import importlib.util
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
import urllib.error
from email.message import Message
from pathlib import Path
from typing import Any
from unittest.mock import patch

import release as r

spec = importlib.util.spec_from_file_location("synthetic_publication", Path(__file__).with_name("synthetic-publication.py"))
if spec is None or spec.loader is None:
    raise AssertionError("fixture module unavailable")
p = importlib.util.module_from_spec(spec)
spec.loader.exec_module(p)
SOURCE = "1" * 40
WORKFLOW = Path(__file__).resolve().parents[1] / ".github/workflows/publish-synthetic-image.yml"


def run(workflow, number, attempt=1):
    return {"id": number, "run_attempt": attempt, "repository": {"full_name": p.REPO},
            "head_repository": {"full_name": p.REPO}, "head_sha": SOURCE, "head_branch": "main",
            "path": ".github/workflows/" + workflow, "event": "workflow_dispatch",
            "status": "completed", "conclusion": "success"}


def job(name, runner, steps):
    return {"name": name, "runner_id": runner, "labels": ["ubuntu-latest"], "run_id": 20,
            "run_attempt": 1, "head_sha": SOURCE, "status": "completed", "conclusion": "success",
            "steps": [{"name": step, "status": "completed", "conclusion": "success"} for step in steps]}


class FakeAPI:
    def __init__(self):
        self.reads = []
        self.ctx = {"source": SOURCE, "run": 30, "qualification_run": 20, "qualification_attempt": 1}
        self.repo_data: dict[str, Any] = {"id": 99, "full_name": p.REPO, "private": True,
                                         "owner": {"login": p.OWNER}, "default_branch": "main"}
        self.current = dict(run(p.WORKFLOW, 30), status="in_progress", actor={"login": p.OWNER},
                            triggering_actor={"login": p.OWNER})
        self.checks = [run("check.yml", 10)]
        self.qualified = run("qualify-synthetic-image.yml", 20)
        steps = ["Prewarm only external pinned dependencies", "Refuse valid application and image-stage outputs",
                 "Independently build native Linux image", "Record immutable archive and manifest identities"]
        self.jobs = [job("source", 1, ["Admit successful exact-current-main checks"]),
                     job("build (a)", 2, steps + ["Qualify this exact A image, including disposable startup"]),
                     job("build (b)", 3, steps), job("compare", 4, ["Require independent matching manifests"])]
        self.artifacts = []
        for number, name in enumerate([f"intake-synthetic-20-1-{SOURCE}-a", f"intake-synthetic-20-1-{SOURCE}-b",
                                       f"intake-synthetic-archive-20-1-{SOURCE}"], 100):
            self.artifacts.append({"id": number, "name": name, "expired": False,
                                   "workflow_run": {"id": 20, "head_sha": SOURCE, "head_branch": "main",
                                                    "repository_id": 99, "head_repository_id": 99}})
        self.main = SOURCE
        self.copied = False
        self.before: tuple[int, Any] = (404, None)
        self.after = (200, {"name": p.PACKAGE, "package_type": "container", "owner": {"login": p.OWNER},
                            "visibility": "private", "repository": {"full_name": p.REPO, "private": True}})
        self.on_read: Any = None

    def repo(self, suffix=""):
        self.reads.append(suffix)
        if self.on_read:
            self.on_read(suffix)
        if suffix == "":
            value = self.repo_data
        elif suffix == "/git/ref/heads/main":
            value = {"object": {"sha": self.main}}
        elif suffix == "/actions/runs/30":
            value = self.current
        elif suffix.startswith("/actions/runs/10"):
            value = self.checks[0]
        elif suffix.startswith("/actions/runs/20"):
            value = self.qualified
        else:
            raise AssertionError("unexpected fake read")
        return copy.deepcopy(value)

    def pages(self, suffix, field):
        self.reads.append(suffix)
        return copy.deepcopy({"workflow_runs": self.checks, "jobs": self.jobs, "artifacts": self.artifacts}[field])

    def package(self):
        self.reads.append("package")
        return copy.deepcopy(self.after if self.copied else self.before)


class Fixture:
    def __init__(self, root):
        self.root = Path(root)
        self.api = FakeAPI()
        self.calls = []
        self.copy_error: BaseException | None = None
        self.post_error: BaseException | None = None
        self.post_raw: bytes | None = None
        self.authpath: Any = None
        self.archive = b"isolated synthetic archive bytes"
        self.archive_hash = hashlib.sha256(self.archive).hexdigest()
        self.config = json.dumps({"os": "linux", "architecture": "amd64"}).encode()
        self.config_hash = "sha256:" + hashlib.sha256(self.config).hexdigest()
        self.raw = json.dumps({"schemaVersion": 2, "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
                               "config": {"digest": self.config_hash}}).encode()
        self.digest = "sha256:" + hashlib.sha256(self.raw).hexdigest()
        for name in ("a", "b", "archive"):
            (self.root / name).mkdir()
        (self.root / "archive/image.tar.gz").write_bytes(self.archive)
        for name in ("a", "b"):
            (self.root / name / "digest").write_text(self.digest + "\n")
            (self.root / name / "archive-sha256").write_text(self.archive_hash + "\n")
        self.q = {"image": self.digest, "config_identity": self.config_hash, "archive_sha256": self.archive_hash,
                  "platform": "linux/amd64", "cases": dict.fromkeys(r.QUALIFICATIONS, True),
                  "previous_release_compatibility": "unknown", "native_operator": dict.fromkeys(p.NATIVE_CASES, True)}
        self.q["native_operator"]["browser_inside_image"] = "not_run"
        self.synthetic = {"config_identity": self.config_hash, "archive_sha256": self.archive_hash,
                          "platform": "linux/amd64", "previous_release_compatibility": "unknown",
                          "synthetic": dict.fromkeys(p.SYNTHETIC_CASES, True)}
        self.save()

    def save(self):
        (self.root / "a/qualification.json").write_text(json.dumps(self.q))
        (self.root / "a/synthetic-qualification.json").write_text(json.dumps(self.synthetic))

    def tool(self, args, code, timeout=120):
        self.calls.append(args)
        if args == ["skopeo", "--version"]:
            return b"skopeo version 1.24.1\n"
        if args[1] == "copy":
            self.authpath = Path(args[args.index("--authfile") + 1])
            if self.authpath.stat().st_mode & 0o777 != 0o600 or self.authpath.parent.stat().st_mode & 0o777 != 0o700:
                raise AssertionError("auth fixture permissions")
            self.api.copied = True
            if self.copy_error:
                raise self.copy_error
            return b"opaque upstream text never retained"
        if "--authfile" in args:
            if self.post_error:
                raise self.post_error
            return self.raw if self.post_raw is None else self.post_raw
        return self.config if "--config" in args else self.raw

    def publication(self, **kwargs):
        return p.Publication(self.api, self.api.ctx, self.root, {"a": 100, "b": 101, "archive": 102},
                             self.tool, context_reader=lambda: self.api.ctx, **kwargs)


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="intake-publication-test-")
        self.addCleanup(self.tmp.cleanup)
        self.f = Fixture(self.tmp.name)
        self.env = patch.dict(os.environ, {"GITHUB_TOKEN": "SYNTHETIC_SECRET_SENTINEL", "GITHUB_ACTOR": p.OWNER})
        self.env.start()
        self.addCleanup(self.env.stop)

    def refused(self, operation, code=None, outcome="no_effect"):
        with self.assertRaises(r.Stop) as caught:
            operation()
        if code:
            self.assertEqual(caught.exception.code, code)
        self.assertEqual(caught.exception.outcome, outcome)
        self.assertNotIn("SYNTHETIC_SECRET_SENTINEL", json.dumps(r.diagnostic(caught.exception)))
        self.assertNotIn("hostile", json.dumps(r.diagnostic(caught.exception)))
        return caught.exception

    def test_single_copy_closed_receipt_and_cleanup(self):
        result = self.f.publication().execute()
        self.assertEqual(result, {"publicSourceSHA": SOURCE, "imageDigest": self.f.digest, "archiveSHA": self.f.archive_hash})
        copies = [args for args in self.f.calls if args[1] == "copy"]
        self.assertEqual(len(copies), 1)
        self.assertIn("--preserve-digests", copies[0])
        self.assertEqual(copies[0][copies[0].index("--retry-times") + 1], "0")
        self.assertEqual(copies[0][-1], "docker://" + p.REGISTRY + ":" + SOURCE)
        self.assertNotIn("SYNTHETIC_SECRET_SENTINEL", json.dumps(self.f.calls))
        self.assertNotIn("SYNTHETIC_SECRET_SENTINEL", json.dumps(result))
        self.assertFalse(self.f.authpath.exists())
        self.assertFalse(self.f.authpath.parent.exists())
        self.assertEqual(self.f.api.reads.count("package"), 3)
        self.assertEqual(self.f.api.reads.count("/git/ref/heads/main"), 3)

    def test_unknown_copy_never_retries_even_if_may_have_published(self):
        self.f.copy_error = RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL 404")
        instance = self.f.publication()
        self.refused(instance.execute, "synthetic_copy", "unknown")
        self.refused(instance.execute, "partial")
        self.assertEqual(sum(args[1] == "copy" for args in self.f.calls), 1)
        self.assertFalse(self.f.authpath.parent.exists())

    def test_completed_copy_later_fault_keeps_completed(self):
        self.f.post_error = RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL")
        self.refused(self.f.publication().execute, "synthetic_private", "completed")
        self.assertFalse(self.f.authpath.parent.exists())

    def test_interrupted_copy_never_replayed_and_cleanup_join_contract(self):
        self.f.copy_error = KeyboardInterrupt()
        instance = self.f.publication()
        self.refused(instance.execute, "synthetic_copy", "unknown")
        self.refused(instance.execute, "partial")
        self.assertFalse(self.f.authpath.parent.exists())

    def test_post_package_http_failure_retains_status_and_completed(self):
        original = self.f.api.package
        def package():
            if self.f.api.copied:
                raise r.Stop("synthetic_namespace", status=403)
            return original()
        with patch.object(self.f.api, "package", side_effect=package):
            error = self.refused(self.f.publication().execute, "synthetic_private", "completed")
            self.assertEqual(r.diagnostic(error)["status"], 403)

    def test_actual_private_autolink_digest_required(self):
        self.f.api.copied = True
        changes = [("visibility", "public"), ("visibility", "internal"), ("package_type", "npm"),
                   ("repository", {"full_name": "other/repo", "private": True}), ("repository", None),
                   ("repository", {"full_name": p.REPO, "private": False}), ("owner", {"login": "other"})]
        for key, value in changes:
            with self.subTest(key=key, value=value):
                self.f.api.after[1][key] = value
                self.refused(lambda: p.boundary("synthetic_private", lambda: p.private_gate(self.f.api,
                            {"publicSourceSHA": SOURCE, "imageDigest": self.f.digest}, Path("unused"), self.f.tool)),
                             "synthetic_private")
                self.f.api.after[1][key] = {"visibility": "private", "package_type": "container",
                    "repository": {"full_name": p.REPO, "private": True}, "owner": {"login": p.OWNER}}[key]
        self.f.api.copied = False
        self.f.post_raw = self.f.raw + b"changed"
        self.refused(self.f.publication().execute, "synthetic_private", "completed")

    def test_visible_existing_package_refuses_all_visibility_and_link_states(self):
        for visibility in ("private", "public", "internal"):
            for repository in (None, {"full_name": p.REPO}):
                self.f.api.before = (200, {"visibility": visibility, "repository": repository})
                self.refused(self.f.publication().execute, "synthetic_namespace")
        self.assertFalse(any(args[1] == "copy" for args in self.f.calls))

    def test_404_is_inaccessible_not_programmatic_absence(self):
        # Owner confirmation is mandatory elsewhere; this gate intentionally
        # cannot tell absent from hidden. No tag probe/error-text inference exists.
        p.namespace_gate(self.f.api)
        for status in (401, 403, 429, 500, 503, 0):
            self.f.api.before = (status, None)
            self.refused(lambda: p.namespace_gate(self.f.api), "synthetic_namespace")
        self.assertFalse(any(args[1] == "copy" for args in self.f.calls))

    def test_source_and_ci_refusals(self):
        vectors = [(self.f.api.repo_data, "private", False), (self.f.api.repo_data, "full_name", "fork/repo"),
                   (self.f.api.current, "run_attempt", 2), (self.f.api.current, "actor", {"login": "other"}),
                   (self.f.api.current, "triggering_actor", {"login": "other"}), (self.f.api.current, "event", "pull_request"),
                   (self.f.api.current, "head_repository", {"full_name": "fork/repo"}),
                   (self.f.api.current, "head_sha", "2" * 40)]
        for target, key, value in vectors:
            original = target[key]
            target[key] = value
            self.refused(lambda: p.admit(self.f.api, self.f.api.ctx))
            target[key] = original
        self.f.api.main = "2" * 40
        self.refused(lambda: p.admit(self.f.api, self.f.api.ctx), "stale")
        self.f.api.main = SOURCE
        for key, value in (("head_sha", "2" * 40), ("event", "pull_request"), ("path", ".github/workflows/other.yml"),
                           ("head_repository", {"full_name": "fork/repo"}), ("head_branch", "feature"),
                           ("conclusion", "failure"), ("status", "in_progress")):
            original = self.f.api.checks[0][key]
            self.f.api.checks[0][key] = value
            self.refused(lambda: p.admit(self.f.api, self.f.api.ctx), "checks")
            self.f.api.checks[0][key] = original

    def test_qualification_exact_attempt_and_source(self):
        for key, value in (("id", 21), ("run_attempt", 2), ("head_sha", "2" * 40), ("event", "push"),
                           ("conclusion", "failure"), ("path", ".github/workflows/check.yml"),
                           ("head_repository", {"full_name": "fork/repo"})):
            original = self.f.api.qualified[key]
            self.f.api.qualified[key] = value
            self.refused(lambda: p.admit(self.f.api, self.f.api.ctx))
            self.f.api.qualified[key] = original

    def test_independent_native_runner_and_fresh_steps(self):
        for key, value in (("runner_id", 2), ("runner_id", 0), ("labels", ["self-hosted", "ubuntu-latest"]),
                           ("labels", ["macos-latest"]), ("run_attempt", 2), ("head_sha", "2" * 40),
                           ("run_id", 21), ("conclusion", "failure")):
            original = self.f.api.jobs[2][key]
            self.f.api.jobs[2][key] = value
            self.refused(lambda: p.admit(self.f.api, self.f.api.ctx), "build")
            self.f.api.jobs[2][key] = original
        for job in self.f.api.jobs:
            for step in job["steps"]:
                step["conclusion"] = "skipped"
                self.refused(lambda: p.admit(self.f.api, self.f.api.ctx), "build")
                step["conclusion"] = "success"

    def test_artifacts_bound_run_attempt_source_repo_no_caller_ids(self):
        for item in self.f.api.artifacts:
            for target, key, value in ((item, "expired", True), (item, "id", 0), (item, "name", "unbound"),
                        (item["workflow_run"], "id", 21), (item["workflow_run"], "head_sha", "2" * 40),
                        (item["workflow_run"], "head_branch", "feature"), (item["workflow_run"], "repository_id", 100),
                        (item["workflow_run"], "head_repository_id", 100)):
                original = target[key]
                target[key] = value
                self.refused(lambda: p.admit(self.f.api, self.f.api.ctx), "synthetic_evidence")
                target[key] = original
        self.f.api.artifacts.append(copy.deepcopy(self.f.api.artifacts[0]))
        self.refused(lambda: p.admit(self.f.api, self.f.api.ctx), "synthetic_evidence")

    def test_downloaded_ids_not_replaced_by_fresh_metadata(self):
        self.f.api.artifacts[0]["id"] = 900
        self.refused(self.f.publication().execute, "synthetic_evidence")
        self.assertFalse(any(args[1] == "copy" for args in self.f.calls))

    def test_archive_and_b_digest_hash_refusals(self):
        admitted = p.admit(self.f.api, self.f.api.ctx)
        for path, value in (("b/digest", "sha256:" + "2" * 64), ("b/archive-sha256", "2" * 64),
                            ("archive/image.tar.gz", "changed"), ("a/digest", "invalid")):
            file = self.f.root / path
            original = file.read_bytes()
            file.write_text(value)
            self.refused(lambda: p.archive_gate(self.f.root, admitted, self.f.tool), "synthetic_archive")
            file.write_bytes(original)
        self.f.raw += b" "
        self.refused(lambda: p.archive_gate(self.f.root, admitted, self.f.tool), "synthetic_archive")

    def test_all_actual_qualification_cases_required_no_fake_compatibility(self):
        admitted = p.admit(self.f.api, self.f.api.ctx)
        for evidence, field in ((self.f.q, "cases"), (self.f.q, "native_operator"), (self.f.synthetic, "synthetic")):
            for key in evidence[field]:
                original = evidence[field][key]
                evidence[field][key] = False
                self.f.save()
                self.refused(lambda: p.archive_gate(self.f.root, admitted, self.f.tool))
                evidence[field][key] = original
        for evidence in (self.f.q, self.f.synthetic):
            for field, value in (("config_identity", "sha256:" + "2" * 64), ("archive_sha256", "2" * 64),
                                 ("platform", "linux/arm64"), ("previous_release_compatibility", "passed")):
                original = evidence[field]
                evidence[field] = value
                self.f.save()
                self.refused(lambda: p.archive_gate(self.f.root, admitted, self.f.tool))
                evidence[field] = original
        self.f.save()

    def test_current_native_schema_rejects_historical_and_mixed_claims_before_copy(self):
        native = self.f.q["native_operator"].copy()
        old = native.copy()
        old["held_balance_refused"] = old.pop("held_balance_stock_edit")
        old["logs_settings_blocked"] = old.pop("sdk_logging_forced_off")
        for value in (old, {**native, "held_balance_refused": True},
                      {**native, "logs_settings_blocked": False}):
            self.f.q["native_operator"] = value
            self.f.save()
            self.refused(self.f.publication().execute, "qualification")
        self.assertFalse(any(args[1] == "copy" for args in self.f.calls))
        self.assertIsNone(self.f.authpath)

    def test_manifest_format_config_platform_and_version(self):
        admitted = p.admit(self.f.api, self.f.api.ctx)
        for config in ({"os": "darwin", "architecture": "amd64"}, {"os": "linux", "architecture": "arm64"}):
            self.f.config = json.dumps(config).encode()
            manifest = json.loads(self.f.raw)
            manifest["config"]["digest"] = "sha256:" + hashlib.sha256(self.f.config).hexdigest()
            self.f.raw = json.dumps(manifest).encode()
            digest = "sha256:" + hashlib.sha256(self.f.raw).hexdigest()
            for builder in ("a", "b"):
                (self.f.root / builder / "digest").write_text(digest)
            self.refused(lambda: p.archive_gate(self.f.root, admitted, self.f.tool), "synthetic_archive")
        self.refused(lambda: p.archive_gate(self.f.root, admitted, lambda args, code: b"skopeo version 1.23.0"), "synthetic_archive")

    def test_unknown_json_tool_text_and_unsupported_manifest_refuse_before_copy(self):
        admitted = p.admit(self.f.api, self.f.api.ctx)
        self.f.raw = b"hostile SYNTHETIC_SECRET_SENTINEL 404"
        self.refused(lambda: p.boundary("synthetic_archive", lambda: p.archive_gate(self.f.root, admitted, self.f.tool)),
                     "synthetic_archive")
        self.f.raw = json.dumps({"schemaVersion": 2, "mediaType": "unsupported", "config": {"digest": self.f.config_hash}}).encode()
        digest = "sha256:" + hashlib.sha256(self.f.raw).hexdigest()
        for builder in ("a", "b"):
            (self.f.root / builder / "digest").write_text(digest)
        self.refused(self.f.publication().execute, "synthetic_archive")
        self.assertFalse(any(args[1] == "copy" for args in self.f.calls))

    def test_recheck_before_copy_and_auth_cleanup_on_pre_copy_failure(self):
        reads = 0
        def change(suffix):
            nonlocal reads
            if suffix == "/git/ref/heads/main":
                reads += 1
                if reads == 2:
                    self.f.api.main = "2" * 40
        self.f.api.on_read = change
        auth = p.Authfile()
        self.refused(self.f.publication(auth=auth).execute, "stale")
        self.assertIsNone(auth.directory)
        self.assertFalse(any(args[1] == "copy" for args in self.f.calls))

    def test_cleanup_failure_preserves_primary_and_completed_outcome(self):
        class BadCleanup(p.Authfile):
            def cleanup(self):
                super().cleanup()
                raise RuntimeError("hostile cleanup")
        self.refused(self.f.publication(auth=BadCleanup()).execute, "synthetic_cleanup", "completed")
        self.f.api.copied = False
        self.f.copy_error = RuntimeError("hostile copy")
        error = self.refused(self.f.publication(auth=BadCleanup()).execute, "synthetic_copy", "unknown")
        value = p.diagnostic(error)
        self.assertEqual(value["code"], "DELIVERY_SYNTHETIC_COPY")
        self.assertEqual(value["outcome"], "unknown")
        self.assertEqual(value["secondary"], [r.diagnostic(r.Stop("synthetic_cleanup", outcome="unknown"))])
        self.assertNotIn("hostile", json.dumps(value))

    def test_retained_auth_cleanup_fault_visible_with_each_primary_outcome(self):
        class RetainedAuth(p.Authfile):
            fail_prepare = False
            def prepare(self):
                path = super().prepare()
                if self.fail_prepare:
                    raise RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL")
                return path
            def cleanup(self):
                raise RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL")
        for stage, outcome in (("synthetic_auth", "no_effect"), ("synthetic_copy", "unknown"),
                               ("synthetic_private", "completed")):
            with self.subTest(stage=stage):
                self.f.api.copied = False
                self.f.copy_error = RuntimeError("hostile") if stage == "synthetic_copy" else None
                self.f.post_error = RuntimeError("hostile") if stage == "synthetic_private" else None
                auth = RetainedAuth()
                auth.fail_prepare = stage == "synthetic_auth"
                try:
                    error = self.refused(self.f.publication(auth=auth).execute, stage, outcome)
                    value = p.diagnostic(error)
                    self.assertEqual(value["code"], "DELIVERY_" + stage.upper())
                    self.assertEqual(value["outcome"], outcome)
                    self.assertEqual(value["secondary"], [r.diagnostic(r.Stop("synthetic_cleanup", outcome=outcome))])
                    self.assertNotIn("hostile", json.dumps(value))
                    self.assertNotIn("SYNTHETIC_SECRET_SENTINEL", json.dumps(value))
                    self.assertTrue(auth.path.exists())
                finally:
                    p.Authfile.cleanup(auth)

    def test_cleanup_interruption_keeps_completed_copy_and_secondary_fault(self):
        class InterruptedCleanup(p.Authfile):
            def cleanup(self):
                super().cleanup()
                raise KeyboardInterrupt()
        self.f.post_error = RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL")
        error = self.refused(self.f.publication(auth=InterruptedCleanup()).execute, "synthetic_private", "completed")
        self.assertEqual(p.diagnostic(error)["secondary"],
                         [r.diagnostic(r.Stop("synthetic_cleanup", outcome="completed"))])
        auth = p.Authfile()
        auth.prepare()
        try:
            with patch.object(p.shutil, "rmtree", side_effect=KeyboardInterrupt()), self.assertRaises(r.Stop) as caught:
                auth.cleanup()
            self.assertEqual(caught.exception.code, "synthetic_cleanup")
        finally:
            auth.cleanup()

    def test_final_main_change_or_owner_confirmation_revocation_prevents_copy(self):
        original = self.f.api.repo
        main_reads = 0
        def changed(suffix=""):
            nonlocal main_reads
            if suffix == "/git/ref/heads/main":
                main_reads += 1
                if main_reads == 3:
                    self.f.api.main = "2" * 40
            return original(suffix)
        with patch.object(self.f.api, "repo", side_effect=changed):
            self.refused(self.f.publication().execute, "stale")
        self.f.api.main = SOURCE
        instance = self.f.publication()
        with patch.object(instance, "context_reader", side_effect=[self.f.api.ctx, r.Stop("synthetic_approval")]):
            self.refused(instance.execute, "synthetic_approval")
        self.assertFalse(any(args[1] == "copy" for args in self.f.calls))

    def test_authfile_never_uses_inherited_tmpdir_and_credentials_not_argv(self):
        with patch.dict(os.environ, {"TMPDIR": str(self.f.root)}):
            auth = p.Authfile()
            path = auth.prepare()
            try:
                self.assertEqual(path.parent.parent, Path("/tmp"))  # noqa: S108 - deliberate fixed system-temp contract
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            finally:
                auth.cleanup()


class ControlBoundaryTests(unittest.TestCase):
    def context_fixture(self, root):
        event = {"repository": {"full_name": p.REPO, "private": True}, "sender": {"login": p.OWNER},
                 "inputs": {"publish": "true", "qualification_run": "20", "qualification_attempt": "1",
                            "namespace_unused_confirmed": "true", "namespace_confirmation_source": SOURCE}}
        env = {"GITHUB_REPOSITORY": p.REPO, "GITHUB_REF": "refs/heads/main", "GITHUB_SHA": SOURCE,
               "GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_ACTOR": p.OWNER, "GITHUB_RUN_ATTEMPT": "1",
               "GITHUB_RUN_ID": "30", "GITHUB_EVENT_PATH": str(root / "event.json")}
        return event, env

    def test_owner_bool_hash_activation_event_rerun_refusals(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            event, env = self.context_fixture(root)
            vectors = [(env, "GITHUB_ACTOR", "other"), (env, "GITHUB_RUN_ATTEMPT", "2"),
                       (env, "GITHUB_EVENT_NAME", "workflow_run"), (env, "GITHUB_REF", "refs/pull/1/merge"),
                       (event["sender"], "login", "other"), (event["repository"], "private", False),
                       (event["inputs"], "publish", "false"), (event["inputs"], "namespace_unused_confirmed", "false"),
                       (event["inputs"], "namespace_unused_confirmed", True), (event["inputs"], "namespace_confirmation_source", "2" * 40),
                       (event["inputs"], "namespace_confirmation_source", ""), (event["inputs"], "qualification_attempt", "0"),
                       (event["inputs"], "qualification_run", "20; hostile"), (event["inputs"], "qualification_run", "01")]
            for target, key, value in vectors:
                original = target[key]
                target[key] = value
                (root / "event.json").write_text(json.dumps(event))
                with self.assertRaises(r.Stop):
                    p.context(env)
                target[key] = original
            (root / "event.json").write_text(json.dumps(event))
            self.assertEqual(p.context(env)["source"], SOURCE)
            for key in ("namespace_unused_confirmed", "namespace_confirmation_source", "publish"):
                missing = copy.deepcopy(event)
                del missing["inputs"][key]
                (root / "event.json").write_text(json.dumps(missing))
                with self.assertRaises(r.Stop):
                    p.context(env)

    def test_passive_status_and_report_no_api_or_tool(self):
        with patch.object(p.GitHub, "get", side_effect=AssertionError("network")), patch.object(p, "command", side_effect=AssertionError("mutation")):
            for args in (["status"], ["report"]):
                with patch("sys.stdout", new_callable=io.StringIO) as output:
                    p.main(args)
                    self.assertFalse(json.loads(output.getvalue())["runtime_verified"])

    def test_false_activation_and_receipt_presentation_failure_no_mutation_replay(self):
        with patch.object(p, "context", side_effect=r.Stop("disabled")), patch.object(p.GitHub, "get") as api:
            with self.assertRaises(r.Stop):
                p.main(["publish"])
            api.assert_not_called()
        receipt = {"publicSourceSHA": SOURCE, "imageDigest": "sha256:" + "2" * 64, "archiveSHA": "3" * 64}
        with patch.object(p, "context", return_value={}), patch.object(p.Publication, "execute", return_value=receipt) as execute, patch.object(Path, "write_text", side_effect=OSError("hostile credential")), patch.dict(os.environ, {"ACCEPTED_A_ARTIFACT_ID": "100", "ACCEPTED_B_ARTIFACT_ID": "101", "ACCEPTED_ARCHIVE_ARTIFACT_ID": "102"}):
            with self.assertRaises(r.Stop) as caught:
                p.main(["publish"])
            self.assertEqual(caught.exception.code, "synthetic_receipt")
            self.assertEqual(caught.exception.outcome, "completed")
            self.assertEqual(execute.call_count, 1)

    def test_cli_reports_primary_and_independent_cleanup_without_initiating_work(self):
        error = p.PublicationStop("synthetic_private", status=403, outcome="completed")
        error.cleanup_failed = True
        with patch.object(p, "main", side_effect=error), patch.object(p.GitHub, "get") as api, \
                patch.object(p, "command") as command, patch("sys.stdout", new_callable=io.StringIO) as output:
            with self.assertRaises(SystemExit) as exit_status:
                p.cli()
            self.assertEqual(exit_status.exception.code, 1)
            value = json.loads(output.getvalue())
            self.assertEqual(value["code"], "DELIVERY_SYNTHETIC_PRIVATE")
            self.assertEqual(value["status"], 403)
            self.assertEqual(value["outcome"], "completed")
            self.assertEqual(value["secondary"], [r.diagnostic(r.Stop("synthetic_cleanup", outcome="completed"))])
            api.assert_not_called()
            command.assert_not_called()

    def test_fixed_http_status_and_hostile_body_suppression(self):
        api = p.GitHub()
        with patch.dict(os.environ, {"GITHUB_TOKEN": "SYNTHETIC_SECRET_SENTINEL"}):
            for status in (301, 401, 403, 404, 429, 500):
                error = urllib.error.HTTPError("https://api.github.com", status, "hostile secret", Message(), io.BytesIO(b"hostile secret"))
                with patch("urllib.request.OpenerDirector.open", side_effect=error):
                    if status == 404:
                        self.assertEqual(api.package(), (404, None))
                    else:
                        with self.assertRaises(r.Stop) as caught:
                            api.package()
                        diagnostic = r.diagnostic(caught.exception)
                        self.assertEqual(diagnostic["status"], status)
                        self.assertNotIn("hostile", json.dumps(diagnostic))
            with patch("urllib.request.OpenerDirector.open", side_effect=RuntimeError("hostile 404")):
                with self.assertRaises(r.Stop) as caught:
                    api.package()
                self.assertIsNone(caught.exception.status)
        self.assertIsNone(p.NoRedirect().redirect_request(None, None, 302, "", {}, "https://other"))

    def test_api_get_only_fixed_tls_and_no_token_in_args(self):
        class Response:
            status = 200
            def __enter__(self):
                return self
            def __exit__(self, *args):
                pass
            def read(self, limit):
                self.limit = limit
                return b'{}'
        response = Response()
        with patch.dict(os.environ, {"GITHUB_TOKEN": "SYNTHETIC_SECRET_SENTINEL"}), patch("urllib.request.OpenerDirector.open", return_value=response) as request:
            p.GitHub().repo()
            req = request.call_args.args[0]
            self.assertEqual(req.get_method(), "GET")
            self.assertEqual(req.full_url, "https://api.github.com/repos/" + p.REPO)
            self.assertEqual(response.limit, p.MAX_READ + 1)
            self.assertEqual(request.call_args.kwargs["timeout"], 30)

    def test_command_allowlist_bounded_streams_join_and_closed_failure(self):
        with patch.dict(os.environ, {"PRIVATE_EXTRA": "SYNTHETIC_SECRET_SENTINEL", "GITHUB_TOKEN": "SYNTHETIC_SECRET_SENTINEL"}), patch.object(p.subprocess, "Popen", wraps=subprocess.Popen) as launch:
            result = p.command([sys.executable, "-c", "import os,json; print(json.dumps(sorted(os.environ)))"], "synthetic_archive")
            passed = launch.call_args.kwargs["env"]
            self.assertEqual(set(passed) - {"PATH", "SSL_CERT_FILE"}, set())
            self.assertNotIn(b"PRIVATE_EXTRA", result)
            self.assertNotIn(b"GITHUB_TOKEN", result)
        for args, timeout in (([sys.executable, "-c", "import sys; print('hostile secret',file=sys.stderr); sys.exit(1)"], 10),
                              ([sys.executable, "-c", "import time; time.sleep(10)"], 0.01),
                              ([sys.executable, "-c", "print('x'*10000)"], 10)):
            with patch.object(p, "MAX_READ", 1000):
                with self.assertRaises(r.Stop) as caught:
                    p.command(args, "synthetic_copy", timeout)
                self.assertNotIn("hostile", json.dumps(r.diagnostic(caught.exception)))

    def test_paginated_evidence_complete_or_refused(self):
        api = p.GitHub()
        with patch.object(api, "repo", side_effect=[{"items": [1] * 100}, {"items": [2]}]) as read:
            self.assertEqual(len(api.pages("/actions/runs/20/jobs", "items")), 101)
            self.assertIn("page=2", read.call_args.args[0])
        with patch.object(api, "repo", return_value={"items": [1] * 100}), self.assertRaises(r.Stop):
            api.pages("/actions/runs/20/jobs", "items")

    def test_workflow_manual_off_least_privilege_pins_and_bound_downloads(self):
        source = WORKFLOW.read_text()
        self.assertEqual(source.count("default: false"), 2)
        self.assertIn("namespace_confirmation_source:", source)
        self.assertIn("cancel-in-progress: false", source)
        self.assertEqual(source.count("packages: write"), 1)
        validate = source.split("  validate:")[1].split("  publish:")[0]
        self.assertNotIn("packages:", validate)
        self.assertIn("github.run_attempt == 1", source)
        self.assertIn("github.actor == 'ajbt200128'", source)
        self.assertIn("persist-credentials: false", source)
        self.assertEqual(source.count("artifact-ids: ${{ needs.validate.outputs."), 3)
        self.assertEqual(source.count("run-id: ${{ needs.validate.outputs.qualification_run }}"), 3)
        self.assertIn('nix shell --inputs-from . --option builders "" nixpkgs#skopeo', source)
        self.assertIn("retention-days: 1", source)
        for forbidden in ("workflow_run:", "pull_request:", "push:", "contents: write", "actions: write", "id-token: write",
                          "attestations: write", "continue-on-error", "always()", "container create", "release create"):
            self.assertNotIn(forbidden, source)
        for pin in ("8e8c483db84b4bee98b60c0593521ed34d9990e8", "21a544727d0c62386e78b4befe52d19ad12692e3",
                    "d3f86a106a0bac45b974a628896c90dbdf5c8093", "ea165f8d65b6e75b540449e92b4886f43607fa02"):
            self.assertIn(pin, source)


if __name__ == "__main__":
    unittest.main()
