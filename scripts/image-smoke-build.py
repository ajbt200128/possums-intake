#!/usr/bin/env python3
"""Passive fresh-output guard for independent clean CI builders.
Never deletes store paths, builds artifacts, retries or runs a remote builder.
"""
import json
import sys
from pathlib import Path

import release as r


def fresh(flake="."):
    try:
        raw = r.read_command(["nix", "eval", "--json", flake + "#release-build-deps.rebuildDerivations"])
        drvs = json.loads(raw)
        r.require(isinstance(drvs, list) and len(drvs) >= 5, "build")
        for drv in drvs:
            r.require(isinstance(drv, str) and drv.startswith("/nix/store/") and drv.endswith(".drv"), "build")
            shown = json.loads(r.read_command(["nix", "derivation", "show", drv]))
            shown = shown.get("derivations", shown)
            r.require(len(shown) == 1, "build")
            outputs = next(iter(shown.values()))["outputs"]
            r.require(bool(outputs), "build")
            for output in outputs.values():
                path = output.get("path")
                r.require(isinstance(path, str) and path.startswith("/nix/store/"), "build")
                r.require(not Path(path).exists(), "build")
    except r.Stop:
        raise
    except (OSError, ValueError, TypeError, KeyError, StopIteration):
        raise r.Stop("build") from None
    return {"passed": True, "stage": "fresh_output_guard"}


if __name__ == "__main__":
    try:
        r.require(len(sys.argv) <= 2, "arguments")
        print(json.dumps(fresh(sys.argv[1] if len(sys.argv) == 2 else ".")))
    except Exception as error:
        print(json.dumps(r.diagnostic(error)))
        sys.exit(1)
