#!/usr/bin/env python3
"""Explicit read-only host acceptance; never publication or resource authority.

Only the supported native gh SDK verifies cryptography. Internal fake ports are
for offline tests, not CLI evidence. Public temporary bytes and native SDK/Nix/
host history have their normal persistence boundaries; no diagnostic log/cache.
"""
import hashlib
import importlib.util
import json
import re
import shutil
import sys
import tempfile
from contextlib import suppress
from pathlib import Path
from typing import Any

import release as r
import smoke

# Reuse the reviewed identity, rendering, relation and provenance contracts.
_spec = importlib.util.spec_from_file_location("acceptance_signing", Path(__file__).with_name("public-tinfoil-release.py"))
if _spec is None or _spec.loader is None:
    raise r.Stop("internal")
s = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(s)
c, p = s.c, s.p
o = s.load("acceptance_native_owner", "owner-synthetic-publication.py")
FLAGS = {"--sourceSHA": "source", "--releaseSHA": "release", "--tag": "tag", "--signing-run": "run",
         "--checks-run": "checks_run", "--checks-attempt": "checks_attempt",
         "--release-checks-run": "release_checks_run", "--release-checks-attempt": "release_checks_attempt",
         "--qualification-run": "qualification_run", "--qualification-attempt": "qualification_attempt",
         "--image-digest": "image", "--archive-sha256": "archive", "--storage-phase": "phase",
         "--a-artifact-id": "a", "--b-artifact-id": "b", "--archive-artifact-id": "archive_id"}
CONSTRAINTS = {
    "input": "exact_explicit_public_pins_and_distinct_original_artifact_ids",
    "auth": "native_saved_owner_identity_without_token_introspection",
    "read": "bounded_fixed_github_get_with_observed_http_status",
    "start": "native_sdk_child_start_available",
    "deadline": "native_sdk_read_only_command_within_deadline_after_join",
    "reader": "native_sdk_output_readers_successfully_joined",
    "acquisition": "native_sdk_output_within_64k_per_stream",
    "decode": "native_sdk_output_decodes_as_json",
    "run": "exact_completed_success_owner_tag_signing_attempt_one",
    "prior": "complete_inventory_only_selected_signing_invocation",
    "repository": "fixed_independent_public_repository_and_package",
    "refs": "current_main_and_lightweight_tag_equal_selected_release",
    "checks": "separate_exact_successful_main_source_and_release_checks",
    "artifacts": "original_ids_exact_source_qualification_distinct_hosted_runners",
    "release": "published_nondraft_nonprerelease_selected_tag",
    "assets": "complete_unique_uploaded_standard_assets_and_bounded_sizes",
    "download": "actual_asset_sizes_and_canonical_hash_of_manifest_bytes",
    "manifest": "fixed_template_config_vendor_pins_and_four_disk_shape",
    "signature": "supported_sdk_verified_predicate_subject_and_tagged_certificate",
    "invocation": "verified_selected_signing_invocation_attempt_one",
    "recheck": "unchanged_authenticated_evidence_after_verification",
    "workspace": "owned_private_public_manifest_workspace",
    "cleanup": "joined_child_before_owned_public_workspace_removal",
    "join": "child_join_unconfirmed_owned_public_material_retained",
    "output": "closed_receipt_delivery_preserves_observed_evidence",
}
for _name, _constraint in CONSTRAINTS.items():
    r.CODES["acceptance_" + _name] = ("public_release_acceptance_" + _name, _constraint,
        "Acceptance is unavailable at this stage; underlying cause is unknown. Inspect the selected evidence privately. "
        "This read-only command neither publishes nor authorizes resource creation; do not replay mutations.")


class AcceptanceStop(r.Stop):
    secondary: r.Stop | None = None
    observed: dict


SMOKE_FAILURES = {
    "child_start_unavailable": ("acceptance_start", True),
    "child_deadline_unknown_outcome": ("acceptance_deadline", True),
    "child_join_unconfirmed": ("acceptance_join", False),
    "output_reader_joined": ("acceptance_reader", False),
    "output_within_64k": ("acceptance_acquisition", False),
}
NATIVE_FAILURES = {code for code, _joined in SMOKE_FAILURES.values()} | {"acceptance_decode"}


def stage(name, operation):
    try:
        return operation()
    except r.Stop as error:
        if error.code in NATIVE_FAILURES:
            raise
        raise r.Stop("acceptance_" + name, status=error.status) from None
    except (Exception, KeyboardInterrupt):
        raise r.Stop("acceptance_" + name) from None


def inventory(api, suffix, field=None):
    # Bypass the signing transport projection, not pagination/schema checks.
    return s.inventory(api, suffix, field, read=api.repo)


def validate(ctx, artifacts):
    s.validate_context(ctx)
    r.require(type(ctx["phase"]) is str
              and type(artifacts) is dict and set(artifacts) == {"a", "b", "archive"}
              and all(type(v) is int and 0 < v < 10 ** 20 for v in artifacts.values())
              and len(set(artifacts.values())) == 3
              and ctx["checks_run"] != ctx["release_checks_run"], "acceptance_input")


def parse(args):
    r.require(type(args) is list and all(type(v) is str for v in args)
              and len(args) == 1 + 2 * len(FLAGS) and args[0] == "accept", "acceptance_input")
    values = {}
    for flag, value in zip(args[1::2], args[2::2], strict=True):
        r.require(flag in FLAGS and FLAGS[flag] not in values, "acceptance_input")
        key = FLAGS[flag]
        values[key] = s.positive(value) if key in s.NUMBERS | {"a", "b", "archive_id"} else value
    artifacts = {"a": values.pop("a"), "b": values.pop("b"), "archive": values.pop("archive_id")}
    validate(values, artifacts)
    return values, artifacts


def receipt():
    return {"read_only": True, "publication_attempted": False, "live_activation_observed": False,
            "successful_workflow_observed": False, "release_observed": False,
            "cryptographically_verified": False, "accepted_release_observed": False,
            "runtime_verified": False, "resource_creation_authorized": False}


class Native:
    """Thin fixed native SDK GET/verification port; no tokens or HTTP client."""
    def __init__(self):
        self.env = stage("auth", o.native_env)
        stage("read", lambda: o.require_native_tool(o.NATIVE_GH))
        self.joined = True

    def execute(self, args):
        self.joined = False
        try:
            result = smoke.run_bounded([str(o.NATIVE_GH), *args], self.env, "public_release_acceptance", 120)
        except smoke.SmokeError as error:
            code, self.joined = SMOKE_FAILURES.get(error.constraint, ("acceptance_read", False))
            raise r.Stop(code) from None
        self.joined = True
        return result

    def get(self, path) -> Any:
        # No URL/host projection from metadata. All suffixes come from typed pins
        # and the reused gates; reject anything outside their fixed GET grammar.
        number = r"[1-9][0-9]{0,19}"
        suffix = (r"(?:/git/ref/(?:heads/main|tags/" + r.VERSION + r")"
                  r"|/actions/runs/" + number + r"(?:/attempts/" + number + r"(?:/jobs)?|/artifacts)?"
                  r"|/actions/workflows/" + re.escape(s.WORKFLOW) + r"/runs"
                  r"|/releases(?:/tags/" + r.VERSION + r"|/" + number + r"(?:/assets)?)?)?")
        base, sep, query = path.partition("?")
        allowed = base in {"user", f"users/{c.OWNER}/packages/container/{c.PACKAGE}"} or re.fullmatch(
            "repos/" + re.escape(c.REPOSITORY) + suffix, base) is not None
        r.require(allowed and (not sep or re.fullmatch(
            r"(?:(?:head_sha=" + r.SHA + r"|branch=" + r.VERSION + r")&)?per_page=100&page=[1-9][0-9]{0,2}", query)),
            "acceptance_read")
        rc, raw, _ = self.execute(["api", "--method", "GET", path, "--include",
                                   "-H", "Accept: application/vnd.github+json"])
        # --include supplies the actually observed HTTP status even for rc != 0.
        header, separator, body = raw.partition(b"\r\n\r\n")
        if not separator:
            header, separator, body = raw.partition(b"\n\n")
        line = header.splitlines()[0] if header else b""
        match = re.fullmatch(rb"HTTP/[0-9.]+ ([1-5][0-9]{2})(?: [^\r\n]*)?", line)
        if not separator or match is None:
            raise r.Stop("acceptance_read")
        try:
            status = int(match[1])
        except ValueError:
            raise r.Stop("acceptance_read") from None
        if rc != 0 or status != 200:
            raise r.Stop("acceptance_read", status=status)
        try:
            return json.loads(body)
        except (ValueError, TypeError):
            raise r.Stop("acceptance_decode", status=status) from None
        except (Exception, KeyboardInterrupt):
            raise r.Stop("acceptance_read", status=status) from None

    def identity(self):
        return self.get("user")

    def repo(self, suffix=""):
        return self.get("repos/" + c.REPOSITORY + suffix)

    def package(self):
        return 200, self.get(f"users/{c.OWNER}/packages/container/{c.PACKAGE}")

    def pages(self, suffix, field):
        return inventory(self, suffix, field)

    def asset(self, number):
        r.require(type(number) is int and 0 < number < 10 ** 20, "acceptance_download")
        rc, raw, _ = self.execute(["api", "--method", "GET", f"repos/{c.REPOSITORY}/releases/assets/{number}",
                                   "-H", "Accept: application/octet-stream"])
        # SDK redirect handling is native. Never infer an HTTP status from rc or
        # upstream text; the octet stream deliberately contains no headers.
        r.require(rc == 0, "acceptance_download")
        return raw

    def verify(self, args):
        r.require(args[:3] == ["gh", "attestation", "verify"], "acceptance_signature")
        rc, raw, _ = self.execute(args[1:])
        r.require(rc == 0, "acceptance_signature")
        # Preserve SDK decoding separately from the shared verifier's legacy fallback.
        try:
            json.loads(raw)
        except (ValueError, TypeError):
            raise r.Stop("acceptance_decode") from None
        return raw


def signing_run(value, ctx):
    r.validate_run(value, c.REPOSITORY, s.WORKFLOW, ctx["release"], ctx["tag"], {"workflow_dispatch"})
    r.require(type(value.get("id")) is int and value["id"] == ctx["run"]
              and type(value.get("run_attempt")) is int and value["run_attempt"] == 1
              and value.get("actor", {}).get("login") == c.OWNER
              and value.get("triggering_actor", {}).get("login") == c.OWNER, "acceptance_run")
    # Ignore irrelevant URLs and projections, not authority fields.
    return {key: value[key] for key in ("id", "run_attempt", "path", "head_sha", "head_branch", "event",
            "status", "conclusion", "repository", "head_repository", "actor", "triggering_actor")}


def evidence(api, ctx, artifacts, state):
    def repository():
        repo = api.repo()
        status, package = api.package()
        if status != 200:
            raise r.Stop("acceptance_repository", status=status)
        c.repository_gate(repo, package)
        return repo
    repo = stage("repository", repository)
    runs = []
    for suffix in (f"/actions/runs/{ctx['run']}", f"/actions/runs/{ctx['run']}/attempts/1"):
        runs.append(stage("run", lambda suffix=suffix: signing_run(api.repo(suffix), ctx)))
    for query in ("head_sha=" + ctx["release"], "branch=" + ctx["tag"]):
        def history(query=query):
            values = inventory(api, "/actions/workflows/" + s.WORKFLOW + "/runs?" + query, "workflow_runs")
            r.require(len(values) == 1, "acceptance_prior")
            return signing_run(values[0], ctx)
        runs.append(stage("prior", history))
    state["successful_workflow_observed"] = True  # Not itself signing-step/crypto evidence.
    refs = []
    for suffix, ref in (("heads/main", "refs/heads/main"), ("tags/" + ctx["tag"], "refs/tags/" + ctx["tag"])):
        def read_ref(suffix=suffix, ref=ref):
            value = api.repo("/git/ref/" + suffix)
            r.require(value.get("ref") == ref and value.get("object", {}).get("type") == "commit"
                      and value["object"].get("sha") == ctx["release"], "acceptance_refs")
            return (value["ref"], value["object"]["type"], value["object"]["sha"])
        refs.append(stage("refs", read_ref))
    for label in ("source", "release"):
        check_ctx = {"source": ctx[label], "checks_run": ctx["checks_run" if label == "source" else "release_checks_run"],
                     "checks_attempt": ctx["checks_attempt" if label == "source" else "release_checks_attempt"]}
        checks = stage("checks", lambda check_ctx=check_ctx: p.qualified_checks(
            api, check_ctx, propagate=NATIVE_FAILURES | {"acceptance_read"}))
        r.require(len(checks) == 1, "acceptance_checks")
    admitted = stage("artifacts", lambda: p.qualified_artifacts(api, ctx, repo))
    r.require(admitted["artifacts"] == artifacts, "acceptance_artifacts")
    def release():
        value: Any = api.repo("/releases/tags/" + ctx["tag"])
        number = value.get("id") if type(value) is dict else None
        r.require(type(value) is dict and type(number) is int and 0 < number < 10 ** 20
                  and value.get("tag_name") == ctx["tag"] and value.get("draft") is False
                  and value.get("prerelease") is False
                  and r.pin(value.get("published_at"), r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z"),
                  "acceptance_release")
        # Official action omits --target. target_commitish is NOT tag authority.
        return {key: value[key] for key in ("id", "tag_name", "draft", "prerelease", "published_at")}
    selected = stage("release", release)
    state["release_observed"] = True
    def assets():
        values = inventory(api, f"/releases/{selected['id']}/assets")
        r.require(len(values) == 2, "acceptance_assets")
        result = {}
        for value in values:
            name, size = value.get("name"), value.get("size")
            limit = {"tinfoil-deployment.json": c.MANIFEST_LIMIT, "tinfoil.hash": 128}.get(name, 0)
            r.require(name not in result and limit > 0 and value.get("state") == "uploaded"
                      and type(size) is int and 0 < size <= limit
                      and value["id"] < 10 ** 20, "acceptance_assets")
            result[name] = {key: value[key] for key in ("id", "name", "size", "state")}
        return result
    return {"repository_id": repo["id"], "runs": runs, "refs": refs, "qualified": admitted,
            "release": selected, "assets": stage("assets", assets)}


def accept(ctx, artifacts, root, *, api=None, tool=p.command, command=None, state=None):
    """Internal ports are synthetic-test seams, never command-line evidence."""
    stage("input", lambda: validate(ctx, artifacts))  # Before native auth/files/tools.
    state = receipt() if state is None else state
    directory, primary = None, None
    try:
        if api is None:
            api = Native()
            command = api.verify
        r.require(callable(command), "acceptance_input")
        def identity():
            user = api.identity()
            r.require(type(user) is dict and user.get("login") == c.OWNER, "acceptance_auth")
        stage("auth", identity)
        before = evidence(api, ctx, artifacts, state)
        s.relation_gate(ctx, Path(root), tool)
        raw = stage("download", lambda: api.asset(before["assets"]["tinfoil-deployment.json"]["id"]))
        hashfile = stage("download", lambda: api.asset(before["assets"]["tinfoil.hash"]["id"]))
        def download():
            r.require(type(raw) is bytes and type(hashfile) is bytes
                      and len(raw) == before["assets"]["tinfoil-deployment.json"]["size"]
                      and len(hashfile) == before["assets"]["tinfoil.hash"]["size"], "acceptance_download")
            digest = hashlib.sha256(raw).hexdigest()
            r.require(hashfile == digest.encode() + b"\n", "acceptance_download")
            return digest
        digest = stage("download", download)
        def manifest():
            template = s.regular(Path(root), c.TEMPLATE, "acceptance_manifest")
            r.require(template.stat().st_size <= 8192, "acceptance_manifest")
            template = template.read_bytes()
            config = c.render(template, ctx["image"], phase=ctx["phase"])
            return c.manifest_gate(raw, config, template=template, image=ctx["image"], phase=ctx["phase"])
        manifest = stage("manifest", manifest)
        def workspace():
            nonlocal directory
            directory = Path(tempfile.mkdtemp(prefix="intake-public-acceptance-", dir="/tmp"))
            directory.chmod(0o700)
            path = directory / "tinfoil-deployment.json"
            with path.open("xb") as stream:
                path.chmod(0o600)
                stream.write(raw)
            return path
        path = stage("workspace", workspace)
        checked = stage("signature", lambda: r.verify_file(str(path), c.REPOSITORY, ctx["release"], ctx["tag"], command=command))
        def signature():
            invocation = r.statement_gate(checked, manifest, digest, c.REPOSITORY, ctx["release"], ctx["tag"])
            r.require(checked[0]["verificationResult"]["statement"]["subject"][0].get("name") == "tinfoil-deployment.json",
                      "acceptance_signature")
            return invocation
        run, attempt = stage("signature", signature)
        r.require((run, attempt) == (str(ctx["run"]), "1"), "acceptance_invocation")
        state["cryptographically_verified"] = True
        after = stage("recheck", lambda: evidence(api, ctx, artifacts, state))
        r.require(before == after, "acceptance_recheck")
        state["accepted_release_observed"] = True
    except (Exception, KeyboardInterrupt) as error:
        primary = AcceptanceStop(error.code, status=error.status) if isinstance(error, r.Stop) else AcceptanceStop("acceptance_read")
        if getattr(error, "cleanup_failed", False) is True:
            primary.secondary = r.Stop("signing_cleanup")
    finally:
        if directory is not None:
            cleanup = None
            if getattr(api, "joined", False) is not True:
                cleanup = r.Stop("acceptance_join")
            else:
                try:
                    stage("cleanup", lambda directory=directory: shutil.rmtree(directory))
                except r.Stop as error:
                    cleanup = error
            if cleanup:
                if primary is None:
                    primary = AcceptanceStop(cleanup.code, status=cleanup.status)
                else:
                    primary.secondary = cleanup
    if primary:
        primary.observed = dict(state)
        raise primary from None
    return {**state, "passed": True, "stage": "public_release_acceptance"}


def diagnostic(error, state):
    value = {**r.diagnostic(error), **state}
    if isinstance(error, AcceptanceStop) and error.secondary is not None:
        value["secondary"] = [r.diagnostic(error.secondary)]
    return value


def main(args=None):
    args = sys.argv[1:] if args is None else args
    state, primary = receipt(), None
    try:
        if args == ["status"]:
            result = {**state, "stage": "offline_public_release_acceptance_status"}
        else:
            ctx, artifacts = stage("input", lambda: parse(args))
            result = accept(ctx, artifacts, Path(__file__).resolve().parents[1], state=state)
        text = json.dumps(result, sort_keys=True) + "\n"
    except (Exception, KeyboardInterrupt) as error:
        primary = error if isinstance(error, r.Stop) else r.Stop("acceptance_input")
        text = json.dumps(diagnostic(primary, state), sort_keys=True) + "\n"
    try:
        c.emit(sys.stdout, text)
    except r.Stop:
        with suppress(r.Stop):
            c.emit(sys.stderr, json.dumps(diagnostic(primary or r.Stop("acceptance_output"), state), sort_keys=True) + "\n")
        return 1
    return 1 if primary else 0


if __name__ == "__main__":
    sys.exit(main())
