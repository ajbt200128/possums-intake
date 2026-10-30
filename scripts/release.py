#!/usr/bin/env python3
"""Content-free delivery gates. Read-only live source/provenance commands;
publication is an injected boundary, exercised offline, not a live adapter.
No retry, tag creation, environment setup, or mutation in reporting commands.
"""
import base64
import hashlib
import json
import os
import re
import selectors
import subprocess
import sys
import time
from pathlib import Path

CONFIG = "nix/release-config.yml"  # Deliberately absent until operator selects policy.
PREDICATE = "https://tinfoil.sh/predicate/snp-tdx-multiplatform/v1"
SHA = r"[0-9a-f]{40}"
DIGEST = r"sha256:[0-9a-f]{64}"
VERSION = r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
CODES = {
    "arguments": ("input", "validated_public_pins", "Check documented arguments; no operation was attempted."),
    "disabled": ("activation", "separate_explicit_activation", "Delivery is disabled. Obtain separate authorization; do not provision anything from this report."),
    "prerequisites": ("configuration", "selected_operator_policy_and_adapter", "Approve identities, original volume/key delivery and a supported adapter before activation."),
    "event": ("source", "trusted_current_main_event", "Use this repository's completed main check or current-main manual invocation."),
    "checks": ("source", "exact_successful_ci", "Complete check.yml on the exact source and ref; PR checks do not authorize release."),
    "stale": ("eligibility", "exact_current_main", "Use the current main source. No mutation was attempted by this check."),
    "build": ("image", "independent_fresh_identical_builds", "Resolve independent A/B digest or fresh-build evidence before publication."),
    "qualification": ("image", "exact_archive_runtime_qualification", "Qualify this generated archive's readiness, persistence and privacy; source tests alone are insufficient."),
    "relation": ("release", "immutable_source_tag_config_binding", "Resolve the immutable tag and single config-only release transform; never repoint a tag."),
    "manifest": ("provenance", "exact_manifest_config_image_binding", "Resolve the measured manifest and configuration binding before proceeding."),
    "signature": ("provenance", "cryptographically_verified_tagged_signer", "Require supported cryptographic verification of the exact tagged publisher and predicate."),
    "invocation": ("provenance", "successful_exact_signed_attempt", "Require the actual successful signed invocation and matching build attempt, not the latest rerun."),
    "partial": ("publication", "first_nonexisting_publication", "Reconcile prior publication privately. Never replay or overwrite an uncertain publication."),
    "publication_unknown": ("publication", "one_nonreplayed_publication", "Publication outcome is unknown. Inspect authoritative state privately; no retry or rollback."),
    "transport": ("evidence_transport", "bounded_successful_read", "Evidence could not be read; underlying cause unknown. Check permissions/availability privately; evidence reads never replay mutations."),
    "internal": ("validation", "completed_local_validation", "Underlying validation cause unknown. Inspect privately; raw diagnostics were suppressed."),
}


class Stop(Exception):
    def __init__(self, code, status=None, outcome="no_effect"):
        self.code = code if code in CODES else "internal"
        self.status = status if type(status) is int and 100 <= status <= 599 else None
        self.outcome = outcome if outcome in {"no_effect", "unknown", "completed"} else "unknown"


def require(condition, code):
    if not condition:
        raise Stop(code)


def diagnostic(error):
    if not isinstance(error, Stop):
        error = Stop("internal")
    stage, constraint, next_step = CODES[error.code]
    value: dict[str, object] = {"code": "DELIVERY_" + error.code.upper(), "stage": stage,
                                "constraint": constraint, "outcome": error.outcome, "next_step": next_step}
    if error.status is not None:
        value["status"] = error.status
    return value


def boolean(value, expected):
    return type(value) is bool and value == expected


def pin(value, pattern):
    return isinstance(value, str) and re.fullmatch(pattern, value) is not None


def pins(source, release=None, tag=None, image=None):
    require(pin(source, SHA), "arguments")
    for value, pattern in ((release, SHA), (tag, VERSION), (image, DIGEST)):
        if value is not None:
            require(pin(value, pattern), "arguments")


def validate_run(run, repo, workflow, sha, branch, events):
    require(run.get("repository", {}).get("full_name") == repo
            and run.get("head_repository", {}).get("full_name") == repo
            and run.get("path") == ".github/workflows/" + workflow
            and run.get("head_sha") == sha and run.get("head_branch") == branch
            and run.get("event") in events, "event")
    require(run.get("status") == "completed" and run.get("conclusion") == "success", "checks")


def source_gate(view, repo, source):
    pins(source)
    require(pin(repo, r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+"), "prerequisites")
    require(view.get("repository") == repo and view.get("ref") == "refs/heads/main"
            and view.get("event") in {"workflow_dispatch", "workflow_run"}, "event")
    if view["event"] == "workflow_run":
        require(view.get("action") == "completed", "event")
        validate_run(view["trigger"], repo, "check.yml", source, "main", {"push"})
    else:
        require(view.get("context_sha") == source, "event")
    require(view.get("main") == source, "stale")
    eligible = False
    for run in view.get("checks", []):
        try:
            validate_run(run, repo, "check.yml", source, "main", {"push", "workflow_dispatch"})
            eligible = True
        except Stop:
            pass
    require(eligible, "checks")


def transform(config, repository, image):
    pins("0" * 40, image=image)
    require(pin(repository, r"ghcr\.io/[a-z0-9_.-]+/[a-z0-9_.-]+"), "prerequisites")
    pattern = rb"(?m)^    image: " + re.escape(repository.encode()) + rb"@sha256:[0-9a-f]{64}$"
    output, count = re.subn(pattern, b"    image: " + repository.encode() + b"@" + image.encode(), config)
    require(count == 1, "relation")
    return output


QUALIFICATIONS = {"missing_state_refused", "explicit_empty_init", "read_only_nonroot",
                  "ready_original_state", "restart_persistent", "unsupported_schema_refused",
                  "migration_failure_refused", "schema_drift_refused", "telemetry_disabled", "no_raw_logs"}


NATIVE_CASES = {"actual_entrypoint", "native_login_create_patch", "shared_key_separation", "held_balance_stock_edit",
                "sdk_logging_forced_off", "empty_logs_auth_origins_stdio", "frozen_asset_http_hashes"}


def build_gate(evidence, source, image):
    builds = evidence["builds"]
    require(set(builds) == {"a", "b"}, "build")
    require(builds["a"]["runner"] != builds["b"]["runner"], "build")
    for build in builds.values():
        require(build["source"] == source and build["image"] == image
                and boolean(build["success"], True) and boolean(build["fresh"], True)
                and boolean(build["substitutes"], False) and boolean(build["remote_builders"], False)
                and build["platform"] == "x86_64-linux", "build")
    q = evidence["qualification"]
    require(q["image"] == image and q["archive_sha256"] == builds["a"]["archive_sha256"]
            and pin(q["archive_sha256"], r"[0-9a-f]{64}")
            and set(q["cases"]) == QUALIFICATIONS
            and all(boolean(value, True) for value in q["cases"].values()), "qualification")
    native = q.get("native_operator")
    require(type(native) is dict and set(native) == NATIVE_CASES | {"browser_inside_image"}
            and all(boolean(native[key], True) for key in NATIVE_CASES)
            and native["browser_inside_image"] == "not_run", "qualification")
    # No prior released ledger exists. This must remain unknown, not a false pass.
    require(q.get("previous_release_compatibility") == "unknown", "qualification")


def relation(evidence, source, release, tag, image, registry):
    require(evidence["tag_commit"] == release and evidence["parent"] == source
            and evidence["source_tree_without_config"] == evidence["release_tree_without_config"]
            and evidence["config_modes"] == ["100644", "100644"], "relation")
    source_config = base64.b64decode(evidence["source_config"], validate=True)
    config = base64.b64decode(evidence["release_config"], validate=True)
    require(config == transform(source_config, registry, image), "relation")
    require(evidence["source_image_drv"] == evidence["release_image_drv"]
            and evidence["source_image_drv"].startswith("/nix/store/"), "build")
    return config


def manifest_gate(raw, expected_digest, config, image, registry):
    require(pin(expected_digest, r"[0-9a-f]{64}")
            and hashlib.sha256(raw).hexdigest() == expected_digest, "manifest")
    try:
        manifest = json.loads(raw)
    except (ValueError, TypeError):
        raise Stop("manifest") from None
    require(base64.b64decode(manifest["config"], validate=True) == config
            and transform(config, registry, image) == config, "manifest")
    require("tinfoil-config-hash=" + hashlib.sha256(config).hexdigest() in manifest["cmdline"].split(), "manifest")
    shape = manifest["vm_shape"]
    # Persistent disk shape must be selected/qualified separately. Do not import
    # the gateway's disk count. CPU/RAM/GPU are the selected public minimum.
    require(shape.get("cpus") == 2 and type(shape["cpus"]) is int
            and shape.get("memory_mb") == 8192 and type(shape["memory_mb"]) is int
            and shape.get("gpus") == 0 and type(shape["gpus"]) is int, "manifest")
    return manifest


def statement_gate(checked, manifest, digest, repo, release, tag):
    # checked MUST be output of supported gh attestation verify (or fake port
    # in offline tests), never an unverified downloaded signature statement.
    require(len(checked) == 1, "signature")
    verified = checked[0]["verificationResult"]
    statement, cert = verified["statement"], verified["signature"]["certificate"]
    require(statement["predicateType"] == PREDICATE and statement["predicate"] == manifest
            and len(statement["subject"]) == 1
            and statement["subject"][0]["digest"] == {"sha256": digest}, "signature")
    require(cert["subjectAlternativeName"] == f"https://github.com/{repo}/.github/workflows/tinfoil-release-publish.yml@refs/tags/{tag}"
            and cert["buildSignerDigest"] == cert["sourceRepositoryDigest"] == release
            and cert["sourceRepositoryRef"] == "refs/tags/" + tag
            and cert["runnerEnvironment"] == "github-hosted", "signature")
    match = re.fullmatch(r"https://github\.com/" + re.escape(repo)
                        + r"/actions/runs/([1-9][0-9]*)/attempts/([1-9][0-9]*)", cert["runInvocationURI"])
    require(match is not None, "invocation")
    return match.groups()


def candidate_gate(port, candidate):
    """Port reads authenticated evidence; this function never publishes."""
    source, release, tag, image, digest = (candidate[k] for k in ("source", "release", "tag", "image", "manifest"))
    repo, registry = candidate["repository"], candidate["registry"]
    pins(source, release, tag, image)
    try:
        evidence = port.evidence(candidate)
    except Stop:
        raise
    except Exception:
        raise Stop("transport") from None
    try:
        source_gate(evidence["source"], repo, source)
    except Stop:
        raise
    except Exception:
        raise Stop("event") from None
    try:
        build_gate(evidence, source, image)
    except Stop:
        raise
    except Exception:
        raise Stop("build") from None
    try:
        config = relation(evidence["git"], source, release, tag, image, registry)
    except Stop:
        raise
    except Exception:
        raise Stop("relation") from None
    try:
        raw = port.manifest(candidate)
        manifest = manifest_gate(raw, digest, config, image, registry)
    except Stop:
        raise
    except Exception:
        raise Stop("manifest") from None
    try:
        checked = port.verify_attestation(candidate, raw)
        run, attempt = statement_gate(checked, manifest, digest, repo, release, tag)
    except Stop:
        raise
    except Exception:
        raise Stop("signature") from None
    try:
        invocation = evidence["publisher"]
        validate_run(invocation, repo, "tinfoil-release-publish.yml", release, tag, {"workflow_dispatch"})
        require(str(invocation["id"]) == run and str(invocation["run_attempt"]) == attempt
                and invocation["build_run"] == evidence["build_run"]
                and invocation["build_attempt"] == evidence["build_attempt"], "invocation")
    except Stop:
        raise
    except Exception:
        raise Stop("invocation") from None
    return {"passed": True, "stage": "public_provenance", "runtime_verified": False}


class Publication:
    """Offline-tested single-attempt session; no implemented live port."""
    def __init__(self, port, candidate, enabled=False):
        self.port, self.candidate, self.enabled = port, candidate, enabled
        self.used = False

    def execute(self):
        require(boolean(self.enabled, True), "disabled")
        require(not self.used and self.port.publication_attempt() == 1, "partial")
        require(boolean(self.port.publication_absent(self.candidate), True), "partial")
        # Rechecks main, tag/config, fresh A/B and exact archive qualification.
        self.port.publication_gate(self.candidate)
        self.used = True
        try:
            receipt = self.port.publish(self.candidate)  # One call; never replay.
        except Exception:
            raise Stop("publication_unknown", outcome="unknown") from None
        published = isinstance(receipt, dict) and set(receipt) == {"published"} and boolean(receipt["published"], True)
        try:
            require(published, "publication_unknown")
            return candidate_gate(self.port, self.candidate)
        except Exception as error:
            if isinstance(error, Stop):
                error.outcome = "completed" if published else "unknown"
                raise
            raise Stop("publication_unknown", outcome="completed" if published else "unknown") from None


def read_command(args, timeout=120):
    # Read-only commands only. Suppress child output on failure. Do not persist
    # raw control-plane data or pass admin credentials to provenance verification.
    env = {key: os.environ[key] for key in ("PATH", "HOME", "SSL_CERT_FILE", "GH_TOKEN") if key in os.environ}
    try:
        with subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                              stderr=subprocess.PIPE, env=env) as process:
            try:
                buffers = [bytearray(), bytearray()]
                deadline = time.monotonic() + timeout
                with selectors.DefaultSelector() as selector:
                    for index, stream in enumerate((process.stdout, process.stderr)):
                        if stream is None:
                            raise Stop("transport")
                        selector.register(stream, selectors.EVENT_READ, index)
                    while selector.get_map():
                        require(time.monotonic() < deadline, "transport")
                        for key, _ in selector.select(timeout=0.1):
                            chunk = os.read(key.fd, 65536)
                            if not chunk:
                                selector.unregister(key.fileobj)
                                continue
                            buffers[key.data].extend(chunk)
                            require(len(buffers[key.data]) <= 8 << 20, "transport")
                require(process.wait(timeout=max(0.01, deadline - time.monotonic())) == 0, "transport")
                return bytes(buffers[0])
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait()
    except (OSError, subprocess.TimeoutExpired):
        raise Stop("transport") from None


def api(repo, path):
    require(pin(repo, r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+"), "prerequisites")
    try:
        return json.loads(read_command(["gh", "api", f"repos/{repo}/{path}"]))
    except (ValueError, TypeError):
        raise Stop("transport") from None


def live_source():
    require(os.environ.get("RELEASE_AUTOMATION_ENABLED") == "true", "disabled")
    repo = os.environ.get("DELIVERY_REPOSITORY", "")
    require(repo == os.environ.get("GITHUB_REPOSITORY") and bool(repo), "prerequisites")
    try:
        event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_bytes())
    except (OSError, ValueError, TypeError, KeyError):
        raise Stop("event") from None
    kind = os.environ["GITHUB_EVENT_NAME"]
    source = event.get("workflow_run", {}).get("head_sha") if kind == "workflow_run" else os.environ["GITHUB_SHA"]
    pins(source)
    view = {"repository": event["repository"]["full_name"], "event": kind,
            "ref": os.environ["GITHUB_REF"], "context_sha": os.environ["GITHUB_SHA"], "action": event.get("action"),
            "main": api(repo, "git/ref/heads/main")["object"]["sha"],
            "checks": api(repo, f"actions/workflows/check.yml/runs?head_sha={source}&per_page=100")["workflow_runs"]}
    if kind == "workflow_run":
        run_id = event["workflow_run"].get("id")
        require(type(run_id) is int and run_id > 0, "event")
        view["trigger"] = api(repo, f"actions/runs/{run_id}")
    source_gate(view, repo, source)
    try:
        with open(os.environ["GITHUB_OUTPUT"], "a") as output:
            output.write("source=" + source + "\n")
    except (OSError, KeyError):
        raise Stop("transport") from None


def verify_file(path, repo, release, tag, *, command=None):
    pins("0" * 40, release, tag)
    require(pin(repo, r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+"), "prerequisites")
    identity = f"https://github.com/{repo}/.github/workflows/tinfoil-release-publish.yml@refs/tags/{tag}"
    try:
        return json.loads((read_command if command is None else command)(["gh", "attestation", "verify", path, "--repo", repo,
            "--cert-identity", identity, "--source-ref", "refs/tags/" + tag, "--source-digest", release,
            "--signer-digest", release, "--deny-self-hosted-runners", "--predicate-type", PREDICATE, "--format", "json"]))
    except (ValueError, TypeError):
        raise Stop("signature") from None


def main():
    args = sys.argv[1:]
    if args == ["source"]:
        live_source()
    elif len(args) == 5 and args[0] == "verify-signature":
        verify_file(*args[1:])
        print(json.dumps({"passed": True, "stage": "signature_only", "runtime_verified": False}))
    elif args in (["prepare"], ["publication-guard"], ["dispatch-deploy"]):
        require(os.environ.get("RELEASE_AUTOMATION_ENABLED") == "true", "disabled")
        raise Stop("prerequisites")
    elif args == ["status"]:
        print(json.dumps({"release_enabled": False, "deploy_enabled": False, "live_adapter": False,
                          "stage": "local_artifacts", "runtime_verified": False}))
    else:
        raise Stop("arguments")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(json.dumps(diagnostic(error)))
        sys.exit(1)
