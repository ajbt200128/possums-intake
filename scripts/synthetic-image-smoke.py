#!/usr/bin/env python3
"""Disposable synthetic-serve image qualification, LOCAL Docker only.
Reuses the frozen ordinary-image identity checks; adds a separate fixture for
`intake synthetic-serve`. No pulls/uploads, network, production state, volume
or alternative source-only pass. The exec-only probe is never shipped.
"""
import argparse
import hashlib
import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import uuid
from pathlib import Path

import release as r

r.CODES.update({
    "synthetic_image": ("synthetic_image", "isolated_disposable_synthetic_serve", "Disposable synthetic image evidence is unconfirmed. Inspect the local fixture privately; no production state, retry or upload was used."),
    "synthetic_cleanup": ("synthetic_cleanup", "confirmed_owned_container_removal_and_attach_join", "Disposable fixture teardown is unconfirmed; underlying cause unknown. Inspect the owned local fixture privately before further qualification. No image was published or deployed."),
    "synthetic_startup": ("synthetic_startup", "one_expected_closed_startup_failure_projection", "Isolated synthetic startup-failure evidence is unconfirmed. Inspect the owned local fixture privately; no ledger init, retry, state or volume was used."),
})


def base_module():
    # Reuse the frozen ordinary-image identity checks verbatim; never fork them.
    spec = importlib.util.spec_from_file_location("image-smoke", Path(__file__).parent / "image-smoke.py")
    if spec is None or spec.loader is None:
        raise r.Stop("image_tools")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


smoke = base_module()


def run(args, *, env=None, timeout=30, code="image_runtime"):
    try:
        result = subprocess.run(args, stdin=subprocess.DEVNULL, capture_output=True, timeout=timeout, check=False, env=env)
        r.require(len(result.stdout) <= 65536 and len(result.stderr) <= 65536, code)
        return result
    except (OSError, subprocess.TimeoutExpired):
        raise r.Stop(code, outcome="unknown") from None


def popen(args, env):
    return subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)


DIAGNOSTIC_FIELDS = frozenset({"code", "stage", "constraint", "outcome", "next_step"})
STARTUP_DIAGNOSTICS = ("healthy_healthcheck", "missing_key_capture", "invalid_operator_capture",
                       "signal_cancels_hold", "closed_failure_output")


def closed_projection(raw, code, stage, constraint, outcome, next_step):
    """Accept only the exact five-field locally authored diagnostic with closed
    values. Duplicate keys, extras, unknown codes and hostile bytes fail closed,
    and nothing from the payload is echoed."""
    def unique(pairs):
        keys = [key for key, _ in pairs]
        if len(keys) != len(set(keys)):
            raise ValueError("duplicate diagnostic field")
        return dict(pairs)

    try:
        view = json.loads(raw, object_pairs_hook=unique)
    except (ValueError, TypeError):
        return False
    return (type(view) is dict and set(view) == DIAGNOSTIC_FIELDS
            and view["code"] == code and view["stage"] == stage
            and view["constraint"] == constraint and view["outcome"] == outcome
            and view["next_step"] == next_step)


def negative_cases():
    """Two isolated startup faults, one per owned fixture, using only disposable
    fake values with a single fault each. A variable's name reaches Docker argv;
    its value only ever travels through the child environment."""
    operator_password = "synthetic-only-" + uuid.uuid4().hex
    return [
        {"name": "missing_key_capture",
         "credentials": {"LEDGER_OPERATOR_EMAIL": "synthetic-image-operator@example.invalid",
                         "LEDGER_OPERATOR_PASSWORD": operator_password},
         "code": "LEDGER_RUNTIME_KEY_INVALID", "health_constraint": "valid_runtime_admin_key",
         "health_next_step": "Inject a valid high-entropy LEDGER_ADMIN_KEY through the runtime environment; never put credentials in arguments or logs.",
         "primary_stage": "startup", "primary_constraint": "runtime_admin_key_at_least_32_bytes",
         "primary_next_step": "Inject a high-entropy admin key of at least 32 bytes through LEDGER_ADMIN_KEY. Never put it in command arguments or logs."},
        {"name": "invalid_operator_capture",
         "credentials": {"LEDGER_ADMIN_KEY": "synthetic-image-credential-" + uuid.uuid4().hex,
                         "LEDGER_OPERATOR_EMAIL": "not-an-email",
                         "LEDGER_OPERATOR_PASSWORD": operator_password},
         "code": "LEDGER_OPERATOR_SETUP_INVALID", "health_constraint": "valid_runtime_operator_setup",
         "health_next_step": "Supply valid throwaway runtime operator credentials and the qualified operator setup; inspect privately, never print credentials.",
         "primary_stage": "operator_setup", "primary_constraint": "valid_runtime_operator_credentials",
         "primary_next_step": "Supply valid LEDGER_OPERATOR_EMAIL and LEDGER_OPERATOR_PASSWORD through runtime environment for synthetic-serve. No scratch state was created."},
    ]


class Synthetic:
    def __init__(self, image, probe, asset_manifest):
        self.image, self.probe, self.asset_manifest = image, probe, asset_manifest
        self.name = "intake-local-synthetic-" + uuid.uuid4().hex
        self.code = "synthetic_image"
        self.credentials = {
            "LEDGER_ADMIN_KEY": "synthetic-image-credential-" + uuid.uuid4().hex,
            "LEDGER_OPERATOR_EMAIL": "synthetic-image-operator@example.invalid",
            "LEDGER_OPERATOR_PASSWORD": "synthetic-only-" + uuid.uuid4().hex,
            "OTEL_SDK_DISABLED": "true",
            "LEDGER_SYNTHETIC_STARTUP_DIAGNOSTICS": "true",
        }
        self.process = None
        self.create_attempted = False

    def env(self):
        # Values reach Docker only through the child environment, never argv.
        return {**os.environ, **self.credentials}

    def flags(self):
        # No /state volume or mount and no manual init: synthetic-serve always
        # creates its own fresh disposable scratch under a bounded tmpfs.
        return ["--pull", "never", "--network", "none", "--read-only", "--cap-drop", "ALL",
                "--security-opt", "no-new-privileges", "--ulimit", "core=0", "--log-driver", "none",
                "--tmpfs", "/tmp:rw,size=128m,mode=1777",  # noqa: S108 - bounded Docker tmpfs, not a host temp path
                "--mount", "type=bind,src=" + self.probe + ",dst=/probe,readonly",
                "--mount", "type=bind,src=" + self.asset_manifest + ",dst=/asset-manifest.json,readonly",
                "-e", "LEDGER_ADMIN_KEY", "-e", "LEDGER_OPERATOR_EMAIL",
                "-e", "LEDGER_OPERATOR_PASSWORD", "-e", "OTEL_SDK_DISABLED",
                "-e", "LEDGER_SYNTHETIC_STARTUP_DIAGNOSTICS"]

    def command(self):
        return ["synthetic-serve", "--listen", "0.0.0.0:8090"]

    def create(self):
        # A lost create response cannot establish absence. This unique fixture
        # name remains owned for cleanup even when creation is unconfirmed.
        self.create_attempted = True
        result = run(["docker", "create", "--name", self.name, *self.flags(), self.image, *self.command()],
                     env=self.env(), code=self.code)
        r.require(result.returncode == 0, self.code)

    def attach(self):
        self.process = popen(["docker", "start", "-a", self.name], self.env())

    def health(self):
        # Passive health reads only, bounded, no mutation replay.
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            result = run(["docker", "exec", self.name, "/probe", "health"], env=self.env(), code="synthetic_image")
            if result.returncode == 0:
                return
            time.sleep(0.1)
        raise r.Stop("synthetic_image", outcome="unknown")

    def healthcheck(self):
        # Passive executable healthcheck: run the currently running intake binary
        # from the same Docker PID namespace/user. Only exit 0 with empty
        # stdout/stderr is healthy; non-empty success is a privacy failure.
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            result = run(["docker", "exec", self.name, "/proc/1/exe", "synthetic-healthcheck"],
                         env=self.env(), code="synthetic_image")
            if result.returncode == 0:
                r.require(result.stdout == b"" and result.stderr == b"", "image_privacy")
                return
            time.sleep(0.1)
        raise r.Stop("synthetic_image", outcome="unknown")

    def exec(self, mode):
        result = run(["docker", "exec", self.name, "/probe", mode], env=self.env(), code="synthetic_image")
        try:
            r.require(result.returncode == 0 and result.stderr == b""
                      and json.loads(result.stdout) == {"passed": True, "stage": "synthetic_probe"}, "synthetic_image")
        except (ValueError, TypeError):
            raise r.Stop("synthetic_image") from None

    def detach(self):
        result = run(["docker", "stop", "--time", "10", self.name], env=self.env(), code="synthetic_image")
        r.require(result.returncode == 0, "synthetic_image")
        if self.process is None:
            raise r.Stop("synthetic_image")
        try:
            out, err = self.process.communicate(timeout=15)
            r.require(self.process.returncode == 0 and out == b"" and err == b"", "image_privacy")
        except subprocess.TimeoutExpired:
            raise r.Stop("synthetic_image", outcome="unknown") from None
        self.process = None

    def sequence(self):
        self.create()
        self.attach()
        self.health()
        self.healthcheck()
        self.exec("operator")
        self.exec("synthetic-wildcard")
        self.exec("synthetic-privacy")
        self.detach()
        # Same container, no state init: scratch must be new and empty again.
        self.attach()
        self.health()
        self.healthcheck()
        self.exec("synthetic-fresh")
        self.exec("synthetic-privacy")
        self.detach()

    def cleanup(self, *, primary_failed=False):
        # Teardown uncertainty refuses a new pass, but cannot erase an already
        # observed primary failure. Never retry removal or abandon a join claim.
        uncertain = False
        if self.create_attempted:
            try:
                result = run(["docker", "rm", "-f", self.name], env=self.env(), code="synthetic_cleanup")
                uncertain = result.returncode != 0
            except Exception:
                uncertain = True
        if self.process is not None:
            joined = False
            try:
                self.process.communicate(timeout=15)
                joined = True
            except subprocess.TimeoutExpired:
                try:
                    self.process.kill()
                    self.process.communicate(timeout=15)
                    joined = True
                except Exception:
                    uncertain = True
            except Exception:
                uncertain = True
            if joined:
                self.process = None
        if uncertain and not primary_failed:
            raise r.Stop("synthetic_cleanup", outcome="unknown")


class NegativeStartup(Synthetic):
    """One owned isolated startup-failure fixture. Reuses Synthetic's lifecycle
    and cleanup; adds opt-in diagnostics, a bounded passive health capture and a
    single SIGTERM join that must observe the original primary diagnostic."""

    def __init__(self, image, case):
        self.image = image
        self.name = "intake-local-synthetic-negative-" + uuid.uuid4().hex
        self.code = "synthetic_startup"
        self.case = case
        self.credentials = {**case["credentials"], "OTEL_SDK_DISABLED": "true",
                            "LEDGER_SYNTHETIC_STARTUP_DIAGNOSTICS": "true"}
        self.sentinels = tuple(case["credentials"].values())
        self.process: subprocess.Popen | None = None
        self.create_attempted = False

    def env(self):
        # Never inherit a host credential that could mask the intended fault.
        base = {key: os.environ[key] for key in ("PATH", "HOME") if key in os.environ}
        return {**base, **self.credentials}

    def flags(self):
        # The probe is not needed: the fault is captured before listener bind.
        flags = ["--pull", "never", "--network", "none", "--read-only", "--cap-drop", "ALL",
                 "--security-opt", "no-new-privileges", "--ulimit", "core=0", "--log-driver", "none",
                 "--tmpfs", "/tmp:rw,size=128m,mode=1777"]  # noqa: S108 - bounded Docker tmpfs
        for name in self.credentials:
            flags += ["-e", name]
        return flags

    def leaks(self, raw):
        return any(sentinel.encode() in raw for sentinel in self.sentinels)

    def capture(self):
        # Bounded passive healthcheck: the running intake binary in the same PID
        # namespace/user. Only the one expected closed failure projection ends it.
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            result = run(["docker", "exec", self.name, "/proc/1/exe", "synthetic-healthcheck"],
                         env=self.env(), code=self.code)
            if result.returncode == 1 and result.stdout == b"" and not self.leaks(result.stderr) \
                    and closed_projection(result.stderr, self.case["code"], "validation",
                                          self.case["health_constraint"], "not_ready", self.case["health_next_step"]):
                return
            time.sleep(0.2)
        raise r.Stop(self.code, outcome="unknown")

    def running(self):
        # Only the boolean running state; never raw Docker configuration.
        result = run(["docker", "inspect", "--format", "{{.State.Running}}", self.name],
                     env=self.env(), code=self.code)
        return result.returncode == 0 and result.stdout.strip() == b"true"

    def stop(self):
        # SIGTERM must cancel the bounded diagnostic hold: a clean exit 1 with the
        # original primary JSON proves cancellation rather than a SIGKILL timeout.
        result = run(["docker", "stop", "--time", "10", self.name], env=self.env(), code=self.code)
        r.require(result.returncode == 0, self.code)
        process = self.process
        if process is None:
            raise r.Stop(self.code)
        try:
            out, err = process.communicate(timeout=15)
        except subprocess.TimeoutExpired:
            raise r.Stop(self.code, outcome="unknown") from None
        self.process = None
        r.require(process.returncode == 1 and out == b"" and not self.leaks(err)
                  and closed_projection(err, self.case["code"], self.case["primary_stage"],
                                        self.case["primary_constraint"], "no_effect", self.case["primary_next_step"]), self.code)

    def sequence(self):
        self.create()
        self.attach()
        self.capture()
        r.require(self.running(), self.code)
        self.stop()


def qualify(archive, probe):
    r.require(shutil.which("docker") is not None and shutil.which("skopeo") is not None
              and Path(probe).is_file(), "image_tools")
    manifest = Path(__file__).resolve().parents[1] / "internal/operator/ui/assets.json"
    r.require(manifest.is_file(), "image_archive")
    config_identity, architecture = smoke.archive_identity(archive)
    runtime = smoke.success(["docker", "info", "--format", "{{.OSType}}/{{.Architecture}}"], "image_tools").decode().strip()
    aliases = {"linux/aarch64": "arm64", "linux/arm64": "arm64", "linux/x86_64": "amd64", "linux/amd64": "amd64"}
    r.require(aliases.get(runtime) == architecture, "image_tools")
    smoke.success(["docker", "load", "-i", str(archive)], "image_archive")
    image = smoke.loaded_image_identity(config_identity)
    with tempfile.TemporaryDirectory(prefix="intake-local-synthetic-probe-") as directory:
        local_probe = Path(directory) / "probe"
        shutil.copyfile(probe, local_probe)
        local_probe.chmod(0o555)
        fixture = Synthetic(image, str(local_probe.resolve()), str(manifest))
        primary_failed = True
        try:
            fixture.sequence()
            for case in negative_cases():
                negative = NegativeStartup(image, case)
                failed = True
                try:
                    negative.sequence()
                    failed = False
                finally:
                    negative.cleanup(primary_failed=failed)
            primary_failed = False
        finally:
            fixture.cleanup(primary_failed=primary_failed)
    try:
        with open(archive, "rb") as stream:
            archive_hash = hashlib.file_digest(stream, "sha256").hexdigest()
    except OSError:
        raise r.Stop("image_archive") from None
    # Public artifact identities and closed fixture booleans only.
    return {"config_identity": config_identity, "docker_runtime_identity": image,
            "archive_sha256": archive_hash, "platform": "linux/" + architecture,
            "synthetic": {"wildcard_listener_reachable": True, "native_operator": True,
                          "empty_auth_origins_and_logs": True, "fresh_state_after_restart": True,
                          "attached_stdio_empty": True, "telemetry_disabled": True},
            # Literal all-true projection, reached only after every new case passed.
            "startup_diagnostics": dict.fromkeys(STARTUP_DIAGNOSTICS, True),
            "previous_release_compatibility": "unknown"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive")
    parser.add_argument("probe", help="Generated image-smoke-probe/bin/nix")
    args = parser.parse_args()
    evidence = qualify(args.archive, args.probe)
    print(json.dumps(evidence, sort_keys=True))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(json.dumps(r.diagnostic(error)))
        sys.exit(1)
