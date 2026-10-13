#!/usr/bin/env python3
"""Passive fresh-output guard for independent clean CI builders.
Never deletes store paths, builds artifacts, retries or runs a remote builder.
"""
import json
import sys
from pathlib import Path

import release as r

r.CODES.update({
    "fresh_metadata": ("fresh_output_guard", "supported_nix_derivation_output_metadata", "Read-only Nix output metadata could not be validated. Check its supported JSON version; no image build, publication or deployment was started."),
    "fresh_cached": ("fresh_output_guard", "no_existing_application_or_image_stage_outputs", "An application or image-stage output already exists. Use clean independent CI builders; do not delete paths or bypass this guard."),
})


def output_paths(shown):
    r.require(isinstance(shown, dict), "fresh_metadata")
    root_version = shown.get("version")
    derivations = shown.get("derivations", shown)
    r.require(isinstance(derivations, dict) and len(derivations) == 1, "fresh_metadata")
    entry = next(iter(derivations.values()))
    r.require(isinstance(entry, dict), "fresh_metadata")
    version = entry.get("version", root_version)
    r.require(version is None or type(version) is int and version in (1, 2, 3, 4), "fresh_metadata")
    r.require(root_version is None or type(root_version) is int and root_version == version, "fresh_metadata")
    outputs = entry.get("outputs")
    r.require(isinstance(outputs, dict) and bool(outputs), "fresh_metadata")
    paths = []
    for output in outputs.values():
        r.require(isinstance(output, dict) and isinstance(output.get("path"), str), "fresh_metadata")
        path = output["path"]
        if version in (3, 4):
            # Nix JSON v3/v4 encodes store-path basenames. The selected
            # derivations above already require the qualified /nix/store root.
            r.require(r.pin(path, r"[0-9abcdfghijklmnpqrsvwxyz]{32}-[A-Za-z0-9+._?=-]+"), "fresh_metadata")
            path = "/nix/store/" + path
        else:
            r.require(path.startswith("/nix/store/") and "/" not in path[len("/nix/store/"):]
                      and path[len("/nix/store/"):] not in ("", ".", ".."), "fresh_metadata")
        paths.append(path)
    return paths


def fresh(flake="."):
    try:
        raw = r.read_command(["nix", "eval", "--json", flake + "#release-build-deps.rebuildDerivations"])
        drvs = json.loads(raw)
        r.require(isinstance(drvs, list) and len(drvs) >= 5, "build")
        for drv in drvs:
            r.require(isinstance(drv, str) and drv.startswith("/nix/store/") and drv.endswith(".drv"), "build")
            shown = json.loads(r.read_command(["nix", "derivation", "show", drv]))
            for path in output_paths(shown):
                r.require(not Path(path).exists(), "fresh_cached")
    except r.Stop:
        raise
    except (OSError, ValueError, TypeError, KeyError, StopIteration):
        raise r.Stop("fresh_metadata") from None
    return {"passed": True, "stage": "fresh_output_guard"}


if __name__ == "__main__":
    try:
        r.require(len(sys.argv) <= 2, "arguments")
        print(json.dumps(fresh(sys.argv[1] if len(sys.argv) == 2 else ".")))
    except Exception as error:
        print(json.dumps(r.diagnostic(error)))
        sys.exit(1)
