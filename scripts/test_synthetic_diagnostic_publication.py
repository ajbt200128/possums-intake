"""Offline PUBLIC diagnostic tests: injected API/tools, no registry/Docker/GitHub."""
import copy
import importlib.util
import io
import json
import os
import re
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import patch

import release as r
import test_synthetic_publication as old

spec = importlib.util.spec_from_file_location("synthetic_diagnostic_publication", Path(__file__).with_name("synthetic-diagnostic-publication.py"))
if spec is None or spec.loader is None:
    raise AssertionError("fixture module unavailable")
d = importlib.util.module_from_spec(spec)
spec.loader.exec_module(d)
p = d.p
SOURCE = old.SOURCE
WORKFLOW = Path(__file__).resolve().parents[1] / ".github/workflows/publish-synthetic-diagnostic-image.yml"


def version(number, digest=d.BASELINE, tags=None):
    return {"id": number, "name": digest, "metadata": {"container": {"tags": [] if tags is None else tags}}}


class FakeAPI(old.FakeAPI):
    def __init__(self):
        super().__init__()
        self.ctx["baseline"] = d.BASELINE
        self.current["path"] = ".github/workflows/" + d.WORKFLOW
        self.before = (200, {"id": 200, "name": p.PACKAGE, "package_type": "container",
                            "owner": {"login": p.OWNER}, "visibility": "public",
                            "repository": {"id": 99, "full_name": p.REPO, "private": True}})
        self.after: tuple[int, Any] = copy.deepcopy(self.before)
        self.version_pages: list[Any] = [[version(1)]]
        self.on_package: Any = None
        self.on_version: Any = None

    def package(self):
        if self.on_package:
            self.on_package()
        return super().package()

    def version_page(self, page):
        self.reads.append(f"versions:{page}")
        if self.on_version:
            self.on_version(page)
        batch = self.version_pages[page - 1]
        if isinstance(batch, BaseException):
            raise batch
        return copy.deepcopy(batch if isinstance(batch, tuple) else (200, batch))


class Fixture(old.Fixture):
    def __init__(self, root):
        super().__init__(root)
        self.api = FakeAPI()
        self.synthetic["startup_diagnostics"] = dict.fromkeys(d.STARTUP_CASES, True)
        self.save()

    def publication(self, **kwargs):
        return d.Publication(self.api, self.api.ctx, self.root, {"a": 100, "b": 101, "archive": 102},
                             self.tool, context_reader=lambda: self.api.ctx, **kwargs)


class DiagnosticTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="intake-diagnostic-test-")
        self.addCleanup(self.tmp.cleanup)
        self.f = Fixture(self.tmp.name)
        self.env = patch.dict(os.environ, {"GITHUB_TOKEN": "SYNTHETIC_SECRET_SENTINEL", "GITHUB_ACTOR": p.OWNER})
        self.env.start()
        self.addCleanup(self.env.stop)

    def refused(self, operation, code=None, outcome="no_effect"):
        with self.assertRaises(r.Stop) as caught:
            operation()
        error = caught.exception
        if code:
            self.assertEqual(error.code, code)
        self.assertEqual(error.outcome, outcome)
        value = json.dumps(p.diagnostic(error))
        self.assertNotIn("SYNTHETIC_SECRET_SENTINEL", value)
        self.assertNotIn("hostile", value)
        return error

    def gate(self):
        return p.boundary("diagnostic_package", lambda: d.public_gate(self.f.api, self.f.api.ctx))

    def assert_no_copy(self):
        self.assertFalse(any(args[1] == "copy" for args in self.f.calls))

    def test_single_copy_exact_tag_closed_receipt_and_auth_cleanup(self):
        instance = self.f.publication()
        receipt = instance.execute()
        self.assertEqual(receipt, {"publicSourceSHA": SOURCE, "imageDigest": self.f.digest, "archiveSHA": self.f.archive_hash})
        copies = [args for args in self.f.calls if args[1] == "copy"]
        self.assertEqual(len(copies), 1)
        self.assertEqual(copies[0], ["skopeo", "copy", "--preserve-digests", "--retry-times", "0", "--authfile",
                                   str(self.f.authpath), "docker-archive:" + str((self.f.root / "archive/image.tar.gz").resolve()),
                                   "docker://" + p.REGISTRY + ":" + SOURCE])
        self.assertEqual(instance.outcome, "completed")
        self.assertFalse(self.f.authpath.parent.exists())
        self.assertNotIn("SYNTHETIC_SECRET_SENTINEL", json.dumps(self.f.calls))
        self.assertEqual(self.f.api.reads.count("package"), 3)
        self.assertEqual(self.f.api.reads.count("versions:1"), 2)
        self.assertEqual(self.f.api.reads.count("/git/ref/heads/main"), 3)
        self.refused(instance.execute, "diagnostic_used", "completed")
        self.assertEqual(len([args for args in self.f.calls if args[1] == "copy"]), 1)

    def test_existing_first_publisher_and_default_admission_unchanged(self):
        self.refused(lambda: p.namespace_gate(self.f.api), "synthetic_namespace")
        self.refused(lambda: p.admit(self.f.api, self.f.api.ctx), "synthetic_approval")
        d.admit(self.f.api, self.f.api.ctx)
        self.f.api.current["path"] = ".github/workflows/" + p.WORKFLOW
        p.admit(self.f.api, self.f.api.ctx)
        self.refused(lambda: d.admit(self.f.api, self.f.api.ctx), "synthetic_approval")
        self.assert_no_copy()

    def test_fixed_owner_repo_link_visibility_and_ids(self):
        package = self.f.api.before[1]
        vectors = [(package, "name", "other"), (package, "package_type", "npm"),
                   (package, "owner", {"login": "other"}), (package, "visibility", "private"),
                   (package, "visibility", "internal"), (package, "id", True), (package, "id", 0),
                   (package, "repository", None), (package, "repository", {}),
                   (package["repository"], "id", 100), (package["repository"], "id", True),
                   (package["repository"], "full_name", "other/repo"),
                   (package["repository"], "private", False), (package["repository"], "private", 1),
                   (self.f.api.repo_data, "full_name", "other/repo"),
                   (self.f.api.repo_data, "owner", {"login": "other"}),
                   (self.f.api.repo_data, "private", False), (self.f.api.repo_data, "id", True)]
        for target, key, value in vectors:
            with self.subTest(key=key, value=value):
                original = target[key]
                target[key] = value
                self.refused(self.gate, "diagnostic_package")
                target[key] = original
        self.assert_no_copy()

    def test_404_unavailable_not_absence_or_authority(self):
        for status in (404, 401, 403, 429, 500):
            self.f.api.before = (status, None)
            error = self.refused(self.gate, "diagnostic_package")
            self.assertEqual(error.status, status)
        self.f.api.before = copy.deepcopy(self.f.api.after)
        for status in (404, 401, 403, 429, 500):
            self.f.api.version_pages = [(status, None)]
            error = self.refused(self.gate, "diagnostic_package")
            self.assertEqual(error.status, status)
        self.assert_no_copy()

    def test_package_repository_failure_uses_package_stage_and_observed_status(self):
        for failure in (r.Stop("synthetic_evidence", status=403),
                        RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL 403"), KeyboardInterrupt()):
            with patch.object(self.f.api, "repo", side_effect=failure):
                error = self.refused(self.gate, "diagnostic_package")
            self.assertEqual(error.status, 403 if isinstance(failure, r.Stop) else None)
            self.assertEqual(p.diagnostic(error)["stage"], "diagnostic_public_package")
        self.assert_no_copy()

    def test_postcopy_repository_failure_preserves_completed_outcome_without_replay(self):
        for index, failure in enumerate((r.Stop("synthetic_evidence", status=403),
                                         RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL 403"), KeyboardInterrupt())):
            root = self.f.root / ("postcopy-repository-" + str(index))
            root.mkdir()
            fixture = Fixture(root)
            original = fixture.api.repo
            def repo(suffix="", original=original, fixture=fixture, failure=failure):
                if fixture.api.copied and suffix == "":
                    raise failure
                return original(suffix)
            instance = fixture.publication()
            with patch.object(fixture.api, "repo", side_effect=repo):
                error = self.refused(instance.execute, "diagnostic_public", "completed")
            self.assertEqual(error.status, 403 if isinstance(failure, r.Stop) else None)
            self.assertEqual(p.diagnostic(error)["stage"], "diagnostic_post_publication")
            self.refused(instance.execute, "diagnostic_used", "completed")
            self.assertEqual(sum(args[1] == "copy" for args in fixture.calls), 1)
            self.assertFalse(fixture.authpath.parent.exists())

    def test_complete_pages_baseline_anywhere_and_tag_anywhere(self):
        first = [version(number, "sha256:" + "2" * 64) for number in range(1, 101)]
        self.f.api.version_pages = [first, [version(101)]]
        self.assertEqual(self.gate(), {"package": 200, "repository": 99})
        self.assertIn("versions:2", self.f.api.reads)
        # Even a first-page baseline MUST NOT short circuit later tag evidence.
        first[0]["name"] = d.BASELINE
        self.f.api.version_pages[1][0]["metadata"]["container"]["tags"] = [SOURCE]
        self.refused(self.gate, "diagnostic_package")
        self.f.api.version_pages = [[version(1, tags=[SOURCE])]]
        self.refused(self.gate, "diagnostic_package")
        self.f.api.version_pages = [[version(1, "sha256:" + "2" * 64)]]
        self.refused(self.gate, "diagnostic_package")
        self.f.api.version_pages = [[]]
        self.refused(self.gate, "diagnostic_package")

    def test_partial_pagination_malformed_duplicates_fail_closed(self):
        first = [version(number) for number in range(1, 101)]
        for second in (r.Stop("synthetic_namespace", status=403), RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL"),
                       None, {"unexpected": "hostile"}, [version(1)]):
            self.f.api.version_pages = [first, second]
            self.refused(self.gate, "diagnostic_package")
        self.f.api.version_pages = [[version(page * 100 + number) for number in range(100)] for page in range(100)]
        # Number zero is invalid; use all-valid IDs for the actual cap refusal.
        for page, batch in enumerate(self.f.api.version_pages):
            for number, item in enumerate(batch):
                item["id"] = page * 100 + number + 1
        self.refused(self.gate, "diagnostic_package")
        self.assertIn("versions:100", self.f.api.reads)
        self.assertNotIn("versions:101", self.f.api.reads)
        for field, value in (("id", True), ("id", 0), ("name", "hostile SYNTHETIC_SECRET_SENTINEL"),
                             ("metadata", None), ("metadata", {}),
                             ("metadata", {"container": {"tags": None}}),
                             ("metadata", {"container": {"tags": [True]}}),
                             ("metadata", {"container": {"tags": ["x", "x"]}})):
            item = version(1)
            item[field] = value
            self.f.api.version_pages = [[item]]
            self.refused(self.gate, "diagnostic_package")
        self.assert_no_copy()

    def test_startup_dict_exact_boolean_true_no_source_only_substitute(self):
        admitted = d.admit(self.f.api, self.f.api.ctx)
        expected = dict.fromkeys(d.STARTUP_CASES, True)
        cases = [None, True, 1, [], {}, {**expected, "unknown": True}]
        for key in expected:
            for bad in (False, 0, 1, None, "true", {}, []):
                cases.append({**expected, key: bad})
            missing = expected.copy()
            del missing[key]
            cases.append(missing)
        for value in cases:
            self.f.synthetic["startup_diagnostics"] = value
            self.f.save()
            self.refused(lambda: d.archive_gate(self.f.root, admitted, self.f.tool), "diagnostic_startup")
        del self.f.synthetic["startup_diagnostics"]
        self.f.save()
        # The FIRST publisher still accepts its original evidence schema.
        p.archive_gate(self.f.root, admitted, self.f.tool)
        self.refused(self.f.publication().execute, "diagnostic_startup")
        self.assert_no_copy()

    def test_original_six_ordinary_native_and_archive_gates_still_required(self):
        for evidence, field in ((self.f.q, "cases"), (self.f.q, "native_operator"), (self.f.synthetic, "synthetic")):
            for key in evidence[field]:
                original = evidence[field][key]
                evidence[field][key] = False
                self.f.save()
                self.refused(self.f.publication().execute)
                evidence[field][key] = original
        self.f.save()
        (self.f.root / "b/digest").write_text("sha256:" + "2" * 64)
        self.refused(self.f.publication().execute, "synthetic_archive")
        self.assert_no_copy()

    def test_source_ci_artifact_mismatch_and_unknown_source_error(self):
        api = self.f.api
        vectors = [(api.repo_data, "private", False), (api.repo_data, "owner", {"login": "other"}),
                   (api.current, "run_attempt", 2), (api.current, "triggering_actor", {"login": "other"}),
                   (api.current, "status", "completed"), (api.current, "head_sha", "2" * 40),
                   (api.current, "path", ".github/workflows/" + p.WORKFLOW),
                   (api.checks[0], "conclusion", "failure"), (api.checks[0], "head_sha", "2" * 40),
                   (api.artifacts[0], "expired", True), (api.artifacts[0]["workflow_run"], "repository_id", 100),
                   (api.jobs[2], "runner_id", 2), (api.jobs[2], "labels", ["self-hosted", "ubuntu-latest"])]
        for target, key, value in vectors:
            original = target[key]
            target[key] = value
            self.refused(self.f.publication().execute)
            target[key] = original
        instance = self.f.publication()
        instance.downloaded["a"] = 999
        self.refused(instance.execute, "diagnostic_evidence")
        with patch.object(api, "repo", side_effect=RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL 404")):
            error = self.refused(self.f.publication().execute, "diagnostic_evidence")
            self.assertIsNone(error.status)
        self.assert_no_copy()

    def test_recheck_context_main_archive_artifacts_and_package_before_copy(self):
        for changed in ("tag", "baseline", "identity", "visibility", "archive", "artifact"):
            with self.subTest(changed=changed):
                root = self.f.root / ("change-" + changed)
                root.mkdir()
                fixture = Fixture(root)
                reads = 0
                def change(changed=changed, fixture=fixture):
                    nonlocal reads
                    reads += 1
                    if reads == 2:
                        if changed == "tag":
                            fixture.api.version_pages[0][0]["metadata"]["container"]["tags"] = [SOURCE]
                        elif changed == "baseline":
                            fixture.api.version_pages[0][0]["name"] = "sha256:" + "2" * 64
                        elif changed == "identity":
                            fixture.api.before[1]["id"] = 201
                        elif changed == "visibility":
                            fixture.api.before[1]["visibility"] = "private"
                fixture.api.on_package = change
                auth = p.Authfile()
                original_prepare = auth.prepare
                def prepare(changed=changed, fixture=fixture, original_prepare=original_prepare):
                    path = original_prepare()
                    if changed == "archive":
                        (fixture.root / "archive/image.tar.gz").write_bytes(b"changed")
                    elif changed == "artifact":
                        fixture.api.artifacts[0]["id"] = 999
                    return path
                with patch.object(auth, "prepare", side_effect=prepare):
                    self.refused(fixture.publication(auth=auth).execute)
                self.assertIsNone(auth.directory)
                self.assertFalse(any(args[1] == "copy" for args in fixture.calls))
        for read_number in (2, 3):
            original = self.f.api.repo
            main_reads = 0
            def repo(suffix="", read_number=read_number, original=original):
                nonlocal main_reads
                if suffix == "/git/ref/heads/main":
                    main_reads += 1
                    if main_reads == read_number:
                        self.f.api.main = "2" * 40
                return original(suffix)
            with patch.object(self.f.api, "repo", side_effect=repo):
                self.refused(self.f.publication().execute, "stale")
            self.f.api.main = SOURCE
        instance = self.f.publication()
        with patch.object(instance, "context_reader", side_effect=[self.f.api.ctx, r.Stop("diagnostic_approval")]):
            self.refused(instance.execute, "diagnostic_approval")
        self.assert_no_copy()

    def test_used_unknown_before_copy_and_no_retry_on_error_or_interrupt(self):
        for failure in (RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL 404"), KeyboardInterrupt()):
            self.f.api.copied = False
            self.f.copy_error = failure
            instance = self.f.publication()
            original = self.f.tool
            def tool(args, code, timeout=120, instance=instance, original=original):
                if args[1] == "copy":
                    self.assertTrue(instance.used)
                    self.assertEqual(instance.outcome, "unknown")
                return original(args, code, timeout)
            instance.tool = tool
            self.refused(instance.execute, "diagnostic_copy", "unknown")
            self.refused(instance.execute, "diagnostic_used", "unknown")
            self.assertFalse(self.f.authpath.parent.exists())
        self.assertEqual(sum(args[1] == "copy" for args in self.f.calls), 2)  # Separate injected sessions, one each.

    def test_successful_copy_completed_on_postcheck_identity_digest_or_unexpected_failure(self):
        for kind in ("identity", "visibility", "private_link", "digest", "unexpected", "http"):
            with self.subTest(kind=kind):
                self.f.api.copied = False
                self.f.api.after = copy.deepcopy(self.f.api.before)
                self.f.post_raw = None
                self.f.post_error = None
                if kind == "identity":
                    self.f.api.after[1]["id"] = 201
                elif kind == "visibility":
                    self.f.api.after[1]["visibility"] = "private"
                elif kind == "private_link":
                    self.f.api.after[1]["repository"]["private"] = False
                elif kind == "digest":
                    self.f.post_raw = self.f.raw + b"changed"
                elif kind == "unexpected":
                    self.f.post_error = RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL")
                original = self.f.api.package
                def package(kind=kind, original=original):
                    if self.f.api.copied and kind == "http":
                        raise r.Stop("synthetic_namespace", status=403)
                    return original()
                instance = self.f.publication()
                with patch.object(self.f.api, "package", side_effect=package):
                    error = self.refused(instance.execute, "diagnostic_public", "completed")
                    if kind == "http":
                        self.assertEqual(error.status, 403)
                self.refused(instance.execute, "diagnostic_used", "completed")
                self.assertFalse(self.f.authpath.parent.exists())

    def test_cleanup_primary_precedence_and_auth_prepare_failure(self):
        class BadCleanup(p.Authfile):
            def cleanup(self):
                super().cleanup()
                raise RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL")
        self.f.copy_error = RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL")
        error = self.refused(self.f.publication(auth=BadCleanup()).execute, "diagnostic_copy", "unknown")
        self.assertEqual(p.diagnostic(error)["secondary"], [r.diagnostic(r.Stop("synthetic_cleanup", outcome="unknown"))])
        self.f.api.copied = False
        self.f.copy_error = None
        self.refused(self.f.publication(auth=BadCleanup()).execute, "synthetic_cleanup", "completed")
        class BadPrepare(p.Authfile):
            def prepare(self):
                super().prepare()
                raise RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL")
        self.f.api.copied = False
        auth = BadPrepare()
        self.refused(self.f.publication(auth=auth).execute, "synthetic_auth")
        self.assertIsNone(auth.directory)
        with patch.dict(os.environ, {"TMPDIR": str(self.f.root)}):
            auth = p.Authfile()
            path = auth.prepare()
            try:
                self.assertEqual(path.parent.parent, Path("/tmp"))  # noqa: S108 - fixed shared authfile contract
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            finally:
                auth.cleanup()


class ControlTests(unittest.TestCase):
    def test_context_fixed_authority_source_baseline_booleans_closed_inputs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            event = {"repository": {"full_name": p.REPO, "private": True}, "sender": {"login": p.OWNER},
                     "inputs": {"publish": "true", "qualification_run": "20", "qualification_attempt": "1",
                                "expected_source": SOURCE, "known_baseline": d.BASELINE,
                                "writers_quiet_confirmed": "true", "quiet_confirmation_source": SOURCE}}
            env = {"GITHUB_REPOSITORY": p.REPO, "GITHUB_REF": "refs/heads/main", "GITHUB_SHA": SOURCE,
                   "GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_ACTOR": p.OWNER,
                   "GITHUB_RUN_ATTEMPT": "1", "GITHUB_RUN_ID": "30", "GITHUB_EVENT_PATH": str(root / "event.json")}
            vectors = [(env, "GITHUB_ACTOR", "other"), (env, "GITHUB_REPOSITORY", "other/repo"),
                       (env, "GITHUB_RUN_ATTEMPT", "2"), (env, "GITHUB_EVENT_NAME", "push"),
                       (env, "GITHUB_REF", "refs/tags/v1.0.0"), (event["sender"], "login", "other"),
                       (event["repository"], "full_name", "other/repo"), (event["repository"], "private", False),
                       (event["repository"], "private", 1), (event["inputs"], "publish", "false"),
                       (event["inputs"], "publish", True), (event["inputs"], "writers_quiet_confirmed", True),
                       (event["inputs"], "writers_quiet_confirmed", "false"),
                       (event["inputs"], "quiet_confirmation_source", "2" * 40),
                       (event["inputs"], "expected_source", "2" * 40),
                       (event["inputs"], "known_baseline", "sha256:" + "2" * 64),
                       (event["inputs"], "qualification_run", "20;hostile"),
                       (event["inputs"], "qualification_attempt", "01")]
            for target, key, value in vectors:
                original = target[key]
                target[key] = value
                (root / "event.json").write_text(json.dumps(event))
                with self.assertRaises(r.Stop):
                    d.context(env)
                target[key] = original
            for key in event["inputs"]:
                bad = copy.deepcopy(event)
                del bad["inputs"][key]
                (root / "event.json").write_text(json.dumps(bad))
                with self.assertRaises(r.Stop):
                    d.context(env)
            event["inputs"]["unknown"] = "hostile"
            (root / "event.json").write_text(json.dumps(event))
            with self.assertRaises(r.Stop):
                d.context(env)
            del event["inputs"]["unknown"]
            (root / "event.json").write_text(json.dumps(event))
            self.assertEqual(d.context(env), {"source": SOURCE, "run": 30, "qualification_run": 20,
                                              "qualification_attempt": 1, "baseline": d.BASELINE})

    def test_fixed_paginated_gets_status_and_hostile_errors_no_real_transport(self):
        api = d.GitHub()
        with patch.object(api, "get", return_value=(200, [])) as get:
            self.assertEqual(api.version_page(2), (200, []))
            self.assertEqual(get.call_args.args, ("/users/" + p.OWNER + "/packages/container/" + p.PACKAGE
                                                + "/versions?state=active&per_page=100&page=2",))
            self.assertTrue(get.call_args.kwargs["package"])
        for failure in (r.Stop("synthetic_namespace", status=429), RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL")):
            with self.assertRaises(r.Stop) as caught:
                d.package_read(lambda failure=failure: self.raise_error(failure), "diagnostic_package")
            self.assertEqual(caught.exception.code, "diagnostic_package")
            self.assertNotIn("hostile", json.dumps(r.diagnostic(caught.exception)))

    @staticmethod
    def raise_error(failure):
        raise failure

    def test_admit_reads_fixed_package_and_outputs_only_selected_ids(self):
        api = FakeAPI()
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "outputs"
            with patch.object(d, "context", return_value=api.ctx), patch.object(d, "GitHub", return_value=api), \
                    patch.object(d, "Publication", side_effect=AssertionError("copy")) as publication, \
                    patch.dict(os.environ, {"GITHUB_OUTPUT": str(output)}):
                d.main(["admit"])
                self.assertEqual(output.read_text(), "source=" + SOURCE + "\nqualification_run=20\na=100\nb=101\narchive=102\n")
                self.assertIn("package", api.reads)
                self.assertIn("versions:1", api.reads)
                publication.assert_not_called()
                output.unlink()
                api.before = (404, None)
                with self.assertRaises(r.Stop):
                    d.main(["admit"])
                self.assertFalse(output.exists())

    def test_passive_status_report_and_disabled_no_work(self):
        with patch.object(d.GitHub, "get", side_effect=AssertionError("network")) as api, \
                patch.object(d, "Publication", side_effect=AssertionError("copy")) as publication:
            for command in ("status", "report"):
                with patch("sys.stdout", new_callable=io.StringIO) as output:
                    d.main([command])
                    self.assertFalse(json.loads(output.getvalue())["runtime_verified"])
            with patch.object(d, "context", side_effect=r.Stop("disabled")), self.assertRaises(r.Stop):
                d.main(["publish"])
            api.assert_not_called()
            publication.assert_not_called()

    def test_receipt_failure_completed_no_replay_and_cli_closed_unknown(self):
        receipt = {"publicSourceSHA": SOURCE, "imageDigest": "sha256:" + "2" * 64, "archiveSHA": "3" * 64}
        with patch.object(d, "context", return_value={}), patch.object(d.Publication, "execute", return_value=receipt) as execute, \
                patch.object(Path, "write_text", side_effect=OSError("hostile SYNTHETIC_SECRET_SENTINEL")), \
                patch.dict(os.environ, {"ACCEPTED_A_ARTIFACT_ID": "100", "ACCEPTED_B_ARTIFACT_ID": "101", "ACCEPTED_ARCHIVE_ARTIFACT_ID": "102"}):
            with self.assertRaises(r.Stop) as caught:
                d.main(["publish"])
            self.assertEqual(caught.exception.code, "synthetic_receipt")
            self.assertEqual(caught.exception.outcome, "completed")
            self.assertEqual(execute.call_count, 1)
        for error in (RuntimeError("hostile SYNTHETIC_SECRET_SENTINEL"), p.PublicationStop("diagnostic_copy", outcome="unknown")):
            with patch.object(d, "main", side_effect=error), patch("sys.stdout", new_callable=io.StringIO) as output:
                with self.assertRaises(SystemExit):
                    d.cli()
                value = json.loads(output.getvalue())
                self.assertNotIn("hostile", json.dumps(value))
                self.assertNotIn("SYNTHETIC_SECRET_SENTINEL", json.dumps(value))
                if isinstance(error, r.Stop):
                    self.assertEqual(value["outcome"], "unknown")
                else:
                    self.assertEqual(value["code"], "DELIVERY_DIAGNOSTIC_INTERNAL")
                    self.assertIn("cause unknown", value["next_step"])

    def test_workflow_manual_defaultfalse_pins_readonly_validation_exact_downloads(self):
        source = WORKFLOW.read_text()
        original = old.WORKFLOW.read_text()
        self.assertEqual(source.count("default: false"), 2)
        self.assertIn("expected_source:", source)
        self.assertIn("quiet_confirmation_source:", source)
        self.assertIn(d.BASELINE, source)
        self.assertIn("group: intake-synthetic-first-publication", source)
        self.assertIn("cancel-in-progress: false", source)
        self.assertEqual(source.count("packages: write"), 1)
        validation = source.split("  validate:")[1].split("  publish:")[0]
        self.assertNotIn(": write", validation)
        self.assertIn("packages: read", validation)
        self.assertIn("github.run_attempt == 1", source)
        self.assertIn("github.actor == 'ajbt200128'", source)
        self.assertEqual(source.count("persist-credentials: false"), 2)
        self.assertEqual(source.count("artifact-ids: ${{ needs.validate.outputs."), 3)
        self.assertEqual(source.count("run-id: ${{ needs.validate.outputs.qualification_run }}"), 3)
        self.assertIn("retention-days: 1", source)
        self.assertIn("scripts/synthetic-diagnostic-publication.py admit", validation)
        self.assertIn("scripts/synthetic-diagnostic-publication.py publish", source)
        self.assertEqual(set(re.findall(r"uses: (\S+)", source)), set(re.findall(r"uses: (\S+)", original)))
        for forbidden in ("workflow_run:", "pull_request:", "push:", "contents: write", "actions: write", "id-token: write",
                          "attestations: write", "continue-on-error", "always()", "container create", "release create", "environment:"):
            self.assertNotIn(forbidden, source)


if __name__ == "__main__":
    unittest.main()
