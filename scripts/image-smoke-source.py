#!/usr/bin/env python3
"""Freeze declared public source without staging unborn/untracked Git files.
Never copy .git, .devenv, env, private state, credentials or arbitrary files.
Use path:<snapshot> for Nix, NOT path:<dirty checkout> (which can import extras).
"""
import hashlib
import json
import shutil
import sys
from pathlib import Path

import release as r

r.CODES.update({
    "source_snapshot": ("local_source", "stable_declared_public_inputs", "Select a new empty snapshot outside the checkout and wait for source edits to finish. No Git files were staged or committed."),
})


def inputs(root):
    files = [root / name for name in ("flake.nix", "flake.lock", "go.mod", "go.sum")]
    for folder, extensions in (("cmd", {".go"}), ("internal", {".go"}), ("nix", {".nix", ".go"}),
                               ("scripts", {".py"}), (".github/workflows", {".yml"})):
        base = root / folder
        r.require(not base.is_symlink(), "source_snapshot")
        for path in base.rglob("*"):
            r.require(not path.is_symlink(), "source_snapshot")
            if path.is_file() and path.suffix in extensions:
                files.append(path)
    r.require(all(path.is_file() and not path.is_symlink() for path in files), "source_snapshot")
    return sorted(files)


def snapshot(root, destination):
    root, destination = root.resolve(), destination.resolve()
    r.require(destination != root and root not in destination.parents
              and not destination.exists(), "source_snapshot")
    created = False
    try:
        files = inputs(root)
        destination.mkdir(parents=True, mode=0o700)
        created = True
        hashes = {}
        for path in files:
            relative = path.relative_to(root)
            raw = path.read_bytes()
            target = destination / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(raw)
            hashes[str(relative)] = hashlib.sha256(raw).hexdigest()
        r.require(files == inputs(root), "source_snapshot")
        for path in files:
            r.require(hashlib.sha256(path.read_bytes()).hexdigest() == hashes[str(path.relative_to(root))], "source_snapshot")
        canonical = json.dumps(hashes, sort_keys=True, separators=(",", ":")).encode()
        identity = hashlib.sha256(canonical).hexdigest()
        manifest = {"source_identity": identity, "files": hashes, "git_commit": "unavailable"}
        (destination / "source-manifest.json").write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n")
        return {"stage": "local_source", "source_identity": identity, "git_commit": "unavailable"}
    except Exception as error:
        # Only newly-created disposable snapshot files, never caller/source files.
        if created:
            shutil.rmtree(destination, ignore_errors=True)
        if isinstance(error, r.Stop):
            raise
        raise r.Stop("source_snapshot") from None


if __name__ == "__main__":
    try:
        r.require(len(sys.argv) == 2, "arguments")
        print(json.dumps(snapshot(Path(__file__).resolve().parents[1], Path(sys.argv[1]))))
    except Exception as error:
        print(json.dumps(r.diagnostic(error)))
        sys.exit(1)
