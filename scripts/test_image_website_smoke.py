#!/usr/bin/env python3
"""Offline sequencing checks; optional disposable actual local HTTP fixture.
No image/deployment claim follows from the local-binary fixture.
"""
import importlib.util
import json
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import Mock, call

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
spec = importlib.util.spec_from_file_location("image_website_smoke", ROOT / "scripts/image-smoke.py")
assert spec is not None and spec.loader is not None
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class WebsiteSmokeTests(unittest.TestCase):
    def fixture(self):
        fixture = smoke.Fixture("synthetic-image", "/synthetic-probe")
        fixture.volume = Mock(return_value="synthetic-volume")
        fixture.once = Mock()
        fixture.start = Mock(side_effect=["first", "restart"])
        fixture.exec = Mock()
        fixture.stop = Mock()
        return fixture

    def test_mandatory_website_and_restart_order(self):
        fixture = self.fixture()
        events = Mock()
        for name in ("once", "start", "exec", "stop"):
            events.attach_mock(getattr(fixture, name), name)
        fixture.native_operator()
        self.assertEqual(events.mock_calls, [
            call.once("synthetic-volume", "init", operator=True),
            call.start("synthetic-volume", operator=True),
            call.exec("first", "operator"), call.exec("first", "website"),
            call.stop("first"), call.once("synthetic-volume", "operator-privacy", probe=True),
            call.start("synthetic-volume", operator=True),
            call.exec("restart", "website-restart"), call.stop("restart"),
            call.once("synthetic-volume", "operator-privacy", probe=True),
        ])

    def test_website_failure_aborts_before_receipt(self):
        for failure in ("website", "website-restart"):
            with self.subTest(stage=failure):
                fixture = self.fixture()
                def execute(_server, mode, expected=failure):
                    if mode == expected:
                        raise smoke.r.Stop("image_runtime")
                fixture.exec.side_effect = execute
                with self.assertRaises(smoke.r.Stop):
                    fixture.native_operator()
                self.assertEqual(fixture.stop.call_count, 0 if failure == "website" else 1)
                self.assertEqual(fixture.once.call_count, 1 if failure == "website" else 2)

    @unittest.skipUnless(os.environ.get("INTAKE_WEBSITE_SMOKE_BINARY") and
                         os.environ.get("INTAKE_WEBSITE_SMOKE_PROBE"),
                         "optional pinned local binaries not selected")
    def test_disposable_local_http_integration(self):
        binary = str(Path(os.environ["INTAKE_WEBSITE_SMOKE_BINARY"]).resolve())
        probe = str(Path(os.environ["INTAKE_WEBSITE_SMOKE_PROBE"]).resolve())
        # Never connect to or stop a pre-existing preview listener.
        with socket.socket() as check:
            check.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            try:
                check.bind(("127.0.0.1", 8090))
            except OSError:
                self.skipTest("private fixture port unavailable; existing process untouched")
        env = dict(os.environ, OTEL_SDK_DISABLED="true",
                   LEDGER_ADMIN_KEY=secrets.token_hex(32),
                   LEDGER_OPERATOR_EMAIL="synthetic-image@example.invalid",
                   LEDGER_OPERATOR_PASSWORD=secrets.token_hex(32))
        def run(args):
            try:
                result = subprocess.run(args, env=env, capture_output=True, timeout=30, check=False)
            except (OSError, subprocess.TimeoutExpired):
                self.fail("LOCAL_WEBSITE_COMMAND_UNAVAILABLE; inspect disposable fixture privately")
            # Never attach subprocess bytes, arguments or credentials to failures.
            code = "LOCAL_WEBSITE_COMMAND_UNCONFIRMED"
            if result.returncode != 0:
                try:
                    candidate = json.loads(result.stderr).get("code")
                    if candidate in {
                        "IMAGE_WEBSITE_NATIVE_AUTH_UNCONFIRMED", "IMAGE_WEBSITE_PROVISION_UNCONFIRMED",
                        "IMAGE_WEBSITE_SOURCE_UNCONFIRMED", "IMAGE_WEBSITE_PUBLIC_UNCONFIRMED",
                        "IMAGE_WEBSITE_ASSETS_UNCONFIRMED", "IMAGE_WEBSITE_CUSTOMER_UNCONFIRMED",
                        "IMAGE_WEBSITE_SNAPSHOT_UNCONFIRMED", "IMAGE_WEBSITE_BALANCE_API_UNCONFIRMED",
                        "IMAGE_WEBSITE_PENDING_UNCONFIRMED", "IMAGE_WEBSITE_ACCOUNT_PAGE_UNCONFIRMED",
                        "IMAGE_WEBSITE_COMPLETE_TEXT_UNCONFIRMED", "IMAGE_WEBSITE_ISOLATION_UNCONFIRMED",
                        "IMAGE_WEBSITE_PUBLICATION_UNCONFIRMED", "IMAGE_WEBSITE_LOGOUT_UNCONFIRMED",
                    }:
                        code = candidate
                except (ValueError, AttributeError, TypeError):
                    pass
            self.assertTrue(result.returncode == 0 and result.stderr == b"", code)
            return result.stdout
        with tempfile.TemporaryDirectory(prefix="intake-website-http-") as state:
            self.assertEqual(json.loads(run([binary, "init", "--operator", "--state", state])),
                             {"status": "initialized_empty"})
            for mode in ("website", "website-restart"):
                process = subprocess.Popen([binary, "serve", "--operator", "--state", state,
                                            "--listen", "127.0.0.1:8090"], env=env,
                                           stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                try:
                    deadline = time.monotonic() + 15
                    while time.monotonic() < deadline:
                        ready = subprocess.run([probe, "health"], env=env, capture_output=True,
                                               timeout=5, check=False)
                        if ready.returncode == 0:
                            break
                        time.sleep(0.1)
                    else:
                        self.fail("LOCAL_WEBSITE_READINESS_UNCONFIRMED")
                    self.assertEqual(json.loads(run([probe, mode])),
                                     {"passed": True, "stage": "synthetic_probe"})
                finally:
                    process.terminate()
                    try:
                        out, err = process.communicate(timeout=15)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.communicate()
                        self.fail("LOCAL_WEBSITE_SHUTDOWN_UNCONFIRMED")
                    self.assertTrue(process.returncode == 0 and out == b"" and err == b"",
                                    "LOCAL_WEBSITE_STDIO_PRIVACY_UNCONFIRMED")


if __name__ == "__main__":
    unittest.main()
