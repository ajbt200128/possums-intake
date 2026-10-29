#!/usr/bin/env python3
"""Explicit PUBLIC-source native owner publication of an already qualified image.

Only publish selects authenticated PUBLIC source/package admission. The legacy
PRIVATE policy is unchanged; there is no visibility fallback or Actions publisher
identity. Reuse native saved-owner authentication, exact qualification/download,
archive checks and the once-only copy executor. No build, signing or deployment.
PRIVACY.md and ../possums/PRIVACY.md apply, including native host persistence and
private retained workspace/auth cleanup boundaries documented by the owner tool.
"""
import importlib.util
import json
import sys
from contextlib import suppress
from pathlib import Path

import release as r

_spec = importlib.util.spec_from_file_location("public_native_owner", Path(__file__).with_name("owner-synthetic-publication.py"))
if _spec is None or _spec.loader is None:
    raise r.Stop("internal")
o = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(o)

_spec = importlib.util.spec_from_file_location("public_owner_contract", Path(__file__).with_name("public-release.py"))
if _spec is None or _spec.loader is None:
    raise r.Stop("internal")
c = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(c)
p, d = o.p, o.d


def repository_gate(api):
    """The existing authenticated fixed API supplies both independent identities."""
    repository = p.boundary("diagnostic_evidence", api.repo)
    def read_package():
        status, package = api.package()
        if type(status) is not int or status != 200:
            raise r.Stop("diagnostic_package_read", status=status)
        return package
    package = d.package_read(read_package, "diagnostic_package_read")
    c.repository_gate(repository, package)
    return repository


def admit(api, ctx):
    return o.admit(api, ctx, repository_reader=repository_gate)


def present(receipt):
    c.emit(sys.stdout, json.dumps(receipt, sort_keys=True, separators=(",", ":")) + "\n")


def main(args=None):
    args = sys.argv[1:] if args is None else args
    try:
        if args in ([], ["status"], ["report"]):
            c.emit(sys.stdout, json.dumps({"stage": "source_only", "publication": "not_observed",
                                           "runtime_verified": False}) + "\n")
        else:
            # Complete strict native argument admission before auth/env/workspace.
            o.publish(o.parse(args), admitter=admit, presenter=present)
        return 0
    except (Exception, KeyboardInterrupt) as error:
        primary = error if isinstance(error, r.Stop) else r.Stop("diagnostic_internal")
    # Receipt delivery already failed once; never try delivering it again. The
    # shared emitter neutralizes failed file descriptors against raw shutdown IO.
    stream = sys.stderr if primary.code in {"synthetic_receipt", "public_command"} else sys.stdout
    text = json.dumps(o.diagnostic(primary)) + "\n"
    try:
        c.emit(stream, text)
    except (Exception, KeyboardInterrupt):
        if stream is sys.stdout:
            with suppress(Exception, KeyboardInterrupt):
                c.emit(sys.stderr, text)
    return 1


if __name__ == "__main__":
    sys.exit(main())
