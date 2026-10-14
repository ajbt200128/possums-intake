#!/usr/bin/env python3
"""One owner-approved diagnostic tag in the fixed EXISTING PUBLIC package.

Not the first/private publisher, deployment, recovery or an immutable-tag protocol.
Complete authenticated version pages and fresh checks establish observed absence,
not atomic absence: the owner must keep every other registry writer quiet through
copy/postcheck. A GitHub 404 is inaccessible evidence, never absence/authority.
The public binary remains reverse-engineerable; the linked source stays private.
Only the existing three public image identities may become a private receipt.
PRIVACY.md and ../possums/PRIVACY.md apply; native host history is separate.
"""
import hashlib
import importlib.util
import json
import os
import sys
from pathlib import Path

import release as r

_spec = importlib.util.spec_from_file_location("synthetic_first_publication", Path(__file__).with_name("synthetic-publication.py"))
if _spec is None or _spec.loader is None:
    raise r.Stop("internal")
p = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(p)

WORKFLOW = "publish-synthetic-diagnostic-image.yml"
BASELINE = "sha256:e37d729571bd8ab82eb715a0e5ff97ab3e5a0d8c76b98c1baa1dfa27fb2d55ca"
STARTUP_CASES = {"healthy_healthcheck", "missing_key_capture", "invalid_operator_capture",
                 "signal_cancels_hold", "closed_failure_output"}
r.CODES.update({
    "diagnostic_approval": ("diagnostic_owner_approval", "private_owner_main_first_attempt_source_bound_quiet_confirmation",
        "Owner must confirm this exact source and fixed baseline, and keep every other registry writer stopped until postcheck. Underlying approval-read cause unknown; do not replay publication."),
    "diagnostic_evidence": ("diagnostic_github_evidence", "authenticated_exact_source_run_attempt_and_artifacts",
        "Authenticated source/CI/artifact evidence is unavailable; underlying cause unknown. Inspect privately; do not replay publication."),
    "diagnostic_package": ("diagnostic_public_package", "fixed_public_package_private_source_link_complete_versions_baseline_and_unused_source_tag",
        "Public package identity, complete version evidence or unused source tag is not established; underlying evidence-read cause unknown. Inspect authoritative state privately. A 404 is inaccessible, not absence; stop other writers."),
    "diagnostic_startup": ("diagnostic_archive", "exact_actual_image_startup_diagnostics_all_true",
        "Actual archive startup-diagnostic acceptance is unavailable; underlying cause unknown. Qualify the exact A image; source-only tests are insufficient."),
    "diagnostic_copy": ("diagnostic_registry_copy", "single_preserved_digest_nonreplayed_copy",
        "Copy outcome is unknown; underlying cause unknown. Inspect authoritative registry state privately. Never retry, overwrite, delete, change visibility/grants or roll back."),
    "diagnostic_public": ("diagnostic_post_publication", "same_public_package_private_source_link_and_exact_raw_registry_digest",
        "Copy completed, but public package/link/raw digest acceptance is unavailable; underlying cause unknown. Inspect privately before deployment; never replay or modify the package."),
    "diagnostic_used": ("diagnostic_publication", "single_attempt_session",
        "This session already attempted its one copy. Inspect authoritative state privately; no retry or rollback."),
    "diagnostic_internal": ("diagnostic_command", "closed_local_command_execution",
        "Underlying command cause unknown. Inspect privately; no diagnostic-triggered publication or retry is permitted."),
})


def context(env=None):
    env = os.environ if env is None else env
    def read():
        try:
            event = json.loads(p.read_file(Path(env["GITHUB_EVENT_PATH"])))
        except (Exception, KeyboardInterrupt):
            raise r.Stop("diagnostic_approval") from None
        inputs = event.get("inputs", {})
        source = env.get("GITHUB_SHA", "")
        r.require(r.pin(source, r.SHA)
                  and env.get("GITHUB_REPOSITORY") == p.REPO and env.get("GITHUB_REF") == "refs/heads/main"
                  and env.get("GITHUB_EVENT_NAME") == "workflow_dispatch" and env.get("GITHUB_ACTOR") == p.OWNER
                  and env.get("GITHUB_RUN_ATTEMPT") == "1" and event.get("sender", {}).get("login") == p.OWNER
                  and event.get("repository", {}).get("full_name") == p.REPO
                  and r.boolean(event.get("repository", {}).get("private"), True), "diagnostic_approval")
        r.require(type(inputs) is dict and set(inputs) == {"publish", "qualification_run", "qualification_attempt",
                  "expected_source", "known_baseline", "writers_quiet_confirmed", "quiet_confirmation_source"}, "diagnostic_approval")
        r.require(inputs["publish"] == "true", "disabled")
        r.require(inputs["expected_source"] == source and inputs["known_baseline"] == BASELINE
                  and inputs["writers_quiet_confirmed"] == "true"
                  and inputs["quiet_confirmation_source"] == source, "diagnostic_approval")
        return {"source": source, "run": p.positive(env.get("GITHUB_RUN_ID", "")),
                "qualification_run": p.positive(inputs["qualification_run"]),
                "qualification_attempt": p.positive(inputs["qualification_attempt"]), "baseline": BASELINE}
    return p.boundary("diagnostic_approval", read)


def admit(api, ctx):
    r.require(ctx.get("baseline") == BASELINE, "diagnostic_approval")
    return p.boundary("diagnostic_evidence", lambda: p.admit(api, ctx, workflow=WORKFLOW))


class GitHub(p.GitHub):
    def version_page(self, page):
        r.require(type(page) is int and 1 <= page <= 100, "diagnostic_package")
        return self.get("/users/" + p.OWNER + "/packages/container/" + p.PACKAGE
                        + f"/versions?state=active&per_page=100&page={page}", package=True)


def package_read(operation, code):
    # The inherited transport's package classification is first-publication-only.
    # Preserve observed HTTP status, but author the correct public-stage category.
    try:
        return operation()
    except r.Stop as error:
        raise r.Stop(code, status=error.status) from None
    except (Exception, KeyboardInterrupt):
        raise r.Stop(code) from None


def package_identity(api, code):
    status, package = package_read(api.package, code)
    if status != 200:
        raise r.Stop(code, status=status)
    repo = package_read(api.repo, code)
    r.require(isinstance(package, dict)
              and type(package.get("id")) is int and package["id"] > 0
              and package.get("name") == p.PACKAGE and package.get("package_type") == "container"
              and package.get("owner", {}).get("login") == p.OWNER and package.get("visibility") == "public"
              and type(repo.get("id")) is int and repo["id"] > 0 and repo.get("full_name") == p.REPO
              and repo.get("owner", {}).get("login") == p.OWNER and r.boolean(repo.get("private"), True)
              and repo.get("default_branch") == "main"
              and type(package.get("repository", {}).get("id")) is int
              and package["repository"]["id"] == repo["id"]
              and package["repository"].get("full_name") == p.REPO
              and r.boolean(package["repository"].get("private"), True), code)
    return {"package": package["id"], "repository": repo["id"]}


def public_gate(api, ctx):
    """Bounded complete active pages; never early-exit on finding the baseline."""
    code = "diagnostic_package"
    r.require(ctx.get("baseline") == BASELINE and r.pin(ctx.get("source"), r.SHA), code)
    identity = package_identity(api, code)
    seen, baseline = set(), False
    for page in range(1, 101):
        status, batch = package_read(lambda page=page: api.version_page(page), code)
        if status != 200:
            raise r.Stop(code, status=status)
        r.require(type(batch) is list and len(batch) <= 100, code)
        for version in batch:
            if not isinstance(version, dict):
                raise r.Stop(code)
            r.require(type(version.get("id")) is int and version["id"] > 0
                      and version["id"] not in seen and r.pin(version.get("name"), r.DIGEST), code)
            seen.add(version["id"])
            tags = version.get("metadata", {}).get("container", {}).get("tags")
            r.require(type(tags) is list and all(type(tag) is str and 1 <= len(tag) <= 128 for tag in tags)
                      and len(tags) == len(set(tags)) and ctx["source"] not in tags, code)
            baseline = baseline or version["name"] == BASELINE
        if len(batch) < 100:
            r.require(baseline, code)
            return identity
    raise r.Stop(code)  # Cap reached: incomplete evidence, not a truncated success.


def archive_gate(root, admitted, tool=p.command):
    receipt = p.archive_gate(root, admitted, tool)
    def startup():
        try:
            evidence = json.loads(p.read_file(Path(root) / "a/synthetic-qualification.json"))
        except r.Stop:
            raise
        except (Exception, KeyboardInterrupt):
            raise r.Stop("diagnostic_startup") from None
        cases = evidence.get("startup_diagnostics")
        r.require(type(cases) is dict and set(cases) == STARTUP_CASES
                  and all(r.boolean(value, True) for value in cases.values()), "diagnostic_startup")
    p.boundary("diagnostic_startup", startup)
    return receipt


def post_gate(api, identity, receipt, authfile, tool=p.command):
    r.require(package_identity(api, "diagnostic_public") == identity, "diagnostic_public")
    raw = tool(["skopeo", "inspect", "--authfile", str(authfile), "--retry-times", "0", "--raw",
                "docker://" + p.REGISTRY + ":" + receipt["publicSourceSHA"]], "diagnostic_public")
    r.require("sha256:" + hashlib.sha256(raw).hexdigest() == receipt["imageDigest"], "diagnostic_public")


class Publication:
    """Explicit PUBLIC executor. The original Publication/context stay FIRST-only."""
    def __init__(self, api, ctx, root, downloaded, tool=p.command, auth=None, context_reader=context):
        self.api, self.ctx, self.root, self.downloaded = api, ctx, Path(root), downloaded
        self.tool, self.auth = tool, auth if auth is not None else p.Authfile()
        self.context_reader = context_reader
        self.used, self.outcome = False, "no_effect"

    def execute(self):
        if self.used:
            raise p.PublicationStop("diagnostic_used", outcome=self.outcome)
        primary = None
        try:
            r.require(p.boundary("diagnostic_approval", self.context_reader) == self.ctx, "diagnostic_approval")
            admitted = admit(self.api, self.ctx)
            r.require(admitted["artifacts"] == self.downloaded, "diagnostic_evidence")
            receipt = p.boundary("synthetic_archive", lambda: archive_gate(self.root, admitted, self.tool))
            identity = p.boundary("diagnostic_package", lambda: public_gate(self.api, self.ctx))
            authfile = p.boundary("synthetic_auth", self.auth.prepare)
            # Re-read source-bound dispatch, exact current main/CI/attempt/artifacts,
            # archive bytes, and fully paginated public/tag state before ONE copy.
            r.require(p.boundary("diagnostic_approval", self.context_reader) == self.ctx, "diagnostic_approval")
            refreshed = admit(self.api, self.ctx)
            r.require(refreshed == admitted, "diagnostic_evidence")
            r.require(p.boundary("synthetic_archive", lambda: archive_gate(self.root, refreshed, self.tool)) == receipt,
                      "synthetic_archive")
            r.require(p.boundary("diagnostic_package", lambda: public_gate(self.api, self.ctx)) == identity,
                      "diagnostic_package")
            r.require(p.boundary("diagnostic_evidence", lambda: self.api.repo("/git/ref/heads/main"))["object"]["sha"]
                      == self.ctx["source"], "stale")
            self.used, self.outcome = True, "unknown"  # Before crossing side-effect boundary.
            p.boundary("diagnostic_copy", lambda: self.tool(["skopeo", "copy", "--preserve-digests", "--retry-times", "0",
                "--authfile", str(authfile), "docker-archive:" + str((self.root / "archive/image.tar.gz").resolve()),
                "docker://" + p.REGISTRY + ":" + receipt["publicSourceSHA"]], "diagnostic_copy", timeout=600))
            self.outcome = "completed"  # COPY completed, independently of acceptance/presentation.
            p.boundary("diagnostic_public", lambda: post_gate(self.api, identity, receipt, authfile, self.tool))
            return receipt
        except (Exception, KeyboardInterrupt) as error:
            primary = p.PublicationStop(error.code, status=error.status, outcome=self.outcome) if isinstance(error, r.Stop) else p.PublicationStop("diagnostic_evidence", outcome=self.outcome)
            raise primary from None
        finally:
            try:
                self.auth.cleanup()  # Shared command joins tool before auth removal.
            except (Exception, KeyboardInterrupt):
                if primary is None:
                    raise r.Stop("synthetic_cleanup", outcome=self.outcome) from None
                primary.cleanup_failed = True


def main(args=None):
    args = sys.argv[1:] if args is None else args
    if args in (["status"], ["report"]):
        print(json.dumps({"stage": "source_only", "publication": "not_observed", "runtime_verified": False}))
    elif args == ["admit"]:
        ctx = context()
        admitted = admit(GitHub(), ctx)
        p.boundary("diagnostic_package", lambda: public_gate(GitHub(), ctx))
        p.outputs(admitted)  # Only admitted artifact IDs; never dispatch-supplied IDs.
    elif args == ["publish"]:
        ctx = context()
        downloaded = p.boundary("diagnostic_evidence", lambda: {
            key: p.positive(os.environ["ACCEPTED_" + key.upper() + "_ARTIFACT_ID"]) for key in ("a", "b", "archive")})
        receipt = Publication(GitHub(), ctx, Path("accepted"), downloaded).execute()
        try:
            Path("published-receipt.json").write_text(json.dumps(receipt, sort_keys=True, separators=(",", ":")) + "\n")
            print(json.dumps(receipt, sort_keys=True, separators=(",", ":")))
        except (Exception, KeyboardInterrupt):
            raise r.Stop("synthetic_receipt", outcome="completed") from None
    else:
        raise r.Stop("arguments")


def cli():
    try:
        main()
    except (Exception, KeyboardInterrupt) as error:
        if not isinstance(error, r.Stop):
            error = r.Stop("diagnostic_internal")
        print(json.dumps(p.diagnostic(error)))
        sys.exit(1)


if __name__ == "__main__":
    cli()
