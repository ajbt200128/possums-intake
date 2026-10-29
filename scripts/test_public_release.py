#!/usr/bin/env python3
"""Synthetic offline public-contract tests. No credentials or delivery adapters."""
import base64
import contextlib
import copy
import hashlib
import importlib.util
import io
import json
import os
import socket
import subprocess
import sys
import tempfile
import unittest
import urllib.request
from pathlib import Path
from unittest.mock import patch

import release as r


def load_tool(filename, name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).parent / filename)
    if spec is None or spec.loader is None:
        raise AssertionError("fixture module unavailable")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


public = load_tool("public-release.py", "intake_public_release_contract_tests")
snapshot = load_tool("image-smoke-source.py", "intake_public_release_snapshot_tests")
ROOT = Path(__file__).resolve().parents[1]
TEMPLATE = (ROOT / public.TEMPLATE).read_bytes()
IMAGE = "sha256:" + "a" * 64
HOSTILE = "hostile-credential-body-url-sentinel"


def metadata():
    repository = {"id": 17, "name": "possums-intake", "full_name": public.REPOSITORY,
                  "owner": {"login": public.OWNER}, "private": False, "default_branch": "main"}
    package = {"id": 23, "name": public.PACKAGE, "package_type": "container",
               "owner": {"login": public.OWNER}, "visibility": "public", "repository": copy.deepcopy(repository)}
    return repository, package


def manifest(config):
    return {"config": base64.b64encode(config).decode(), "snp_measurement": "1" * 96,
            "tdx_measurement": {"mrtd": "2" * 96, "rtmr1": "3" * 96, "rtmr2": "4" * 96},
            "vm_shape": {"cpus": 2, "memory_mb": 8192, "gpus": 0, "disks": 4},
            "hashes": dict(public.CVM_HASHES),
            "firmware": {"sev_snp": {"type": "ovmf", "version": "v0.0.4", "sha256": public.FIRMWARE_HASH}},
            "cmdline": "readonly=on pci=realloc,nocrs modprobe.blacklist=nouveau nouveau.modeset=0 "
                       "root=/dev/mapper/root roothash=" + public.CVM_HASHES["root"]
                       + " tinfoil-config-hash=" + hashlib.sha256(config).hexdigest()}


class PublicReleaseTests(unittest.TestCase):
    def stop(self, code, operation):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err), self.assertRaises(r.Stop) as caught:
            operation()
        self.assertEqual(caught.exception.code, code)
        diagnostic = r.diagnostic(caught.exception)
        self.assertEqual(diagnostic["code"], "DELIVERY_" + code.upper())
        self.assertEqual(diagnostic["outcome"], "no_effect")
        self.assertNotIn(HOSTILE, json.dumps(diagnostic))
        self.assertEqual((out.getvalue(), err.getvalue()), ("", ""))
        return diagnostic

    def test_fixed_template_identity_and_existing_only_serving(self):
        self.assertEqual(hashlib.sha256(TEMPLATE).hexdigest(), public.TEMPLATE_SHA256)
        config = public.render(TEMPLATE, IMAGE, phase="persistent-live-stripe-serve")
        self.assertEqual(config, TEMPLATE.replace(b"@CVM_PIN@", b"0.14.14").replace(b"@IMAGE_DIGEST@", IMAGE.encode()))
        self.assertIn(b"command: ['serve', '--state', '/state/ledger', '--tinfoil-volume', 'ledger'", config)
        self.assertIn(b"read_only: true", config)
        self.assertIn(b"restart: 'no'", config)
        self.assertIn(b"'--operator', '--stripe-live'", config)
        self.assertIn(b"public-egress:\n    egress: open", config)
        self.assertIn(b"networks: [public-egress]", config)
        self.assertIn(b"OTEL_SDK_DISABLED: 'false'", config)
        self.assertIn(b"STRIPE_LIVE_TAX_CODE: 'txcd_10103000'", config)
        self.assertIn(b"STRIPE_RETURN_ORIGIN: 'https://possums.dev'", config)
        self.assertEqual(config.split(b"    secrets:\n")[1].splitlines(), [
            b"      - LEDGER_VOLUME_KEY_B64", b"      - LEDGER_ADMIN_KEY",
            b"      - INTAKE_METRICS_HONEYCOMB_API_KEY", b"      - STRIPE_LIVE_API_KEY",
            b"      - STRIPE_LIVE_WEBHOOK_SECRET"])
        for prohibited in (b"LEDGER_OPERATOR_PASSWORD", b"LEDGER_OPERATOR_EMAIL", b"STARTUP_DIAGNOSTICS",
                           b"ledger-replace", b"ledger-init", b"initialize", b"@CVM_PIN@", b"@IMAGE_DIGEST@"):
            self.assertNotIn(prohibited, config)

    def test_live_serving_and_configuration_substitution_refuse(self):
        mutations = ((b", '--stripe-live'", b""), (b"networks: [public-egress]", b"networks: []"),
                     (b"egress: open", b"egress: closed"), (b"OTEL_SDK_DISABLED: 'false'", b"OTEL_SDK_DISABLED: 'true'"),
                     (b"STRIPE_LIVE_TAX_CODE: 'txcd_10103000'", b"STRIPE_LIVE_TAX_CODE: 'other'"),
                     (b"https://possums.dev", b"https://other.invalid"),
                     (b"      - STRIPE_LIVE_WEBHOOK_SECRET", b"      - UNAPPROVED_SECRET"),
                     (b"['serve'", b"['live-payments-init'"))
        for old, new in mutations:
            with self.subTest(field=old):
                changed = TEMPLATE.replace(old, new)
                self.assertNotEqual(changed, TEMPLATE)
                self.stop("public_config", lambda changed=changed: public.render(changed, IMAGE, phase=public.PHASE))

    def test_render_refuses_other_phases_template_mutations_and_placeholders(self):
        for phase in (None, True, {}, "", "disposable", "persistent-serve", "persistent-bootstrap", "persistent-ledger-init",
                      "persistent-ledger-replace", "persistent-website-init", "persistent-ledger-upgrade",
                      "persistent-diagnostic-serve", HOSTILE):
            with self.subTest(phase_type=type(phase).__name__):
                self.stop("public_config", lambda phase=phase: public.render(TEMPLATE, IMAGE, phase=phase))
        for image in (None, True, {}, HOSTILE, "sha256:" + "0" * 64, "sha256:" + "A" * 64, IMAGE + "\n"):
            self.stop("public_config", lambda image=image: public.render(TEMPLATE, image, phase=public.PHASE))
        for template in (TEMPLATE + b"\n", TEMPLATE.replace(b"['serve'", b"['init'"), TEMPLATE.decode(), None):
            self.stop("public_config", lambda template=template: public.render(template, IMAGE, phase=public.PHASE))
        for cvm in ("0.14.12", "0.14.14-rc1", "v0.14.14", None, HOSTILE):
            self.stop("public_config", lambda cvm=cvm: public.render(TEMPLATE, IMAGE, phase=public.PHASE, cvm=cvm))

    def test_only_public_repository_and_public_matching_package_are_accepted(self):
        repository, package = metadata()
        self.assertIsNone(public.repository_gate(repository, package))
        for visibility in (True, None, "false", 0, 1):
            changed = copy.deepcopy(repository)
            changed["private"] = visibility
            self.stop("public_repository", lambda changed=changed: public.repository_gate(changed, package))
        for field in repository:
            changed = copy.deepcopy(repository)
            del changed[field]
            self.stop("public_repository", lambda changed=changed: public.repository_gate(changed, package))
        for field, value in (("id", True), ("id", 0), ("id", "17"), ("name", HOSTILE),
                             ("full_name", "ajbt200128/possums-intake-deploy"), ("default_branch", "feature"),
                             ("owner", {"login": HOSTILE}), ("owner", None)):
            changed = copy.deepcopy(repository)
            changed[field] = value
            self.stop("public_repository", lambda changed=changed: public.repository_gate(changed, package))
        for field, value in (("id", True), ("id", 0), ("id", "23"), ("name", HOSTILE),
                             ("package_type", HOSTILE), ("visibility", "private"), ("owner", None)):
            changed = copy.deepcopy(package)
            changed[field] = value
            self.stop("public_repository", lambda changed=changed: public.repository_gate(repository, changed))
        for bad in (None, [], HOSTILE):
            self.stop("public_repository", lambda bad=bad: public.repository_gate(bad, package))
            self.stop("public_repository", lambda bad=bad: public.repository_gate(repository, bad))

    def test_optional_package_repository_projection_is_never_inspected(self):
        class UnreadableAssociation(dict):
            def get(self, key, *default):
                raise AssertionError("optional source association must not be read")

            def __getitem__(self, key):
                raise AssertionError("optional source association must not be read")

        repository, package = metadata()
        package.pop("repository")
        self.assertIsNone(public.repository_gate(repository, package))
        for projection in (None, True, HOSTILE, [], {}, {"private": True},
                           {"id": True, "full_name": HOSTILE, "private": False},
                           UnreadableAssociation()):
            package["repository"] = projection
            self.assertIsNone(public.repository_gate(repository, package))
        changed = dict(repository, private=True)
        self.stop("public_repository", lambda: public.repository_gate(changed, package))

    def check_manifest(self, raw, config=None, **kwargs):
        config = config if config is not None else public.render(TEMPLATE, IMAGE, phase=public.PHASE)
        return public.manifest_gate(raw, config, template=TEMPLATE, image=IMAGE, phase=public.PHASE, **kwargs)

    def test_exact_manifest_binding_is_structural_not_crypto_evidence(self):
        config = public.render(TEMPLATE, IMAGE, phase=public.PHASE)
        value = manifest(config)
        self.assertEqual(self.check_manifest(json.dumps(value).encode()), value)
        self.assertNotIn("runtime_verified", value)
        self.stop("public_manifest", lambda: self.check_manifest(json.dumps(value).encode(), config + b"\n"))
        other = manifest(public.render(TEMPLATE, "sha256:" + "b" * 64, phase=public.PHASE))
        self.stop("public_manifest", lambda: self.check_manifest(json.dumps(other).encode()))

    def test_manifest_refuses_each_wrong_vendor_hash_and_shape(self):
        config = public.render(TEMPLATE, IMAGE, phase=public.PHASE)
        value = manifest(config)
        for field in public.CVM_HASHES:
            changed = copy.deepcopy(value)
            changed["hashes"][field] = "f" * 64
            if field == "root":
                changed["cmdline"] = changed["cmdline"].replace(public.CVM_HASHES["root"], "f" * 64)
            self.stop("public_manifest", lambda changed=changed: self.check_manifest(json.dumps(changed).encode()))
        for field in value["vm_shape"]:
            for replacement in (None, 1.0, False, "4", 3):
                changed = copy.deepcopy(value)
                changed["vm_shape"][field] = replacement
                self.stop("public_manifest", lambda changed=changed: self.check_manifest(json.dumps(changed).encode()))
        for field, replacement in (("config", HOSTILE), ("cmdline", value["cmdline"] + " " + HOSTILE),
                                   ("firmware", {}), ("tdx_measurement", {}),
                                   ("tdx_measurement", {"rtmr1": "1" * 96, "rtmr2": HOSTILE}),
                                   ("tdx_measurement", {"rtmr1": "1" * 96, "rtmr2": "2" * 96, HOSTILE: "3" * 96}),
                                   ("snp_measurement", HOSTILE), ("extra", HOSTILE)):
            changed = copy.deepcopy(value)
            changed[field] = replacement
            self.stop("public_manifest", lambda changed=changed: self.check_manifest(json.dumps(changed).encode()))
        for field in value:
            changed = copy.deepcopy(value)
            del changed[field]
            self.stop("public_manifest", lambda changed=changed: self.check_manifest(json.dumps(changed).encode()))

    def test_malformed_duplicate_nonfinite_and_oversized_json_refuses_without_echo(self):
        config = public.render(TEMPLATE, IMAGE, phase=public.PHASE)
        valid = json.dumps(manifest(config)).encode()
        duplicates = (valid[:-1] + b',"config":"' + HOSTILE.encode() + b'"}',
                      valid.replace(b'"disks": 4', b'"disks": 4, "disks": 4'))
        for raw in (b"", b"\xff", HOSTILE.encode(), b"null", b"[]", b"NaN", b"Infinity", valid + valid,
                    b" " * (public.MANIFEST_LIMIT + 1), valid.decode(), *duplicates):
            self.stop("public_manifest", lambda raw=raw: self.check_manifest(raw))

    def test_unexpected_failures_preserve_stage_and_suppress_hostile_exception(self):
        with patch.object(public.hashlib, "sha256", side_effect=OSError(HOSTILE)):
            diag = self.stop("public_config", lambda: public.render(TEMPLATE, IMAGE, phase=public.PHASE))
        self.assertIn("unknown", str(diag["next_step"]))
        with patch.object(public.json, "loads", side_effect=RuntimeError(HOSTILE)):
            diag = self.stop("public_manifest", lambda: self.check_manifest(b"{}"))
        self.assertIn("unknown", str(diag["next_step"]))
        repository, package = metadata()
        repository["owner"] = None
        self.stop("public_repository", lambda: public.repository_gate(repository, package))

    def test_import_status_render_and_refusals_do_not_initiate_work(self):
        with patch.object(socket, "socket", side_effect=AssertionError(HOSTILE)) as network, \
                patch.object(urllib.request, "urlopen", side_effect=AssertionError(HOSTILE)) as http, \
                patch.object(subprocess, "Popen", side_effect=AssertionError(HOSTILE)) as process:
            tool = load_tool("public-release.py", "intake_public_release_side_effect_test")
            for args in (["status"], [], ["publish"], ["deploy"], ["render", HOSTILE, IMAGE],
                         ["render", public.PHASE, "sha256:" + "0" * 64]):
                out, err = io.StringIO(), io.StringIO()
                with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err), \
                        patch.object(tool, "read_template", side_effect=AssertionError(HOSTILE)) as read:
                    result = tool.main(args)
                read.assert_not_called()
                self.assertEqual(result, 0 if args == ["status"] else 1)
                value = json.loads(out.getvalue())
                self.assertNotIn(HOSTILE, out.getvalue())
                self.assertEqual(err.getvalue(), "")
                if result == 0:
                    self.assertIs(value["execution_implemented"], False)
                    self.assertIs(value["authenticated_evidence"], False)
                    self.assertIs(value["runtime_verified"], False)
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                self.assertEqual(tool.main(["render", public.PHASE, IMAGE]), 0)
            self.assertEqual(out.getvalue().encode(), public.render(TEMPLATE, IMAGE, phase=public.PHASE))
            network.assert_not_called()
            http.assert_not_called()
            process.assert_not_called()

    def test_template_read_and_output_failures_are_closed_without_second_report(self):
        with patch.object(public.Path, "open", side_effect=OSError(HOSTILE)):
            self.stop("public_config", public.read_template)
        for method in ("write", "flush"):
            out, err = io.StringIO(), io.StringIO()
            with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err), \
                    patch.object(out, method, side_effect=OSError(HOSTILE)) as output:
                self.assertEqual(public.main(["status"]), 1)
            self.assertEqual(output.call_count, 1)
            self.assertNotIn(HOSTILE, err.getvalue())
            self.assertEqual(json.loads(err.getvalue())["code"], "DELIVERY_PUBLIC_COMMAND")

    def test_real_cli_closed_pipes_never_leak_shutdown_exceptions(self):
        cases = ((["status"], "DELIVERY_PUBLIC_COMMAND"),
                 (["render", public.PHASE, IMAGE], "DELIVERY_PUBLIC_COMMAND"),
                 ([HOSTILE], "DELIVERY_PUBLIC_INPUT"))
        for args, code in cases:
            for close_stderr in (False, True):
                read_fd, write_fd = os.pipe()
                os.close(read_fd)
                with os.fdopen(write_fd, "wb") as output:
                    result = subprocess.run([sys.executable, str(ROOT / "scripts/public-release.py"), *args],
                                            stdin=subprocess.DEVNULL, stdout=output,
                                            stderr=output if close_stderr else subprocess.PIPE,
                                            timeout=10, check=False)
                self.assertEqual(result.returncode, 1)
                if not close_stderr:
                    self.assertNotIn(HOSTILE.encode(), result.stderr)
                    self.assertEqual(json.loads(result.stderr)["code"], code)
                    self.assertNotIn(b"Exception ignored", result.stderr)
                    self.assertNotIn(b"BrokenPipeError", result.stderr)

    def test_already_closed_descriptors_do_not_reclose_replacement_or_leak_shutdown_errors(self):
        wrapper = (
            "import os,runpy,sys\n"
            "close_stderr=sys.argv[1]=='both'\n"
            "script=sys.argv[2]\n"
            "sys.path.insert(0,os.path.dirname(script))\n"
            "sys.argv=sys.argv[2:]\n"
            "os.close(1)\n"
            "if close_stderr: os.close(2)\n"
            "runpy.run_path(script,run_name='__main__')\n"
        )
        cases = ((["status"], "DELIVERY_PUBLIC_COMMAND"),
                 (["render", public.PHASE, IMAGE], "DELIVERY_PUBLIC_COMMAND"),
                 ([HOSTILE], "DELIVERY_PUBLIC_INPUT"))
        for args, code in cases:
            for closed in ("stdout", "both"):
                with self.subTest(args=args[0], closed=closed):
                    result = subprocess.run(
                        [sys.executable, "-c", wrapper, closed, str(ROOT / "scripts/public-release.py"), *args],
                        stdin=subprocess.DEVNULL, capture_output=True, timeout=10, check=False)
                    self.assertEqual(result.returncode, 1)
                    self.assertEqual(result.stdout, b"")
                    self.assertNotIn(b"Exception ignored", result.stderr)
                    self.assertNotIn(b"Bad file descriptor", result.stderr)
                    self.assertNotIn(HOSTILE.encode(), result.stderr)
                    if closed == "stdout":
                        self.assertEqual(json.loads(result.stderr)["code"], code)
                    else:
                        self.assertEqual(result.stderr, b"")

    def test_frozen_source_requires_only_exact_public_template(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "source"
            root.mkdir()
            for name in ("flake.nix", "flake.lock", "go.mod", "go.sum", "scripts/public-release.py", public.TEMPLATE):
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(TEMPLATE if name == public.TEMPLATE else b"synthetic public input")
            for name in ("nix/private.yml.in", "nix/release-config.yml", "nix/other.yaml", ".env"):
                (root / name).write_text(HOSTILE)
            target = Path(directory) / "snapshot"
            snapshot.snapshot(root, target)
            inventory = json.loads((target / "source-manifest.json").read_text())["files"]
            self.assertIn(public.TEMPLATE, inventory)
            self.assertEqual((target / public.TEMPLATE).read_bytes(), TEMPLATE)
            for name in ("nix/private.yml.in", "nix/release-config.yml", "nix/other.yaml", ".env"):
                self.assertNotIn(name, inventory)
                self.assertFalse((target / name).exists())
            template = root / public.TEMPLATE
            template.unlink()
            self.stop("source_snapshot", lambda: snapshot.snapshot(root, Path(directory) / "missing"))
            template.symlink_to(root / "nix/private.yml.in")
            self.stop("source_snapshot", lambda: snapshot.snapshot(root, Path(directory) / "symlink"))
            self.assertFalse((Path(directory) / "missing").exists())
            self.assertFalse((Path(directory) / "symlink").exists())


if __name__ == "__main__":
    unittest.main()
