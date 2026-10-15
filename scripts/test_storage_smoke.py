"""Offline negatives for the local storage fixture; no service/network calls."""
import contextlib
import importlib.util
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

import smoke

SPEC = importlib.util.spec_from_file_location("storage_smoke", Path(__file__).with_name("storage-smoke.py"))
assert SPEC is not None and SPEC.loader is not None
fixture = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(fixture)


class StorageFixtureTests(unittest.TestCase):
    def test_pipe_close_failure_is_closed_and_cannot_pass(self):
        pipe = mock.Mock()
        pipe.read.return_value = b""
        pipe.close.side_effect = RuntimeError("SYNTHETIC_PRIVATE_CLOSE_VALUE")
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            capture = smoke.PipeCapture(pipe)
            with self.assertRaises(smoke.SmokeError) as caught:
                capture.finish("storage_capture")
        self.assertEqual(caught.exception.constraint, "output_reader_joined")
        self.assertEqual(stderr.getvalue(), "")
        self.assertFalse(capture.thread.is_alive())
        pipe.close.assert_called_once()

    def test_supported_outcome_pairs_only(self):
        self.assertEqual(fixture.capacity_outcome({"code": "LEDGER_STORAGE_UNAVAILABLE", "outcome": "no_effect"}), "no_effect")
        self.assertEqual(fixture.capacity_outcome({"code": "LEDGER_COMMIT_UNKNOWN", "outcome": "unknown"}), "unknown")
        for pair in (("LEDGER_STORAGE_UNAVAILABLE", "unknown"), ("LEDGER_COMMIT_UNKNOWN", "no_effect")):
            with self.assertRaises(smoke.SmokeError):
                fixture.capacity_outcome({"code": pair[0], "outcome": pair[1]})

    def test_known_rollback_cannot_have_extra_credit(self):
        fixture.capacity_balance(105, 5, "no_effect")
        with self.assertRaises(smoke.SmokeError):
            fixture.capacity_balance(106, 5, "no_effect")
        fixture.capacity_balance(105, 5, "unknown")
        fixture.capacity_balance(106, 5, "unknown")
        with self.assertRaises(smoke.SmokeError):
            fixture.capacity_balance(107, 5, "unknown")

    def test_hostile_arguments_never_echo(self):
        sentinel = "SYNTHETIC_PRIVATE_ARGUMENT_VALUE"
        for argv in (["--binary", sys.executable, "--unexpected", sentinel], [sentinel], ["--binary"]):
            out, err = io.StringIO(), io.StringIO()
            with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err), mock.patch.object(fixture, "run") as run:
                self.assertEqual(fixture.main(argv), 1)
            self.assertNotIn(sentinel, out.getvalue() + err.getvalue())
            self.assertEqual(err.getvalue(), "")
            self.assertEqual(json.loads(out.getvalue())["stage"], "arguments")
            run.assert_not_called()

    def test_cleanup_cannot_replace_primary_failure(self):
        primary = smoke.SmokeError("storage_privacy", "empty_stdio")
        service = mock.Mock(proc=object())
        service.stop.side_effect = smoke.SmokeError("shutdown", "secondary_failure")
        with tempfile.TemporaryDirectory() as root, \
                mock.patch.object(fixture.tempfile, "mkdtemp", return_value=root), \
                mock.patch.object(fixture.s, "Service", return_value=service), \
                mock.patch.object(fixture, "crash_and_expiry", side_effect=primary), \
                mock.patch.object(fixture.shutil, "rmtree") as remove:
            with self.assertRaises(smoke.SmokeError) as caught:
                fixture.run(sys.executable)
            self.assertIs(caught.exception, primary)
            self.assertIn("STORAGE_TEARDOWN_UNCONFIRMED", primary.__notes__)
            remove.assert_not_called()
            service.stop.assert_called_once()
        out = io.StringIO()
        with contextlib.redirect_stdout(out), mock.patch.object(fixture, "run", side_effect=primary):
            self.assertEqual(fixture.main(["--binary", sys.executable]), 1)
        diagnostic = json.loads(out.getvalue())
        self.assertEqual(diagnostic["stage"], "storage_privacy")
        self.assertEqual(diagnostic["constraint"], "empty_stdio")
        self.assertEqual(diagnostic["cleanup_failure"], "STORAGE_TEARDOWN_UNCONFIRMED")

    def test_short_hold_must_still_be_active_before_crash(self):
        active = {"reservation_id": "a"}
        settled = {"reservation_id": "s"}
        refunded = {"reservation_id": "r"}
        short = {"reservation_id": "b", "expires_at": "2099-01-01T00:00:00.000Z"}
        reader = mock.Mock()
        snapshot = {"account_balance": [{"account_id": "synthetic-storage", "balance": "97", "enabled": True}],
                    "pending_reservations": [active], "available": "87"}
        with mock.patch.object(fixture, "prepare", return_value=("key", {})), \
                mock.patch.object(fixture.sqlite3, "connect", return_value=reader), \
                mock.patch.object(fixture, "reserve", side_effect=[({}, "", row) for row in (active, settled, refunded, short)]), \
                mock.patch.object(fixture, "rpc", side_effect=[{"reservation": settled}, {"reservation": refunded}]), \
                mock.patch.object(fixture, "snapshot", return_value=snapshot), \
                mock.patch.object(fixture, "crash") as crash:
            with self.assertRaises(smoke.SmokeError) as caught:
                fixture.crash_and_expiry(SimpleNamespace(state="/synthetic-unused"))
            self.assertEqual(caught.exception.constraint, "both_holds_still_active")
            crash.assert_not_called()
            reader.close.assert_called_once()


if __name__ == "__main__":
    unittest.main()
