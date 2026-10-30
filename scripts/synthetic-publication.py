#!/usr/bin/env python3
"""One owner-approved FIRST publication; never an update or recovery tool.

Authority: the owner freshly checks authenticated owner/read:packages inventory
outside CI, then dispatches with source-bound confirmation and keeps the namespace
quiet until copy completes. CI's package 404 is NOT independent absence evidence.
Concurrency serializes only this workflow, not other operators/registry writers.
GitHub documents new first-push packages as private and GITHUB_TOKEN auto-linking;
these defaults are NOT observed visibility. Actual private/link/digest checks follow
copy. A hidden pre-existing unlinked package is not authorized by confirmation and
GitHub's token permission contract forbids pushing it. Operator/platform honesty
and the quiet window remain trust boundaries, not atomic name-creation guarantees.

PRIVACY.md forbidden-collection/export boundaries and ../possums/PRIVACY.md apply.
Only public image identities enter the short-retention private receipt. No raw
exceptions, private API inventory, credentials or tool output are retained. Native
Actions/agent history is a separate host persistence boundary. No measurement,
signing, release, deployment, SDK error-coverage or runtime-verification claim.
"""
import base64
import hashlib
import json
import os
import selectors
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

import release as r

OWNER = "ajbt200128"
REPO = OWNER + "/possums-intake"
PACKAGE = "possums-intake-synthetic"
REGISTRY = "ghcr.io/" + OWNER + "/" + PACKAGE
WORKFLOW = "publish-synthetic-image.yml"
MAX_READ = 8 << 20
SYNTHETIC_CASES = {"wildcard_listener_reachable", "native_operator", "empty_auth_origins_and_logs",
                   "fresh_state_after_restart", "attached_stdio_empty", "telemetry_disabled"}
NATIVE_CASES = r.NATIVE_CASES
r.CODES.update({
    "synthetic_approval": ("owner_approval", "private_owner_main_first_attempt_source_bound_confirmation", "Owner must freshly confirm unused namespace from authenticated full inventory for this exact source and keep all other writers stopped; no CI absence proof is claimed."),
    "synthetic_evidence": ("github_evidence", "bounded_authenticated_exact_run_attempt_artifacts", "Required authenticated evidence is unavailable; underlying cause unknown. Check the selected run/attempt privately; do not replay publication."),
    "synthetic_namespace": ("first_publication", "no_visible_existing_package_and_trusted_owner_quiet_window", "A visible package or unavailable package-state response refuses initial publication. Reconcile privately; existing packages are never updated by this tool."),
    "synthetic_archive": ("accepted_archive", "matching_native_amd64_archive_manifest_config_and_runtime_cases", "Acceptance is unavailable; underlying cause unknown. Requalify exact A/B native images and the actual A archive; source tests or replacement images are insufficient."),
    "synthetic_copy": ("registry_copy", "single_preserved_digest_nonreplayed_copy", "Copy outcome is unknown; underlying cause unknown. Inspect authoritative package state privately. Never retry, delete, change visibility or roll back."),
    "synthetic_private": ("post_publication", "observed_private_auto_link_and_raw_registry_digest", "Copy completed, but private package/link/digest acceptance is unavailable. Inspect privately before measuring or deploying; do not replay or modify the package."),
    "synthetic_auth": ("registry_authentication", "temporary_private_authfile_outside_checkout", "Registry authentication preparation is unavailable; underlying cause unknown. Inspect local permissions privately; credentials were not printed."),
    "synthetic_cleanup": ("registry_auth_cleanup", "joined_tool_and_owned_authfile_removal", "Owned authentication-file cleanup is unconfirmed; underlying cause unknown. Remove only the owned local temporary material privately; do not replay publication."),
    "synthetic_receipt": ("publication_receipt", "closed_public_image_identities_only", "Copy completed but receipt presentation is unavailable; underlying cause unknown. Inspect authoritative state privately; do not replay publication."),
})


def boundary(code, operation):
    try:
        return operation()
    except r.Stop:
        raise
    except (Exception, KeyboardInterrupt):
        raise r.Stop(code) from None


def positive(value):
    r.require(r.pin(value, r"[1-9][0-9]{0,19}"), "arguments")
    return int(value)


def command(args, code, timeout=120):
    """Bound both streams while running, kill AND join on limits; never echo them."""
    env = {key: os.environ[key] for key in ("PATH", "SSL_CERT_FILE") if key in os.environ}
    try:
        with subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                              stderr=subprocess.PIPE, env=env) as process:
            try:
                buffers = [bytearray(), bytearray()]
                deadline = time.monotonic() + timeout
                with selectors.DefaultSelector() as selector:
                    for index, stream in enumerate((process.stdout, process.stderr)):
                        if stream is None:
                            raise r.Stop(code)
                        selector.register(stream, selectors.EVENT_READ, index)
                    while selector.get_map():
                        r.require(time.monotonic() < deadline, code)
                        for key, _ in selector.select(timeout=0.1):
                            chunk = os.read(key.fd, 65536)
                            if not chunk:
                                selector.unregister(key.fileobj)
                                continue
                            buffers[key.data].extend(chunk)
                            r.require(len(buffers[key.data]) <= MAX_READ, code)
                r.require(process.wait(timeout=max(0.01, deadline - time.monotonic())) == 0, code)
                return bytes(buffers[0])
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait()
    except r.Stop:
        raise
    except (Exception, KeyboardInterrupt):
        raise r.Stop(code) from None


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class GitHub:
    """Fixed GitHub TLS GETs only; no registry bearer protocol or API mutation."""
    def get(self, path, *, package=False) -> tuple[int, Any]:
        code = "synthetic_namespace" if package else "synthetic_evidence"
        r.require(path.startswith(("/repos/" + REPO, "/users/" + OWNER + "/packages/container/" + PACKAGE))
                  and "\n" not in path, code)
        token = os.environ.get("GITHUB_TOKEN", "")
        r.require(bool(token), code)
        request = urllib.request.Request("https://api.github.com" + path, headers={
            "Authorization": "Bearer " + token, "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "intake-first-publication"})
        try:
            with urllib.request.build_opener(NoRedirect()).open(request, timeout=30) as response:
                status = response.status
                raw = response.read(MAX_READ + 1)
                r.require(status == 200 and len(raw) <= MAX_READ, code)
                return status, boundary(code, lambda: json.loads(raw))
        except urllib.error.HTTPError as error:
            status = error.code
            error.close()
            if package and status == 404:
                return status, None  # Inaccessible; NEVER an absence assertion.
            raise r.Stop(code, status=status) from None
        except r.Stop:
            raise
        except Exception:
            raise r.Stop(code) from None

    def repo(self, suffix=""):
        return self.get("/repos/" + REPO + suffix)[1]

    def pages(self, suffix, field):
        values = []
        for page in range(1, 101):
            data = self.repo(suffix + ("&" if "?" in suffix else "?") + f"per_page=100&page={page}")
            batch = data[field]
            r.require(isinstance(batch, list) and len(batch) <= 100, "synthetic_evidence")
            values.extend(batch)
            if len(batch) < 100:
                return values
        raise r.Stop("synthetic_evidence")  # Never silently truncate evidence.

    def package(self):
        return self.get("/users/" + OWNER + "/packages/container/" + PACKAGE, package=True)


def context(env=None):
    env = os.environ if env is None else env
    try:
        path = Path(env["GITHUB_EVENT_PATH"])
        r.require(path.stat().st_size <= MAX_READ, "synthetic_approval")
        event = json.loads(path.read_bytes())
    except r.Stop:
        raise
    except Exception:
        raise r.Stop("synthetic_approval") from None
    inputs = event.get("inputs", {})
    source = env.get("GITHUB_SHA", "")
    r.pins(source)
    r.require(env.get("GITHUB_REPOSITORY") == REPO and env.get("GITHUB_REF") == "refs/heads/main"
              and env.get("GITHUB_EVENT_NAME") == "workflow_dispatch" and env.get("GITHUB_ACTOR") == OWNER
              and env.get("GITHUB_RUN_ATTEMPT") == "1" and event.get("sender", {}).get("login") == OWNER
              and event.get("repository", {}).get("full_name") == REPO
              and r.boolean(event.get("repository", {}).get("private"), True), "synthetic_approval")
    r.require(inputs.get("publish") == "true", "disabled")
    r.require(inputs.get("namespace_unused_confirmed") == "true"
              and inputs.get("namespace_confirmation_source") == source, "synthetic_approval")
    return {"source": source, "run": positive(env.get("GITHUB_RUN_ID", "")),
            "qualification_run": positive(inputs.get("qualification_run", "")),
            "qualification_attempt": positive(inputs.get("qualification_attempt", ""))}


def run_gate(run, workflow, source, attempt, events):
    r.validate_run(run, REPO, workflow, source, "main", events)
    r.require(type(run.get("id")) is int and run["id"] > 0
              and type(run.get("run_attempt")) is int and run["run_attempt"] == attempt, "synthetic_evidence")


def step_gate(job, names):
    r.require(job.get("status") == "completed" and job.get("conclusion") == "success", "build")
    for name in names:
        matches = [step for step in job.get("steps", []) if step.get("name") == name]
        r.require(len(matches) == 1 and matches[0].get("status") == "completed"
                  and matches[0].get("conclusion") == "success", "build")


def repository_gate(api):
    repo = api.repo()
    r.require(type(repo.get("id")) is int and repo["id"] > 0
              and repo.get("full_name") == REPO and r.boolean(repo.get("private"), True)
              and repo.get("owner", {}).get("login") == OWNER and repo.get("default_branch") == "main", "synthetic_approval")
    return repo


def admit(api, ctx, workflow=WORKFLOW):
    """CI authority stays separate from reusable authenticated qualification."""
    source = ctx["source"]
    repo = repository_gate(api)
    current = api.repo(f"/actions/runs/{ctx['run']}")
    r.require(current.get("id") == ctx["run"] and type(current.get("run_attempt")) is int
              and current["run_attempt"] == 1
              and current.get("path") == ".github/workflows/" + workflow
              and current.get("head_sha") == source and current.get("head_branch") == "main"
              and current.get("event") == "workflow_dispatch" and current.get("status") == "in_progress"
              and current.get("repository", {}).get("full_name") == REPO
              and current.get("head_repository", {}).get("full_name") == REPO
              and current.get("actor", {}).get("login") == OWNER
              and current.get("triggering_actor", {}).get("login") == OWNER, "synthetic_approval")
    return admit_qualified(api, ctx, repo)


def qualified_checks(api, ctx, *, propagate=()):
    """Read exact successful checks separately from current-main admission."""
    source = ctx["source"]
    if "checks_run" in ctx or "checks_attempt" in ctx:
        r.require(all(type(ctx.get(key)) is int and ctx[key] > 0 for key in ("checks_run", "checks_attempt")),
                  "arguments")
        check = api.repo(f"/actions/runs/{ctx['checks_run']}")
        run_gate(check, "check.yml", source, ctx["checks_attempt"], {"push", "workflow_dispatch"})
        r.require(check["id"] == ctx["checks_run"], "synthetic_evidence")
        checks = [check]
    else:
        checks = api.pages(f"/actions/workflows/check.yml/runs?branch=main&head_sha={source}&status=completed", "workflow_runs")
    eligible = []
    for check in checks:
        try:
            run_gate(check, "check.yml", source, check.get("run_attempt"), {"push", "workflow_dispatch"})
            r.require(type(check.get("run_attempt")) is int and check["run_attempt"] > 0, "checks")
            observed = api.repo(f"/actions/runs/{check['id']}/attempts/{check['run_attempt']}")
            run_gate(observed, "check.yml", source, check["run_attempt"], {"push", "workflow_dispatch"})
            r.require(observed["id"] == check["id"], "synthetic_evidence")
            eligible.append(observed)
        except r.Stop as error:
            # Adapter transport evidence must not become an ineligible-check result.
            if error.code == "synthetic_evidence" or error.code in propagate:
                raise
    return eligible


def admit_qualified(api, ctx, repo):
    """Caller supplies checked authority/repo; native callers pin exact checks."""
    source = ctx["source"]
    eligible = qualified_checks(api, ctx)
    view = {"repository": REPO, "ref": "refs/heads/main", "event": "workflow_dispatch",
            "context_sha": source, "main": api.repo("/git/ref/heads/main")["object"]["sha"], "checks": eligible}
    r.source_gate(view, REPO, source)
    return qualified_artifacts(api, ctx, repo)


def qualified_artifacts(api, ctx, repo):
    """Read source-owned native artifacts; caller must admit source and authority."""
    source = ctx["source"]
    qrun, attempt = ctx["qualification_run"], ctx["qualification_attempt"]
    q = api.repo(f"/actions/runs/{qrun}")
    run_gate(q, "qualify-synthetic-image.yml", source, attempt, {"workflow_dispatch"})
    r.require(q["id"] == qrun, "synthetic_evidence")
    exact = api.repo(f"/actions/runs/{qrun}/attempts/{attempt}")
    run_gate(exact, "qualify-synthetic-image.yml", source, attempt, {"workflow_dispatch"})
    r.require(exact["id"] == qrun, "synthetic_evidence")
    jobs = api.pages(f"/actions/runs/{qrun}/attempts/{attempt}/jobs", "jobs")
    builders = {}
    for builder in ("a", "b"):
        matches = [job for job in jobs if job.get("name") == f"build ({builder})"]
        r.require(len(matches) == 1, "build")
        job = matches[0]
        r.require(job.get("head_sha") == source and job.get("run_id") == qrun
                  and job.get("run_attempt") == attempt and type(job.get("runner_id")) is int
                  and job["runner_id"] > 0 and "ubuntu-latest" in job.get("labels", [])
                  and "self-hosted" not in job.get("labels", []), "build")
        steps = ["Prewarm only external pinned dependencies", "Refuse valid application and image-stage outputs",
                 "Independently build native Linux image", "Record immutable archive and manifest identities"]
        if builder == "a":
            steps.append("Qualify this exact A image, including disposable startup")
        step_gate(job, steps)
        builders[builder] = job["runner_id"]
    r.require(builders["a"] != builders["b"], "build")
    for name in ("source", "compare"):
        matches = [job for job in jobs if job.get("name") == name]
        r.require(len(matches) == 1, "build")
        step_gate(matches[0], ["Admit successful exact-current-main checks"] if name == "source"
                  else ["Require independent matching manifests"])
    artifacts = api.pages(f"/actions/runs/{qrun}/artifacts", "artifacts")
    ids = {}
    for key, name in (("a", f"intake-synthetic-{qrun}-{attempt}-{source}-a"),
                      ("b", f"intake-synthetic-{qrun}-{attempt}-{source}-b"),
                      ("archive", f"intake-synthetic-archive-{qrun}-{attempt}-{source}")):
        matches = [artifact for artifact in artifacts if artifact.get("name") == name]
        r.require(len(matches) == 1, "synthetic_evidence")
        item = matches[0]
        binding = item.get("workflow_run", {})
        r.require(type(item.get("id")) is int and item["id"] > 0 and r.boolean(item.get("expired"), False)
                  and binding.get("id") == qrun and binding.get("head_sha") == source
                  and binding.get("head_branch") == "main" and binding.get("repository_id") == repo.get("id")
                  and binding.get("head_repository_id") == repo.get("id"), "synthetic_evidence")
        ids[key] = item["id"]
    r.require(len(set(ids.values())) == 3, "synthetic_evidence")
    return {"source": source, "qualification_run": qrun, "qualification_attempt": attempt,
            "artifacts": ids, "runners": builders}


def read_file(path):
    r.require(path.is_file() and not path.is_symlink() and path.stat().st_size <= MAX_READ, "synthetic_archive")
    return path.read_bytes()


def archive_gate(root, admitted, tool=command):
    root = Path(root)
    a, b = root / "a", root / "b"
    archive = root / "archive/image.tar.gz"
    r.require(archive.is_file() and not archive.is_symlink() and archive.stat().st_size <= 1 << 30, "synthetic_archive")
    with archive.open("rb") as stream:
        archive_sha = hashlib.file_digest(stream, "sha256").hexdigest()
    digests = [read_file(folder / "digest").decode().strip() for folder in (a, b)]
    hashes = [read_file(folder / "archive-sha256").decode().strip() for folder in (a, b)]
    r.require(r.pin(digests[0], r.DIGEST) and digests[0] == digests[1]
              and hashes == [archive_sha, archive_sha], "synthetic_archive")
    r.require(tool(["skopeo", "--version"], "synthetic_archive").strip() == b"skopeo version 1.24.1", "synthetic_archive")
    raw = tool(["skopeo", "inspect", "--raw", "docker-archive:" + str(archive.resolve())], "synthetic_archive")
    config_raw = tool(["skopeo", "inspect", "--config", "--raw", "docker-archive:" + str(archive.resolve())], "synthetic_archive")
    try:
        manifest, config = json.loads(raw), json.loads(config_raw)
    except Exception:
        raise r.Stop("synthetic_archive") from None
    config_digest = "sha256:" + hashlib.sha256(config_raw).hexdigest()
    r.require("sha256:" + hashlib.sha256(raw).hexdigest() == digests[0]
              and manifest.get("schemaVersion") == 2
              and manifest.get("mediaType") == "application/vnd.docker.distribution.manifest.v2+json"
              and manifest.get("config", {}).get("digest") == config_digest
              and config.get("os") == "linux" and config.get("architecture") == "amd64", "synthetic_archive")
    try:
        q = json.loads(read_file(a / "qualification.json"))
        synthetic = json.loads(read_file(a / "synthetic-qualification.json"))
    except r.Stop:
        raise
    except Exception:
        raise r.Stop("synthetic_archive") from None
    builds = {key: {"runner": admitted["runners"][key], "source": admitted["source"], "image": digests[0],
                    "success": True, "fresh": True, "substitutes": False, "remote_builders": False,
                    "platform": "x86_64-linux", "archive_sha256": archive_sha} for key in ("a", "b")}
    r.build_gate({"builds": builds, "qualification": q}, admitted["source"], digests[0])
    for evidence in (q, synthetic):
        r.require(evidence.get("config_identity") == config_digest and evidence.get("archive_sha256") == archive_sha
                  and evidence.get("platform") == "linux/amd64"
                  and evidence.get("previous_release_compatibility") == "unknown", "synthetic_archive")
    r.require(set(synthetic.get("synthetic", {})) == SYNTHETIC_CASES
              and all(r.boolean(value, True) for value in synthetic["synthetic"].values()), "synthetic_archive")
    return {"publicSourceSHA": admitted["source"], "imageDigest": digests[0], "archiveSHA": archive_sha}


def namespace_gate(api):
    status, value = api.package()
    # An actual visible package refuses even if private/empty/matching/linked.
    # 404 means only inaccessible to this app; owner approval supplies authority.
    r.require(status == 404 and value is None, "synthetic_namespace")


def private_gate(api, receipt, authfile, tool=command):
    try:
        status, package = api.package()
    except r.Stop as error:
        raise r.Stop("synthetic_private", status=error.status) from None
    r.require(status == 200 and package.get("name") == PACKAGE and package.get("package_type") == "container"
              and package.get("owner", {}).get("login") == OWNER and package.get("visibility") == "private"
              and package.get("repository", {}).get("full_name") == REPO
              and r.boolean(package.get("repository", {}).get("private"), True), "synthetic_private")
    raw = tool(["skopeo", "inspect", "--authfile", str(authfile), "--retry-times", "0", "--raw",
                "docker://" + REGISTRY + ":" + receipt["publicSourceSHA"]], "synthetic_private")
    r.require("sha256:" + hashlib.sha256(raw).hexdigest() == receipt["imageDigest"], "synthetic_private")


class Authfile:
    def __init__(self):
        self.directory = None
        self.path = None

    def prepare(self):
        token, actor = os.environ.get("GITHUB_TOKEN", ""), os.environ.get("GITHUB_ACTOR", "")
        r.require(bool(token) and actor == OWNER, "synthetic_auth")
        # Fixed system temp, never checkout/Git or artifact tree. No inherited TMPDIR.
        self.directory = Path(tempfile.mkdtemp(prefix="intake-registry-auth-", dir="/tmp"))
        self.directory.chmod(0o700)
        root = Path(__file__).resolve().parents[1]
        r.require(root not in self.directory.resolve().parents, "synthetic_auth")
        self.path = self.directory / "auth.json"
        data = {"auths": {"ghcr.io": {"auth": base64.b64encode((actor + ":" + token).encode()).decode()}}}
        try:
            with open(self.path, "x", opener=lambda path, flags: os.open(path, flags, 0o600)) as stream:
                json.dump(data, stream)
        except Exception:
            raise r.Stop("synthetic_auth") from None
        r.require(self.directory.stat().st_mode & 0o777 == 0o700 and self.path.stat().st_mode & 0o777 == 0o600,
                  "synthetic_auth")
        return self.path

    def cleanup(self):
        directory = self.directory
        if directory is not None:
            try:
                shutil.rmtree(directory)
            except (Exception, KeyboardInterrupt):
                raise r.Stop("synthetic_cleanup") from None
            self.directory = None
            self.path = None


class PublicationStop(r.Stop):
    """Closed primary plus one independently observed auth-cleanup flag."""
    def __init__(self, code, status=None, outcome="no_effect"):
        super().__init__(code, status=status, outcome=outcome)
        self.cleanup_failed = False


def diagnostic(error):
    value = r.diagnostic(error)
    if isinstance(error, PublicationStop) and r.boolean(error.cleanup_failed, True):
        value["secondary"] = [r.diagnostic(r.Stop("synthetic_cleanup", outcome=error.outcome))]
    return value


class Publication:
    def __init__(self, api, ctx, root, downloaded, tool=command, auth=None, context_reader=context):
        self.api, self.ctx, self.root, self.tool = api, ctx, Path(root), tool
        self.downloaded = downloaded
        self.auth = auth if auth is not None else Authfile()
        self.context_reader = context_reader
        self.used = False
        self.outcome = "no_effect"

    def execute(self):
        r.require(not self.used, "partial")
        primary = None
        try:
            r.require(self.context_reader() == self.ctx, "synthetic_approval")
            admitted = boundary("synthetic_evidence", lambda: admit(self.api, self.ctx))
            r.require(admitted["artifacts"] == self.downloaded, "synthetic_evidence")
            receipt = boundary("synthetic_archive", lambda: archive_gate(self.root, admitted, self.tool))
            boundary("synthetic_namespace", lambda: namespace_gate(self.api))
            authfile = boundary("synthetic_auth", self.auth.prepare)
            # Re-read dispatch approval, current-main, successful exact CI/run,
            # attempt/jobs/artifact identities and package state immediately before
            # ONE copy. Local archive is rehashed too; nothing is rebuilt/replaced.
            r.require(self.context_reader() == self.ctx, "synthetic_approval")
            refreshed = boundary("synthetic_evidence", lambda: admit(self.api, self.ctx))
            r.require(refreshed == admitted, "synthetic_evidence")
            r.require(boundary("synthetic_archive", lambda: archive_gate(self.root, refreshed, self.tool)) == receipt,
                      "synthetic_archive")
            boundary("synthetic_namespace", lambda: namespace_gate(self.api))
            r.require(boundary("synthetic_evidence", lambda: self.api.repo("/git/ref/heads/main"))["object"]["sha"]
                      == self.ctx["source"], "stale")
            self.used = True
            self.outcome = "unknown"
            # No retries, delete, tag probes, visibility/grant writes or recovery.
            boundary("synthetic_copy", lambda: self.tool(["skopeo", "copy", "--preserve-digests", "--retry-times", "0",
                      "--authfile", str(authfile), "docker-archive:" + str((self.root / "archive/image.tar.gz").resolve()),
                      "docker://" + REGISTRY + ":" + receipt["publicSourceSHA"]], "synthetic_copy", timeout=600))
            self.outcome = "completed"  # Successful COPY, even if later verification fails.
            boundary("synthetic_private", lambda: private_gate(self.api, receipt, authfile, self.tool))
            return receipt
        except (Exception, KeyboardInterrupt) as error:
            primary = PublicationStop(error.code, status=error.status, outcome=self.outcome) if isinstance(error, r.Stop) else PublicationStop("synthetic_evidence", outcome=self.outcome)
            raise primary from None
        finally:
            try:
                self.auth.cleanup()  # Tool is joined before any credential removal.
            except (Exception, KeyboardInterrupt):
                if primary is None:
                    raise r.Stop("synthetic_cleanup", outcome=self.outcome) from None
                primary.cleanup_failed = True


def outputs(admitted):
    values = {"source": admitted["source"], "qualification_run": admitted["qualification_run"],
              "a": admitted["artifacts"]["a"], "b": admitted["artifacts"]["b"], "archive": admitted["artifacts"]["archive"]}
    try:
        with open(os.environ["GITHUB_OUTPUT"], "a") as stream:
            for name, value in values.items():
                stream.write(f"{name}={value}\n")
    except Exception:
        raise r.Stop("synthetic_evidence") from None


def main(args=None):
    args = sys.argv[1:] if args is None else args
    if args in (["status"], ["report"]):
        print(json.dumps({"stage": "source_only", "publication": "not_observed", "runtime_verified": False}))
    elif args == ["admit"]:
        ctx = boundary("synthetic_approval", context)
        boundary("synthetic_evidence", lambda: outputs(admit(GitHub(), ctx)))
    elif args == ["publish"]:
        ctx = boundary("synthetic_approval", context)
        downloaded = boundary("synthetic_evidence", lambda: {
            key: positive(os.environ["ACCEPTED_" + key.upper() + "_ARTIFACT_ID"]) for key in ("a", "b", "archive")})
        receipt = Publication(GitHub(), ctx, Path("accepted"), downloaded).execute()
        try:
            # Only these three public identities; no raw/private evidence files.
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
        print(json.dumps(diagnostic(error)))
        sys.exit(1)


if __name__ == "__main__":
    cli()
