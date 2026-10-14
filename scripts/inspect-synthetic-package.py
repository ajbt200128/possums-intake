#!/usr/bin/env python3
"""Explicit fixed-package GET probe; booleans only, never publication authority.

PRIVACY.md forbidden collection and ../possums/PRIVACY.md processor boundaries
apply. Manual Actions stdout is native retained history, not transient telemetry.
"""
import importlib.util
import json
import os
import sys
from pathlib import Path

import release as r

_spec = importlib.util.spec_from_file_location("inspect_fixed_publication", Path(__file__).with_name("synthetic-publication.py"))
if _spec is None or _spec.loader is None:
    raise r.Stop("internal")
p = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(p)
BASELINE = "sha256:e37d729571bd8ab82eb715a0e5ff97ab3e5a0d8c76b98c1baa1dfa27fb2d55ca"
for _code, _stage in (("inspect_context", "inspect_context"), ("inspect_source", "inspect_source_read"),
                       ("inspect_package", "inspect_package_read"), ("inspect_versions", "inspect_versions_read"),
                       ("inspect_output", "inspect_output"), ("inspect_command", "inspect_command")):
    r.CODES[_code] = (_stage, "explicit_fixed_read_only_complete_evidence",
                     "Required inspection evidence is unavailable; underlying cause unknown. Inspect privately using this code/stage and any observed HTTP status. No mutation occurred; do not replay publication or change grants.")


def read(code, operation):
    try:
        return operation()
    except r.Stop as error:
        if error.code in {"synthetic_namespace", "synthetic_evidence"}:
            raise r.Stop(code, status=error.status) from None
        raise
    except (Exception, KeyboardInterrupt):
        raise r.Stop(code) from None


def obj(value):
    return value if type(value) is dict else {}


def positive(value):
    return type(value) is int and value > 0


def context(env=None):
    env = os.environ if env is None else env
    def validate():
        event = json.loads(p.read_file(Path(env["GITHUB_EVENT_PATH"])))
        inputs, repository = obj(obj(event).get("inputs")), obj(obj(event).get("repository"))
        source = env.get("GITHUB_SHA")
        r.require(set(inputs) == {"inspect", "expected_source"} and inputs["inspect"] == "true"
                  and r.pin(source, r.SHA) and inputs["expected_source"] == source
                  and env.get("GITHUB_REPOSITORY") == p.REPO and env.get("GITHUB_REF") == "refs/heads/main"
                  and env.get("GITHUB_EVENT_NAME") == "workflow_dispatch" and env.get("GITHUB_ACTOR") == p.OWNER
                  and env.get("GITHUB_RUN_ATTEMPT") == "1" and obj(obj(event).get("sender")).get("login") == p.OWNER
                  and repository.get("full_name") == p.REPO and r.boolean(repository.get("private"), True), "inspect_context")
        return source
    # Event-file helper errors concern this probe's context, not an archive.
    try:
        return validate()
    except r.Stop:
        raise r.Stop("inspect_context") from None
    except (Exception, KeyboardInterrupt):
        raise r.Stop("inspect_context") from None


def project(package, repo):
    a, b = obj(package), obj(repo)
    owner, link = obj(a.get("owner")), obj(a.get("repository"))
    return {
        "packageobject": type(package) is dict, "packageid": positive(a.get("id")),
        "packagename": a.get("name") == p.PACKAGE, "packagetype": a.get("package_type") == "container",
        "packageownerpresence": type(a.get("owner")) is dict, "packageownermatch": owner.get("login") == p.OWNER,
        "packagepublicvisibility": a.get("visibility") == "public", "sourceRepoobject": type(repo) is dict,
        "sourceRepoid": positive(b.get("id")), "sourceReponame": b.get("full_name") == p.REPO,
        "sourceRepoowner": obj(b.get("owner")).get("login") == p.OWNER,
        "sourceRepoprivate": r.boolean(b.get("private"), True), "sourceRepodefaultmain": b.get("default_branch") == "main",
        "repositorypresence": type(a.get("repository")) is dict, "repositoryidpresent": positive(link.get("id")),
        "repositoryidmatch": positive(link.get("id")) and positive(b.get("id")) and link["id"] == b["id"],
        "repositoryfullnamematch": link.get("full_name") == p.REPO, "repositoryprivatefieldpresence": "private" in link,
        "repositoryprivatebooltrue": r.boolean(link.get("private"), True),
    }


def inspect(api, source):
    r.require(r.pin(source, r.SHA), "inspect_context")
    repo = read("inspect_source", api.repo)
    result = read("inspect_source", lambda: project(None, repo))
    result.update(dict.fromkeys(("versionLIST", "versionschema", "allidpositive", "allidunique", "digestvalid",
                                "tagslist", "tagstringbounds", "tagsunique", "completeinventory", "baselinepresent", "targetsourceTagabsent"), False))
    if not all(result[key] for key in ("sourceRepoobject", "sourceRepoid", "sourceReponame", "sourceRepoowner",
                                       "sourceRepoprivate", "sourceRepodefaultmain")):
        return result  # Unestablished repository identity refuses all further reads.
    head = read("inspect_source", lambda: api.repo("/git/ref/heads/main"))
    r.require(obj(obj(head).get("object")).get("sha") == source, "inspect_source")
    package = response("inspect_package", api.package)
    result = read("inspect_package", lambda: project(package, repo))
    result.update(dict.fromkeys(("versionLIST", "versionschema", "allidpositive", "allidunique", "digestvalid",
                                "tagslist", "tagstringbounds", "tagsunique"), True))
    result.update(completeinventory=False, baselinepresent=False, targetsourceTagabsent=False)
    return read("inspect_versions", lambda: inventory(api, source, result))


def response(code, operation):
    def validate():
        status, body = operation()
        if type(status) is not int or status != 200:
            raise r.Stop(code, status=status)
        return body
    return read(code, validate)


def inventory(api, source, result):
    seen, target_absent = set(), True
    for page in range(1, 101):
        batch = response("inspect_versions", lambda page=page: api.get("/users/" + p.OWNER + "/packages/container/" + p.PACKAGE
                         + f"/versions?state=active&per_page=100&page={page}", package=True))
        r.require(type(batch) is list and len(batch) <= 100, "inspect_versions")
        for version in batch:
            v = obj(version)
            metadata, container = obj(v.get("metadata")), obj(obj(v.get("metadata")).get("container"))
            tags, identifier = container.get("tags"), v.get("id")
            valid_id, valid_tags = positive(identifier), type(tags) is list
            result["versionschema"] &= type(version) is dict and type(v.get("metadata")) is dict and type(metadata.get("container")) is dict
            result["allidpositive"] &= valid_id
            result["allidunique"] &= valid_id and identifier not in seen
            if valid_id:
                seen.add(identifier)
            result["digestvalid"] &= r.pin(v.get("name"), r.DIGEST)
            result["tagslist"] &= valid_tags
            tags = tags if type(tags) is list else []
            strings = valid_tags and all(type(tag) is str and 1 <= len(tag) <= 128 for tag in tags)
            result["tagstringbounds"] &= strings
            result["tagsunique"] &= strings and len(tags) == len(set(tags))
            target_absent &= strings and source not in tags
            result["baselinepresent"] |= v.get("name") == BASELINE
        if len(batch) < 100:
            result["completeinventory"] = True
            result["targetsourceTagabsent"] = target_absent
            return result
    raise r.Stop("inspect_versions")  # Full cap never establishes complete inventory.


def main(args=None):
    r.require((sys.argv[1:] if args is None else args) == ["inspect"], "inspect_command")
    source = context()  # No token/network access before explicit bound approval.
    result = read("inspect_command", lambda: inspect(p.GitHub(), source))
    read("inspect_output", lambda: print(json.dumps(result, sort_keys=True, separators=(",", ":"))))


def cli():
    try:
        main()
    except (Exception, KeyboardInterrupt) as error:
        try:
            read("inspect_output", lambda error=error: print(json.dumps(r.diagnostic(error if isinstance(error, r.Stop) else r.Stop("inspect_command")), separators=(",", ":"))))
        except r.Stop:
            sys.exit(1)  # Output unavailable; no traceback or delivery retry.
        sys.exit(1)


if __name__ == "__main__":
    cli()
