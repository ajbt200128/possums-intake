#!/usr/bin/env python3
"""Manual CI-token package-view diagnosis, NEVER release/runtime evidence.

Read-only fixed GitHub metadata sweep: repository, current main, package, then
<=100 active-version pages of <=100 entries. Uses d.GitHub's existing 8 MiB,
30-second, no-redirect GET transport; no retries, polling, registry/image calls,
context/admission/archive/command/copy/execution or local evidence files.

Schema ci_package_evidence_v1: exact PREDICATES boolean keys (false means not
established; *_read and versions_complete distinguish unavailable/partial
observations), closed CLASSIFICATIONS strings, optional integer HTTP 100..599.
One compact, validated <=4096-byte JSON GitHub notice/warning, command-escaped.
No backend values/IDs/names, raw errors, credentials, headers or URLs escape.
The token is consumed only by the existing transport from ephemeral GITHUB_TOKEN.
Missing embedded private, explicit false, and canonical private remain distinct.
Malformed/incomplete tag evidence cannot establish source-tag absence; a
complete empty view still fails baseline_present.

Intake PRIVACY.md#forbidden-collection-and-defaults and gateway
PRIVACY.md#telemetry-permitted-signals-and-forbidden-data apply. This explicitly
requested CI diagnosis is not production logging/telemetry. Normal Actions
history/annotation retention is a separate existing persistence boundary; no
artifact, support bundle or additional local log. NEVER merge this branch's
manual-only check.yml into main or count its different SHA as qualification.
"""
import importlib.util
import json
import sys
from pathlib import Path

EXPECTED_SOURCE = "1946aa43f21f3ccd6ceaee7819aa53a5f8221f6f"
MAX_PAGES = 100
PER_PAGE = 100
MAX_RESULT_BYTES = 4096
PREDICATES = (
    "repository_read", "repository_object", "repository_id", "repository_full_name",
    "repository_owner", "repository_private", "repository_default_main",
    "current_main_read", "current_main_object", "current_main_matches_expected",
    "package_read", "package_object", "package_id", "package_name", "package_type",
    "package_owner", "package_public", "embedded_repository_object",
    "embedded_repository_id_present", "embedded_repository_id_integer",
    "embedded_repository_id_equal", "embedded_repository_name",
    "embedded_private_field_present", "embedded_private_true",
    "versions_read", "version_pages_shape", "versions_complete", "version_entry_object",
    "version_entry_id", "version_ids_unique", "version_digest", "version_tags_shape",
    "version_tags_unique", "source_tag_absent", "baseline_present",
)
STAGES = ("module", "repository", "current_main", "package", "versions", "projection", "arguments")
CLASSIFICATIONS = {
    "CI_PACKAGE_PROBE_COMPLETE": ("projection", "all_fixed_predicates_established", "not_applicable"),
    "CI_PACKAGE_PROBE_PREDICATES_NOT_ESTABLISHED":
        ("projection", "inspect_false_and_read_completion_predicates", "not_applicable"),
}
for _stage in STAGES:
    for _kind, _cause in (("HTTP_UNAVAILABLE", "observed_http_status"), ("CAUSE_UNKNOWN", "cause_unknown")):
        CLASSIFICATIONS["CI_PACKAGE_PROBE_" + _stage.upper() + "_" + _kind] = (
            _stage, "bounded_fixed_github_read" if _stage in STAGES[1:5]
            else "closed_local_probe_operation", _cause)


def load_delivery():
    spec = importlib.util.spec_from_file_location(
        "ci_probe_delivery", Path(__file__).with_name("synthetic-diagnostic-publication.py"))
    if spec is None or spec.loader is None:
        raise RuntimeError
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def result(predicates, code, status=None):
    stage, constraint, cause = CLASSIFICATIONS[code]
    value = {"schema": "ci_package_evidence_v1", "code": code, "stage": stage,
             "constraint": constraint, "cause": cause,
             "next_step": "inspect_annotation_no_publication_retry_or_release_authority",
             "release_eligible": False, "runtime_verified": False,
             "predicates": predicates.copy()}
    if type(status) is int and 100 <= status <= 599:
        value["http_status"] = status
    return value


def obj(value):
    return value if type(value) is dict else {}


def positive_id(value):
    return type(value) is int and value > 0


def sweep(api=None, delivery=None, *, arguments_valid=True):
    """Project independently even after semantic mismatch; stop on read failure.

    Version entry predicates aggregate ALL observed entries. Only a short final
    page establishes completion; source_tag_absent additionally requires valid
    tags throughout. A full page 100 is incomplete, even with a known baseline.
    """
    predicates = dict.fromkeys(PREDICATES, False)
    stage = "module"
    d = None
    try:
        if not arguments_valid:
            stage = "arguments"
            raise ValueError
        d = load_delivery() if delivery is None else delivery
        api = d.GitHub() if api is None else api
        stage = "repository"
        repo_raw = api.repo()
        predicates["repository_read"] = True
        repo = obj(repo_raw)
        predicates.update({
            "repository_object": type(repo_raw) is dict,
            "repository_id": positive_id(repo.get("id")),
            "repository_full_name": repo.get("full_name") == d.p.REPO,
            "repository_owner": obj(repo.get("owner")).get("login") == d.p.OWNER,
            "repository_private": d.r.boolean(repo.get("private"), True),
            "repository_default_main": repo.get("default_branch") == "main",
        })
        stage = "current_main"
        main_raw = api.repo("/git/ref/heads/main")
        predicates["current_main_read"] = True
        main = obj(main_raw)
        predicates["current_main_object"] = type(main_raw) is dict and type(main.get("object")) is dict
        predicates["current_main_matches_expected"] = obj(main.get("object")).get("sha") == EXPECTED_SOURCE
        stage = "package"
        status, package_raw = api.package()
        if status != 200:
            raise d.r.Stop("diagnostic_package", status=status)
        predicates["package_read"] = True
        package = obj(package_raw)
        embedded_raw = package.get("repository")
        embedded = obj(embedded_raw)
        predicates.update({
            "package_object": type(package_raw) is dict,
            "package_id": positive_id(package.get("id")),
            "package_name": package.get("name") == d.p.PACKAGE,
            "package_type": package.get("package_type") == "container",
            "package_owner": obj(package.get("owner")).get("login") == d.p.OWNER,
            "package_public": package.get("visibility") == "public",
            "embedded_repository_object": type(embedded_raw) is dict,
            "embedded_repository_id_present": "id" in embedded,
            "embedded_repository_id_integer": type(embedded.get("id")) is int,
            # Preserve the exact current equality predicate (canonical ID is checked separately).
            "embedded_repository_id_equal": type(embedded.get("id")) is int
                and embedded.get("id") == repo.get("id"),
            "embedded_repository_name": embedded.get("full_name") == d.p.REPO,
            "embedded_private_field_present": "private" in embedded,
            "embedded_private_true": d.r.boolean(embedded.get("private"), True),
        })
        stage = "versions"
        seen, baseline, source_absent = set(), False, True
        for page in range(1, MAX_PAGES + 1):
            status, batch = api.version_page(page)
            if status != 200:
                raise d.r.Stop("diagnostic_package", status=status)
            if not predicates["versions_read"]:
                for key in ("version_pages_shape", "version_entry_object", "version_entry_id",
                            "version_ids_unique", "version_digest", "version_tags_shape", "version_tags_unique"):
                    predicates[key] = True
            predicates["versions_read"] = True
            if type(batch) is not list or len(batch) > PER_PAGE:
                predicates["version_pages_shape"] = False
                break
            for raw in batch:
                version = obj(raw)
                predicates["version_entry_object"] &= type(raw) is dict
                number = version.get("id")
                valid_id = positive_id(number)
                predicates["version_entry_id"] &= valid_id
                predicates["version_ids_unique"] &= valid_id and number not in seen
                if valid_id:
                    seen.add(number)
                predicates["version_digest"] &= d.r.pin(version.get("name"), d.r.DIGEST)
                tags_raw = obj(obj(version.get("metadata")).get("container")).get("tags")
                tags = tags_raw if type(tags_raw) is list else []
                tags_shape = type(tags_raw) is list and all(type(tag) is str and 1 <= len(tag) <= 128 for tag in tags)
                predicates["version_tags_shape"] &= tags_shape
                predicates["version_tags_unique"] &= tags_shape and len(tags) == len(set(tags))
                source_absent &= tags_shape and EXPECTED_SOURCE not in tags
                baseline |= version.get("name") == d.BASELINE
            predicates["baseline_present"] = baseline
            if len(batch) < PER_PAGE:
                predicates["versions_complete"] = True
                break
        predicates["source_tag_absent"] = source_absent and predicates["versions_complete"]
        stage = "projection"
        return result(predicates, "CI_PACKAGE_PROBE_COMPLETE" if all(predicates.values())
                      else "CI_PACKAGE_PROBE_PREDICATES_NOT_ESTABLISHED")
    except (Exception, KeyboardInterrupt) as error:
        # The transport intentionally collapses decoding/limits/transport into
        # no-status Stop. Do not infer which cause occurred or echo its text.
        status = getattr(error, "status", None) if d is not None and isinstance(error, d.r.Stop) else None
        valid_status = type(status) is int and 100 <= status <= 599
        return result(predicates, "CI_PACKAGE_PROBE_" + stage.upper()
                      + ("_HTTP_UNAVAILABLE" if valid_status else "_CAUSE_UNKNOWN"), status)


def compact(value):
    """Final exact allowlist, including code-to-stage/constraint/cause binding."""
    keys = {"schema", "code", "stage", "constraint", "cause", "next_step", "release_eligible",
            "runtime_verified", "predicates"}
    if type(value) is not dict or set(value) not in (keys, keys | {"http_status"}):
        raise ValueError
    code = value["code"]
    if type(code) is not str or code not in CLASSIFICATIONS:
        raise ValueError
    if (value["stage"], value["constraint"], value["cause"]) != CLASSIFICATIONS[code]:
        raise ValueError
    if (value["schema"] != "ci_package_evidence_v1"
            or value["next_step"] != "inspect_annotation_no_publication_retry_or_release_authority"
            or value["release_eligible"] is not False or value["runtime_verified"] is not False):
        raise ValueError
    predicates = value["predicates"]
    if type(predicates) is not dict or set(predicates) != set(PREDICATES) or any(type(v) is not bool for v in predicates.values()):
        raise ValueError
    has_status = "http_status" in value
    if has_status != (value["cause"] == "observed_http_status"):
        raise ValueError
    if has_status and not (type(value["http_status"]) is int and 100 <= value["http_status"] <= 599):
        raise ValueError
    payload = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True)
    if len(payload.encode("ascii")) > MAX_RESULT_BYTES:
        raise ValueError
    return payload


def escape_command(value):
    return value.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")


def annotation(value):
    try:
        payload = compact(value)
        success = value["code"] == "CI_PACKAGE_PROBE_COMPLETE"
    except (Exception, KeyboardInterrupt):
        payload = compact(result(dict.fromkeys(PREDICATES, False), "CI_PACKAGE_PROBE_PROJECTION_CAUSE_UNKNOWN"))
        success = False
    return ("::notice::" if success else "::warning::") + escape_command(payload), success


def main(args=None):
    args = sys.argv[1:] if args is None else args
    try:
        value = sweep(arguments_valid=not args)
    except (Exception, KeyboardInterrupt):
        value = result(dict.fromkeys(PREDICATES, False), "CI_PACKAGE_PROBE_PROJECTION_CAUSE_UNKNOWN")
    line, success = annotation(value)
    # Exactly one annotation; no raw diagnostic fallback or output retry if the
    # host's stdout itself fails. Host delivery/termination remains external.
    try:
        print(line, flush=True)
    except (Exception, KeyboardInterrupt):
        return 1
    return 0 if success else 1


if __name__ == "__main__":
    sys.exit(main())
