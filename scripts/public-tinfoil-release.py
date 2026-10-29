#!/usr/bin/env python3
"""Read-only, explicitly selected public signing admission. Never a publisher.

The pinned official workflow action alone publishes. This guard neither creates
nor moves tags/releases/images, deploys, verifies future signatures, nor retries.
Public Git snapshots are temporary host persistence, not ledger/credential state.
"""
import importlib.util
import json
import os
import shutil
import sys
import tempfile
from contextlib import suppress
from pathlib import Path, PurePosixPath
from typing import Any

import release as r


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    if spec is None or spec.loader is None:
        raise r.Stop("internal")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


c = load("intake_signing_contract", "public-release.py")
d = load("intake_signing_archive", "synthetic-diagnostic-publication.py")
p = d.p
WORKFLOW = "tinfoil-release-publish.yml"
CONFIG = "tinfoil-config.yml"
PAGE_CAP = 100
INPUTS = {"source", "checks_run", "checks_attempt", "release_checks_run", "release_checks_attempt",
          "qualification_run", "qualification_attempt", "image_digest", "archive_sha256", "storage_phase"}
NUMBERS = {"run", "checks_run", "checks_attempt", "release_checks_run", "release_checks_attempt",
           "qualification_run", "qualification_attempt"}
r.CODES.update({
    "signing_context": ("public_signing_context", "enabled_public_owner_tag_dispatch_first_attempt",
        "Select the enabled public owner tag dispatch with exact source, checks and qualification pins. Underlying context-read cause is unknown; no publication was attempted."),
    "signing_current": ("public_signing_invocation", "exact_in_progress_owner_tag_first_attempt",
        "Require this exact in-progress tagged owner invocation, attempt one. Underlying evidence cause is unknown; reconcile prior signing before any publication."),
    "signing_repository": ("public_signing_repository", "authenticated_fixed_public_repository_and_package",
        "Require authenticated fixed public repository and independent public package metadata. Underlying read cause is unknown; a 404 is inaccessible evidence, not absence."),
    "signing_source": ("public_signing_source", "release_current_main_and_lightweight_tag",
        "Require the release commit at current main and its exact lightweight version tag. Underlying evidence cause is unknown; never repoint a tag."),
    "signing_checks": ("public_signing_checks", "exact_successful_source_and_release_check_attempts",
        "Require separate exact successful main checks for application source and release. Underlying evidence cause is unknown; do not substitute PR or latest-run evidence."),
    "signing_qualification": ("public_signing_qualification", "exact_source_attempt_artifacts_and_distinct_native_runners",
        "Require exact source qualification, artifact identities and independent hosted runners. Underlying evidence cause is unknown; do not rebuild or substitute artifacts."),
    "signing_inventory_read": ("public_signing_inventory_read", "successful_authenticated_complete_inventory_pages",
        "Inventory read is unavailable; underlying cause is unknown. Inspect authoritative state privately. A 404 never establishes absence; do not retry publication."),
    "signing_inventory": ("public_signing_inventory", "bounded_complete_unique_well_formed_inventory",
        "Inventory is incomplete, malformed or duplicated; underlying cause is unknown. Reconcile complete authoritative evidence without waiving the cap or replaying publication."),
    "signing_prior": ("public_signing_prior_invocation", "only_this_selected_signing_invocation",
        "Prior or unexpected signing evidence refuses admission. Reconcile authoritative signing state; never rerun or replay an uncertain publication."),
    "signing_exists": ("public_signing_release_absence", "selected_tag_absent_from_complete_authenticated_release_list",
        "A release already uses the selected tag. Reconcile authoritative release state; never overwrite, delete or replay publication."),
    "signing_relation": ("public_signing_git_relation", "sole_source_parent_regular_root_config_only_exact_render",
        "Require checked-out release HEAD, sole application-source parent and only a regular root config add/change matching the pinned serving render. Underlying Git cause is unknown; no fallback or state repair."),
    "signing_drv": ("public_signing_image_derivation", "equal_offline_evaluated_committed_source_and_release_drv_paths",
        "Committed source/release image derivation equality is unavailable; underlying evaluation cause is unknown. Qualify the exact commits without building or fetching replacements."),
    "signing_cleanup": ("public_signing_snapshot_cleanup", "joined_commands_and_owned_public_snapshot_removal",
        "Temporary public Git snapshot cleanup is unconfirmed; underlying cause is unknown. Remove only the owned temporary material privately; no publication was attempted."),
    "signing_archive": ("public_signing_archive", "exact_downloaded_artifact_ids_full_archive_and_expected_receipt",
        "Require identical admitted artifact IDs, actual A archive hash, matching manifests and runtime/startup qualification bound to the expected source/image/archive. Underlying cause is unknown; no replacement or fallback."),
    "signing_output": ("public_signing_output", "bounded_closed_admitted_artifact_outputs",
        "Admitted workflow-output delivery is unavailable; underlying cause is unknown. No publication was attempted by this guard; inspect the workflow without replay."),
    "signing_command": ("public_signing_command", "status_or_explicit_read_only_admit_guard",
        "Underlying command cause is unknown. Use status or the explicitly selected workflow guard; reporting never initiates publication, deployment or retry."),
})


def stage(code, operation):
    """Reclassify only the specific external boundary, retaining observed status."""
    try:
        return operation()
    except r.Stop as error:
        raise r.Stop(code, status=error.status) from None
    except (Exception, KeyboardInterrupt):
        raise r.Stop(code) from None


def positive(value):
    r.require(r.pin(value, r"[1-9][0-9]{0,19}"), "signing_context")
    try:
        return int(value)
    except (ValueError, TypeError):
        raise r.Stop("signing_context") from None


def validate_context(ctx: dict[str, Any]):
    r.require(type(ctx) is dict and set(ctx) == NUMBERS | {"source", "release", "tag", "image", "archive", "phase"},
              "signing_context")
    r.require(all(type(ctx[key]) is int and 0 < ctx[key] < 10 ** 20 for key in NUMBERS)
              and r.pin(ctx["source"], r.SHA) and r.pin(ctx["release"], r.SHA)
              and ctx["source"] != ctx["release"] and r.pin(ctx["tag"], r.VERSION)
              and r.pin(ctx["image"], r.DIGEST) and ctx["image"] != "sha256:" + "0" * 64
              and r.pin(ctx["archive"], r"[0-9a-f]{64}") and ctx["phase"] == c.PHASE,
              "signing_context")


def context(env=None):
    env = os.environ if env is None else env
    # Refuse invalid/disabled selections before event-file or external reads.
    r.require(env.get("PUBLIC_INTAKE_RELEASE_ENABLED") == "true", "disabled")
    ref, release = env.get("GITHUB_REF", ""), env.get("GITHUB_SHA", "")
    r.require(env.get("GITHUB_REPOSITORY") == c.REPOSITORY
              and env.get("GITHUB_EVENT_NAME") == "workflow_dispatch"
              and env.get("GITHUB_ACTOR") == env.get("GITHUB_TRIGGERING_ACTOR") == c.OWNER
              and env.get("GITHUB_RUN_ATTEMPT") == "1" and ref.startswith("refs/tags/")
              and r.pin(ref[10:], r.VERSION) and r.pin(release, r.SHA), "signing_context")
    run = positive(env.get("GITHUB_RUN_ID", ""))
    def read():
        try:
            event = json.loads(p.read_file(Path(env["GITHUB_EVENT_PATH"])))
        except (Exception, KeyboardInterrupt):
            raise r.Stop("signing_context") from None
        repo, inputs = event.get("repository", {}), event.get("inputs", {})
        r.require(event.get("sender", {}).get("login") == c.OWNER
                  and repo.get("full_name") == c.REPOSITORY and repo.get("private") is False
                  and repo.get("owner", {}).get("login") == c.OWNER
                  and type(inputs) is dict and set(inputs) == INPUTS, "signing_context")
        ctx: dict[str, Any] = {key: positive(inputs[key]) for key in NUMBERS - {"run"}}
        ctx.update(run=run, source=inputs["source"], release=release, tag=ref[10:],
                   image=inputs["image_digest"], archive=inputs["archive_sha256"], phase=inputs["storage_phase"])
        validate_context(ctx)
        return ctx
    return c.checked("signing_context", read)


def repo_read(api, suffix, code):
    return stage(code, lambda: api.repo(suffix))


def current_gate(run, ctx):
    r.require(type(run) is dict and type(run.get("id")) is int and run["id"] == ctx["run"]
              and type(run.get("run_attempt")) is int and run["run_attempt"] == 1
              and run.get("path") == ".github/workflows/" + WORKFLOW
              and run.get("head_sha") == ctx["release"] and run.get("head_branch") == ctx["tag"]
              and run.get("event") == "workflow_dispatch" and run.get("status") == "in_progress"
              and run.get("conclusion") is None
              and run.get("repository", {}).get("full_name") == c.REPOSITORY
              and run.get("head_repository", {}).get("full_name") == c.REPOSITORY
              and run.get("actor", {}).get("login") == c.OWNER
              and run.get("triggering_actor", {}).get("login") == c.OWNER, "signing_current")


def inventory(api, suffix, field=None):
    """Consume every page; incomplete cap and duplicate IDs never mean absence."""
    values, seen = [], set()
    for page in range(1, PAGE_CAP + 1):
        data = repo_read(api, suffix + ("&" if "?" in suffix else "?")
                         + f"per_page=100&page={page}", "signing_inventory_read")
        batch: Any = data.get(field) if field and type(data) is dict else data if field is None else None
        r.require(type(batch) is list and len(batch) <= 100, "signing_inventory")
        for item in batch:
            if type(item) is not dict:
                raise r.Stop("signing_inventory")
            number: Any = item.get("id")
            r.require(type(number) is int and number > 0 and number not in seen, "signing_inventory")
            seen.add(number)
            values.append(item)
        if len(batch) < 100:
            if field:
                r.require(type(data.get("total_count")) is int and data["total_count"] == len(values),
                          "signing_inventory")
            return values
    raise r.Stop("signing_inventory")


def absence_gate(api, ctx):
    # Check both commit and tag history. Neither query may conceal another run.
    for query in ("head_sha=" + ctx["release"], "branch=" + ctx["tag"]):
        runs = inventory(api, "/actions/workflows/" + WORKFLOW + "/runs?" + query, "workflow_runs")
        r.require(len(runs) == 1 and runs[0]["id"] == ctx["run"], "signing_prior")
        current_gate(runs[0], ctx)
    tags = set()
    for item in inventory(api, "/releases"):
        tag = item.get("tag_name")
        r.require(r.pin(tag, r.VERSION) and tag not in tags
                  and type(item.get("draft")) is bool and type(item.get("prerelease")) is bool,
                  "signing_inventory")
        tags.add(tag)
    r.require(ctx["tag"] not in tags, "signing_exists")


def git(tool, root, *args, code="signing_relation"):
    return stage(code, lambda: tool(["git", "-C", str(root), *args], code))


def regular(root, relative, code):
    path = root / relative
    r.require(not path.is_symlink() and path.is_file()
              and all(not (root / parent).is_symlink() for parent in Path(relative).parents), code)
    return path


class SnapshotStop(r.Stop):
    cleanup_failed = False


def derivations(root, ctx, tool):
    """Archive immutable Git commits, never evaluate an injected drv assertion.

    Validate archive member modes/paths BEFORE system tar extraction. Reject all
    symlinks/submodules, avoiding link traversal even in otherwise trusted Git.
    Commands are bounded, killed/joined by the shared command port before cleanup.
    """
    directory, primary = None, None
    try:
        directory = Path(tempfile.mkdtemp(prefix="intake-public-signing-", dir="/tmp"))
        directory.chmod(0o700)
        paths = []
        for label in ("source", "release"):
            sha = ctx[label]
            listing = git(tool, root, "ls-tree", "-rz", "--full-tree", sha, code="signing_drv")
            entries = listing.split(b"\0")
            r.require(entries[-1] == b"" and len(entries) > 1, "signing_drv")
            seen = {}
            for entry in entries[:-1]:
                header, name = entry.split(b"\t", 1)
                mode, kind, blob = header.split(b" ")
                relative = PurePosixPath(name.decode("utf-8"))
                r.require(mode in (b"100644", b"100755") and kind == b"blob"
                          and r.pin(blob.decode(), r.SHA) and not relative.is_absolute()
                          and relative.parts and ".." not in relative.parts
                          and name.decode("utf-8") not in seen and not name.startswith(b"-")
                          and all(byte >= 32 and byte != 127 for byte in name), "signing_drv")
                seen[name.decode("utf-8")] = (mode, blob.decode())
            archive, snapshot = directory / (label + ".tar"), directory / label
            snapshot.mkdir(mode=0o700)
            git(tool, root, "-c", "core.attributesFile=/dev/null", "archive", "--format=tar",
                "--output=" + str(archive), sha, code="signing_drv")
            r.require(archive.is_file() and not archive.is_symlink() and 0 < archive.stat().st_size <= 256 << 20,
                      "signing_drv")
            stage("signing_drv", lambda archive=archive, snapshot=snapshot: tool(
                ["tar", "--extract", "--file", str(archive), "--directory", str(snapshot),
                 "--no-same-owner", "--no-same-permissions"], "signing_drv"))
            # Git attributes may omit/substitute archive members. Verify exact
            # committed blob bytes/modes, refusing such a transformed snapshot.
            extracted = set()
            for path in snapshot.rglob("*"):
                r.require(not path.is_symlink(), "signing_drv")
                if path.is_dir():
                    continue
                name = path.relative_to(snapshot).as_posix()
                r.require(path.is_file() and name in seen and path.stat().st_size <= 256 << 20, "signing_drv")
                mode, _blob = seen[name]
                r.require(bool(path.stat().st_mode & 0o111) == (mode == b"100755"), "signing_drv")
                extracted.add(name)
            r.require(extracted == set(seen), "signing_drv")
            names = sorted(extracted)
            # Git implements its object hash; --no-filters disables attributes,
            # and omitting -w means no object-store mutation.
            hashes = git(tool, root, "hash-object", "--no-filters", "--",
                         *(str(snapshot / name) for name in names), code="signing_drv").splitlines()
            r.require(hashes == [seen[name][1].encode() for name in names], "signing_drv")
            regular(snapshot, c.TEMPLATE, "signing_drv")
            if label == "release":
                regular(snapshot, CONFIG, "signing_drv")
            raw = stage("signing_drv", lambda snapshot=snapshot: tool(
                ["nix", "eval", "--offline", "--raw",
                 "path:" + str(snapshot) + "#packages.x86_64-linux.ledger-image.drvPath",
                 "--option", "allow-import-from-derivation", "false"], "signing_drv"))
            drv = raw.decode("ascii")
            r.require(r.pin(drv, r"/nix/store/[0-9a-df-np-sv-z]{32}-[A-Za-z0-9+._?=-]+\.drv"), "signing_drv")
            paths.append(drv)
        r.require(paths[0] == paths[1], "signing_drv")
    except (Exception, KeyboardInterrupt) as error:
        primary = SnapshotStop(error.code, status=error.status) if isinstance(error, r.Stop) else SnapshotStop("signing_drv")
        raise primary from None
    finally:
        if directory is not None:
            try:
                shutil.rmtree(directory)
            except (Exception, KeyboardInterrupt):
                if primary is None:
                    raise r.Stop("signing_cleanup") from None
                primary.cleanup_failed = True


def relation_gate(ctx, root, tool=p.command):
    def validate():
        r.require(git(tool, root, "rev-parse", "HEAD").strip() == ctx["release"].encode(), "signing_relation")
        r.require(git(tool, root, "rev-list", "--parents", "-n", "1", ctx["release"]).split()
                  == [ctx["release"].encode(), ctx["source"].encode()], "signing_relation")
        changed = git(tool, root, "diff-tree", "--no-commit-id", "--name-status", "-r", "--no-renames", "-z",
                      ctx["source"], ctx["release"]).split(b"\0")
        r.require(changed in ([b"A", CONFIG.encode(), b""], [b"M", CONFIG.encode(), b""]), "signing_relation")
        tree = git(tool, root, "ls-tree", ctx["release"], "--", CONFIG).split()
        r.require(len(tree) == 4 and tree[0:2] == [b"100644", b"blob"] and tree[3] == CONFIG.encode(),
                  "signing_relation")
        template_path = regular(root, c.TEMPLATE, "signing_relation")
        r.require(template_path.stat().st_size <= 8192, "signing_relation")
        template = template_path.read_bytes()
        expected = c.render(template, ctx["image"], phase=ctx["phase"])
        committed = git(tool, root, "show", ctx["release"] + ":" + CONFIG)
        active = regular(root, CONFIG, "signing_relation")
        r.require(active.stat().st_size <= 8192 and active.read_bytes() == committed == expected, "signing_relation")
        # Local template must also be the exact regular committed template.
        r.require(git(tool, root, "ls-tree", ctx["release"], "--", c.TEMPLATE).split()[0:2]
                  == [b"100644", b"blob"]
                  and git(tool, root, "show", ctx["release"] + ":" + c.TEMPLATE) == template, "signing_relation")
        derivations(root, ctx, tool)
    return c.checked("signing_relation", validate)


def admit(api, ctx, root, tool=p.command):
    validate_context(ctx)
    def repository():
        repo = api.repo()
        status, package = api.package()
        if status != 200:
            raise r.Stop("signing_repository", status=status)
        c.repository_gate(repo, package)
        return repo
    repo = stage("signing_repository", repository)
    c.checked("signing_current", lambda: current_gate(repo_read(api, f"/actions/runs/{ctx['run']}", "signing_current"), ctx))
    def source():
        for suffix, ref in (("heads/main", "refs/heads/main"), ("tags/" + ctx["tag"], "refs/tags/" + ctx["tag"])):
            value = repo_read(api, "/git/ref/" + suffix, "signing_source")
            r.require(value.get("ref") == ref and value.get("object", {}).get("type") == "commit"
                      and value["object"].get("sha") == ctx["release"], "signing_source")
    c.checked("signing_source", source)
    for label in ("source", "release"):
        check_ctx = {"source": ctx[label], "checks_run": ctx["checks_run" if label == "source" else "release_checks_run"],
                     "checks_attempt": ctx["checks_attempt" if label == "source" else "release_checks_attempt"]}
        checks = stage("signing_checks", lambda check_ctx=check_ctx: p.qualified_checks(api, check_ctx))
        r.require(len(checks) == 1, "signing_checks")
    admitted = stage("signing_qualification", lambda: p.qualified_artifacts(api, ctx, repo))
    c.checked("signing_inventory", lambda: absence_gate(api, ctx))
    relation_gate(ctx, Path(root), tool)
    return admitted


def guard(api, ctx, root, downloaded, *, tool=p.command, accepted=Path("accepted")):
    validate_context(ctx)
    r.require(type(downloaded) is dict and set(downloaded) == {"a", "b", "archive"}
              and all(type(value) is int and value > 0 for value in downloaded.values())
              and len(set(downloaded.values())) == 3, "signing_archive")
    admitted = admit(api, ctx, root, tool)
    r.require(admitted["artifacts"] == downloaded, "signing_archive")
    receipt = stage("signing_archive", lambda: d.archive_gate(accepted, admitted, tool))
    r.require(receipt == {"publicSourceSHA": ctx["source"], "imageDigest": ctx["image"], "archiveSHA": ctx["archive"]},
              "signing_archive")
    return {"passed": True, "stage": "public_signing_guard", "published": False,
            "cryptographically_verified": False, "runtime_verified": False}


def outputs(admitted, env):
    """Only authenticated artifact IDs/names and their run binding; never evidence."""
    def write():
        source, run, attempt = admitted["source"], admitted["qualification_run"], admitted["qualification_attempt"]
        r.require(r.pin(source, r.SHA) and type(run) is int and 0 < run < 10 ** 20
                  and type(attempt) is int and 0 < attempt < 10 ** 20, "signing_output")
        values = {"qualification_run": str(run), "qualification_attempt": str(attempt)}
        for key in ("a", "b", "archive"):
            number = admitted["artifacts"][key]
            r.require(type(number) is int and 0 < number < 10 ** 20, "signing_output")
            values[key] = str(number)
            values[key + "_name"] = (f"intake-synthetic-archive-{run}-{attempt}-{source}" if key == "archive"
                                    else f"intake-synthetic-{run}-{attempt}-{source}-{key}")
        text = "".join(key + "=" + value + "\n" for key, value in values.items())
        r.require(len(text) <= 1024, "signing_output")
        path = Path(env["GITHUB_OUTPUT"])
        r.require(path.is_file() and not path.is_symlink(), "signing_output")
        with path.open("a") as stream:
            stage("signing_output", lambda: c.emit(stream, text))
    stage("signing_output", write)


def diagnostic(error):
    value = r.diagnostic(error)
    if getattr(error, "cleanup_failed", False) is True:
        value["secondary"] = [r.diagnostic(r.Stop("signing_cleanup"))]
    return value


def main(args=None):
    args = sys.argv[1:] if args is None else args
    primary = None
    def execute():
        if args == ["status"]:
            return {"stage": "offline_public_signing_status", "release_enabled": False,
                    "published": False, "cryptographically_verified": False, "runtime_verified": False}
        r.require(args in (["admit"], ["guard"]), "signing_command")
        ctx = context()
        downloaded = None
        if args == ["guard"]:
            downloaded = {key: positive(os.environ["ACCEPTED_" + key.upper() + "_ARTIFACT_ID"])
                          for key in ("a", "b", "archive")}
        root = Path(__file__).resolve().parents[1]
        if args == ["admit"]:
            admitted = admit(p.GitHub(), ctx, root)
            outputs(admitted, os.environ)
            return {"passed": True, "stage": "public_signing_admission", "published": False}
        return guard(p.GitHub(), ctx, root, downloaded)
    try:
        result = c.checked("signing_command", execute)
        text = json.dumps(result, sort_keys=True) + "\n"
    except (Exception, KeyboardInterrupt) as error:
        primary = error if isinstance(error, r.Stop) else r.Stop("signing_command")
        text = json.dumps(diagnostic(primary), sort_keys=True) + "\n"
    try:
        c.emit(sys.stdout, text)
    except r.Stop:
        with suppress(r.Stop):
            c.emit(sys.stderr, json.dumps(diagnostic(primary or r.Stop("signing_output"))) + "\n")
        return 1
    return 1 if primary else 0


if __name__ == "__main__":
    sys.exit(main())
