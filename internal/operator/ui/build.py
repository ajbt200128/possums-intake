#!/usr/bin/env python3
"""Rebuild pinned upstream UI, using only a disposable project-local npm tree.
Run from the checkout with the pinned Node/npm and Go available. No Git/network
source checkout, global install, runtime keys, telemetry or image build.
"""
import hashlib
import json
import os
import re
import shutil
import subprocess
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]


# No inherited PB/Vite/npm/OTel/configuration/credential variables. Version
# probes and cached-source lookup use only these explicit host/tool fields.
TOOL_ENV = {"PATH": os.environ.get("PATH", ""), "HOME": str(Path.home()),
            "GOENV": "off", "GOTOOLCHAIN": "local", "LC_ALL": "C", "CI": "true"}


class BuildFailure(Exception):
    """Only locally authored, content-free recipe failures may be displayed."""


def command(args, cwd, env=None):
    result = subprocess.run(args, cwd=cwd, capture_output=True, env=env or TOOL_ENV)
    if result.returncode:
        stage = {"node": "node_version", "npm": "npm_command", "go": "module_cache", "patch": "source_patch"}.get(args[0], "local_command")
        raise BuildFailure(f"OPERATOR_UI_BUILD_UNAVAILABLE; stage={stage}; status={result.returncode}; inspect pinned inputs privately; output withheld")
    return result.stdout.decode().strip()


def license_inventory(work, pin):
    out = work / "dist/licenses"
    out.mkdir()
    inventory = []
    sources = json.loads((HERE / "licenses/sources.json").read_text())
    for name, item in sorted(sources.items()):
        raw = (HERE / "licenses" / name).read_bytes()
        if hashlib.sha256(raw).hexdigest() != item["sha256"]:
            raise BuildFailure("OPERATOR_UI_LICENSE_MISMATCH; restore reviewed license inputs")
        (out / name).write_bytes(raw)
        inventory.append({"component": name, "origin": item["origin"], "license_file": "licenses/" + name,
                          "sha256": item["sha256"], "scope": "static_upstream_library_or_font"})
    raw = (HERE / "LICENSE.pocketbase.txt").read_bytes()
    (out / "pocketbase.txt").write_bytes(raw)
    inventory.append({"component": "PocketBase UI and upstream graphical assets", "version": pin["version"],
                      "origin": pin["module"], "license_file": "licenses/pocketbase.txt",
                      "sha256": hashlib.sha256(raw).hexdigest(), "scope": "upstream_source_and_assets"})
    bundled = json.loads((work / ".bundled-packages.json").read_text())
    # Vite's generated modulepreload helper is emitted, not a chunk module path.
    if bundled != ["leaflet", "pocketbase"]:
        raise BuildFailure("OPERATOR_UI_BUNDLED_DEPENDENCY_CHANGED; review full bundled license closure")
    for package in [*bundled, "vite"]:
        folder = work / "node_modules" / package
        metadata = json.loads((folder / "package.json").read_text())
        files = [p for p in folder.iterdir() if p.is_file() and p.name.upper().startswith(("LICENSE", "LICENCE", "COPYING", "NOTICE"))]
        if not files:
            raise BuildFailure("OPERATOR_UI_REQUIRED_LICENSE_MISSING; inspect pinned package privately")
        for path in sorted(files):
            name = "npm-" + package + "-" + path.name
            raw = path.read_bytes()
            (out / name).write_bytes(raw)
            inventory.append({"component": package, "version": metadata["version"], "license": metadata.get("license"),
                              "origin": "pinned package-lock.json", "license_file": "licenses/" + name,
                              "sha256": hashlib.sha256(raw).hexdigest(),
                              "scope": "bundled_package" if package in bundled else "generated_modulepreload_helper"})
    notice = (HERE / "licenses/NOTICE.txt").read_bytes()
    (out / "NOTICE.txt").write_bytes(notice)
    return inventory


def build():
    try:
        pin = json.loads((HERE / "source.json").read_text())
    except (OSError, ValueError):
        raise BuildFailure("OPERATOR_UI_PIN_UNAVAILABLE; restore the reviewed source manifest") from None
    cache = Path(command(["go", "env", "GOMODCACHE"], ROOT))
    upstream = cache / (pin["module"] + "@" + pin["version"]) / "ui"
    files = {str(p.relative_to(upstream)): p.read_bytes() for p in upstream.rglob("*") if p.is_file() and "dist" not in p.relative_to(upstream).parts}
    hashes = {n: hashlib.sha256(b).hexdigest() for n, b in sorted(files.items())}
    digest = hashlib.sha256(json.dumps(hashes, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    if digest != pin["source_tree_sha256"] or hashes.get("package-lock.json") != pin["package_lock_sha256"]:
        raise BuildFailure("OPERATOR_UI_SOURCE_MISMATCH; use the cached checksum-qualified PocketBase v0.40.5 module")
    with tempfile.TemporaryDirectory(prefix=".operator-ui-build-", dir=ROOT) as directory:
        work = Path(directory)
        home, temporary = work / ".home", work / ".tmp"
        home.mkdir()
        temporary.mkdir()
        empty_config = work / ".empty-npmrc"
        global_config = work / ".empty-global-npmrc"
        empty_config.write_text("")
        global_config.write_text("")
        env = {**TOOL_ENV, "HOME": str(home), "TMPDIR": str(temporary),
               "NPM_CONFIG_USERCONFIG": str(empty_config), "NPM_CONFIG_GLOBALCONFIG": str(global_config)}
        if command(["node", "--version"], ROOT, env) != pin["node"] or command(["npm", "--version"], ROOT, env) != pin["npm"]:
            raise BuildFailure("OPERATOR_UI_TOOLCHAIN_MISMATCH; use the recorded Node/npm without upgrades")
        for name, raw in files.items():
            target = work / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(raw)
        command(["patch", "-s", "-p1", "-i", str(HERE / "upstream.patch")], work)
        # Source-input transforms, not post-build bundle editing. The verified
        # source tree bounds these mechanical patches. Include vendored browser
        # libs (Shablon otherwise logs render exceptions with user values).
        for path in sorted(work.rglob("*.js")):
            text = path.read_text()
            text = re.sub(r"console\.(?:warn|log|error|info|debug|trace)\b", "(() => {})", text)
            if "src" in path.relative_to(work).parts:
                text = text.replace("window?.localStorage?", "app.operatorStorage").replace("window.localStorage", "app.operatorStorage").replace("localStorage.", "app.operatorStorage.")
            path.write_text(text)
        # npm's cache and dependencies never leave the project-local temp tree.
        command(["npm", "ci", "--cache", str(work / ".npm-cache"), "--no-audit", "--no-fund"], work, env)
        # Pinned SDK distribution is a Vite source input; it contains raw
        # console.warn fallbacks too. Patch before bundling, never afterward.
        for package in ("pocketbase", "leaflet"):
            for path in sorted((work / "node_modules" / package / "dist").glob("*")):
                if path.suffix in {".js", ".mjs", ".cjs"}:
                    path.write_text(re.sub(r"console\.(?:warn|log|error|info|debug|trace)\b", "(() => {})", path.read_text()))
        config = work / "vite.config.js"
        config.write_text('import { writeFileSync } from "node:fs";\n' + config.read_text().replace(
            '    envPrefix: "PB",',
            '''    envPrefix: "PB",
    plugins: [{ name: "intake-license-inventory", generateBundle(_, bundle) {
        const packages = new Set();
        for (const chunk of Object.values(bundle)) {
            for (const id of Object.keys(chunk.modules || {})) {
                const match = id.match(/node_modules\\/(?:(@[^/]+\\/[^/]+)|([^/]+))\\//);
                if (match) packages.add(match[1] || match[2]);
            }
        }
        writeFileSync(".bundled-packages.json", JSON.stringify([...packages].sort()));
    } }],'''))
        command(["npm", "run", "build"], work, env)
        inventory = license_inventory(work, pin)
        (work / "dist/licenses/inventory.json").write_text(json.dumps(inventory, sort_keys=True, indent=2) + "\n")
        assets = {str(p.relative_to(work / "dist")): p.read_bytes() for p in (work / "dist").rglob("*") if p.is_file()}
        # Fail rather than accidentally ship source maps or an unexpected SDK
        # console fallback. Documentation example strings are also removed.
        if any(n.endswith(".map") for n in assets) or any(re.search(rb"console\.(warn|log|error|info|debug|trace)\b", b) for n, b in assets.items() if n.endswith(".js")):
            raise BuildFailure("OPERATOR_UI_ASSET_PRIVACY_INVALID; inspect console/source-map boundary privately")
        out = HERE / "dist"
        if out.exists():
            try:
                shutil.rmtree(out)
            except OSError:
                raise BuildFailure("OPERATOR_UI_ASSET_WRITE_UNAVAILABLE; preserve existing assets and inspect local permissions privately") from None
        for name, raw in assets.items():
            target = out / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(raw)
        manifest = {n: hashlib.sha256(b).hexdigest() for n, b in sorted(assets.items())}
        (HERE / "assets.json").write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n")
        (HERE / "licenses.json").write_text(json.dumps(inventory, sort_keys=True, indent=2) + "\n")
        print("OPERATOR_UI_BUILD_COMPLETE; pinned source; local temporary dependencies removed on exit")


if __name__ == "__main__":
    try:
        build()
    except BuildFailure as error:
        print(str(error))
        raise SystemExit(1) from None
    except Exception:
        print("OPERATOR_UI_BUILD_UNAVAILABLE; stage=local_build; source/toolchain/asset constraint unknown; inspect privately; no image built")
        raise SystemExit(1) from None
