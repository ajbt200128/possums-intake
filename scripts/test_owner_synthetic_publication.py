"""Focused offline native publisher security regressions; no external activity."""
import copy
import hashlib
import importlib.util
import io
import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import release as r
import test_synthetic_diagnostic_publication as fixtures

_spec = importlib.util.spec_from_file_location("owner_synthetic_publication", Path(__file__).with_name("owner-synthetic-publication.py"))
if _spec is None or _spec.loader is None:
    raise AssertionError("fixture module unavailable")
n = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(n)
p, d = n.p, n.d
SECRET = "SYNTHETIC_SECRET_SENTINEL"  # noqa: S105 - offline synthetic sentinel, not a credential


class NativePinsTests(unittest.TestCase):
    def test_production_pins_match_independently_qualified_packages(self):
        self.assertEqual(n.NATIVE_GH, Path("/nix/store/a1i791nra2ivwww08klq5q97wngql6yn-gh-2.88.1/bin/gh"))
        self.assertEqual(n.NATIVE_SKOPEO, Path("/nix/store/dl4adb8hifsh3gk65qcsxlbnpzk9iwq6-skopeo-1.24.1/bin/skopeo"))
        self.assertEqual(n.NATIVE_PATH,
                         "/nix/store/a1i791nra2ivwww08klq5q97wngql6yn-gh-2.88.1/bin:"
                         "/nix/store/dl4adb8hifsh3gk65qcsxlbnpzk9iwq6-skopeo-1.24.1/bin:"
                         "/usr/bin:/bin:/usr/sbin:/sbin")


class OwnerTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="intake-owner-test-")
        self.addCleanup(self.tmp.cleanup)
        self.f = fixtures.Fixture(self.tmp.name)
        self.ctx = {key: value for key, value in self.f.api.ctx.items() if key != "run"}
        self.ctx.update(expected_image=self.f.digest, expected_archive=self.f.archive_hash,
                        writers_quiet_confirmed=True)
        self.env = patch.dict(os.environ, {"GITHUB_TOKEN": SECRET, "GITHUB_ACTOR": p.OWNER})
        self.env.start()
        self.addCleanup(self.env.stop)
        self.addCleanup(patch.stopall)
        self.tools_tmp = tempfile.TemporaryDirectory(prefix="intake-owner-tools-test-")
        self.addCleanup(self.tools_tmp.cleanup)
        for name, constant in (("gh", "NATIVE_GH"), ("skopeo", "NATIVE_SKOPEO")):
            binary = Path(self.tools_tmp.name) / name
            binary.write_bytes(b"offline fixture; never execute")
            binary.chmod(0o700)
            patch.object(n, constant, binary).start()
        # Any unmocked native/network work is a test failure, never a live probe.
        patch("urllib.request.OpenerDirector.open", side_effect=AssertionError("network forbidden")).start()
        patch.object(n.smoke.subprocess, "Popen", side_effect=AssertionError("native tool forbidden")).start()

    def args(self):
        return ["publish", "--sourceSHA", self.ctx["source"], "--qualification-run", "20",
                "--qualification-attempt", "1", "--expected-image", self.f.digest,
                "--expected-archive", self.f.archive_hash, "--writers-quiet-confirmed", "true"]

    def refused(self, operation, code=None, outcome="no_effect"):
        with self.assertRaises(r.Stop) as caught:
            operation()
        error = caught.exception
        if code:
            self.assertEqual(error.code, code)
        self.assertEqual(error.outcome, outcome)
        value = json.dumps(n.diagnostic(error))
        self.assertNotIn(SECRET, value)
        self.assertNotIn("hostile", value)
        return error

    def instance(self, auth=None):
        admitted = n.admit(self.f.api, self.ctx)
        tools = n.Tools({})
        tools.skopeo = self.f.tool
        return n.Publication(self.f.api, self.ctx, self.f.root, admitted, tools, auth=auth)

    def native_command(self, args, code, timeout=120):
        # The shared fixture models bare adapter input; assert the actual dispatch
        # binding before translating back to that fixture's vocabulary.
        self.assertEqual(args[0], str(n.NATIVE_SKOPEO))
        return self.f.tool(["skopeo", *args[1:]], code, timeout)

    def no_copy(self):
        self.assertFalse(any(args[1] == "copy" for args in self.f.calls))

    def test_default_status_report_and_invalid_arguments_do_zero_work(self):
        with patch.object(n, "publish") as publish, patch.object(n, "native_env") as env, \
                patch.object(Path, "lstat", side_effect=AssertionError("filesystem forbidden")) as stat, \
                patch.object(n.smoke, "run_bounded") as run, patch.object(p, "command") as command:
            for args in ([], ["status"], ["report"]):
                with patch("sys.stdout", new_callable=io.StringIO) as output:
                    n.main(args)
                    self.assertFalse(json.loads(output.getvalue())["runtime_verified"])
            for args in (["publish"], ["hostile"], self.args() + ["--unknown", SECRET]):
                self.refused(lambda args=args: n.main(args), "arguments")
            for index, bad in ((2, SECRET), (4, "01"), (6, "0"), (8, "hostile"),
                               (10, "hostile"), (12, "false"), (11, "--sourceSHA")):
                args = self.args()
                args[index] = bad
                self.refused(lambda args=args: n.main(args), "arguments")
            publish.assert_not_called()
            env.assert_not_called()
            stat.assert_not_called()
            run.assert_not_called()
            command.assert_not_called()
        self.assertEqual(n.parse(self.args()), self.ctx)

    def test_native_real_qualification_without_actions_forgery_ci_guards_unchanged(self):
        original = copy.deepcopy(self.f.api.current)
        self.f.api.current["status"] = "completed"
        self.f.api.current["actor"] = {"login": "not-owner"}
        self.assertEqual(n.admit(self.f.api, self.ctx)["artifacts"], {"a": 100, "b": 101, "archive": 102})
        self.assertNotIn("/actions/runs/30", self.f.api.reads)
        self.refused(lambda: p.admit(self.f.api, self.f.api.ctx, d.WORKFLOW), "synthetic_approval")
        self.f.api.current = original
        p.admit(self.f.api, self.f.api.ctx, d.WORKFLOW)
        self.refused(lambda: p.admit(self.f.api, self.f.api.ctx), "synthetic_approval")
        with patch.object(p, "context", side_effect=AssertionError("Actions forgery")):
            n.admit(self.f.api, self.ctx)
        self.no_copy()

    def test_ci_authority_gates_precede_reusable_qualification_for_both_workflows(self):
        for workflow in (p.WORKFLOW, d.WORKFLOW):
            self.f.api.current["path"] = ".github/workflows/" + workflow
            with patch.object(p, "admit_qualified", return_value={}) as qualified:
                p.admit(self.f.api, self.f.api.ctx, workflow)
                qualified.assert_called_once_with(self.f.api, self.f.api.ctx, self.f.api.repo_data)
            vectors = [(self.f.api.repo_data, "id", 0), (self.f.api.repo_data, "id", True),
                       (self.f.api.repo_data, "full_name", "fork/repo"),
                       (self.f.api.repo_data, "private", False),
                       (self.f.api.repo_data, "owner", {"login": "other"}),
                       (self.f.api.repo_data, "default_branch", "feature")]
            vectors += [(self.f.api.current, key, value) for key, value in (
                ("id", 31), ("run_attempt", 2), ("run_attempt", True),
                ("path", ".github/workflows/other.yml"), ("head_sha", "2" * 40),
                ("head_branch", "feature"), ("event", "push"), ("status", "completed"),
                ("repository", {"full_name": "fork/repo"}),
                ("head_repository", {"full_name": "fork/repo"}),
                ("actor", {"login": "other"}), ("triggering_actor", {"login": "other"}))]
            for target, key, value in vectors:
                with self.subTest(workflow=workflow, key=key, value=value):
                    original = target[key]
                    target[key] = value
                    try:
                        with patch.object(p, "admit_qualified") as qualified:
                            self.refused(lambda workflow=workflow: p.admit(self.f.api, self.f.api.ctx, workflow), "synthetic_approval")
                            qualified.assert_not_called()
                    finally:
                        target[key] = original
        self.no_copy()

    def test_shared_stale_fork_expired_and_runner_guards_refuse_native(self):
        for target, key, value in ((self.f.api.repo_data, "full_name", "fork/repo"),
                                   (self.f.api.repo_data, "private", False),
                                   (self.f.api.qualified, "head_repository", {"full_name": "fork/repo"}),
                                   (self.f.api.artifacts[0], "expired", True),
                                   (self.f.api.jobs[2], "runner_id", 2)):
            original = target[key]
            target[key] = value
            self.refused(lambda: n.admit(self.f.api, self.ctx))
            target[key] = original
        self.f.api.main = "2" * 40
        self.refused(lambda: n.admit(self.f.api, self.ctx), "stale")
        self.no_copy()

    def test_package_wrong_link_used_tag_and_expected_digest_refuse_before_copy(self):
        instance = self.instance()
        self.f.api.before[1]["repository"]["id"] = 100
        self.refused(instance.execute, "diagnostic_package")
        self.f.api.before[1]["repository"]["id"] = 99
        self.f.api.version_pages[0][0]["metadata"]["container"]["tags"] = [self.ctx["source"]]
        self.refused(instance.execute, "diagnostic_package")
        self.f.api.version_pages[0][0]["metadata"]["container"]["tags"] = []
        for key, bad in (("expected_image", "sha256:" + "2" * 64), ("expected_archive", "2" * 64)):
            original = instance.ctx[key]
            instance.ctx[key] = bad
            self.refused(instance.execute, "synthetic_archive")
            instance.ctx[key] = original
        self.no_copy()

    def test_one_copy_exact_receipt_and_reuse_refusal(self):
        instance = self.instance()
        receipt = instance.execute()
        self.assertEqual(receipt, {"publicSourceSHA": self.ctx["source"], "imageDigest": self.f.digest,
                                   "archiveSHA": self.f.archive_hash})
        copies = [args for args in self.f.calls if args[1] == "copy"]
        self.assertEqual(len(copies), 1)
        self.assertEqual(copies[0][-1], "docker://" + p.REGISTRY + ":" + self.ctx["source"])
        self.assertIn("--preserve-digests", copies[0])
        self.assertEqual(copies[0][copies[0].index("--retry-times") + 1], "0")
        self.assertFalse(self.f.authpath.parent.exists())
        self.assertNotIn(SECRET, json.dumps(self.f.calls))
        self.refused(instance.execute, "diagnostic_used", "completed")

    def test_copy_unknown_completed_postfailure_and_cleanup_secondary(self):
        class BadCleanup(p.Authfile):
            def cleanup(self):
                super().cleanup()
                raise RuntimeError("hostile " + SECRET)
        self.f.copy_error = RuntimeError("hostile " + SECRET)
        instance = self.instance(BadCleanup())
        error = self.refused(instance.execute, "diagnostic_copy", "unknown")
        self.assertEqual(n.diagnostic(error)["secondary"], [r.diagnostic(r.Stop("synthetic_cleanup", outcome="unknown"))])
        self.refused(instance.execute, "diagnostic_used", "unknown")
        self.f.copy_error, self.f.api.copied = None, False
        self.f.post_raw = self.f.raw + b"changed"
        instance = self.instance()
        self.refused(instance.execute, "diagnostic_public", "completed")
        self.refused(instance.execute, "diagnostic_used", "completed")
        self.assertEqual(sum(args[1] == "copy" for args in self.f.calls), 2)

    def test_completed_copy_postcheck_and_retained_auth_cleanup_failures_both_project(self):
        class RetainedAuth(p.Authfile):
            def cleanup(self):
                raise RuntimeError("hostile " + SECRET)
        self.f.post_raw = self.f.raw + b"changed"
        instance = self.instance(RetainedAuth())
        error = self.refused(instance.execute, "diagnostic_public", "completed")
        self.assertTrue(instance.tools.joined)
        self.assertTrue(instance.auth.path.exists())
        self.assertEqual(n.diagnostic(error)["secondary"],
                         [r.diagnostic(r.Stop("synthetic_cleanup", outcome="completed"))])
        self.assertEqual(sum(args[1] == "copy" for args in self.f.calls), 1)
        self.refused(instance.execute, "diagnostic_used", "completed")
        p.Authfile.cleanup(instance.auth)  # Joined fake fixture only; no publication retry.

    def test_recheck_current_main_selected_ids_archive_and_package(self):
        for kind in ("main", "ids", "archive", "package", "approval"):
            with self.subTest(kind=kind):
                root = self.f.root / ("changed-" + kind)
                root.mkdir()
                f = fixtures.Fixture(root)
                ctx = dict(self.ctx)
                auth = p.Authfile()
                original = auth.prepare
                tools = n.Tools({})
                tools.skopeo = f.tool
                instance = n.Publication(f.api, ctx, root, n.admit(f.api, ctx), tools, auth)
                def prepare(original=original, kind=kind, f=f, root=root, ctx=ctx, instance=instance):
                    path = original()
                    if kind == "main":
                        f.api.main = "2" * 40
                    elif kind == "ids":
                        f.api.artifacts[0]["id"] = 999
                    elif kind == "archive":
                        (root / "archive/image.tar.gz").write_bytes(b"changed")
                    elif kind == "package":
                        f.api.version_pages[0][0]["metadata"]["container"]["tags"] = [ctx["source"]]
                    else:
                        instance.ctx["writers_quiet_confirmed"] = False
                    return path
                with patch.object(auth, "prepare", side_effect=prepare):
                    self.refused(instance.execute)
                self.assertIsNone(auth.directory)
                self.assertFalse(any(args[1] == "copy" for args in f.calls))

    def test_closed_gh_env_fixed_host_and_same_obtained_token_no_secret_argv(self):
        with patch.dict(os.environ, {"HOME": "/native-home", "GH_CONFIG_DIR": "/native-gh", "PATH": "/hostile-bin",
                                     "GH_TOKEN": SECRET, "GITHUB_TOKEN": "inherited_override",
                                     "HTTPS_PROXY": SECRET, "PROVIDER_KEY": SECRET, "ADMIN_KEY": SECRET,
                                     "ACTIONS_RUNTIME_TOKEN": SECRET}, clear=True):
            env = n.native_env()
            self.assertEqual(set(env), {"HOME", "GH_CONFIG_DIR", "PATH", "GH_HOST", "GH_PROMPT_DISABLED",
                                        "OTEL_SDK_DISABLED", "DO_NOT_TRACK", "GH_NO_UPDATE_NOTIFIER",
                                        "GH_NO_EXTENSION_UPDATE_NOTIFIER"})
            self.assertEqual(env["HOME"], "/native-home")
            self.assertEqual(env["GH_CONFIG_DIR"], "/native-gh")
            self.assertEqual(env["PATH"], n.NATIVE_PATH)
            self.assertNotIn("hostile", env["PATH"])
            self.assertEqual(env["GH_HOST"], "github.com")
            self.assertEqual(env["GH_PROMPT_DISABLED"], "1")
            tools = n.Tools(env)
            with patch.object(n.smoke, "run_bounded", return_value=(0, SECRET.encode(), b"hostile")) as run:
                self.assertEqual(tools.gh(["auth", "token", "--hostname", "github.com"], "owner_auth"), SECRET.encode())
                self.assertEqual(run.call_args.args[0], [str(n.NATIVE_GH), "auth", "token", "--hostname", "github.com"])
                self.assertNotIn("GITHUB_TOKEN", run.call_args.args[1])
                admitted = n.admit(self.f.api, self.ctx)
                n.download(self.f.api, self.ctx, admitted, self.f.root, tools, SECRET)
                self.assertEqual(run.call_count, 4)
                for call in run.call_args_list:
                    self.assertEqual(call.args[0][0], str(n.NATIVE_GH))
                    self.assertNotIn(SECRET, json.dumps(call.args[0]))
                    self.assertEqual(call.args[1]["PATH"], n.NATIVE_PATH)
                    self.assertNotIn("inherited_override", json.dumps(call.args))
                for call in run.call_args_list[1:]:
                    self.assertEqual(call.args[0][1:3], ["run", "download"])
                    self.assertEqual(call.args[1]["GITHUB_TOKEN"], SECRET)
            previous = dict(os.environ)
            with n.owner_environment(env, "obtained_token"):
                self.assertEqual(os.environ["GITHUB_TOKEN"], "obtained_token")
                self.assertEqual(os.environ["GITHUB_ACTOR"], p.OWNER)
                self.assertNotIn("HTTPS_PROXY", os.environ)
                self.assertNotIn("GH_TOKEN", os.environ)
            self.assertEqual(dict(os.environ), previous)

    def test_native_environment_requires_home_but_never_caller_path(self):
        with patch.dict(os.environ, {"HOME": "/native-home"}, clear=True):
            self.assertEqual(n.native_env()["PATH"], n.NATIVE_PATH)
        for env in ({}, {"HOME": "", "PATH": "/hostile-bin"}):
            with patch.dict(os.environ, env, clear=True):
                self.refused(n.native_env, "owner_auth")

    def test_invalid_native_binaries_refuse_before_any_dispatch_or_read(self):
        missing = Path(self.tmp.name) / ("hostile-missing-" + SECRET)
        link = Path(self.tmp.name) / ("hostile-link-" + SECRET)
        link.symlink_to(n.NATIVE_GH)
        directory = Path(self.tmp.name)
        not_executable = Path(self.tmp.name) / ("hostile-nonexec-" + SECRET)
        not_executable.write_bytes(b"never read")
        not_executable.chmod(0o600)
        for constant in ("NATIVE_GH", "NATIVE_SKOPEO"):
            for candidate in (missing, link, directory, not_executable):
                for args in ((["auth", "token"], ["run", "download"]) if constant == "NATIVE_GH"
                             else (["skopeo", "--version"], ["skopeo", "inspect", "source"],
                                   ["skopeo", "copy", "source", "destination"])):
                    with self.subTest(tool=constant, candidate=candidate.name, args=args):
                        tools = n.Tools({})
                        with patch.object(n, constant, candidate), \
                                patch.object(n.smoke, "run_bounded") as run, \
                                patch.object(p, "command") as command, patch.object(Path, "read_bytes") as read:
                            operation = tools.gh if constant == "NATIVE_GH" else tools.skopeo
                            error = self.refused(lambda operation=operation, args=args: operation(args, "owner_auth"), "owner_tool")
                            self.assertNotIn(str(candidate), json.dumps(n.diagnostic(error)))
                            self.assertTrue(tools.joined)
                            run.assert_not_called()
                            command.assert_not_called()
                            read.assert_not_called()

    def test_native_metadata_exceptions_are_closed_and_preserve_join_state(self):
        for target, name in ((Path, "lstat"), (n.os, "access")):
            with patch.object(target, name, side_effect=OSError("hostile " + SECRET)), \
                    patch.object(n.smoke, "run_bounded") as run, patch.object(p, "command") as command, \
                    patch.object(Path, "read_bytes") as read:
                tools = n.Tools({})
                self.refused(lambda tools=tools: tools.gh(["auth", "token"], "owner_auth"), "owner_tool")
                self.assertTrue(tools.joined)
                self.refused(lambda tools=tools: tools.skopeo(["skopeo", "--version"], "synthetic_archive"), "owner_tool")
                self.assertTrue(tools.joined)
                run.assert_not_called()
                command.assert_not_called()
                read.assert_not_called()

    def test_invalid_adapter_arguments_refuse_before_filesystem_and_dispatch(self):
        tools = n.Tools({})
        for operation, args in ((tools.gh, []), (tools.skopeo, []),
                                (tools.skopeo, ["hostile", "copy"]),
                                (tools.skopeo, [str(n.NATIVE_SKOPEO), "inspect"])):
            with patch.object(Path, "lstat") as stat, patch.object(p, "command") as command, \
                    patch.object(n.smoke, "run_bounded") as run, patch.object(Path, "read_bytes") as read:
                self.refused(lambda operation=operation, args=args: operation(args, "diagnostic_copy"), "owner_tool")
                self.assertTrue(tools.joined)
                stat.assert_not_called()
                command.assert_not_called()
                run.assert_not_called()
                read.assert_not_called()

    def test_native_binary_is_revalidated_for_each_call(self):
        tools = n.Tools({})
        with patch.object(n.smoke, "run_bounded", return_value=(0, b"", b"")) as run, \
                patch.object(p, "command", return_value=b"") as command:
            tools.gh(["auth", "token"], "owner_auth")
            n.NATIVE_GH.unlink()
            self.refused(lambda: tools.gh(["run", "download"], "owner_download", SECRET), "owner_tool")
            self.assertTrue(tools.joined)
            run.assert_called_once()
            tools.skopeo(["skopeo", "--version"], "synthetic_archive")
            n.NATIVE_SKOPEO.chmod(0o600)
            self.refused(lambda: tools.skopeo(["skopeo", "inspect", "source"], "diagnostic_public"), "owner_tool")
            self.assertTrue(tools.joined)
            command.assert_called_once()
        # A preflight refusal must not erase an earlier unconfirmed join either.
        tools.joined = False
        self.refused(lambda: tools.gh(["auth", "token"], "owner_auth"), "owner_tool")
        self.assertFalse(tools.joined)

    def test_native_copy_uses_only_exact_bundled_policy_without_tls_or_retry_override(self):
        tools = n.Tools({})
        args = ["skopeo", "copy", "--preserve-digests", "--retry-times", "0", "source", "destination"]
        self.assertEqual(str(n.NATIVE_POLICY), "/nix/store/dl4adb8hifsh3gk65qcsxlbnpzk9iwq6-skopeo-1.24.1/etc/containers/default-policy.json")
        self.assertEqual(n.NATIVE_POLICY_SHA, "cddfaa8e6a7e5497b67cc0dd8e8517058d0c97de91bf46fff867528415f2d946")
        policy = Path(self.tmp.name) / "fixture-policy.json"
        policy.write_bytes(b"synthetic bundled policy fixture")
        with patch.object(n, "NATIVE_POLICY", policy), \
                patch.object(n, "NATIVE_POLICY_SHA", hashlib.sha256(policy.read_bytes()).hexdigest()), \
                patch.object(p, "command", return_value=b"") as command:
            tools.skopeo(args, "diagnostic_copy", 600)
            command.assert_called_once_with([str(n.NATIVE_SKOPEO), "copy", "--policy", str(n.NATIVE_POLICY), *args[2:]], "diagnostic_copy", 600)
            command.reset_mock()
            with patch.object(Path, "read_bytes", return_value=b"changed policy"):
                self.refused(lambda: tools.skopeo(args, "diagnostic_copy"), "owner_policy")
            command.assert_not_called()
            with patch.object(Path, "read_bytes", side_effect=OSError("hostile " + SECRET)):
                self.refused(lambda: tools.skopeo(args, "diagnostic_copy"), "owner_policy")
            command.assert_not_called()
            tools.skopeo(["skopeo", "inspect", "--raw", "source"], "diagnostic_public")
            command.assert_called_once_with([str(n.NATIVE_SKOPEO), "inspect", "--raw", "source"], "diagnostic_public", 120)
            command.reset_mock()
            tools.skopeo(["skopeo", "--version"], "synthetic_archive")
            command.assert_called_once_with([str(n.NATIVE_SKOPEO), "--version"], "synthetic_archive", 120)

    def test_publication_dispatches_all_skopeo_calls_to_pin_with_same_obtained_token(self):
        policy = Path(self.tools_tmp.name) / "policy.json"
        policy.write_bytes(b"synthetic bundled policy fixture")
        env = {"HOME": "/fixture", "PATH": n.NATIVE_PATH}
        tools = n.Tools(env)
        instance = n.Publication(self.f.api, self.ctx, self.f.root,
                                 n.admit(self.f.api, self.ctx), tools)
        def command(args, code, timeout=120):
            self.assertEqual(os.environ["PATH"], n.NATIVE_PATH)
            self.assertEqual(os.environ["GITHUB_TOKEN"], SECRET)
            self.assertNotIn(SECRET, json.dumps(args))
            return self.native_command(args, code, timeout)
        with n.owner_environment(env, SECRET), patch.object(n, "NATIVE_POLICY", policy), \
                patch.object(n, "NATIVE_POLICY_SHA", hashlib.sha256(policy.read_bytes()).hexdigest()), \
                patch.object(p, "command", side_effect=command) as dispatch:
            receipt = instance.execute()
        self.assertEqual(receipt["imageDigest"], self.f.digest)
        invocations = [call.args[0] for call in dispatch.call_args_list]
        self.assertTrue(all(args[0] == str(n.NATIVE_SKOPEO) for args in invocations))
        self.assertEqual({args[1] for args in invocations}, {"--version", "inspect", "copy"})
        copies = [args for args in invocations if args[1] == "copy"]
        self.assertEqual(len(copies), 1)
        self.assertEqual(copies[0][2:4], ["--policy", str(policy)])
        self.assertTrue(tools.joined)
        self.assertFalse(self.f.authpath.parent.exists())

    def test_wrong_symlink_missing_and_directory_policy_prevent_copy_and_reuse(self):
        policy = Path(self.tmp.name) / "fixture-policy.json"
        policy.write_bytes(b"synthetic bundled policy fixture")
        expected_sha = hashlib.sha256(policy.read_bytes()).hexdigest()
        symlink = Path(self.tmp.name) / "fixture-policy-link.json"
        symlink.symlink_to(policy)
        missing = Path(self.tmp.name) / "missing-policy.json"
        for candidate, sha in ((policy, "0" * 64), (symlink, expected_sha),
                               (missing, expected_sha), (Path(self.tmp.name), expected_sha)):
            with self.subTest(candidate=candidate.name):
                tools = n.Tools({})
                instance = n.Publication(self.f.api, self.ctx, self.f.root,
                                         n.admit(self.f.api, self.ctx), tools)
                with patch.object(n, "NATIVE_POLICY", candidate), \
                        patch.object(n, "NATIVE_POLICY_SHA", sha), \
                        patch.object(p, "command", side_effect=self.native_command):
                    self.refused(instance.execute, "owner_policy", "unknown")
                    self.assertTrue(instance.used)
                    self.refused(instance.execute, "diagnostic_used", "unknown")
                self.assertTrue(tools.joined)
                self.assertIsNone(instance.auth.directory)
                self.no_copy()
                self.assertFalse(any("docker://" in arg for args in self.f.calls for arg in args))

    def test_fixed_user_get_same_token_no_redirect_and_owner_required(self):
        class Response:
            status = 200
            login = p.OWNER
            def __enter__(self):
                return self
            def __exit__(self, *args):
                pass
            def read(self, limit):
                self.limit = limit
                return json.dumps({"login": self.login}).encode()
        response = Response()
        with patch("urllib.request.OpenerDirector.open", return_value=response) as get:
            n.verify_owner(SECRET)
            request = get.call_args.args[0]
            self.assertEqual(request.full_url, "https://api.github.com/user")
            self.assertEqual(request.get_method(), "GET")
            self.assertEqual(request.get_header("Authorization"), "Bearer " + SECRET)
            self.assertEqual(response.limit, p.MAX_READ + 1)
            response.login = "other"
            self.refused(lambda: n.verify_owner(SECRET), "owner_auth")
        self.refused(lambda: p.GitHub().get("/user"), "synthetic_evidence")
        self.assertIsNone(p.NoRedirect().redirect_request(None, None, 302, "", {}, "hostile"))

    def test_exact_names_inventory_rechecked_and_closed_public_files(self):
        admitted = n.admit(self.f.api, self.ctx)
        tools = n.Tools({})
        # Existing fixture is exactly the accepted native download layout.
        with patch.object(tools, "gh", return_value=b"") as gh:
            n.download(self.f.api, self.ctx, admitted, self.f.root, tools, SECRET)
            self.assertEqual(gh.call_count, 3)
            for call, name in zip(gh.call_args_list, n.artifact_names(self.ctx).values(), strict=True):
                args = call.args[0]
                self.assertEqual(args[:4], ["run", "download", "20", "--repo"])
                self.assertEqual(args[4], "github.com/" + p.REPO)
                self.assertEqual(args[6], name)
                self.assertNotIn(SECRET, json.dumps(args))
            self.f.api.artifacts.append(copy.deepcopy(self.f.api.artifacts[0]))
            self.refused(lambda: n.download(self.f.api, self.ctx, admitted, self.f.root, tools, SECRET))
            self.f.api.artifacts.pop()
            def changed(*args):
                self.f.api.artifacts[0]["id"] = 999
            with patch.object(tools, "gh", side_effect=changed):
                self.refused(lambda: n.download(self.f.api, self.ctx, admitted, self.f.root, tools, SECRET), "owner_download")
            self.f.api.artifacts[0]["id"] = 100
            (self.f.root / "a/unapproved").write_text("public fixture")
            self.refused(lambda: n.download(self.f.api, self.ctx, admitted, self.f.root, tools, SECRET), "owner_download")
        self.no_copy()

    def test_partial_download_failure_stops_remaining_downloads_and_never_copies(self):
        admitted = n.admit(self.f.api, self.ctx)
        tools = n.Tools({})
        with patch.object(tools, "gh", side_effect=[b"", r.Stop("owner_download")]) as gh:
            self.refused(lambda: n.download(self.f.api, self.ctx, admitted, self.f.root, tools, SECRET),
                         "owner_download")
            self.assertEqual(gh.call_count, 2)
            self.assertEqual(gh.call_args.args[0][6], n.artifact_names(self.ctx)["b"])
        self.assertTrue(self.f.root.exists())
        self.no_copy()

    def test_unconfirmed_native_join_retains_auth_and_workspace_no_retry(self):
        instance = self.instance()
        original = self.f.tool
        def tool(args, code, timeout=120):
            if args[1] == "copy":
                self.assertTrue(instance.used)
                self.assertEqual(instance.outcome, "unknown")
                instance.tools.joined = False
                raise RuntimeError("hostile " + SECRET)
            return original(args, code, timeout)
        instance.tools.skopeo = tool
        error = self.refused(instance.execute, "diagnostic_copy", "unknown")
        self.assertEqual(n.diagnostic(error)["secondary"], [r.diagnostic(r.Stop("owner_join", outcome="unknown"))])
        self.assertTrue(instance.auth.path.exists())
        self.assertTrue(self.f.root.exists())
        self.refused(instance.execute, "diagnostic_used", "unknown")
        instance.auth.cleanup()  # Fixture owns all fake tool activity; manual test cleanup.
        for constraint in ("child_join_unconfirmed", "output_reader_joined", "output_within_64k"):
            tools = n.Tools({})
            with patch.object(n.smoke, "run_bounded", side_effect=n.smoke.SmokeError("fixture", constraint)):
                self.refused(lambda tools=tools: tools.gh(["auth", "token"], "owner_auth"), "owner_auth")
                self.assertFalse(tools.joined)

    def test_publication_or_presentation_failure_never_retries_or_deletes_workspace(self):
        # Exercise actual orchestration but every external effect is an offline fixture.
        env_before = dict(os.environ)
        original_temp = tempfile.mkdtemp
        def workspace(*args, **kwargs):
            return str(self.f.root) if kwargs.get("prefix") == "intake-owner-public-" else original_temp(*args, **kwargs)
        with patch.object(n, "native_env", return_value={"HOME": "/fixture", "PATH": "/fixture"}), \
                patch.object(n.Tools, "gh", return_value=SECRET.encode()), \
                patch.object(n, "verify_owner") as owner, patch.object(d, "GitHub", return_value=self.f.api), \
                patch.object(n, "download") as download, patch.object(n.tempfile, "mkdtemp", side_effect=workspace), \
                patch.object(n.Tools, "skopeo", side_effect=self.f.tool), \
                patch("builtins.print", side_effect=OSError("hostile " + SECRET)), patch.object(n.shutil, "rmtree") as remove:
            self.refused(lambda: n.publish(self.ctx), "synthetic_receipt", "completed")
            owner.assert_called_once_with(SECRET)
            download.assert_called_once()
            # Authfile cleanup uses the same shutil module; workspace must be retained.
            self.assertNotIn(self.f.root, [call.args[0] for call in remove.call_args_list])
        self.assertEqual(dict(os.environ), env_before)
        self.assertEqual(sum(args[1] == "copy" for args in self.f.calls), 1)
        # The mocked auth cleanup deliberately retained its owned fixture material.
        import shutil
        shutil.rmtree(self.f.authpath.parent)

    def test_owner_auth_refusal_precedes_repository_download_and_authfile(self):
        with patch.object(n, "native_env", return_value={"HOME": "/fixture", "PATH": "/fixture"}), \
                patch.object(n.Tools, "gh", return_value=SECRET.encode()), \
                patch.object(n, "verify_owner", side_effect=r.Stop("owner_auth")), \
                patch.object(d, "GitHub") as api, patch.object(n, "download") as download, \
                patch.object(p.Authfile, "prepare") as auth:
            self.refused(lambda: n.publish(self.ctx), "owner_auth")
            api.assert_not_called()
            download.assert_not_called()
            auth.assert_not_called()

    def test_download_unconfirmed_join_retains_workspace_with_primary(self):
        original_temp = tempfile.mkdtemp
        def workspace(*args, **kwargs):
            return str(self.f.root) if kwargs.get("prefix") == "intake-owner-public-" else original_temp(*args, **kwargs)
        def download(api, ctx, admitted, root, tools, token):
            tools.joined = False
            raise r.Stop("owner_download")
        with patch.object(n, "native_env", return_value={"HOME": "/fixture", "PATH": "/fixture"}), \
                patch.object(n.Tools, "gh", return_value=SECRET.encode()), patch.object(n, "verify_owner"), \
                patch.object(d, "GitHub", return_value=self.f.api), patch.object(n, "download", side_effect=download), \
                patch.object(n.tempfile, "mkdtemp", side_effect=workspace), patch.object(p.Authfile, "prepare") as auth:
            error = self.refused(lambda: n.publish(self.ctx), "owner_download")
            self.assertEqual(n.diagnostic(error)["secondary"], [r.diagnostic(r.Stop("owner_join"))])
            self.assertTrue(self.f.root.exists())
            auth.assert_not_called()
        self.no_copy()

    def test_cli_unknown_failure_is_closed_and_passive(self):
        with patch.object(n, "main", side_effect=RuntimeError("hostile " + SECRET)), \
                patch("sys.stdout", new_callable=io.StringIO) as output:
            with self.assertRaises(SystemExit):
                n.cli()
            value = json.loads(output.getvalue())
            self.assertEqual(value["code"], "DELIVERY_DIAGNOSTIC_INTERNAL")
            self.assertNotIn(SECRET, output.getvalue())
            self.assertNotIn("hostile", output.getvalue())
        with patch.object(n, "main", side_effect=r.Stop("synthetic_receipt", outcome="completed")), \
                patch("builtins.print", side_effect=OSError("hostile " + SECRET)):
            with self.assertRaises(SystemExit) as exit_status:
                n.cli()
            self.assertEqual(exit_status.exception.code, 1)


if __name__ == "__main__":
    unittest.main()
