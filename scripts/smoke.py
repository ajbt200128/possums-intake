#!/usr/bin/env python3
"""Disposable synthetic acceptance: actual compiled CLI + curl, never live data.

Only --binary (or --self-test) is accepted: no URL, state or credential inputs.
See PLAN.md and PRIVACY.md. Raw child output is bounded, transient, never printed.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import secrets
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
from datetime import datetime

ACCOUNT = "synthetic-smoke-a"
OTHER_ACCOUNT = "synthetic-smoke-b"
RPC_SECONDS = 5
SHUTDOWN_SECONDS = 8
OUTPUT_LIMIT = 64 * 1024
ROUTES = ("/accounts/provision", "/credits", "/reservations",
          "/reservations/renew", "/reservations/settle", "/reservations/refund",
          "/reservations/lookup", "/balance")
RESERVATION_FIELDS = {"reservation_id", "idempotency_key", "account_id", "maximum",
                      "timeout_seconds", "initial_expires_at", "expires_at", "state", "charged_amount",
                      "terminal_at", "retain_until"}
TIME_PATTERN = re.compile(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z", re.ASCII)


class SmokeError(Exception):
    def __init__(self, stage: str, constraint: str):
        self.stage, self.constraint = stage, constraint


def expect(condition: bool, stage: str, constraint: str) -> None:
    if not condition:
        raise SmokeError(stage, constraint)


def clean_env() -> dict[str, str]:
    # Do not inherit admin/metrics credentials, proxy or curl/OTel configuration.
    return {"PATH": os.environ.get("PATH", os.defpath), "OTEL_SDK_DISABLED": "true"}


class PipeCapture:
    """Drain a child pipe continuously; retain at most OUTPUT_LIMIT bytes."""
    def __init__(self, pipe):
        self.pipe = pipe
        self.data = bytearray()
        self.overflow = False
        self.failed = False
        self.thread = threading.Thread(target=self._read, daemon=True)
        self.thread.start()

    def _read(self):
        try:
            while chunk := self.pipe.read(4096):
                room = OUTPUT_LIMIT - len(self.data)
                self.data.extend(chunk[:room])
                self.overflow |= len(chunk) > room
        except Exception:
            self.failed = True
        finally:
            try:
                self.pipe.close()
            except Exception:
                self.failed = True

    def finish(self, stage: str) -> bytes:
        self.thread.join(timeout=2)
        expect(not self.thread.is_alive() and not self.failed, stage, "output_reader_joined")
        expect(not self.overflow, stage, "output_within_64k")
        return bytes(self.data)


def run_bounded(argv: list[str], env: dict[str, str], stage: str,
                timeout: float, config: bytes | None = None) -> tuple[int, bytes, bytes]:
    try:
        proc = subprocess.Popen(argv, env=env, stdin=subprocess.PIPE if config else subprocess.DEVNULL,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    except OSError:
        raise SmokeError(stage, "child_start_unavailable") from None
    out, err = PipeCapture(proc.stdout), PipeCapture(proc.stderr)
    timed_out = False
    try:
        if config:
            assert proc.stdin is not None
            try:
                proc.stdin.write(config)
                proc.stdin.close()
            except BrokenPipeError:
                pass  # Nonzero exit below, not a fabricated HTTP response.
        try:
            proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
    finally:
        if proc.poll() is None:
            try:
                proc.kill()
                proc.wait(timeout=5)
            except (OSError, subprocess.TimeoutExpired):
                raise SmokeError(stage, "child_join_unconfirmed") from None
    stdout, stderr = out.finish(stage), err.finish(stage)
    expect(not timed_out, stage, "child_deadline_unknown_outcome")
    return proc.returncode, stdout, stderr


def config_quote(value: str) -> str:
    return '"' + (value.replace("\\", "\\\\").replace('"', '\\"')
                  .replace("\n", "\\n").replace("\r", "\\r").replace("\t", "\\t")) + '"'


def curl_config(key: str | None, body: dict | None, idem: str | None) -> bytes:
    lines = []
    if key is not None:
        lines.append("header = " + config_quote("Authorization: Bearer " + key))
    if idem is not None:
        lines.append("header = " + config_quote("Idempotency-Key: " + idem))
    if body is not None:
        lines.append('header = "Content-Type: application/json"')
        lines.append("data = " + config_quote(json.dumps(body, separators=(",", ":"))))
    return ("\n".join(lines) + "\n").encode()


def run_curl(port: int, key: str | None, method: str, path: str,
             body: dict | None, idem: str | None, stage: str) -> tuple[int, bytes]:
    # --disable MUST be first: no .curlrc. No proxy, redirect, retry or DNS target.
    argv = ["curl", "--disable", "--noproxy", "*", "--silent", "--show-error",
            "--config", "-", "--request", method, "--proto", "=http",
            "--url", f"http://127.0.0.1:{port}{path}", "--max-time", str(RPC_SECONDS),
            "--connect-timeout", str(RPC_SECONDS), "--max-redirs", "0",
            "--write-out", "\n%{http_code}"]
    rc, raw, stderr = run_bounded(argv, clean_env(), stage, RPC_SECONDS + 1,
                                 curl_config(key, body, idem))
    expect(rc == 0, stage, "curl_exit_nonzero_unknown_outcome")
    expect(not stderr, stage, "curl_stderr_empty")
    payload, marker, status = raw.rpartition(b"\n")
    expect(bool(marker) and re.fullmatch(rb"[1-5][0-9]{2}", status) is not None,
           stage, "curl_http_status")
    return int(status), payload


def decode(payload: bytes, stage: str) -> dict:
    try:
        doc = json.loads(payload)
    except (ValueError, UnicodeDecodeError):
        raise SmokeError(stage, "json_object_response") from None
    expect(isinstance(doc, dict), stage, "json_object_response")
    return doc


def new_key() -> str:
    return f"v1.{time.time_ns() // 1_000_000:013d}.{secrets.token_hex(16)}"


class Service:
    def __init__(self, binary: str, state: str):
        self.binary, self.state = binary, state
        self.key = secrets.token_urlsafe(48)
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            self.port = listener.getsockname()[1]
        self.proc = None
        self.output = None

    def env(self) -> dict[str, str]:
        return {**clean_env(), "LEDGER_ADMIN_KEY": self.key}

    def init(self):
        rc, out, err = run_bounded([self.binary, "init", "--state", self.state],
                                  self.env(), "init", 30)
        expect(rc == 0, "init", "successful_explicit_init")
        expect(out == b'{"status":"initialized_empty"}\n' and not err,
               "init", "fixed_init_output")

    def request(self, path: str, body: dict | None = None, *, stage: str,
                status: int = 200, idem: str | None = None, auth: str = "correct") -> dict:
        key = {"correct": self.key, "missing": None,
               "wrong": "SYNTHETIC_WRONG_KEY_AT_LEAST_32_BYTES_LONG"}[auth]
        observed, payload = run_curl(self.port, key, "GET" if body is None else "POST",
                                     path, body, idem, stage)
        expect(self.key.encode() not in payload, stage, "no_key_in_response")
        expect(observed == status, stage, "expected_http_status")
        return decode(payload, stage)

    def start(self):
        self.proc = subprocess.Popen([self.binary, "serve", "--state", self.state,
                                      "--listen", f"127.0.0.1:{self.port}"],
                                     env=self.env(), stdin=subprocess.DEVNULL,
                                     stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        self.output = PipeCapture(self.proc.stdout)
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            expect(self.proc.poll() is None, "serve", "alive_before_readiness")
            try:
                doc = self.request("/healthz", stage="health", auth="missing")
            except SmokeError as error:
                # Only fixed public readiness polling may repeat, never mutations.
                if error.constraint not in ("curl_exit_nonzero_unknown_outcome", "expected_http_status"):
                    raise
            else:
                expect(doc == {"status": "ok"}, "health", "fixed_public_readiness")
                return
            time.sleep(0.05)
        raise SmokeError("serve", "readiness_deadline")

    def stop(self):
        if self.proc is None:
            return
        proc = self.proc
        forced = False
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=SHUTDOWN_SECONDS)
            except subprocess.TimeoutExpired:
                forced = True
                proc.kill()
                proc.wait(timeout=5)
        self.proc = None  # Joined BEFORE caller may remove the synthetic state.
        assert self.output is not None
        out = self.output.finish("shutdown")
        expect(not forced, "shutdown", "forced_termination_inflight_unknown")
        expect(proc.returncode == 0, "shutdown", "normal_zero_exit_joined")
        # Any normal service output is a privacy failure, including discarded overflow.
        expect(not out, "privacy", "service_output_empty")


def account(rows, account_id: str, balance: str, stage: str):
    expect(rows == [{"account_id": account_id, "balance": balance, "enabled": True}]
           and isinstance(rows[0]["enabled"], bool), stage, "exact_account_list")


def utc(value, stage: str):
    expect(isinstance(value, str) and TIME_PATTERN.fullmatch(value) is not None,
           stage, "fixed_utc_milliseconds")
    try:
        datetime.strptime(value, "%Y-%m-%dT%H:%M:%S.%fZ")
    except ValueError:
        raise SmokeError(stage, "valid_utc_time") from None


def reservation(row, stage: str):
    expect(isinstance(row, dict) and set(row) == RESERVATION_FIELDS,
           stage, "complete_reservation_fields")
    expect(isinstance(row["reservation_id"], str) and
           re.fullmatch(r"[0-9a-f]{32}", row["reservation_id"]) is not None,
           stage, "reservation_id_shape")
    expect(isinstance(row["idempotency_key"], str) and
           re.fullmatch(r"v1\.[0-9]{13}\.[0-9a-f]{32}", row["idempotency_key"]) is not None,
           stage, "idempotency_key_shape")
    expect(row["account_id"] == ACCOUNT, stage, "scoped_account")
    expect(isinstance(row["maximum"], str) and
           re.fullmatch(r"[1-9][0-9]{0,19}", row["maximum"]) is not None and
           int(row["maximum"]) <= 2**64 - 1, stage, "exact_positive_money")
    expect(type(row["timeout_seconds"]) is int and 1 <= row["timeout_seconds"] <= 1800,
           stage, "required_timeout")
    utc(row["initial_expires_at"], stage)
    utc(row["expires_at"], stage)
    expect(row["initial_expires_at"] <= row["expires_at"], stage, "original_expiry_preserved")
    if row["state"] == "active":
        expect(all(row[field] is None for field in ("charged_amount", "terminal_at", "retain_until")),
               stage, "active_null_terminal_fields")
    else:
        expect(row["state"] in ("settled", "refunded", "expired"), stage, "terminal_state")
        expect(isinstance(row["charged_amount"], str) and
               re.fullmatch(r"0|[1-9][0-9]{0,19}", row["charged_amount"]) is not None and
               int(row["charged_amount"]) <= int(row["maximum"]), stage, "terminal_charge")
        utc(row["terminal_at"], stage)
        utc(row["retain_until"], stage)
        if row["state"] != "settled":
            expect(row["charged_amount"] == "0", stage, "released_without_debit")


def run_cases(s: Service):
    rpc = s.request
    expect(rpc("/healthz", stage="health", auth="missing") == {"status": "ok"},
           "health", "fixed_public_health")
    for path in ROUTES:
        for auth in ("missing", "wrong"):
            doc = rpc(path, {}, stage="auth", status=401, auth=auth)
            expect(set(doc) == {"code", "stage", "constraint", "outcome", "next_step"} and
                   doc["code"] == "LEDGER_HTTP_UNAUTHORIZED", "auth", "closed_unauthorized")

    for account_id in (ACCOUNT, OTHER_ACCOUNT):
        doc = rpc("/accounts/provision", {"account_id": account_id}, stage="provision")
        account(doc.get("account_balance"), account_id, "0", "provision")
    credit_input = {"account_id": ACCOUNT, "amount": "1000"}
    credit_key = new_key()
    first_credit = rpc("/credits", credit_input, stage="credit", idem=credit_key)
    row = first_credit.get("credit", {})
    expect(set(row) == {"idempotency_key", "account_id", "amount", "balance_after", "committed_at", "retain_until"}
           and row["idempotency_key"] == credit_key and row["account_id"] == ACCOUNT
           and row["amount"] == row["balance_after"] == "1000", "credit", "exact_credit_fields")
    utc(row["committed_at"], "credit")
    utc(row["retain_until"], "credit")
    expect(rpc("/credits", credit_input, stage="credit", idem=credit_key) == first_credit,
           "credit", "duplicate_original_result")
    doc = rpc("/credits", {**credit_input, "amount": "2000"}, stage="credit", idem=credit_key, status=409)
    expect(doc.get("code") == "LEDGER_KEY_CONFLICT", "credit", "altered_input_conflict")
    account(rpc("/accounts/provision", {"account_id": ACCOUNT}, stage="provision").get("account_balance"),
            ACCOUNT, "1000", "provision")

    def reserve(maximum: str, timeout: int = 600, stage: str = "reserve"):
        key = new_key()
        body = {"account_id": ACCOUNT, "maximum": maximum, "timeout_seconds": timeout}
        doc = rpc("/reservations", body, stage=stage, idem=key)
        row = doc.get("reservation", {})
        reservation(row, stage)
        expect(row["state"] == "active" and row["maximum"] == maximum and
               row["timeout_seconds"] == timeout and row["idempotency_key"] == key,
               stage, "original_active_inputs")
        expect(rpc("/reservations", body, stage=stage, idem=key) == doc,
               stage, "immutable_exact_retry")
        conflict = rpc("/reservations", {**body, "timeout_seconds": timeout + 1},
                       stage=stage, idem=key, status=409)
        expect(conflict.get("code") == "LEDGER_KEY_CONFLICT", stage, "timeout_immutable_conflict")
        return row, body

    def balance(expected: str, pending: list[dict], stage: str):
        doc = rpc("/balance", {"account_id": ACCOUNT}, stage=stage)
        expect(set(doc) == {"account_balance", "pending_reservations", "available"}, stage, "balance_fields")
        account(doc["account_balance"], ACCOUNT, expected, stage)
        expect(doc["pending_reservations"] == sorted(pending, key=lambda row: row["reservation_id"]),
               stage, "complete_ordered_pending_snapshot")
        for row in doc["pending_reservations"]:
            reservation(row, stage)
        expect(doc["available"] == str(int(expected) - sum(int(row["maximum"]) for row in pending)),
               stage, "available_from_snapshot")
        other = rpc("/balance", {"account_id": OTHER_ACCOUNT}, stage=stage)
        account(other.get("account_balance"), OTHER_ACCOUNT, "0", stage)
        expect(other == {"account_balance": [{"account_id": OTHER_ACCOUNT, "balance": "0", "enabled": True}],
                         "pending_reservations": [], "available": "0"}, stage, "other_account_isolated")

    hold, _ = reserve("300")
    second, _ = reserve("100")
    balance("1000", [hold, second], "balance")
    renewal_input = {"reservation_id": hold["reservation_id"], "expected_expires_at": hold["expires_at"]}
    renewal = rpc("/reservations/renew", renewal_input, stage="renew")
    expect(set(renewal) == {"reservation", "observed_at"}, "renew", "complete_renewal_fields")
    utc(renewal["observed_at"], "renew")
    renewed = renewal["reservation"]
    reservation(renewed, "renew")
    expect(renewed["expires_at"] > hold["expires_at"] and
           {k: v for k, v in renewed.items() if k != "expires_at"} ==
           {k: v for k, v in hold.items() if k != "expires_at"}, "renew", "only_expiry_extended")
    expect(rpc("/reservations/renew", renewal_input, stage="renew")["reservation"] == renewed,
           "renew", "duplicate_does_not_extend_again")
    hold = renewed
    balance("1000", [hold, second], "renew")
    identity = {"reservation_id": hold["reservation_id"]}
    settled = rpc("/reservations/settle", {**identity, "charged_amount": "250"}, stage="settle")
    reservation(settled.get("reservation"), "settle")
    expect(settled["reservation"]["state"] == "settled" and
           settled["reservation"]["charged_amount"] == "250", "settle", "committed_charge")
    closed = rpc("/reservations/renew", renewal_input, stage="renew", status=409)
    expect(closed.get("code") == "LEDGER_RENEWAL_CLOSED" and closed.get("reservation") == settled["reservation"],
           "renew", "terminal_absorbs_renewal")
    for amount in ("250", "200"):
        expect(rpc("/reservations/settle", {**identity, "charged_amount": amount}, stage="settle") == settled,
               "settle", "first_terminal_wins")
    expect(rpc("/reservations/refund", identity, stage="settle") == settled, "settle", "refund_cannot_replace_settlement")
    refunded = rpc("/reservations/refund", {"reservation_id": second["reservation_id"]}, stage="refund")
    reservation(refunded.get("reservation"), "refund")
    expect(refunded["reservation"]["state"] == "refunded", "refund", "explicit_refund")
    balance("750", [], "refund")
    insufficient = rpc("/reservations", {"account_id": ACCOUNT, "maximum": "100000", "timeout_seconds": 600},
                       idem=new_key(), stage="reserve", status=409)
    expect(insufficient.get("code") == "LEDGER_CREDIT_INSUFFICIENT", "reserve", "insufficient_credit")
    expiring, expiry_input = reserve("10", 1, "expiry")
    time.sleep(1.2)
    late = rpc("/reservations/settle", {"reservation_id": expiring["reservation_id"], "charged_amount": "5"},
               stage="expiry", status=409)
    expired = late.get("reservation", {})
    reservation(expired, "expiry")
    expect(late.get("code") == "LEDGER_SETTLEMENT_EXPIRED" and late.get("outcome") == "expired" and
           expired["state"] == "expired" and expired["expires_at"] == expiring["expires_at"] and
           expired["terminal_at"] == expiring["expires_at"], "expiry", "authoritative_original_deadline")
    expect(rpc("/reservations", expiry_input, idem=expiring["idempotency_key"], stage="expiry")["reservation"] == expired,
           "expiry", "retry_cannot_extend_expiry")
    expect(rpc("/reservations/lookup", identity, stage="lookup") == settled, "lookup", "retained_terminal")
    expect(rpc("/reservations/lookup", {"reservation_id": expiring["reservation_id"]}, stage="lookup")["reservation"] == expired,
           "lookup", "retained_expiry")
    balance("750", [], "expiry")
    # Ordinary serve now owns the public website root; it is not a native route.
    # Keep the accounting/native denial assertions without claiming root is absent.
    for method, body, expected in (("GET", None, 200), ("POST", {}, 415)):
        status, payload = run_curl(s.port, None, method, "/", body, None, "website")
        expect(status == expected and s.key.encode() not in payload,
               "website", "public_root_and_no_accounting_key")
    for path in ("/_/", "/api/collections", "/api/settings", "/api/backups",
                 "/api/files", "/api/realtime", "/api/collections/_superusers/auth-with-password"):
        for body in (None, {}):
            doc = rpc(path, body, stage="routes", status=404, auth="missing")
            expect(doc.get("code") == "LEDGER_HTTP_NOT_FOUND", "routes", "native_surface_absent")

    active, active_input = reserve("50", stage="restart")
    balance("750", [active], "restart")
    s.stop()
    s.start()  # Same original directory, never re-init or copy the database.
    balance("750", [active], "restart")
    expect(rpc("/reservations", active_input, stage="restart", idem=active["idempotency_key"])["reservation"] == active,
           "restart", "hold_and_idempotency_preserved")
    expect(rpc("/credits", credit_input, stage="restart", idem=credit_key) == first_credit,
           "restart", "original_credit_not_current_balance")
    balance("750", [active], "restart")


def self_test():
    """Offline/synthetic harness checks; unittest failures never print raw text."""
    import http.server
    import unittest
    from unittest import mock

    class Checks(unittest.TestCase):
        def test_actual_curl_config_and_auth(self):
            received = []

            class Handler(http.server.BaseHTTPRequestHandler):
                def do_POST(self):
                    received.append((self.headers.get("Authorization"),
                                     self.rfile.read(int(self.headers["Content-Length"]))))
                    self.send_response(200)
                    self.end_headers()
                    self.wfile.write(b'{}')

                def log_message(self, format, *args):
                    pass

            server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
            worker = threading.Thread(target=server.serve_forever)
            worker.start()
            key = secrets.token_urlsafe(48)
            body = {"synthetic": 'quote" slash\\ newline\n snowman\u2603'}
            try:
                for auth in (None, key):
                    self.assertEqual(run_curl(server.server_port, auth, "POST", "/", body, None, "self_test"), (200, b'{}'))
                self.assertEqual([item[0] for item in received], [None, "Bearer " + key])
                self.assertTrue(all(json.loads(item[1]) == body for item in received))
            finally:
                server.shutdown()
                worker.join(timeout=2)
                server.server_close()

        def test_clean_environment(self):
            with mock.patch.dict(os.environ, {"HTTP_PROXY": "synthetic", "LEDGER_ADMIN_KEY": "synthetic",
                                              "INTAKE_METRICS_HONEYCOMB_API_KEY": "synthetic",
                                              "OTEL_SDK_DISABLED": "false"}):
                self.assertEqual(set(clean_env()), {"PATH", "OTEL_SDK_DISABLED"})
                self.assertEqual(clean_env()["OTEL_SDK_DISABLED"], "true")

        def test_curl_failure_and_config_not_argv(self):
            with mock.patch(__name__ + ".run_bounded", return_value=(7, b"hostile", b"hostile")) as run:
                with self.assertRaises(SmokeError) as caught:
                    run_curl(1, "synthetic-key", "POST", "/", {}, None, "self_test")
                self.assertEqual(caught.exception.constraint, "curl_exit_nonzero_unknown_outcome")
                argv, env, *_ = run.call_args.args
                self.assertEqual(argv[:4], ["curl", "--disable", "--noproxy", "*"])
                self.assertNotIn("synthetic-key", str(argv) + str(env))

        def test_output_cap_and_deadline(self):
            for code, timeout, constraint in (
                ("import os; os.write(1, b'x' * 100000)", 2, "output_within_64k"),
                ("import time; time.sleep(30)", 0.05, "child_deadline_unknown_outcome"),
            ):
                with self.assertRaises(SmokeError) as caught:
                    run_bounded([sys.executable, "-c", code], clean_env(), "self_test", timeout)
                self.assertEqual(caught.exception.constraint, constraint)

        def test_bad_response_is_closed(self):
            for payload in (b"hostile", b"[]", b"\xff"):
                with self.assertRaises(SmokeError):
                    decode(payload, "self_test")
            with self.assertRaises(SmokeError):
                account([{"account_id": ACCOUNT, "balance": "0", "enabled": 1}], ACCOUNT, "0", "self_test")

        def test_entry_failures_never_echo(self):
            import contextlib
            import io
            for args in (["--url", "synthetic-hostile"], ["--binary", sys.executable]):
                output = io.StringIO()
                with contextlib.redirect_stdout(output), mock.patch("subprocess.Popen", side_effect=OSError("synthetic-hostile")):
                    self.assertEqual(main(args), 1)
                self.assertIn("SMOKE_CHECK", output.getvalue())
                self.assertNotIn("synthetic-hostile", output.getvalue())

        def test_shutdown_must_be_normal(self):
            s = Service(sys.executable, "/synthetic-unused")
            s.proc = mock.Mock()
            s.proc.poll.return_value = None
            s.proc.wait.side_effect = [subprocess.TimeoutExpired("synthetic", 0), 0]
            s.proc.returncode = 0
            s.output = mock.Mock()
            s.output.finish.return_value = b""
            proc = s.proc
            with self.assertRaises(SmokeError) as caught:
                s.stop()
            self.assertEqual(caught.exception.constraint, "forced_termination_inflight_unknown")
            proc.kill.assert_called_once()
            self.assertIsNone(s.proc)

    result = unittest.TestResult()
    unittest.defaultTestLoader.loadTestsFromTestCase(Checks).run(result)
    expect(result.wasSuccessful(), "self_test", "offline_harness_checks")
    print(f"PASS smoke:self_test checks={result.testsRun}")


def report(error: SmokeError):
    print(f"FAIL SMOKE_CHECK stage={error.stage} constraint={error.constraint} outcome=unknown; "
          "inspect this synthetic gate privately; no billing confirmation or automatic replay")


def main(argv: list[str]) -> int:
    class SafeParser(argparse.ArgumentParser):
        def error(self, message):
            raise SmokeError("arguments", "binary_or_self_test_only")

    parser = SafeParser(description=__doc__)
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--binary", help="Path to the compiled local intake executable")
    group.add_argument("--self-test", action="store_true", help="Run offline/synthetic harness checks only")
    service, root = None, None
    failed, state_safe = False, True
    try:
        args = parser.parse_args(argv)
        expect(shutil.which("curl") is not None, "transport", "curl_available")
        if args.self_test:
            self_test()
            return 0
        binary = os.path.abspath(args.binary)
        expect(os.path.isfile(binary) and os.access(binary, os.X_OK), "binary", "compiled_executable_exists")
        root = tempfile.mkdtemp(prefix="possums-intake-smoke-")
        service = Service(binary, os.path.join(root, "state"))
        service.init()
        service.start()
        run_cases(service)
    except SmokeError as error:
        failed = True
        state_safe = error.constraint != "child_join_unconfirmed"
        report(error)
    except KeyboardInterrupt:
        failed = True
        report(SmokeError("harness", "interrupted_inflight_unknown"))
    except Exception:
        failed = True
        report(SmokeError("harness", "unexpected_local_cause_unknown"))
    finally:
        if service is not None:
            try:
                service.stop()
            except SmokeError as error:
                failed = True
                report(error)
            except Exception:
                failed = True
                report(SmokeError("shutdown", "join_cause_unknown"))
        if root is not None:
            if not state_safe or (service is not None and service.proc is not None):
                failed = True
                report(SmokeError("cleanup", "state_preserved_child_not_joined"))
            else:
                try:
                    shutil.rmtree(root)
                except Exception:
                    failed = True
                    report(SmokeError("cleanup", "disposable_state_removal_unconfirmed"))
    if not failed:
        print("PASS smoke:all synthetic_cli_curl_restart_normal_shutdown")
    return int(failed)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
