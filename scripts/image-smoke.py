#!/usr/bin/env python3
"""Actual generated archive qualification, LOCAL synthetic Docker only.
No pulls/uploads, network, production state, or alternative source-only pass.
Probe is bind-mounted for exec, never shipped in the production image.
"""
import argparse
import hashlib
import json
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid
from pathlib import Path

import release as r

r.CODES.update({
    "image_tools": ("image_tools", "available_local_docker_skopeo_probe", "Provide the local runtime, supported digest tool and generated Linux probe. No installation or image pull was attempted."),
    "image_archive": ("image_archive", "generated_immutable_archive", "Build and inspect the declared Nix archive; no source-only qualification substitutes for it."),
    "image_runtime": ("image_runtime", "isolated_actual_image_evidence", "Actual generated image fixture is unconfirmed. Inspect locally; no production state, retry or upload was used."),
    "image_privacy": ("image_privacy", "empty_process_and_framework_logs", "Synthetic generated-image privacy assertion failed. Inspect transient fixture output privately; do not share raw output."),
    "image_migration": ("image_migration", "actual_failed_migration_execution", "Generated-image migration execution is unconfirmed. Run the real init file-limit fixture and inspect rollback/partial-state refusal; schema drift alone is insufficient."),
})


STARTUP_EXPECTED = {
    "LEDGER_STARTUP_MISSING_STATE": {
        "stage": "startup", "constraint": "existing_state_database", "outcome": "unavailable",
        "next_step": "Attach the original state. Only disposable empty local state may use explicit init."},
    "LEDGER_INIT_NONEMPTY": {
        "stage": "init", "constraint": "deliberately_selected_empty_directory", "outcome": "no_effect",
        "next_step": "Preserve existing or partial state; choose a different empty disposable directory."},
    "LEDGER_STARTUP_SCHEMA": {
        "stage": "state_validation", "constraint": "supported_schema_and_metadata", "outcome": "unavailable",
        "next_step": "Inspect schema privately. No down-migration, empty reset or financial restore is authorized."},
    "LEDGER_COMMIT_UNKNOWN": {
        "stage": "init", "constraint": "authoritative_commit_evidence", "outcome": "unknown",
        "next_step": "Initialization incomplete or unconfirmed. Preserve partial state; do not re-run init on it. Inspect privately or select a different empty disposable directory."},
}

# Synthetic per-file cap: pinned bootstrap completes, then the v1 DDL commit
# cannot grow its WAL. Exact init-stage diagnostics and SQL checks are required;
# a platform that instead fails bootstrap must NOT qualify migration execution.
MIGRATION_FILE_LIMIT = 196608


def command(args, code="image_runtime", timeout=30):
    try:
        result = subprocess.run(args, stdin=subprocess.DEVNULL, capture_output=True, timeout=timeout, check=False)
        r.require(len(result.stdout) <= 65536 and len(result.stderr) <= 65536, code)
        return result
    except (OSError, subprocess.TimeoutExpired):
        raise r.Stop(code, outcome="unknown") from None


def success(args, code="image_runtime"):
    result = command(args, code)
    r.require(result.returncode == 0, code)
    return result.stdout


def archive_identity(archive):
    try:
        with tarfile.open(archive, "r:*") as tar:
            stream = tar.extractfile("manifest.json")
            if stream is None:
                raise r.Stop("image_archive")
            manifests = json.loads(stream.read(65537))
            r.require(len(manifests) == 1, "image_archive")
            member = tar.getmember(manifests[0]["Config"])
            r.require(member.isfile() and member.size <= 65536, "image_archive")
            stream = tar.extractfile(member)
            if stream is None:
                raise r.Stop("image_archive")
            raw = stream.read()
            config = json.loads(raw)
    except r.Stop:
        raise
    except Exception:
        raise r.Stop("image_archive") from None
    settings = config["config"]
    r.require(settings["User"] == "65532:65532" and settings["WorkingDir"] == "/"
              and settings["Cmd"] == ["serve", "--state", "/state", "--listen", "127.0.0.1:8090"]
              and len(settings["Entrypoint"]) == 1
              and settings["Entrypoint"][0].endswith("/bin/intake")
              and "OTEL_SDK_DISABLED=true" in settings["Env"]
              and any(x.startswith("SSL_CERT_FILE=/nix/store/") for x in settings["Env"])
              and not any(x.startswith("LEDGER_ADMIN_KEY=") for x in settings["Env"])
              and config["created"] in {"1970-01-01T00:00:01Z", "1970-01-01T00:00:01+00:00"}
              and config["os"] == "linux", "image_archive")
    return "sha256:" + hashlib.sha256(raw).hexdigest(), config["architecture"]


def loaded_image_identity(config_identity):
    # Classic Docker uses the config ID; containerd uses a manifest-based ID.
    # Resolve the loaded tag only once, then verify/save/run the immutable ID.
    image = success(["docker", "image", "inspect", "possums-intake-local:qualification",
                     "--format", "{{.Id}}"], "image_archive").decode().strip()
    r.require(r.pin(image, r.DIGEST), "image_archive")
    with tempfile.TemporaryDirectory(prefix="intake-local-loaded-image-") as directory:
        saved = Path(directory) / "loaded.tar"
        success(["docker", "image", "save", "--output", str(saved), image], "image_archive")
        r.require(archive_identity(saved)[0] == config_identity, "image_archive")
    return image


class Fixture:
    def __init__(self, image, probe):
        self.image, self.probe = image, probe
        self.key = "synthetic-image-credential-" + uuid.uuid4().hex
        self.prefix = "intake-local-fixture-" + uuid.uuid4().hex
        self.containers = []
        self.volumes = []
        self.process = None

    def volume(self):
        name = self.prefix + "-state-" + str(len(self.volumes))
        self.volumes.append(name)
        success(["docker", "volume", "create", name])
        return name

    def flags(self, volume):
        return ["--pull", "never", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
                "--ulimit", "core=0", "--log-driver", "none", "--mount", "type=volume,src=" + volume + ",dst=/state",
                "--mount", "type=bind,src=" + self.probe + ",dst=/probe,readonly",
                "-e", "LEDGER_ADMIN_KEY=" + self.key, "-e", "OTEL_SDK_DISABLED=true"]

    def once(self, volume, mode, *, probe=False, failure=None, file_limit=None):
        name = self.prefix + "-once-" + str(len(self.containers))
        self.containers.append(name)
        args = ["docker", "run", "--rm", "--name", name, *self.flags(volume)]
        if file_limit is not None:
            args += ["--ulimit", "fsize=" + str(file_limit) + ":" + str(file_limit)]
        if probe:
            args += ["--entrypoint", "/probe", self.image, mode]
        else:
            args += [self.image, mode, "--state", "/state"]
        result = command(args)
        if failure:
            r.require(result.returncode == 1 and result.stdout == b"", "image_runtime")
            try:
                view = json.loads(result.stderr)
            except (TypeError, ValueError):
                raise r.Stop("image_privacy") from None
            r.require(failure in STARTUP_EXPECTED and view == {"code": failure, **STARTUP_EXPECTED[failure]}, "image_privacy")
        else:
            r.require(result.returncode == 0 and result.stderr == b"", "image_runtime")
            expected = {"passed": True, "stage": "synthetic_probe"} if probe else {"status": "initialized_empty"}
            try:
                r.require(json.loads(result.stdout) == expected, "image_runtime")
            except (ValueError, TypeError):
                raise r.Stop("image_runtime") from None

    def migration_failure(self):
        volume = self.volume()
        # Run the immutable image's production entrypoint, not a helper's copy
        # of the migration. The exec-only probe only inspects persisted evidence.
        self.once(volume, "init", failure="LEDGER_COMMIT_UNKNOWN", file_limit=MIGRATION_FILE_LIMIT)
        self.once(volume, "migration-rollback", probe=True)
        # Remove the fault constraint, preserving the exact original partial
        # volume: neither serve nor repeated init may recover/reset it.
        self.once(volume, "serve", failure="LEDGER_STARTUP_SCHEMA")
        self.once(volume, "init", failure="LEDGER_INIT_NONEMPTY")
        self.once(volume, "logs", probe=True)

    def start(self, volume):
        name = self.prefix + "-server-" + str(len(self.containers))
        self.containers.append(name)
        success(["docker", "create", "--name", name, *self.flags(volume), self.image])
        # Attach with daemon log driver NONE. Output is inspected in memory and
        # never dumped or persisted by this script, even on hostile errors.
        self.process = subprocess.Popen(["docker", "start", "-a", name], stdin=subprocess.DEVNULL,
                                        stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            result = command(["docker", "exec", name, "/probe", "health"])
            if result.returncode == 0:
                return name
            time.sleep(0.1)  # Passive health reads only, no mutation replay.
        raise r.Stop("image_runtime", outcome="unknown")

    def exec(self, name, mode):
        result = command(["docker", "exec", name, "/probe", mode])
        try:
            r.require(result.returncode == 0 and result.stderr == b""
                      and json.loads(result.stdout) == {"passed": True, "stage": "synthetic_probe"}, "image_runtime")
        except (ValueError, TypeError):
            raise r.Stop("image_runtime") from None

    def stop(self, name):
        success(["docker", "stop", "--time", "10", name])
        if self.process is None:
            raise r.Stop("image_runtime")
        try:
            out, err = self.process.communicate(timeout=15)
            r.require(self.process.returncode == 0 and out == b"" and err == b"", "image_privacy")
        except subprocess.TimeoutExpired:
            raise r.Stop("image_runtime", outcome="unknown") from None
        self.process = None

    def cleanup(self):
        # Fixture cleanup only, not production cancellation or rollback. Cleanup
        # does not replace an earlier observed failure. No state is uploaded.
        for name in self.containers:
            command(["docker", "rm", "-f", name])
        if self.process is not None:
            try:
                self.process.communicate(timeout=15)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.communicate()
        for name in self.volumes:
            command(["docker", "volume", "rm", name])


def qualify(archive, probe):
    r.require(shutil.which("docker") is not None and shutil.which("skopeo") is not None
              and Path(probe).is_file(), "image_tools")
    config_identity, architecture = archive_identity(archive)
    runtime = success(["docker", "info", "--format", "{{.OSType}}/{{.Architecture}}"], "image_tools").decode().strip()
    aliases = {"linux/aarch64": "arm64", "linux/arm64": "arm64", "linux/x86_64": "amd64", "linux/amd64": "amd64"}
    r.require(aliases.get(runtime) == architecture, "image_tools")
    raw = success(["skopeo", "inspect", "--raw", "docker-archive:" + str(Path(archive).resolve())], "image_archive")
    digest = "sha256:" + hashlib.sha256(raw).hexdigest()
    success(["docker", "load", "-i", str(archive)], "image_archive")
    image = loaded_image_identity(config_identity)
    with tempfile.TemporaryDirectory(prefix="intake-local-probe-") as directory:
        local_probe = Path(directory) / "probe"
        shutil.copyfile(probe, local_probe)
        local_probe.chmod(0o555)
        fixture = Fixture(image, str(local_probe.resolve()))
        try:
            volume = fixture.volume()
            fixture.once(volume, "serve", failure="LEDGER_STARTUP_MISSING_STATE")
            fixture.once(volume, "init")
            fixture.once(volume, "init", failure="LEDGER_INIT_NONEMPTY")
            server = fixture.start(volume)
            fixture.exec(server, "seed")
            fixture.exec(server, "balance")
            fixture.exec(server, "privacy")
            fixture.stop(server)
            fixture.once(volume, "logs", probe=True)
            server = fixture.start(volume)
            fixture.exec(server, "balance")
            fixture.stop(server)
            fixture.once(volume, "logs", probe=True)
            for mode in ("unsupported-schema", "schema-drift"):
                bad = fixture.volume()
                fixture.once(bad, "init")
                fixture.once(bad, mode, probe=True)
                fixture.once(bad, "serve", failure="LEDGER_STARTUP_SCHEMA")
            fixture.migration_failure()
        finally:
            fixture.cleanup()
    # Public artifact identities only, no fixture/control-plane/private state.
    try:
        with open(archive, "rb") as stream:
            archive_hash = hashlib.file_digest(stream, "sha256").hexdigest()
    except OSError:
        raise r.Stop("image_archive") from None
    cases = dict.fromkeys(r.QUALIFICATIONS, True)
    # Reached only after actual entrypoint init failure, persisted rollback,
    # and refusal of the same partial state. No previous release exists.
    return {"image": digest, "config_identity": config_identity, "docker_runtime_identity": image,
            "archive_sha256": archive_hash, "platform": "linux/" + architecture,
            "cases": cases, "previous_release_compatibility": "unknown"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive")
    parser.add_argument("probe", help="Generated image-smoke-probe/bin/nix")
    args = parser.parse_args()
    evidence = qualify(args.archive, args.probe)
    print(json.dumps(evidence, sort_keys=True))
    r.require(r.boolean(evidence["cases"]["migration_failure_refused"], True), "image_migration")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(json.dumps(r.diagnostic(error)))
        sys.exit(1)
