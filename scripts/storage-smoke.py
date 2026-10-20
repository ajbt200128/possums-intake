#!/usr/bin/env python3
"""Local synthetic storage faults against a compiled CLI; never live state.

Only --binary is accepted. Reuses smoke's closed environment, curl transport,
bounded output and joined teardown. No platform calls or production fault flags.
"""
import argparse
import json
import os
import shutil
import sqlite3
import sys
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path

import smoke as s


def rpc(service, path, body, *, identity=None, status=200):
    return service.request(path, body, idem=identity, status=status, stage="storage_rpc")


def snapshot(service):
    return rpc(service, "/balance", {"account_id": "synthetic-storage"})


def prepare(service):
    service.init()
    service.start()
    rpc(service, "/accounts/provision", {"account_id": "synthetic-storage"})
    identity = s.new_key()
    receipt = rpc(service, "/credits", {"account_id": "synthetic-storage", "amount": "100"}, identity=identity)
    return identity, receipt


def reserve(service, timeout=600):
    body = {"account_id": "synthetic-storage", "maximum": "10", "timeout_seconds": timeout}
    identity = s.new_key()
    row = rpc(service, "/reservations", body, identity=identity)["reservation"]
    return body, identity, row


def crash(service):
    # Deliberate fixture process loss, never a monetary/cancellation receipt.
    proc = service.proc
    proc.kill()
    proc.wait(timeout=5)
    s.expect(proc.returncode < 0, "storage_crash", "forced_exit_observed")
    s.expect(not service.output.finish("storage_crash"), "storage_privacy", "empty_stdio")
    service.proc = None  # actual join precedes restart of the original directory


def crash_and_expiry(service):
    credit_key, credit = prepare(service)
    reader = sqlite3.connect("file:" + service.state + "/data.db?mode=ro", uri=True)
    try:
        reader.execute("BEGIN")
        reader.execute("SELECT count(*) FROM ledger_reservations").fetchone()
        body, identity, active = reserve(service)
        _, _, terminal = reserve(service)
        terminal = rpc(service, "/reservations/settle", {"reservation_id": terminal["reservation_id"], "charged_amount": "3"})["reservation"]
        _, _, refunded = reserve(service)
        refunded = rpc(service, "/reservations/refund", {"reservation_id": refunded["reservation_id"]})["reservation"]
        short_body, short_key, short = reserve(service, timeout=3)
        deadline = datetime.strptime(short["expires_at"], "%Y-%m-%dT%H:%M:%S.%fZ").replace(tzinfo=timezone.utc).timestamp()
        before = snapshot(service)
        s.account(before["account_balance"], "synthetic-storage", "97", "storage_before_crash")
        expected_holds = sorted([active, short], key=lambda row: row["reservation_id"])
        s.expect(before["pending_reservations"] == expected_holds and before["available"] == "77", "storage_before_crash", "both_holds_still_active")
        wal = Path(service.state, "data.db-wal")
        s.expect(wal.is_file() and wal.stat().st_size > 32, "storage_wal", "nonempty_live_wal")
        crash(service)
        s.expect(time.time() < deadline, "storage_crash", "joined_before_immutable_deadline")
        s.expect(wal.is_file() and wal.stat().st_size > 32, "storage_wal", "wal_survives_process_loss")
    finally:
        primary = sys.exc_info()[1]
        try:
            reader.close()
        except Exception:
            if primary is None:
                raise s.SmokeError("storage_reader", "reader_close_unconfirmed") from None
            primary.add_note("STORAGE_TEARDOWN_UNCONFIRMED")
    # Only a disposable process outage; not a production wait/retry policy.
    time.sleep(max(0, deadline - time.time()) + 0.1)
    service.start()
    after = snapshot(service)
    s.expect(after["account_balance"] == before["account_balance"], "storage_restart", "total_preserved")
    s.expect(after["pending_reservations"] == [active] and after["available"] == "87", "storage_restart", "expired_hold_released_active_preserved")
    s.expect(rpc(service, "/credits", {"account_id": "synthetic-storage", "amount": "100"}, identity=credit_key) == credit, "storage_replay", "original_credit_receipt")
    s.expect(rpc(service, "/reservations", body, identity=identity)["reservation"] == active, "storage_replay", "immutable_active_row")
    s.expect(rpc(service, "/reservations/refund", {"reservation_id": terminal["reservation_id"]})["reservation"] == terminal, "storage_replay", "first_settlement_wins")
    s.expect(rpc(service, "/reservations/settle", {"reservation_id": refunded["reservation_id"], "charged_amount": "10"})["reservation"] == refunded, "storage_replay", "first_refund_wins")
    expired = rpc(service, "/reservations", short_body, identity=short_key)["reservation"]
    late = rpc(service, "/reservations/settle", {"reservation_id": short["reservation_id"], "charged_amount": "1"}, status=409)["reservation"]
    s.expect(expired == late and expired["state"] == "expired" and expired["expires_at"] == short["expires_at"], "storage_expiry", "immutable_expiry_late_charge_refused")
    s.expect(snapshot(service) == after, "storage_replay", "no_duplicate_money_effect")
    service.stop()
    # Necessary rows/WAL may retain synthetic state; credentials must not occur.
    for path in Path(service.state).glob("*"):
        if path.is_file():
            s.expect(service.key.encode() not in path.read_bytes(), "storage_privacy", "admin_key_absent")


def capacity_failure(service, binary, root):
    prepare(service)
    service.stop()
    # RLIMIT_FSIZE exercises actual writes, not a fabricated SQLite exception.
    # It is a per-file growth cap, NOT an ENOSPC/whole-filesystem simulation.
    cap = max(Path(service.state, name).stat().st_size for name in ("data.db", "auxiliary.db")) + 65536
    wrapper = root / "limited-cli"
    wrapper.write_text("#!" + sys.executable + "\nimport os, resource, sys\n"
                       + "resource.setrlimit(resource.RLIMIT_FSIZE, (" + str(cap) + ", " + str(cap) + "))\n"
                       + "os.execve(" + repr(binary) + ", [" + repr(binary) + "] + sys.argv[1:], os.environ)\n")
    wrapper.chmod(0o700)
    service.binary = str(wrapper)
    service.start()
    acknowledged = 0
    failed_key = None
    failure_outcome = None
    for _ in range(256):
        identity = s.new_key()
        status, payload = s.run_curl(service.port, service.key, "POST", "/credits", {"account_id": "synthetic-storage", "amount": "1"}, identity, "storage_capacity")
        s.expect(service.key.encode() not in payload, "storage_privacy", "no_key_in_response")
        response = s.decode(payload, "storage_capacity")
        if status == 200:
            s.expect(response["credit"]["balance_after"] == str(101 + acknowledged), "storage_capacity", "acknowledged_credit_exact")
            acknowledged += 1
            continue
        s.expect(status == 503, "storage_capacity", "storage_http_unavailable")
        failure_outcome = capacity_outcome(response)
        failed_key = identity
        break
    s.expect(failed_key is not None and acknowledged > 0, "storage_capacity", "actual_growth_failure_after_acknowledged_writes")
    service.stop()
    service.binary = binary
    service.start()  # same original state; no init, reset, copy or compensation
    observed = int(snapshot(service)["account_balance"][0]["balance"])
    capacity_balance(observed, acknowledged, failure_outcome)
    rpc(service, "/credits", {"account_id": "synthetic-storage", "amount": "1"}, identity=failed_key)
    s.expect(snapshot(service)["account_balance"][0]["balance"] == str(101 + acknowledged), "storage_capacity", "same_identity_resolves_once")
    service.stop()


def capacity_outcome(response):
    pair = (response.get("code"), response.get("outcome"))
    s.expect(pair in {("LEDGER_STORAGE_UNAVAILABLE", "no_effect"), ("LEDGER_COMMIT_UNKNOWN", "unknown")},
             "storage_capacity", "supported_storage_outcome_pair")
    return pair[1]


def capacity_balance(observed, acknowledged, outcome):
    expected = {100 + acknowledged}
    if outcome == "unknown":
        expected.add(101 + acknowledged)
    s.expect(observed in expected, "storage_capacity", "known_rollback_or_uncertain_credit_only")


def run(binary):
    try:
        binary = str(Path(binary).resolve(strict=True))
        executable = os.access(binary, os.X_OK)
    except OSError:
        raise s.SmokeError("storage_setup", "compiled_binary_executable") from None
    s.expect(executable, "storage_setup", "compiled_binary_executable")
    root = Path(tempfile.mkdtemp(prefix="possums-storage-smoke-"))
    service = None
    primary = None
    try:
        service = s.Service(binary, str(root / "crash_expiry"))
        crash_and_expiry(service)
        print("PASS storage:crash_expiry")
        service = s.Service(binary, str(root / "capacity"))
        capacity_failure(service, binary, root)
        print("PASS storage:capacity")
    except (Exception, KeyboardInterrupt) as error:
        primary = error if isinstance(error, s.SmokeError) else s.SmokeError("storage_fixture", "completed_synthetic_qualification")
        if "STORAGE_TEARDOWN_UNCONFIRMED" in getattr(error, "__notes__", ()):
            primary.add_note("STORAGE_TEARDOWN_UNCONFIRMED")
    finally:
        if service is not None and service.proc is not None:
            try:
                service.stop()
            except (Exception, KeyboardInterrupt):
                if primary is None:
                    primary = s.SmokeError("storage_shutdown", "joined_storage_teardown")
                primary.add_note("STORAGE_TEARDOWN_UNCONFIRMED")
        if primary is None:
            try:
                shutil.rmtree(root)
            except OSError:
                primary = s.SmokeError("storage_cleanup", "disposable_state_removal_unconfirmed")
    if primary is not None:
        raise primary
    print("PASS storage:all original_state_crash_expiry_file_growth_normal_shutdown")


def main(argv):
    class SafeParser(argparse.ArgumentParser):
        def error(self, message):
            raise s.SmokeError("arguments", "local_binary_only")

    try:
        parser = SafeParser()
        parser.add_argument("--binary", required=True)
        args = parser.parse_args(argv)
        run(args.binary)
        return 0
    except (Exception, KeyboardInterrupt) as error:
        failure = error if isinstance(error, s.SmokeError) else s.SmokeError("storage_fixture", "completed_synthetic_qualification")
        diagnostic = {"code": "STORAGE_CHECK", "stage": failure.stage, "constraint": failure.constraint,
                      "outcome": "unknown", "next_step": "Inspect the disposable fixture privately; do not replay live work or initialize as recovery."}
        if "STORAGE_TEARDOWN_UNCONFIRMED" in getattr(error, "__notes__", ()):
            diagnostic["cleanup_failure"] = "STORAGE_TEARDOWN_UNCONFIRMED"
        print(json.dumps(diagnostic))
        return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
