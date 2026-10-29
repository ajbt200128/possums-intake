#!/usr/bin/env python3
"""Offline contract for future intake-owned public releases, not an executor.

Injected repository/manifest evidence is NOT authenticated or cryptographically
verified here. No network, subprocess, environment, publication or deploy adapter.
Legacy private-source publication remains separate and unchanged.
"""
import base64
import hashlib
import json
import os
import sys
from contextlib import suppress
from pathlib import Path

import release as r

OWNER = "ajbt200128"
REPOSITORY = OWNER + "/possums-intake"
PACKAGE = "possums-intake-synthetic"
IMAGE = "ghcr.io/" + OWNER + "/" + PACKAGE
PHASE = "persistent-live-stripe-serve"
TEMPLATE = "nix/persistent-live-stripe-egress-serve-config.yml.in"
TEMPLATE_SHA256 = "f135d7d6cafa2ddda365a9258a75a380861e6dc2a7b7b72a21a6051627eb56ef"
CVM = "0.14.14"
# Public vendor pins retained from the qualified deploy-repository contract.
CVM_HASHES = {
    "version": "v0.14.14",
    "root": "41068e0738e14ee74d5b987586bdfc261d7bdd59e156dd6818154fd188b8ee7b",
    "kernel": "91b9c7979e00d4703cbabf9f6872639d23b1b60df88b971a7d49f11603ba4346",
    "initrd": "422948208e9d9aeddab46968ec7145eb9f0bf9dc82e9b6d0838cbf06995baada",
    "raw": "9634e66fe8761d227b2f39164dd13fbe54c3f1337b808287ce4a3477ee6d83a1",
}
FIRMWARE_HASH = "78c890175928167a1bc095d4bf3bb4ad81de80cd7e4e9e683477fc359119c1c4"
MANIFEST_LIMIT = 512 * 1024
r.CODES.update({
    "public_input": ("public_release_input", "offline_status_or_explicit_serve_render",
        "Use status or render persistent-live-stripe-serve with a non-placeholder image digest. No delivery was attempted."),
    "public_repository": ("public_repository_evidence", "fixed_public_source_and_package_identity",
        "Check public source visibility/identity and independent fixed public package identity. Unexpected evidence failure cause is unknown; no authenticated evidence was acquired or delivery attempted."),
    "public_config": ("public_config_render", "hash_fixed_existing_only_serve_and_vendor_pin",
        "Check the unchanged serving template, explicit phase, image digest and pinned CVM. Unexpected render failure cause is unknown; do not initialize or repair state."),
    "public_manifest": ("public_manifest_validation", "exact_config_vendor_hashes_and_four_disk_shape",
        "Check the exact manifest/config, vendor pins and four-disk shape. Unexpected validation failure cause is unknown; this check is not signature or runtime verification."),
    "public_command": ("public_release_presentation", "offline_result_delivery",
        "The offline command could not deliver its result; underlying cause is unknown. Inspect the local tool without publishing or deploying anything."),
})


def checked(code, operation):
    try:
        return operation()
    except r.Stop:
        raise
    except (Exception, KeyboardInterrupt):
        raise r.Stop(code) from None


def repository_gate(repository, package):
    """Validate injected metadata only; caller must later authenticate its source."""
    def validate():
        r.require(type(repository) is dict and type(package) is dict, "public_repository")
        repository_id, package_id = repository.get("id"), package.get("id")
        r.require(type(repository_id) is int and repository_id > 0
                  and repository.get("full_name") == REPOSITORY
                  and repository.get("name") == "possums-intake"
                  and repository.get("owner", {}).get("login") == OWNER
                  and repository.get("private") is False
                  and repository.get("default_branch") == "main", "public_repository")
        # The package's optional source association is not publication authority.
        # Authenticate source/build provenance separately; never read this projection.
        r.require(type(package_id) is int and package_id > 0
                  and package.get("name") == PACKAGE and package.get("package_type") == "container"
                  and package.get("owner", {}).get("login") == OWNER
                  and package.get("visibility") == "public", "public_repository")
    checked("public_repository", validate)


def render(template, image, *, phase, cvm=CVM):
    """Return public config bytes, never write an active config or select a boot."""
    def validate():
        r.require(type(phase) is str and phase == PHASE and cvm == CVM
                  and type(template) is bytes
                  and hashlib.sha256(template).hexdigest() == TEMPLATE_SHA256
                  and r.pin(image, r.DIGEST) and image != "sha256:" + "0" * 64,
                  "public_config")
        r.require(template.count(b"@CVM_PIN@") == template.count(b"@IMAGE_DIGEST@") == 1,
                  "public_config")
        return template.replace(b"@CVM_PIN@", CVM.encode()).replace(b"@IMAGE_DIGEST@", image.encode())
    return checked("public_config", validate)


def unique_object(pairs):
    value = {}
    for key, item in pairs:
        r.require(key not in value, "public_manifest")
        value[key] = item
    return value


def reject_constant(_value):
    raise r.Stop("public_manifest")


def manifest_gate(raw, config, *, template, image, phase):
    """Structural binding only, not attestation, build or deployment admission."""
    def validate():
        r.require(config == render(template, image, phase=phase), "public_manifest")
        r.require(type(raw) is bytes and 0 < len(raw) <= MANIFEST_LIMIT, "public_manifest")
        try:
            value = json.loads(raw, object_pairs_hook=unique_object, parse_constant=reject_constant)
        except (ValueError, TypeError, RecursionError):
            raise r.Stop("public_manifest") from None
        r.require(type(value) is dict and set(value) == {
            "snp_measurement", "tdx_measurement", "firmware", "vm_shape", "cmdline", "hashes", "config"
        }, "public_manifest")
        r.require(value["config"] == base64.b64encode(config).decode(), "public_manifest")
        shape = value["vm_shape"]
        r.require(type(shape) is dict and shape == {"cpus": 2, "memory_mb": 8192, "gpus": 0, "disks": 4}
                  and all(type(item) is int for item in shape.values()), "public_manifest")
        r.require(value["hashes"] == CVM_HASHES, "public_manifest")
        expected = ("readonly=on pci=realloc,nocrs modprobe.blacklist=nouveau nouveau.modeset=0 "
                    "root=/dev/mapper/root roothash=" + CVM_HASHES["root"]
                    + " tinfoil-config-hash=" + hashlib.sha256(config).hexdigest())
        r.require(value["cmdline"] == expected and r.pin(value["snp_measurement"], r"[0-9a-f]{96}"),
                  "public_manifest")
        tdx = value["tdx_measurement"]
        r.require(type(tdx) is dict and {"rtmr1", "rtmr2"} <= set(tdx)
                  and all(r.pin(key, r"(mrtd|rtmr[0-3])") and r.pin(item, r"[0-9a-fA-F]{96}")
                          for key, item in tdx.items()), "public_manifest")
        r.require(value["firmware"] == {"sev_snp": {
            "type": "ovmf", "version": "v0.0.4", "sha256": FIRMWARE_HASH
        }}, "public_manifest")
        return value
    return checked("public_manifest", validate)


def read_template():
    def read():
        root = Path(__file__).resolve().parents[1]
        path = root / TEMPLATE
        r.require(not (root / "nix").is_symlink() and not path.is_symlink() and path.is_file(), "public_config")
        with path.open("rb") as stream:
            data = stream.read(8193)
        r.require(len(data) <= 8192, "public_config")
        return data
    return checked("public_config", read)


def emit(stream, text):
    try:
        stream.write(text)
        stream.flush()
    except (Exception, KeyboardInterrupt):
        # A failed buffered stream would otherwise flush again at interpreter
        # shutdown, outside this boundary, emitting a raw BrokenPipeError.
        # Non-file test/host streams may not expose a descriptor.
        with suppress(Exception, KeyboardInterrupt):
            descriptor = stream.fileno()
            sink = os.open(os.devnull, os.O_WRONLY)
            try:
                os.dup2(sink, descriptor)
            finally:
                # A closed target can itself be the next allocated descriptor.
                if sink != descriptor:
                    os.close(sink)
        raise r.Stop("public_command") from None


def main(args):
    """The only CLI operations are static status and stdout-only config rendering."""
    def execute():
        if args == ["status"]:
            return json.dumps({"stage": "offline_public_release_contract", "execution_implemented": False,
                               "authenticated_evidence": False, "runtime_verified": False}) + "\n"
        r.require(len(args) == 3 and args[0] == "render", "public_input")
        # Reject arguments before opening even the fixed local public template.
        r.require(args[1] == PHASE and r.pin(args[2], r.DIGEST)
                  and args[2] != "sha256:" + "0" * 64, "public_input")
        return render(read_template(), args[2], phase=args[1]).decode()
    primary = None
    try:
        text = checked("public_command", execute)
    except r.Stop as error:
        primary = error
        text = json.dumps(r.diagnostic(error)) + "\n"
    try:
        emit(sys.stdout, text)
    except r.Stop as error:
        # One best-effort report on the other stream, never retry the result.
        # A later presentation fault must not erase a known validation failure.
        with suppress(r.Stop):
            emit(sys.stderr, json.dumps(r.diagnostic(primary or error)) + "\n")
        return 1
    return 1 if primary else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
