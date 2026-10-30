"""Offline-only acceptance ports. No cryptographic, build or runtime evidence."""
import copy
import hashlib
import importlib.util
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import patch

import release as r
import test_public_release as contract
import test_public_tinfoil_release as signing

spec = importlib.util.spec_from_file_location("public_release_acceptance_tests", Path(__file__).with_name("public-release-acceptance.py"))
if spec is None or spec.loader is None:
    raise AssertionError("fixture module unavailable")
a = importlib.util.module_from_spec(spec)
spec.loader.exec_module(a)
HOSTILE = "hostile-SYNTHETIC_SECRET-url-header-body"
IDS = {"a": 100, "b": 101, "archive": 102}


class FakeAPI(signing.FakeAPI):
    joined = True

    def __init__(self, ctx, raw):
        super().__init__(ctx)
        self.current.update(status="completed", conclusion="success")
        self.user: Any = {"login": a.c.OWNER}
        self.selected: Any = {"id": 400, "tag_name": ctx["tag"], "draft": False, "prerelease": False,
                              "published_at": "2026-01-01T00:00:00Z", "target_commitish": "main"}
        self.bodies: dict[int, Any] = {501: raw, 502: hashlib.sha256(raw).hexdigest().encode() + b"\n"}
        self.assets: Any = [{"id": 501, "name": "tinfoil-deployment.json", "state": "uploaded", "size": len(raw)},
                           {"id": 502, "name": "tinfoil.hash", "state": "uploaded", "size": 65}]
        self.on_asset: Any = None

    def identity(self):
        self.reads.append("user")
        return copy.deepcopy(self.user)

    def repo(self, suffix=""):
        if suffix in ("/actions/runs/30/attempts/1", "/releases/tags/" + self.ctx["tag"]):
            self.reads.append(suffix)
            return copy.deepcopy(self.current if suffix.startswith("/actions") else self.selected)
        if suffix.startswith("/releases/400/assets?"):
            self.reads.append(suffix)
            return copy.deepcopy(self.assets)
        return super().repo(suffix)

    def asset(self, number):
        self.reads.append("asset")
        if self.on_asset:
            self.on_asset(number)
        value = self.bodies[number]
        if isinstance(value, BaseException):
            raise value
        return value


class Fixture(signing.Fixture):
    def __init__(self, root):
        super().__init__(root)
        self.manifest = contract.manifest(self.git.config)
        raw = json.dumps(self.manifest).encode()
        self.api = FakeAPI(self.ctx, raw)
        self.checked: Any = [{"verificationResult": {"statement": {
            "predicateType": r.PREDICATE, "predicate": self.manifest,
            "subject": [{"name": "tinfoil-deployment.json", "digest": {"sha256": hashlib.sha256(raw).hexdigest()}}]},
            "signature": {"certificate": {
                "subjectAlternativeName": f"https://github.com/{a.c.REPOSITORY}/.github/workflows/tinfoil-release-publish.yml@refs/tags/{self.ctx['tag']}",
                "buildSignerDigest": self.ctx["release"], "sourceRepositoryDigest": self.ctx["release"],
                "sourceRepositoryRef": "refs/tags/" + self.ctx["tag"], "runnerEnvironment": "github-hosted",
                "runInvocationURI": f"https://github.com/{a.c.REPOSITORY}/actions/runs/30/attempts/1"}}}}]
        self.sdk_calls = []
        self.sdk_error: BaseException | None = None
        self.on_verify: Any = None
        self.state = a.receipt()

    def verify(self, args):
        self.sdk_calls.append(args)
        path = Path(args[3])
        if path.name != "tinfoil-deployment.json" or path.read_bytes() != self.api.bodies[501]:
            raise AssertionError("SDK subject bytes mismatch")
        if path.parent.stat().st_mode & 0o777 != 0o700 or path.stat().st_mode & 0o777 != 0o600:
            raise AssertionError("workspace permissions")
        if self.on_verify:
            self.on_verify()
        if self.sdk_error:
            raise self.sdk_error
        return json.dumps(self.checked).encode()

    def accept(self):
        return a.accept(self.ctx, IDS, self.checkout, api=self.api, tool=self.command,
                        command=self.verify, state=self.state)

    def args(self):
        values = {**self.ctx, "a": 100, "b": 101, "archive_id": 102}
        return ["accept", *[item for flag, key in a.FLAGS.items() for item in (flag, str(values[key]))]]


class AcceptanceTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="intake-acceptance-test-")
        self.addCleanup(self.tmp.cleanup)
        self.f = Fixture(self.tmp.name)

    def stop(self, operation=None, code=None) -> Any:
        with self.assertRaises(r.Stop) as caught:
            (operation or self.f.accept)()
        error = caught.exception
        if code:
            self.assertEqual(error.code, code)
        encoded = json.dumps(a.diagnostic(error, self.f.state))
        self.assertNotIn(HOSTILE, encoded)
        self.assertNotIn(self.f.ctx["release"], encoded)
        self.assertNotIn("https://", encoded)
        self.assertNotIn("published", json.loads(encoded))
        return error

    def reset_git(self):
        self.f.git.nix_count = 0

    def test_positive_original_ids_official_two_assets_and_default_target_main(self):
        result = self.f.accept()
        for key in ("successful_workflow_observed", "release_observed", "cryptographically_verified", "accepted_release_observed", "passed", "read_only"):
            self.assertIs(result[key], True)
        for key in ("publication_attempted", "live_activation_observed", "runtime_verified", "resource_creation_authorized"):
            self.assertIs(result[key], False)
        self.assertEqual(self.f.git.nix_count, 2)
        self.assertEqual(len(self.f.sdk_calls), 1)
        self.assertFalse(Path(self.f.sdk_calls[0][3]).parent.exists())
        self.assertEqual(self.f.api.reads.count("/releases/tags/" + self.f.ctx["tag"]), 2)
        self.assertIn("/actions/runs/30/attempts/1", self.f.api.reads)
        self.assertEqual(a.parse(self.f.args()), (self.f.ctx, IDS))

    def test_static_status_no_environment_auth_files_template_or_commands(self):
        with patch.object(a, "Native", side_effect=AssertionError("IO")), patch.object(a, "accept", side_effect=AssertionError("IO")), \
                patch.object(a.c, "read_template", side_effect=AssertionError("IO")), \
                patch.object(a.Path, "resolve", side_effect=AssertionError("IO")), \
                patch.dict(os.environ, {}, clear=True), patch("sys.stdout", new_callable=io.StringIO) as out:
            self.assertEqual(a.main(["status"]), 0)
        result = json.loads(out.getvalue())
        self.assertFalse(result["publication_attempted"])
        self.assertFalse(result["cryptographically_verified"])
        self.assertNotIn("release_enabled", result)
        self.assertNotIn("published", result)

    def test_all_malformed_cli_and_library_context_refuse_before_any_io(self):
        valid = self.f.args()
        cases = [[], ["accept"], ["guard"], ["status", "extra"], valid + ["--extra", HOSTILE],
                 valid[:-2], [*valid[:-2], valid[1], valid[2]]]
        for flag in a.FLAGS:
            value = "0" if a.FLAGS[flag] in a.s.NUMBERS | {"a", "b", "archive_id"} else HOSTILE
            changed = valid.copy()
            changed[changed.index(flag) + 1] = value
            cases.append(changed)
        for args in cases:
            with self.subTest(args=args), patch.object(a, "Native", side_effect=AssertionError("early IO")) as native, \
                    patch("sys.stdout", new_callable=io.StringIO):
                self.assertEqual(a.main(args), 1)
                native.assert_not_called()
        for key, value in (("run", True), ("phase", []), ("source", self.f.ctx["release"]), ("checks_run", 11)):
            ctx = {**self.f.ctx, key: value}
            self.stop(lambda ctx=ctx: a.accept(ctx, IDS, self.f.checkout), "acceptance_input")
        self.stop(lambda: a.accept(self.f.ctx, {**IDS, "a": 101}, self.f.checkout), "acceptance_input")
        self.assertEqual(self.f.api.reads, [])

    def test_private_cross_identity_package_and_optional_association_never_read(self):
        class AssociationPoison:
            def __deepcopy__(self, memo):
                return self

            def get(self, *args):
                raise AssertionError("optional association read")
        self.f.api.before = (200, {**self.f.api.before[1], "repository": AssociationPoison()})
        self.assertTrue(self.f.accept()["passed"])
        for field, value in (("private", True), ("full_name", "other/repo"), ("id", True)):
            self.f.api.repo_data[field] = value
            self.stop(code="acceptance_repository")
            self.f.api.repo_data = copy.deepcopy(signing.FakeAPI(self.f.ctx).repo_data)
        for field, value in (("visibility", "private"), ("owner", {"login": "other"}), ("name", "other")):
            original = self.f.api.before
            self.f.api.before = (200, {**original[1], field: value})
            self.stop(code="acceptance_repository")
            self.f.api.before = original
        for status in (403, 404):
            with patch.object(self.f.api, "package", return_value=(status, None)):
                self.assertEqual(self.stop(code="acceptance_repository").status, status)
        self.f.api.user = {"login": "other"}
        self.stop(code="acceptance_auth")

    def test_exact_successful_run_attempt_event_ref_actor_and_repositories(self):
        changes = (("id", 31), ("run_attempt", 2), ("run_attempt", True), ("status", "in_progress"),
                   ("conclusion", "skipped"), ("event", "push"), ("head_sha", self.f.ctx["source"]),
                   ("head_branch", "main"), ("path", ".github/workflows/other.yml"),
                   ("actor", {"login": "other"}), ("triggering_actor", {"login": "other"}),
                   ("head_repository", {"full_name": "other/repo"}))
        for key, value in changes:
            with self.subTest(key=key):
                original = self.f.api.current[key]
                self.f.api.current[key] = value
                self.stop(code="acceptance_run")
                self.assertFalse(self.f.state["successful_workflow_observed"])
                self.f.api.current[key] = original
        self.assertEqual(self.f.git.calls, [])

    def test_separate_source_release_checks_and_native_qualification_are_mandatory(self):
        for check in (self.f.api.checks[0], self.f.api.release_check):
            for key, value in (("run_attempt", 2), ("head_sha", "3" * 40), ("head_branch", "feature"),
                               ("event", "pull_request"), ("conclusion", "failure")):
                original = check[key]
                check[key] = value
                self.stop(code="acceptance_checks")
                check[key] = original
        for key, value in (("run_attempt", 2), ("head_sha", self.f.ctx["release"]), ("conclusion", "failure")):
            original = self.f.api.qualified[key]
            self.f.api.qualified[key] = value
            self.stop(code="acceptance_artifacts")
            self.f.api.qualified[key] = original
        self.f.api.jobs[2]["runner_id"] = self.f.api.jobs[1]["runner_id"]
        self.stop(code="acceptance_artifacts")
        self.f.api.jobs[2]["runner_id"] = 3
        self.f.api.jobs[1]["labels"] = ["ubuntu-latest", "self-hosted"]
        self.stop(code="acceptance_artifacts")
        self.f.api.jobs[1]["labels"] = ["ubuntu-latest"]
        self.f.api.artifacts[0]["id"] = 999
        self.stop(code="acceptance_artifacts")
        self.assertEqual(self.f.git.calls, [])

    def test_main_lightweight_tag_sole_parent_config_modes_and_drv_equality(self):
        for suffix, key, value in (("heads/main", "sha", self.f.ctx["source"]), ("tags/" + self.f.ctx["tag"], "type", "tag")):
            obj = self.f.api.refs["/git/ref/" + suffix]["object"]
            original = obj[key]
            obj[key] = value
            self.stop(code="acceptance_refs")
            obj[key] = original
        for key, value in (("head", b"wrong"), ("parents", b"wrong"), ("change", b"M\0other\0"),
                           ("mode", b"100755"), ("config", b"wrong")):
            original = getattr(self.f.git, key)
            setattr(self.f.git, key, value)
            self.stop(code="signing_relation")
            setattr(self.f.git, key, original)
        self.f.git.drvs[1] = b"/nix/store/" + b"b" * 32 + b"-other.drv"
        self.stop(code="signing_drv")
        self.assertEqual(self.f.sdk_calls, [])

    def test_complete_signing_inventory_prior_rerun_duplicate_capped_and_inaccessible(self):
        for values in ([], [self.f.api.current, {**self.f.api.current, "id": 31}],
                       [self.f.api.current, self.f.api.current]):
            self.f.api.signing = values
            self.stop(code="acceptance_prior")
        self.f.api.signing = [self.f.api.current]
        self.f.api.signing_pages = [[{**self.f.api.current, "id": i + 1} for i in range(100)]]
        with patch.object(a.s, "PAGE_CAP", 1):
            self.stop(code="acceptance_prior")
        self.f.api.signing_pages = None
        original = self.f.api.repo
        def inaccessible(suffix=""):
            if "workflows/" in suffix:
                raise r.Stop("transport", status=404)
            return original(suffix)
        with patch.object(self.f.api, "repo", side_effect=inaccessible):
            self.assertEqual(self.stop(code="acceptance_prior").status, 404)
        self.assertFalse(self.f.state["cryptographically_verified"])

    def test_release_and_asset_metadata_missing_duplicate_oversize_and_unuploaded(self):
        for key, value in (("id", True), ("tag_name", "v9.0.0"), ("draft", True), ("prerelease", True), ("published_at", None)):
            original = self.f.api.selected[key]
            self.f.api.selected[key] = value
            self.stop(code="acceptance_release")
            self.f.api.selected[key] = original
        original = copy.deepcopy(self.f.api.assets)
        variants = [[], original[:1], original + [dict(original[0], id=503)],
                    [original[0], dict(original[1], id=501)],
                    [original[0], dict(original[1], name="tinfoil-deployment.json")]]
        for index, key, value in ((0, "size", a.c.MANIFEST_LIMIT + 1), (1, "size", 129),
                                  (0, "size", True), (0, "state", "new"), (0, "name", "other")):
            changed = copy.deepcopy(original)
            changed[index][key] = value
            variants.append(changed)
        for assets in variants:
            self.f.api.assets = assets
            self.stop(code="acceptance_assets")
        self.assertEqual(self.f.sdk_calls, [])

    def test_raw_hash_canonical_hashfile_sizes_and_structural_manifest_before_sdk(self):
        for number, value in ((501, self.f.api.bodies[501][:-1]), (501, b"x" * len(self.f.api.bodies[501])),
                              (502, b"0" * 64 + b"\n"), (502, self.f.api.bodies[502][:-1]),
                              (501, RuntimeError(HOSTILE))):
            original = self.f.api.bodies[number]
            self.f.api.bodies[number] = value
            self.stop(code="acceptance_download")
            self.f.api.bodies[number] = original
            self.reset_git()
        bad = copy.deepcopy(self.f.manifest)
        bad["vm_shape"]["disks"] = 3
        raw = json.dumps(bad).encode()
        self.f.api.bodies = {501: raw, 502: hashlib.sha256(raw).hexdigest().encode() + b"\n"}
        self.f.api.assets[0]["size"] = len(raw)
        self.stop(code="acceptance_manifest")
        self.assertEqual(self.f.sdk_calls, [])

    def test_sdk_unknown_hostile_and_signed_predicate_cert_subject_invocation_refuse(self):
        for error in (RuntimeError(HOSTILE), r.Stop("transport", status=403)):
            self.f.sdk_error = error
            self.stop(code="acceptance_signature")
            self.reset_git()
        self.f.sdk_error = None
        checked = self.f.checked[0]["verificationResult"]
        cases = [(checked["statement"], "predicateType", "wrong"),
                 (checked["statement"]["subject"][0], "name", "other"),
                 (checked["statement"], "predicate", {}),
                 (checked["signature"]["certificate"], "runnerEnvironment", "self-hosted"),
                 (checked["signature"]["certificate"], "sourceRepositoryDigest", "3" * 40),
                 (checked["signature"]["certificate"], "subjectAlternativeName", "wrong"),
                 (checked["signature"]["certificate"], "runInvocationURI", f"https://github.com/{a.c.REPOSITORY}/actions/runs/31/attempts/1"),
                 (checked["signature"]["certificate"], "runInvocationURI", f"https://github.com/{a.c.REPOSITORY}/actions/runs/30/attempts/2")]
        for obj, key, value in cases:
            original = obj[key]
            obj[key] = value
            self.stop(code="acceptance_invocation" if key == "runInvocationURI" else "acceptance_signature")
            obj[key] = original
            self.reset_git()
        self.assertFalse(self.f.state["cryptographically_verified"])
        self.assertTrue(self.f.state["release_observed"])

    def test_rechecks_changed_release_asset_main_run_and_ids_preserve_crypto(self):
        changes = [lambda: self.f.api.selected.update(id=401), lambda: self.f.api.assets[0].update(id=503),
                   lambda: self.f.api.assets[0].update(state="new"),
                   lambda: self.f.api.refs["/git/ref/heads/main"]["object"].update(sha=self.f.ctx["source"]),
                   lambda: self.f.api.current.update(run_attempt=2), lambda: self.f.api.artifacts[0].update(id=999)]
        for mutate in changes:
            # Each case gets pristine evidence; all rechecks happen after fake SDK success.
            with tempfile.TemporaryDirectory() as directory:
                f = Fixture(directory)
                prior = self.f
                self.f = f
                # Bind mutation to this fixture at call time.
                f.on_verify = mutate
                error = self.stop(code="acceptance_recheck")
                self.assertTrue(error.observed["release_observed"])
                self.assertTrue(error.observed["cryptographically_verified"])
                self.assertFalse(error.observed["accepted_release_observed"])
                self.f = prior

    def test_cleanup_preserves_primary_and_accepted_flags_and_retains_on_unknown_join(self):
        original = a.shutil.rmtree
        def cleanup(path, *args, **kwargs):
            if Path(path).name.startswith("intake-public-acceptance-"):
                raise RuntimeError(HOSTILE)
            return original(path, *args, **kwargs)
        with patch.object(a.shutil, "rmtree", side_effect=cleanup):
            error = self.stop(code="acceptance_cleanup")
        retained = Path(self.f.sdk_calls[-1][3]).parent
        self.addCleanup(lambda: shutil.rmtree(retained, ignore_errors=True))
        self.assertTrue(error.observed["accepted_release_observed"])
        self.assertTrue(retained.exists())
        self.reset_git()
        self.f.on_verify = lambda: setattr(self.f.api, "joined", False)
        self.f.sdk_error = RuntimeError(HOSTILE)
        error = self.stop(code="acceptance_signature")
        retained2 = Path(self.f.sdk_calls[-1][3]).parent
        self.addCleanup(lambda: shutil.rmtree(retained2, ignore_errors=True))
        self.assertEqual(error.secondary.code, "acceptance_join")
        self.assertTrue(retained2.exists())

    def test_workspace_fault_and_presentation_failure_keep_known_flags(self):
        original = a.tempfile.mkdtemp
        def fail_workspace(*args, **kwargs):
            if kwargs.get("prefix") == "intake-public-acceptance-":
                raise RuntimeError(HOSTILE)
            return original(*args, **kwargs)
        with patch.object(a.tempfile, "mkdtemp", side_effect=fail_workspace):
            self.stop(code="acceptance_workspace")
        self.reset_git()
        class Broken:
            def write(self, _):
                raise RuntimeError(HOSTILE)
        def accepted(*args, state, **kwargs):
            state.update(release_observed=True, cryptographically_verified=True, accepted_release_observed=True)
            return {**state, "passed": True}
        with patch.object(a, "accept", side_effect=accepted), patch("sys.stdout", Broken()), \
                patch("sys.stderr", new_callable=io.StringIO) as err:
            self.assertEqual(a.main(self.f.args()), 1)
        result = json.loads(err.getvalue())
        self.assertTrue(result["accepted_release_observed"])
        self.assertTrue(result["cryptographically_verified"])
        self.assertEqual(result["code"], "DELIVERY_ACCEPTANCE_OUTPUT")


class NativeAndCompatibilityTests(unittest.TestCase):
    def native(self):
        with patch.dict(os.environ, {"HOME": "/synthetic-home", "GH_CONFIG_DIR": "/synthetic-config", "GH_TOKEN": HOSTILE,
                                     "GITHUB_TOKEN": HOSTILE, "HTTPS_PROXY": HOSTILE}, clear=True), \
                patch.object(a.o, "require_native_tool") as qualify:
            native = a.Native()
        qualify.assert_called_once_with(a.o.NATIVE_GH)
        self.assertEqual(native.env["HOME"], "/synthetic-home")
        self.assertEqual(native.env["GH_CONFIG_DIR"], "/synthetic-config")
        self.assertNotIn("GH_TOKEN", native.env)
        self.assertNotIn("GITHUB_TOKEN", native.env)
        self.assertNotIn("HTTPS_PROXY", native.env)
        return native

    def transport(self, native, f, calls):
        def transport(argv, env, stage, timeout):
            self.assertEqual(argv[0], str(a.o.NATIVE_GH))
            self.assertEqual(env, native.env)
            calls.append(argv)
            args = argv[1:]
            if args[:2] == ["attestation", "verify"]:
                return 0, f.verify(["gh", *args]), b""
            self.assertEqual(args[:3], ["api", "--method", "GET"])
            path = args[3]
            if "application/octet-stream" in args[-1]:
                number = int(path.rsplit("/", 1)[1])
                return 0, f.api.asset(number), b""
            self.assertIn("--include", args)
            if path == "user":
                value = f.api.identity()
            elif path.startswith("users/"):
                value = f.api.package()[1]
            else:
                self.assertTrue(path.startswith("repos/" + a.c.REPOSITORY))
                suffix = path[len("repos/" + a.c.REPOSITORY):]
                if "/jobs?" in suffix:
                    jobs = [dict(job, id=i + 1) for i, job in enumerate(f.api.jobs)]
                    value = {"jobs": jobs, "total_count": len(jobs)}
                elif "/artifacts?" in suffix:
                    value = {"artifacts": f.api.artifacts, "total_count": len(f.api.artifacts)}
                else:
                    value = f.api.repo(suffix)
            return 0, b"HTTP/2.0 200 Synthetic\r\n\r\n" + json.dumps(value).encode(), b""
        return transport

    def test_positive_acceptance_through_actual_native_command_port_with_only_fake_transport(self):
        native = self.native()
        with tempfile.TemporaryDirectory() as directory:
            f = Fixture(directory)
            calls = []
            with patch.object(a.smoke, "run_bounded", side_effect=self.transport(native, f, calls)), \
                    patch.object(r, "read_command", side_effect=AssertionError("legacy command forbidden")):
                result = a.accept(f.ctx, IDS, f.checkout, api=native, tool=f.command, command=native.verify)
            self.assertTrue(result["accepted_release_observed"])
            self.assertTrue(native.joined)
            self.assertTrue(all(call[1] in ("api", "attestation") for call in calls))
            self.assertFalse(any("auth" in call or "release" in call or "--paginate" in call for call in calls))

    def test_native_inventory_failures_keep_categories_status_and_join_before_and_after_crypto(self):
        boundaries = (("/actions/workflows/" + a.s.WORKFLOW + "/runs?head_sha=", "prior"),
                      ("/actions/runs/20/attempts/1/jobs?", "artifacts"),
                      ("/actions/runs/20/artifacts?", "artifacts"),
                      ("/releases/400/assets?", "assets"),
                      ("/actions/runs/10/attempts/1", "checks"),
                      ("/actions/runs/11/attempts/1", "checks"))
        failures = [*a.SMOKE_FAILURES, "decode", "http403", "http404"]
        for suffix, boundary in boundaries:
            for failure in failures:
                for occurrence in (1, 2):
                    with self.subTest(boundary=boundary, suffix=suffix, failure=failure, occurrence=occurrence), \
                            tempfile.TemporaryDirectory() as directory:
                        native, f, calls = self.native(), Fixture(directory), []
                        successful = self.transport(native, f, calls)
                        seen = 0
                        def transport(argv, env, stage, timeout, *, suffix=suffix, occurrence=occurrence,
                                      failure=failure, successful=successful):
                            nonlocal seen
                            if len(argv) > 4 and argv[4].startswith("repos/" + a.c.REPOSITORY + suffix):
                                seen += 1
                                if seen == occurrence:
                                    if failure in a.SMOKE_FAILURES:
                                        raise a.smoke.SmokeError("synthetic", failure)
                                    if failure == "decode":
                                        return 0, b"HTTP/2.0 200 OK\r\n\r\n{" + HOSTILE.encode(), b""
                                    status = failure[-3:]
                                    return 1, f"HTTP/2.0 {status} Synthetic\r\n\r\n".encode(), HOSTILE.encode()
                            return successful(argv, env, stage, timeout)
                        with patch.object(a.smoke, "run_bounded", side_effect=transport), self.assertRaises(a.AcceptanceStop) as caught:
                            a.accept(f.ctx, IDS, f.checkout, api=native, tool=f.command, command=native.verify, state=f.state)
                        error = caught.exception
                        self.assertEqual(seen, occurrence)  # No retry after the failed read.
                        if failure in a.SMOKE_FAILURES:
                            code, joined = a.SMOKE_FAILURES[failure]
                            status = None
                        elif failure == "decode":
                            code, joined, status = "acceptance_decode", True, 200
                        else:
                            code = "acceptance_" + ("recheck" if occurrence == 2 else boundary)
                            joined, status = True, int(failure[-3:])
                        self.assertEqual(error.code, code)
                        self.assertEqual(error.status, status)
                        self.assertIs(native.joined, joined)
                        self.assertIs(error.observed["cryptographically_verified"], occurrence == 2)
                        self.assertFalse(error.observed["accepted_release_observed"])
                        self.assertNotIn(HOSTILE, json.dumps(a.diagnostic(error, f.state)))
                        if f.sdk_calls:
                            workspace = Path(f.sdk_calls[0][3]).parent
                            self.addCleanup(lambda path=workspace: shutil.rmtree(path, ignore_errors=True))
                            self.assertIs(workspace.exists(), not joined)
                            if not joined:
                                self.assertEqual(error.secondary.code, "acceptance_join")

    def test_inventory_reader_seam_keeps_legacy_default_projection(self):
        native = self.native()
        for code in a.NATIVE_FAILURES:
            with patch.object(native, "repo", side_effect=r.Stop(code, status=200)):
                with self.assertRaises(r.Stop) as legacy:
                    a.s.inventory(native, "/actions/runs/20/artifacts", "artifacts")
                self.assertEqual(legacy.exception.code, "signing_inventory_read")
                self.assertEqual(legacy.exception.status, 200)
                with self.assertRaises(r.Stop) as selected:
                    native.pages("/actions/runs/20/artifacts", "artifacts")
                self.assertEqual(selected.exception.code, code)
                self.assertEqual(selected.exception.status, 200)

    def test_check_failure_propagation_seam_keeps_legacy_default_filtering(self):
        with tempfile.TemporaryDirectory() as directory:
            f = Fixture(directory)
            original = f.api.repo
            for code in a.NATIVE_FAILURES | {"acceptance_read"}:
                def read(suffix="", *, code=code):
                    if suffix == "/actions/runs/10/attempts/1":
                        raise r.Stop(code, status=403)
                    return original(suffix)
                with patch.object(f.api, "repo", side_effect=read):
                    self.assertEqual(a.p.qualified_checks(f.api, f.ctx), [])
                    with self.assertRaises(r.Stop) as selected:
                        a.p.qualified_checks(f.api, f.ctx, propagate=a.NATIVE_FAILURES | {"acceptance_read"})
                    self.assertEqual(selected.exception.code, code)
                    self.assertEqual(selected.exception.status, 403)

    def test_native_pages_complete_count_duplicate_and_cap_fail_closed(self):
        native = self.native()
        for response in ({"total_count": 2, "artifacts": [{"id": 1}]},
                         {"total_count": 2, "artifacts": [{"id": 1}, {"id": 1}]},
                         {"total_count": True, "artifacts": []}):
            with patch.object(native, "repo", return_value=response), self.assertRaises(r.Stop):
                native.pages("/actions/runs/20/artifacts", "artifacts")
        with patch.object(native, "repo", return_value={"total_count": 100, "artifacts": [{"id": i + 1} for i in range(100)]}), \
                patch.object(a.s, "PAGE_CAP", 1), self.assertRaises(r.Stop):
            native.pages("/actions/runs/20/artifacts", "artifacts")

    def test_native_get_observed_status_no_absence_or_fabricated_status_and_fixed_get(self):
        native = self.native()
        for status in (200, 403, 404, 429, 500):
            raw = f"HTTP/2.0 {status} Synthetic\r\nX-Hostile: {HOSTILE}\r\n\r\n".encode() + b'{"login":"ajbt200128"}'
            with patch.object(a.smoke, "run_bounded", return_value=(0 if status == 200 else 1, raw, HOSTILE.encode())) as run:
                if status == 200:
                    self.assertEqual(native.identity(), {"login": a.c.OWNER})
                else:
                    with self.assertRaises(r.Stop) as caught:
                        native.identity()
                    self.assertEqual(caught.exception.status, status)
                    self.assertNotIn(HOSTILE, json.dumps(r.diagnostic(caught.exception)))
                argv, env, _, _ = run.call_args.args
                self.assertEqual(argv[:5], [str(a.o.NATIVE_GH), "api", "--method", "GET", "user"])
                self.assertIn("--include", argv)
                self.assertEqual(env, native.env)
                self.assertTrue(native.joined)
        with patch.object(a.smoke, "run_bounded", return_value=(1, HOSTILE.encode(), b"")):
            with self.assertRaises(r.Stop) as caught:
                native.identity()
            self.assertIsNone(caught.exception.status)
        with patch.object(a.smoke, "run_bounded") as run:
            for path in ("https://hostile/", "repos/other/repo", "user?token=hostile", "repos/" + a.c.REPOSITORY + "/git/ref/heads/main/../other"):
                with self.assertRaises(r.Stop):
                    native.get(path)
            run.assert_not_called()

    def test_native_octet_sdk_command_contract_and_join_failure(self):
        native = self.native()
        with patch.object(a.smoke, "run_bounded", return_value=(0, b"public bytes", b"")) as run:
            self.assertEqual(native.asset(501), b"public bytes")
            self.assertEqual(run.call_args.args[0], [str(a.o.NATIVE_GH), "api", "--method", "GET",
                             f"repos/{a.c.REPOSITORY}/releases/assets/501", "-H", "Accept: application/octet-stream"])
            self.assertNotIn("--include", run.call_args.args[0])
        for constraint, joined in (("child_join_unconfirmed", False), ("output_reader_joined", False),
                                   ("child_start_unavailable", True), ("child_deadline_unknown_outcome", True),
                                   ("output_within_64k", False)):
            with patch.object(a.smoke, "run_bounded", side_effect=a.smoke.SmokeError("synthetic", constraint)):
                with self.assertRaises(r.Stop) as caught:
                    a.stage("recheck", lambda: a.stage("download", lambda: native.asset(501)))
                self.assertEqual(caught.exception.code, a.SMOKE_FAILURES[constraint][0])
                self.assertIs(native.joined, joined)
                self.assertNotIn(HOSTILE, json.dumps(r.diagnostic(caught.exception)))

    def test_native_metadata_and_attestation_decoding_remain_separate(self):
        native = self.native()
        for body in (b"{", b"\xff", HOSTILE.encode()):
            with patch.object(a.smoke, "run_bounded", return_value=(0, b"HTTP/2.0 200 OK\r\n\r\n" + body, b"")):
                with self.assertRaises(r.Stop) as caught:
                    a.stage("auth", native.identity)
                self.assertEqual(caught.exception.code, "acceptance_decode")
                self.assertEqual(caught.exception.status, 200)
            with patch.object(a.smoke, "run_bounded", return_value=(0, body, b"")):
                with self.assertRaises(r.Stop) as caught:
                    a.stage("signature", lambda: r.verify_file(
                        "/synthetic/manifest", a.c.REPOSITORY, "2" * 40, "v0.2.0", command=native.verify))
                self.assertEqual(caught.exception.code, "acceptance_decode")
                self.assertIsNone(caught.exception.status)
                self.assertNotIn(HOSTILE, json.dumps(r.diagnostic(caught.exception)))

    def test_verify_file_dynamic_default_and_explicit_port_compatibility(self):
        with patch.object(r, "read_command", return_value=b"[]") as default:
            self.assertEqual(r.verify_file("/synthetic/manifest", a.c.REPOSITORY, "2" * 40, "v0.2.0"), [])
            argv = default.call_args.args[0]
        self.assertEqual(argv[:4], ["gh", "attestation", "verify", "/synthetic/manifest"])
        self.assertEqual(argv[argv.index("--source-digest") + 1], "2" * 40)
        self.assertEqual(argv[argv.index("--signer-digest") + 1], "2" * 40)
        self.assertIn("--deny-self-hosted-runners", argv)
        self.assertEqual(argv[argv.index("--predicate-type") + 1], r.PREDICATE)
        native = self.native()
        with patch.object(a.smoke, "run_bounded", return_value=(0, b"[]", b"")) as run, \
                patch.object(r, "read_command", side_effect=AssertionError("wrong environment")):
            self.assertEqual(r.verify_file("/synthetic/manifest", a.c.REPOSITORY, "2" * 40, "v0.2.0", command=native.verify), [])
            self.assertEqual(run.call_args.args[0], [str(a.o.NATIVE_GH), *argv[1:]])
        for output in (b"hostile", b"{"):
            with self.assertRaises(r.Stop) as caught:
                r.verify_file("/synthetic/manifest", a.c.REPOSITORY, "2" * 40, "v0.2.0", command=lambda _, output=output: output)
            self.assertEqual(caught.exception.code, "signature")

    def test_actual_subprocess_closed_descriptors_and_broken_pipe_do_not_leak(self):
        scripts = Path(__file__).resolve().parent
        base = f"import sys, os, importlib.util; sys.path.insert(0, {str(scripts)!r}); " \
               f"s=importlib.util.spec_from_file_location('acceptance', {str(scripts / 'public-release-acceptance.py')!r}); " \
               "a=importlib.util.module_from_spec(s); s.loader.exec_module(a); "
        for setup in ("os.close(1); ", "r,w=os.pipe(); os.close(r); os.dup2(w,1); os.close(w); "):
            result = subprocess.run([sys.executable, "-c", base + setup + "sys.exit(a.main(['status']))"],
                                    capture_output=True, env={"PYTHONDONTWRITEBYTECODE": "1"}, timeout=10)
            self.assertEqual(result.returncode, 1)
            self.assertEqual(result.stdout, b"")
            self.assertNotIn(b"Traceback", result.stderr)
            value = json.loads(result.stderr)
            self.assertEqual(value["code"], "DELIVERY_ACCEPTANCE_OUTPUT")
            self.assertFalse(value["cryptographically_verified"])
        pins = {"source": "1" * 40, "release": "2" * 40, "tag": "v0.2.0", "run": 30,
                "checks_run": 10, "checks_attempt": 1, "release_checks_run": 11, "release_checks_attempt": 1,
                "qualification_run": 20, "qualification_attempt": 1, "image": "sha256:" + "a" * 64,
                "archive": "b" * 64, "phase": a.c.PHASE, "a": 100, "b": 101, "archive_id": 102}
        args = ["accept", *[item for flag, key in a.FLAGS.items() for item in (flag, str(pins[key]))]]
        accepted = "a.accept=lambda *args, state, **kwargs: (state.update(release_observed=True, cryptographically_verified=True, accepted_release_observed=True) or dict(state, passed=True)); "
        result = subprocess.run([sys.executable, "-c", base + accepted + f"os.close(1); sys.exit(a.main({args!r}))"],
                                capture_output=True, env={"PYTHONDONTWRITEBYTECODE": "1"}, timeout=10)
        self.assertEqual(result.returncode, 1)
        value = json.loads(result.stderr)
        self.assertTrue(value["release_observed"])
        self.assertTrue(value["cryptographically_verified"])
        self.assertTrue(value["accepted_release_observed"])
        self.assertFalse(value["publication_attempted"])
        self.assertNotIn(b"Traceback", result.stderr)
        result = subprocess.run([sys.executable, "-c", base + "os.close(1); os.close(2); sys.exit(a.main(['status']))"],
                                capture_output=True, env={"PYTHONDONTWRITEBYTECODE": "1"}, timeout=10)
        self.assertEqual(result.returncode, 1)
        self.assertEqual((result.stdout, result.stderr), (b"", b""))


if __name__ == "__main__":
    unittest.main()
