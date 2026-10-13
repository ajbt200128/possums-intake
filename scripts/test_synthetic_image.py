#!/usr/bin/env python3
"""Synthetic offline tests for disposable synthetic-serve image qualification.
No Docker, network, image, CLI, volume or secrets. Actual image execution is
parent-owned; these tests use an injected fake subprocess boundary.
"""
import importlib.util
import json
import subprocess
import sys
import unittest
from pathlib import Path
from unittest.mock import patch

import release as r


def tool_module(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).parent / (name + ".py"))
    if spec is None or spec.loader is None:
        raise AssertionError("fixture module unavailable")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


synthetic = tool_module("synthetic-image-smoke")
smoke = tool_module("image-smoke")

SECRETS = ("synthetic-image-credential-", "synthetic-only-")


class Runner:
    """Injected subprocess boundary: records argv/env, never executes Docker."""

    def __init__(self, results=None):
        self.calls = []
        self.results = list(results or [])

    def run(self, args, *, env=None, timeout=30, code="image_runtime"):
        self.calls.append((list(args), env))
        if self.results:
            return self.results.pop(0)
        return subprocess.CompletedProcess(list(args), 0, b"", b"")

    def popen(self, args, env):
        self.calls.append((list(args), env))
        return FakeProcess()

    def argv(self):
        return [call[0] for call in self.calls]

    def flattened(self):
        return " ".join(" ".join(call[0]) for call in self.calls)

    def envs(self):
        return [call[1] for call in self.calls]


class FakeProcess:
    def __init__(self, out=b"", err=b"", returncode=0):
        self.out, self.err, self.returncode = out, err, returncode
        self.killed = False

    def communicate(self, timeout=None):
        return self.out, self.err

    def kill(self):
        self.killed = True


def fixture():
    return synthetic.Synthetic("sha256:" + "1" * 64, "/synthetic-probe", "/synthetic-assets.json")


class SyntheticImageTests(unittest.TestCase):
    def stop(self, code, call, outcome="no_effect"):
        with self.assertRaises(r.Stop) as caught:
            call()
        self.assertEqual(caught.exception.code, code)
        diag = r.diagnostic(caught.exception)
        self.assertEqual(diag["code"], "DELIVERY_" + code.upper())
        for sentinel in ("synthetic-image-credential-", "synthetic-only-", "/synthetic-probe", "hostile"):
            self.assertNotIn(sentinel, json.dumps(diag))
        return diag

    def test_reuses_frozen_ordinary_image_identity_helpers(self):
        # The separate fixture must load the frozen helpers from the same file,
        # never fork a copy of the identity logic.
        self.assertEqual(str(synthetic.smoke.__file__), str(smoke.__file__))
        self.assertTrue(callable(synthetic.smoke.archive_identity))
        self.assertTrue(callable(synthetic.smoke.loaded_image_identity))
        self.assertEqual(synthetic.smoke.archive_identity.__code__.co_code, smoke.archive_identity.__code__.co_code)

    def test_flags_are_isolated_and_bounded_without_volume_or_init(self):
        flags = fixture().flags()
        self.assertEqual(flags[flags.index("--network") + 1], "none")
        self.assertEqual(flags[flags.index("--pull") + 1], "never")
        self.assertEqual(flags[flags.index("--log-driver") + 1], "none")
        self.assertIn("--read-only", flags)
        self.assertEqual(flags[flags.index("--cap-drop") + 1], "ALL")
        self.assertIn("no-new-privileges", flags)
        self.assertIn("core=0", " ".join(flags))
        self.assertIn("/tmp:rw,size=128m,mode=1777", flags)  # noqa: S108 - asserted Docker tmpfs spec, not a host temp path
        self.assertIn("type=bind,src=/synthetic-probe,dst=/probe,readonly", flags)
        self.assertIn("type=bind,src=/synthetic-assets.json,dst=/asset-manifest.json,readonly", flags)
        self.assertNotIn("--privileged", flags)
        self.assertNotIn("--state", flags)
        self.assertNotIn("--operator", flags)
        self.assertFalse(any("type=volume" in flag for flag in flags))

    def test_credentials_are_names_in_env_and_never_in_argv(self):
        runner, subject = Runner(), fixture()
        with patch.object(synthetic, "run", runner.run):
            subject.create()
            subject.flags()
        argv = runner.flattened()
        for name in ("LEDGER_ADMIN_KEY", "LEDGER_OPERATOR_EMAIL", "LEDGER_OPERATOR_PASSWORD", "OTEL_SDK_DISABLED"):
            self.assertIn(name, argv)  # -e NAME passes the name only.
        for secret in SECRETS:
            self.assertNotIn(secret, argv)
        for value in subject.credentials.values():
            self.assertNotIn(value, argv)
        for env in runner.envs():
            self.assertEqual(env["LEDGER_ADMIN_KEY"], subject.credentials["LEDGER_ADMIN_KEY"])
        self.assertEqual(subject.credentials["OTEL_SDK_DISABLED"], "true")

    def test_create_invokes_synthetic_serve_with_no_state_or_operator(self):
        runner, subject = Runner(), fixture()
        with patch.object(synthetic, "run", runner.run):
            subject.create()
        args = runner.argv()[0]
        self.assertEqual(args[-3:], ["synthetic-serve", "--listen", "0.0.0.0:8090"])
        self.assertEqual(args[0:4], ["docker", "create", "--name", subject.name])

    def test_attach_joins_actual_stdio(self):
        runner, subject = Runner(), fixture()
        with patch.object(synthetic, "popen", runner.popen):
            subject.attach()
        args = runner.argv()[0]
        self.assertEqual(args, ["docker", "start", "-a", subject.name])
        self.assertIsNotNone(subject.process)

    def test_health_is_passive_bounded_and_refuses_unknown(self):
        subject = fixture()
        runner = Runner(results=[subprocess.CompletedProcess([], 1, b"", b"")])
        with patch.object(synthetic, "run", runner.run), patch.object(synthetic.time, "monotonic", side_effect=[0, 0, 20]):
            self.stop("synthetic_image", subject.health, outcome="unknown")

    def test_probe_requires_exact_closed_success_and_rejects_hostile_output(self):
        subject = fixture()
        good = subprocess.CompletedProcess([], 0, b'{"passed":true,"stage":"synthetic_probe"}', b"")
        with patch.object(synthetic, "run", Runner(results=[good]).run):
            subject.exec("operator")
        hostile = subprocess.CompletedProcess([], 0, b'{"passed":true,"stage":"hostile-fixture"}', b"")
        with patch.object(synthetic, "run", Runner(results=[hostile]).run):
            self.stop("synthetic_image", lambda: subject.exec("operator"))

    def test_restart_sequence_has_no_init_or_replay(self):
        subject = fixture()
        with patch.object(subject, "create") as create, patch.object(subject, "attach") as attach, \
                patch.object(subject, "health") as health, patch.object(subject, "exec") as execute, \
                patch.object(subject, "detach") as detach:
            subject.sequence()
        self.assertEqual(create.call_count, 1)
        self.assertEqual(attach.call_count, 2)
        self.assertEqual(health.call_count, 2)
        self.assertEqual(detach.call_count, 2)
        self.assertEqual([call.args[0] for call in execute.call_args_list],
                         ["operator", "synthetic-wildcard", "synthetic-privacy", "synthetic-fresh", "synthetic-privacy"])
        self.assertNotIn("init", " ".join(call.args[0] for call in execute.call_args_list))

    def test_detach_requires_empty_attached_stdio(self):
        subject, runner = fixture(), Runner()
        subject.process = FakeProcess()
        with patch.object(synthetic, "run", runner.run):
            subject.detach()
        self.assertEqual(subject.process, None)
        for out, err in ((b"activity", b""), (b"", b"error")):
            subject.process = FakeProcess(out=out, err=err)
            with patch.object(synthetic, "run", runner.run):
                self.stop("image_privacy", subject.detach)

    def test_cleanup_never_replaces_primary_failure(self):
        subject = fixture()
        subject.create_attempted = True
        primary = r.Stop("synthetic_image", outcome="unknown")
        with patch.object(synthetic, "run", side_effect=OSError("hostile fixture")):
            subject.cleanup(primary_failed=True)  # preserve an already observed primary
        try:
            raise primary
        except r.Stop as error:
            self.assertEqual(error.code, "synthetic_image")
            self.assertEqual(error.outcome, "unknown")

    def test_cleanup_joins_owned_attach_process(self):
        subject = fixture()
        subject.process = FakeProcess()
        with patch.object(synthetic, "run", Runner().run):
            subject.cleanup()
        self.assertIsNone(subject.process)

    def test_cleanup_nonzero_removal_refuses_qualification(self):
        subject = fixture()
        subject.create_attempted = True
        failed = subprocess.CompletedProcess([], 1, b"", b"")
        with patch.object(synthetic, "run", Runner(results=[failed]).run):
            self.stop("synthetic_cleanup", subject.cleanup)

    def test_cleanup_exception_refuses_qualification(self):
        subject = fixture()
        subject.create_attempted = True
        with patch.object(synthetic, "run", side_effect=OSError("hostile fixture")):
            self.stop("synthetic_cleanup", subject.cleanup)

    def test_unknown_create_is_still_owned_for_cleanup(self):
        subject = fixture()
        with patch.object(synthetic, "run", side_effect=r.Stop("synthetic_image", outcome="unknown")):
            self.stop("synthetic_image", subject.create)
        runner = Runner()
        with patch.object(synthetic, "run", runner.run):
            subject.cleanup(primary_failed=True)
        self.assertEqual(runner.argv(), [["docker", "rm", "-f", subject.name]])

    def test_cleanup_final_join_is_bounded_and_not_claimed(self):
        class Stuck(FakeProcess):
            def __init__(self):
                super().__init__()
                self.timeouts = []

            def communicate(self, timeout=None):
                self.timeouts.append(timeout)
                if timeout is None:
                    raise AssertionError("unbounded synthetic process join")
                raise subprocess.TimeoutExpired("synthetic-docker-client", timeout)

        subject = fixture()
        process = Stuck()
        subject.process = process
        with patch.object(synthetic, "run", Runner().run):
            self.stop("synthetic_cleanup", subject.cleanup)
        self.assertEqual(process.timeouts, [15, 15])
        self.assertTrue(process.killed)
        self.assertIs(subject.process, process)  # no false confirmed-join state

    def test_cleanup_does_not_kill_already_joined_process(self):
        subject = fixture()
        process = FakeProcess()
        subject.process = process
        subject.cleanup()
        self.assertFalse(process.killed)
        self.assertIsNone(subject.process)

    def test_qualification_teardown_refuses_pass_but_preserves_primary(self):
        for primary in (None, r.Stop("synthetic_image", outcome="unknown")):
            with self.subTest(primary_present=primary is not None):
                def sequence(subject, failure=primary):
                    subject.create_attempted = True
                    if failure is not None:
                        raise failure

                def copy_probe(_source, target):
                    Path(target).write_bytes(b"synthetic test-only probe")

                failed = subprocess.CompletedProcess([], 1, b"", b"")
                with patch.object(synthetic.shutil, "which", return_value="synthetic-tool"), \
                        patch.object(synthetic.Path, "is_file", return_value=True), \
                        patch.object(synthetic.shutil, "copyfile", side_effect=copy_probe), \
                        patch.object(synthetic.smoke, "archive_identity", return_value=("sha256:" + "1" * 64, "arm64")), \
                        patch.object(synthetic.smoke, "loaded_image_identity", return_value="sha256:" + "2" * 64), \
                        patch.object(synthetic.smoke, "success", side_effect=[b"linux/arm64", b""]), \
                        patch.object(synthetic.Synthetic, "sequence", autospec=True, side_effect=sequence), \
                        patch.object(synthetic, "run", return_value=failed), \
                        self.assertRaises(r.Stop) as caught:
                    synthetic.qualify("synthetic-archive", "synthetic-probe")
                if primary is None:
                    self.assertEqual(caught.exception.code, "synthetic_cleanup")
                    self.assertEqual(caught.exception.outcome, "unknown")
                else:
                    self.assertIs(caught.exception, primary)

    def test_docker_error_is_closed_and_content_free(self):
        runner = Runner(results=[subprocess.CompletedProcess([], 1, b"hostile", b"hostile")])
        with patch.object(synthetic, "run", runner.run):
            self.stop("synthetic_image", fixture().create)

    def test_evidence_is_public_identities_and_closed_booleans_only(self):
        evidence = {"config_identity": "sha256:" + "1" * 64,
                    "docker_runtime_identity": "sha256:" + "1" * 64, "archive_sha256": "3" * 64,
                    "platform": "linux/arm64", "synthetic": {"wildcard_listener_reachable": True},
                    "previous_release_compatibility": "unknown"}
        rendered = json.dumps(evidence, sort_keys=True)
        for secret in SECRETS + ("native-image-fixture", "/synthetic-probe"):
            self.assertNotIn(secret, rendered)
        self.assertEqual(evidence["previous_release_compatibility"], "unknown")

    def test_missing_manifest_or_probe_stops_before_docker(self):
        with patch.object(synthetic.shutil, "which", return_value="/synthetic/bin"), \
                patch.object(synthetic.Path, "is_file", return_value=False), \
                patch.object(synthetic, "run") as run:
            self.stop("image_tools", lambda: synthetic.qualify("/synthetic-archive", "/synthetic-probe"))
        run.assert_not_called()

    def test_main_prints_only_public_evidence(self):
        evidence = {"config_identity": "sha256:" + "1" * 64, "previous_release_compatibility": "unknown"}
        with patch.object(sys, "argv", ["synthetic-image-smoke", "synthetic-archive", "synthetic-probe"]), \
                patch.object(synthetic, "qualify", return_value=evidence), patch("builtins.print") as out:
            synthetic.main()
        self.assertEqual(json.loads(out.call_args.args[0]), evidence)


if __name__ == "__main__":
    unittest.main()
