"""Offline PUBLIC-source owner tests; synthetic evidence, no native/API work."""
import importlib.util
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from contextlib import ExitStack, contextmanager
from pathlib import Path
from unittest.mock import Mock, patch

import release as r
import test_owner_synthetic_publication as legacy


def load_tool(name="public_owner_publication_tests"):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name("public-owner-publication.py"))
    if spec is None or spec.loader is None:
        raise AssertionError("fixture module unavailable")
    tool = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tool)
    return tool


public = load_tool()
o, c, p, d = public.o, public.c, public.p, public.d
HOSTILE = "hostile_SYNTHETIC_SECRET_SENTINEL"


class PublicOwnerTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="intake-public-owner-test-")
        self.addCleanup(temporary.cleanup)
        self.f = legacy.NativeFixture(temporary.name)
        self.f.api.repo_data.update(name="possums-intake", private=False)
        self.ctx = {key: value for key, value in self.f.api.ctx.items() if key != "run"}
        self.ctx.update(expected_image=self.f.digest, expected_archive=self.f.archive_hash,
                        writers_quiet_confirmed=True, checks_run=10, checks_attempt=1)
        self.stack = ExitStack()
        self.addCleanup(self.stack.close)
        self.stack.enter_context(patch.dict(os.environ, {"GITHUB_TOKEN": HOSTILE, "GITHUB_ACTOR": p.OWNER}))
        self.stack.enter_context(patch("urllib.request.OpenerDirector.open", side_effect=AssertionError(HOSTILE)))
        self.stack.enter_context(patch.object(o.smoke, "run_bounded", side_effect=AssertionError(HOSTILE)))
        self.stack.enter_context(patch.object(p, "command", side_effect=AssertionError(HOSTILE)))

    def args(self):
        return ["publish", "--sourceSHA", self.ctx["source"], "--qualification-run", "20",
                "--qualification-attempt", "1", "--expected-image", self.f.digest,
                "--expected-archive", self.f.archive_hash, "--writers-quiet-confirmed", "true",
                "--checks-run", "10", "--checks-attempt", "1"]

    def refused(self, operation, code=None, outcome="no_effect"):
        with self.assertRaises(r.Stop) as caught:
            operation()
        error = caught.exception
        if code:
            self.assertEqual(error.code, code)
        self.assertEqual(error.outcome, outcome)
        diagnostic = json.dumps(o.diagnostic(error))
        self.assertNotIn(HOSTILE, diagnostic)
        self.assertNotIn("hostile", diagnostic)
        return error

    def instance(self, auth=None, admitter=public.admit):
        admitted = public.admit(self.f.api, self.ctx)
        tools = o.Tools({})
        tools.skopeo = self.f.tool
        return o.Publication(self.f.api, self.ctx, self.f.root, admitted, tools, auth,
                             admitter=admitter)

    def copies(self):
        return [args for args in self.f.calls if args[1] == "copy"]

    @contextmanager
    def native_fixtures(self):
        original_temp = tempfile.mkdtemp
        def workspace(*args, **kwargs):
            if kwargs.get("prefix") == "intake-owner-public-":
                return str(self.f.root)
            return original_temp(*args, **kwargs)
        with patch.object(o, "native_env", return_value={"HOME": "/fixture", "PATH": "/fixture"}), \
                patch.object(o.Tools, "gh", return_value=HOSTILE.encode()) as gh, \
                patch.object(o, "verify_owner") as owner, patch.object(d, "GitHub", return_value=self.f.api), \
                patch.object(o.tempfile, "mkdtemp", side_effect=workspace), \
                patch.object(o.Tools, "skopeo", side_effect=self.f.tool):
            yield gh, owner

    def test_old_defaults_still_require_private_source_and_new_policy_requires_public(self):
        self.assertEqual((c.REPOSITORY, c.PACKAGE, c.IMAGE), (p.REPO, p.PACKAGE, p.REGISTRY))
        self.refused(lambda: o.admit(self.f.api, self.ctx), "synthetic_approval")
        self.refused(lambda: legacy.n.admit(self.f.api, self.ctx), "synthetic_approval")
        self.assertEqual(public.admit(self.f.api, self.ctx)["artifacts"], {"a": 100, "b": 101, "archive": 102})
        self.f.api.repo_data["private"] = True
        o.admit(self.f.api, self.ctx)
        self.refused(lambda: public.admit(self.f.api, self.ctx), "public_repository")
        self.assertEqual(self.copies(), [])

    def test_public_source_identity_visibility_and_package_are_independent(self):
        repository = self.f.api.repo_data
        package = self.f.api.before[1]
        vectors = [(repository, key, value) for key, value in (
            ("private", True), ("private", None), ("private", 0), ("id", True), ("id", 0),
            ("full_name", "ajbt200128/possums-intake-deploy"), ("name", HOSTILE),
            ("owner", None), ("owner", {"login": HOSTILE}), ("default_branch", "feature"))]
        vectors += [(package, key, value) for key, value in (
            ("visibility", "private"), ("id", True), ("id", 0), ("name", HOSTILE),
            ("package_type", "npm"), ("owner", None), ("owner", {"login": HOSTILE}))]
        for target, key, value in vectors:
            with self.subTest(key=key, value=value):
                previous = target[key]
                target[key] = value
                try:
                    with patch.object(p, "admit_qualified") as qualification, \
                            patch.object(p.Authfile, "prepare") as auth:
                        self.refused(lambda: public.admit(self.f.api, self.ctx), "public_repository")
                        qualification.assert_not_called()
                        auth.assert_not_called()
                finally:
                    target[key] = previous
        for bad in (None, [], HOSTILE):
            with patch.object(self.f.api, "repo", return_value=bad):
                self.refused(lambda: public.admit(self.f.api, self.ctx), "public_repository")
            with patch.object(self.f.api, "package", return_value=(200, bad)):
                self.refused(lambda: public.admit(self.f.api, self.ctx), "public_repository")
        self.assertEqual(self.copies(), [])

    def test_optional_package_source_association_is_not_authority(self):
        class UnreadableAssociation(dict):
            def get(self, *args):
                raise AssertionError(HOSTILE)
            def __getitem__(self, key):
                raise AssertionError(HOSTILE)
        package = self.f.api.before[1]
        package.pop("repository")
        public.admit(self.f.api, self.ctx)
        for value in (None, True, HOSTILE, [], {}, {"id": 999, "private": True}, UnreadableAssociation()):
            package["repository"] = value
            public.admit(self.f.api, self.ctx)
        # A public association cannot rescue a private source.
        package["repository"] = dict(self.f.api.repo_data)
        self.f.api.repo_data["private"] = True
        self.refused(lambda: public.admit(self.f.api, self.ctx), "public_repository")

    def test_package_http_status_and_hostile_read_failures_are_closed_without_fallback(self):
        for status in (404, 403, 503, None, True, 200.0):
            with patch.object(self.f.api, "package", return_value=(status, self.f.api.before[1])):
                error = self.refused(lambda: public.admit(self.f.api, self.ctx), "diagnostic_package_read")
                self.assertEqual(error.status, status if type(status) is int else None)
        for method, code in (("repo", "diagnostic_evidence"), ("package", "diagnostic_package_read")):
            for error in (RuntimeError(HOSTILE), KeyboardInterrupt(HOSTILE)):
                with patch.object(self.f.api, method, side_effect=error), \
                        patch.object(p, "repository_gate") as private:
                    self.refused(lambda: public.admit(self.f.api, self.ctx), code)
                    private.assert_not_called()
        self.assertEqual(self.copies(), [])

    def test_native_context_checks_precede_all_evidence_and_no_actions_publisher_is_forged(self):
        for key, value in (("checks_run", True), ("checks_attempt", "1"), ("checks_run", 0),
                           ("writers_quiet_confirmed", False), ("baseline", "sha256:" + "0" * 64)):
            with patch.object(self.f.api, "repo") as repo, patch.object(self.f.api, "package") as package:
                self.refused(lambda key=key, value=value: public.admit(self.f.api, dict(self.ctx, **{key: value})))
                repo.assert_not_called()
                package.assert_not_called()
        self.f.api.current.update(status="completed", actor={"login": HOSTILE})
        self.f.api.checks = []  # Exact native check reads, never successful discovery fallback.
        with patch.object(p, "context", side_effect=AssertionError(HOSTILE)) as context:
            public.admit(self.f.api, self.ctx)
            context.assert_not_called()
        self.assertNotIn("/actions/runs/30", self.f.api.reads)
        self.assertIn("/actions/runs/10/attempts/1", self.f.api.reads)
        self.assertFalse(any("/actions/workflows/check.yml/runs" in path for path in self.f.api.reads))
        self.assertEqual(o.parse(self.args()), self.ctx)

    def test_exact_checks_qualification_source_runners_and_artifacts_still_bind(self):
        vectors = [(self.f.api.check_run, "id", 11), (self.f.api.check_attempt, "run_attempt", 2),
                   (self.f.api.check_attempt, "head_sha", "2" * 40),
                   (self.f.api.qualified, "run_attempt", 2),
                   (self.f.api.qualified, "head_repository", {"full_name": "fork/repo"}),
                   (self.f.api.jobs[2], "runner_id", 2), (self.f.api.artifacts[0], "expired", True),
                   (self.f.api.artifacts[0]["workflow_run"], "repository_id", 100)]
        for target, key, value in vectors:
            with self.subTest(key=key):
                previous = target[key]
                target[key] = value
                try:
                    with patch.object(p.Authfile, "prepare") as auth:
                        self.refused(lambda: public.admit(self.f.api, self.ctx))
                        auth.assert_not_called()
                finally:
                    target[key] = previous
        self.f.api.main = "2" * 40
        self.refused(lambda: public.admit(self.f.api, self.ctx), "stale")
        self.assertEqual(self.copies(), [])

    def test_public_download_uses_same_exact_names_and_admission_on_both_sides(self):
        admitted = public.admit(self.f.api, self.ctx)
        tools = o.Tools({})
        with patch.object(tools, "gh", return_value=b"") as gh:
            o.download(self.f.api, self.ctx, admitted, self.f.root, tools, HOSTILE, admitter=public.admit)
            self.assertEqual(gh.call_count, 3)
            self.assertEqual([call.args[0][6] for call in gh.call_args_list], list(o.artifact_names(self.ctx).values()))
            self.assertTrue(all(call.args[0][4] == "github.com/" + c.REPOSITORY for call in gh.call_args_list))
            gh.reset_mock()
            self.f.api.artifacts[0]["id"] = 999
            self.refused(lambda: o.download(self.f.api, self.ctx, admitted, self.f.root, tools, HOSTILE,
                                           admitter=public.admit), "owner_download")
            gh.assert_not_called()
        self.f.api.artifacts[0]["id"] = 100
        def changed(*args):
            self.f.api.artifacts[0]["id"] = 999
        with patch.object(tools, "gh", side_effect=changed) as gh:
            self.refused(lambda: o.download(self.f.api, self.ctx, admitted, self.f.root, tools, HOSTILE,
                                           admitter=public.admit), "owner_download")
            self.assertEqual(gh.call_count, 3)
        self.assertEqual(self.copies(), [])

    def test_same_executor_copies_once_preserves_digest_and_sticky_completed(self):
        instance = self.instance()
        receipt = instance.execute()
        self.assertEqual(receipt, {"publicSourceSHA": self.ctx["source"], "imageDigest": self.f.digest,
                                   "archiveSHA": self.f.archive_hash})
        self.assertEqual(len(self.copies()), 1)
        self.assertEqual(self.copies()[0][1:6], ["copy", "--preserve-digests", "--retry-times", "0", "--authfile"])
        self.assertEqual(self.copies()[0][-1], "docker://" + c.IMAGE + ":" + self.ctx["source"])
        self.assertFalse(self.f.authpath.parent.exists())
        self.refused(instance.execute, "diagnostic_used", "completed")
        self.assertEqual(len(self.copies()), 1)

    def test_completed_postcheck_and_cleanup_failures_are_sticky_and_closed(self):
        class BadCleanup(p.Authfile):
            def cleanup(self):
                super().cleanup()
                raise RuntimeError(HOSTILE)
        self.f.post_raw = self.f.raw + b"changed"
        instance = self.instance(BadCleanup())
        error = self.refused(instance.execute, "diagnostic_public", "completed")
        self.assertEqual(o.diagnostic(error)["secondary"],
                         [r.diagnostic(r.Stop("synthetic_cleanup", outcome="completed"))])
        self.refused(instance.execute, "diagnostic_used", "completed")
        self.assertEqual(len(self.copies()), 1)

    def test_unknown_copy_and_cleanup_only_failure_never_replay(self):
        self.f.copy_error = RuntimeError(HOSTILE)
        instance = self.instance()
        self.refused(instance.execute, "diagnostic_copy", "unknown")
        self.refused(instance.execute, "diagnostic_used", "unknown")
        self.assertEqual(len(self.copies()), 1)
        self.f.copy_error, self.f.api.copied = None, False
        class BadCleanup(p.Authfile):
            def cleanup(self):
                super().cleanup()
                raise KeyboardInterrupt(HOSTILE)
        instance = self.instance(BadCleanup())
        self.refused(instance.execute, "synthetic_cleanup", "completed")
        self.refused(instance.execute, "diagnostic_used", "completed")
        self.assertEqual(len(self.copies()), 2)  # Two separate synthetic sessions, one each.

    def test_refreshed_admission_and_unknown_callback_refuse_before_copy(self):
        admitted = public.admit(self.f.api, self.ctx)
        callback = Mock(side_effect=[admitted, RuntimeError(HOSTILE)])
        instance = self.instance(admitter=callback)
        self.refused(instance.execute, "diagnostic_evidence")
        self.assertEqual(callback.call_count, 2)
        self.assertIsNone(instance.auth.directory)
        self.assertEqual(self.copies(), [])
        instance = self.instance()
        prepare = instance.auth.prepare
        def changed():
            path = prepare()
            self.f.api.repo_data["private"] = True
            return path
        with patch.object(instance.auth, "prepare", side_effect=changed):
            self.refused(instance.execute, "public_repository")
        self.assertEqual(self.copies(), [])

    def test_archive_pins_used_tag_and_startup_evidence_refuse_before_registry_auth(self):
        instance = self.instance()
        for key in ("expected_image", "expected_archive"):
            previous = instance.ctx[key]
            instance.ctx[key] = ("sha256:" if key == "expected_image" else "") + "2" * 64
            with patch.object(instance.auth, "prepare") as auth:
                self.refused(instance.execute, "synthetic_archive")
                auth.assert_not_called()
            instance.ctx[key] = previous
        self.f.synthetic["startup_diagnostics"]["closed_failure_output"] = False
        self.f.save()
        with patch.object(instance.auth, "prepare") as auth:
            self.refused(instance.execute, "diagnostic_startup")
            auth.assert_not_called()
        self.f.synthetic["startup_diagnostics"]["closed_failure_output"] = True
        self.f.save()
        self.f.api.version_pages[0][0]["metadata"]["container"]["tags"] = [self.ctx["source"]]
        with patch.object(instance.auth, "prepare") as auth:
            self.refused(instance.execute, "diagnostic_tag")
            auth.assert_not_called()
        self.assertEqual(self.copies(), [])

    def test_public_cli_reuses_full_owner_download_executor_and_closed_receipt(self):
        with self.native_fixtures() as (gh, owner), patch("sys.stdout", new_callable=io.StringIO) as output:
            self.assertEqual(public.main(self.args()), 0)
        self.assertEqual(gh.call_count, 4)  # One fake native token read and three exact downloads.
        owner.assert_called_once_with(HOSTILE)
        self.assertEqual(json.loads(output.getvalue()), {"publicSourceSHA": self.ctx["source"],
                         "imageDigest": self.f.digest, "archiveSHA": self.f.archive_hash})
        self.assertNotIn(HOSTILE, output.getvalue())
        self.assertEqual(len(self.copies()), 1)
        self.assertFalse(self.f.root.exists())

    def test_presentation_and_workspace_cleanup_cannot_erase_completed(self):
        for failure in (RuntimeError(HOSTILE), r.Stop("public_command"), KeyboardInterrupt(HOSTILE)):
            # Presentation fails after real synthetic execution; isolate each session.
            self.f.api.copied = False
            with self.native_fixtures(), patch.object(o.shutil, "rmtree", wraps=o.shutil.rmtree) as remove:
                self.refused(lambda failure=failure: o.publish(self.ctx, admitter=public.admit,
                                                               presenter=Mock(side_effect=failure)),
                             "synthetic_receipt", "completed")
                self.assertNotIn(self.f.root, [call.args[0] for call in remove.call_args_list])
            self.assertTrue(self.f.root.exists())
        self.f.api.copied = False
        original_remove = o.shutil.rmtree
        def remove(path, *args, **kwargs):
            if path == self.f.root:
                raise RuntimeError(HOSTILE)
            return original_remove(path, *args, **kwargs)
        with self.native_fixtures(), patch.object(o.shutil, "rmtree", side_effect=remove):
            self.refused(lambda: o.publish(self.ctx, admitter=public.admit, presenter=lambda receipt: None),
                         "owner_workspace", "completed")
        self.assertEqual(len(self.copies()), 4)

    def test_cli_presentation_failure_preserves_completed_on_other_stream(self):
        out, err = io.StringIO(), io.StringIO()
        with self.native_fixtures(), patch("sys.stdout", out), patch("sys.stderr", err), \
                patch.object(out, "flush", side_effect=OSError(HOSTILE)):
            self.assertEqual(public.main(self.args()), 1)
        value = json.loads(err.getvalue())
        self.assertEqual((value["code"], value["outcome"]), ("DELIVERY_SYNTHETIC_RECEIPT", "completed"))
        self.assertNotIn(HOSTILE, err.getvalue())
        self.assertEqual(len(self.copies()), 1)
        self.assertTrue(self.f.root.exists())

    def test_bad_authenticated_source_stops_before_download_workspace_or_registry_auth(self):
        self.f.api.repo_data["full_name"] = "fork/repo"
        with self.native_fixtures() as (gh, owner), patch.object(o, "download") as download, \
                patch.object(o.tempfile, "mkdtemp") as workspace, patch.object(p.Authfile, "prepare") as auth:
            self.refused(lambda: o.publish(self.ctx, admitter=public.admit), "public_repository")
            gh.assert_called_once_with(["auth", "token", "--hostname", "github.com"], "owner_auth")
            owner.assert_called_once_with(HOSTILE)
            download.assert_not_called()
            workspace.assert_not_called()
            auth.assert_not_called()
        self.assertEqual(self.copies(), [])

    def test_bad_source_and_invalid_cli_start_no_auth_copy_or_workspace(self):
        bad = self.args()
        bad[2] = HOSTILE
        cases = (["publish"], ["admit"], [HOSTILE], bad, self.args() + ["--unknown", HOSTILE])
        with patch.object(o, "native_env") as env, patch.object(o, "Tools") as tools, \
                patch.object(o.tempfile, "mkdtemp") as workspace, patch.object(o, "publish") as publish:
            for args in cases:
                with patch("sys.stdout", new_callable=io.StringIO) as output:
                    self.assertEqual(public.main(args), 1)
                self.assertEqual(json.loads(output.getvalue())["code"], "DELIVERY_ARGUMENTS")
                self.assertNotIn(HOSTILE, output.getvalue())
            for mock in (env, tools, workspace, publish):
                mock.assert_not_called()
        self.assertEqual(self.copies(), [])

    def test_import_status_and_report_do_not_read_environment_or_initiate_work(self):
        class NoEnvironment(dict):
            def get(self, *args):
                raise AssertionError(HOSTILE)
            def __getitem__(self, key):
                raise AssertionError(HOSTILE)
            def __iter__(self):
                raise AssertionError(HOSTILE)
        with patch.object(os, "environ", NoEnvironment()), \
                patch.object(subprocess, "Popen", side_effect=AssertionError(HOSTILE)) as process, \
                patch.object(tempfile, "mkdtemp", side_effect=AssertionError(HOSTILE)) as workspace:
            tool = load_tool("public_owner_passive_import_tests")
            with patch.object(tool.o, "publish") as publish, patch.object(tool.o, "native_env") as env:
                for args in ([], ["status"], ["report"], [HOSTILE]):
                    with patch("sys.stdout", new_callable=io.StringIO) as output:
                        self.assertEqual(tool.main(args), 1 if args == [HOSTILE] else 0)
                    self.assertNotIn(HOSTILE, output.getvalue())
                    if args != [HOSTILE]:
                        self.assertFalse(json.loads(output.getvalue())["runtime_verified"])
                publish.assert_not_called()
                env.assert_not_called()
            process.assert_not_called()
            workspace.assert_not_called()

    def test_unexpected_cli_error_is_closed_and_diagnostic_delivery_does_not_retry_work(self):
        with patch.object(o, "publish", side_effect=RuntimeError(HOSTILE)) as publish, \
                patch("sys.stdout", new_callable=io.StringIO) as output:
            self.assertEqual(public.main(self.args()), 1)
        publish.assert_called_once_with(self.ctx, admitter=public.admit, presenter=public.present)
        self.assertEqual(json.loads(output.getvalue())["code"], "DELIVERY_DIAGNOSTIC_INTERNAL")
        self.assertNotIn(HOSTILE, output.getvalue())
        for method in ("write", "flush"):
            out, err = io.StringIO(), io.StringIO()
            with patch("sys.stdout", out), patch("sys.stderr", err), \
                    patch.object(out, method, side_effect=OSError(HOSTILE)) as delivery, \
                    patch.object(o, "publish") as publish:
                self.assertEqual(public.main(["status"]), 1)
            self.assertEqual(delivery.call_count, 1)
            self.assertEqual(json.loads(err.getvalue())["code"], "DELIVERY_PUBLIC_COMMAND")
            publish.assert_not_called()


class PublicOwnerPipeTests(unittest.TestCase):
    def test_actual_cli_closed_stdout_and_stderr_never_emit_raw_shutdown_errors(self):
        script = str(Path(__file__).with_name("public-owner-publication.py"))
        for args, code in ((["status"], "DELIVERY_PUBLIC_COMMAND"), (["report"], "DELIVERY_PUBLIC_COMMAND"),
                           ([HOSTILE], "DELIVERY_ARGUMENTS"), (["publish"], "DELIVERY_ARGUMENTS")):
            for close_stderr in (False, True):
                read_fd, write_fd = os.pipe()
                os.close(read_fd)
                with os.fdopen(write_fd, "wb") as output:
                    result = subprocess.run([sys.executable, script, *args], stdin=subprocess.DEVNULL,
                                            stdout=output, stderr=output if close_stderr else subprocess.PIPE,
                                            timeout=10, check=False)
                self.assertEqual(result.returncode, 1)
                if not close_stderr:
                    self.assertEqual(json.loads(result.stderr)["code"], code)
                    for forbidden in (HOSTILE.encode(), b"Exception ignored", b"BrokenPipeError", b"Traceback"):
                        self.assertNotIn(forbidden, result.stderr)


if __name__ == "__main__":
    unittest.main()
