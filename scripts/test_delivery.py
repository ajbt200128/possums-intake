#!/usr/bin/env python3
"""Synthetic offline tests. No GitHub, registry, CLI, SDK, network or secrets."""
import base64
import copy
import hashlib
import importlib.util
import io
import json
import os
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import call, patch

import deploy as d
import release as r


def tool_module(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).parent / (name + ".py"))
    if spec is None or spec.loader is None:
        raise AssertionError("fixture module unavailable")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


smoke = tool_module("image-smoke")
source_snapshot = tool_module("image-smoke-source")
fresh_build = tool_module("image-smoke-build")


class Fake:
    def __init__(self):
        self.calls = []
        self.candidate = {'source': "1" * 40, 'release': "2" * 40, 'tag': "v0.1.0",
                              'image': "sha256:" + "3" * 64, 'repository': "fixture/ledger", 'registry': "ghcr.io/fixture/ledger"}
        c = self.candidate
        self.source: dict[str, Any] = {'repository': c["repository"], 'ref': "refs/heads/main", 'event': "workflow_dispatch",
                           'context_sha': c["source"], 'main': c["source"], 'checks': [self.run("check.yml", c["source"], "main", "push")]}
        config = b"containers:\n  ledger:\n    image: ghcr.io/fixture/ledger@sha256:" + b"3" * 64 + b"\n"
        self.raw = json.dumps({'config': base64.b64encode(config).decode(),
                                   'cmdline': "tinfoil-config-hash=" + hashlib.sha256(config).hexdigest(),
                                   'vm_shape': {'cpus': 2, 'memory_mb': 8192, 'gpus': 0, 'disks': 4}}).encode()
        c["manifest"] = hashlib.sha256(self.raw).hexdigest()
        build = {'source': c["source"], 'image': c["image"], 'success': True, 'fresh': True,
                     'substitutes': False, 'remote_builders': False, 'platform': "x86_64-linux", 'archive_sha256': "4" * 64}
        self.data: dict[str, Any] = {'source': self.source, 'build_run': "10", 'build_attempt': "1",
                         'builds': {"a": dict(build, runner="a"), "b": dict(build, runner="b")},
                         'qualification': {'image': c["image"], 'archive_sha256': "4" * 64,
                                            'cases': dict.fromkeys(r.QUALIFICATIONS, True), 'previous_release_compatibility': "unknown",
                                            'native_operator': {**dict.fromkeys(r.NATIVE_CASES, True), 'browser_inside_image': "not_run"}},
                         'git': {'tag_commit': c["release"], 'parent': c["source"],
                                  'source_tree_without_config': ["100644 same-file"], 'release_tree_without_config': ["100644 same-file"],
                                  'config_modes': ["100644", "100644"], 'source_config': base64.b64encode(config).decode(),
                                  'release_config': base64.b64encode(config).decode(),
                                  'source_image_drv': "/nix/store/fixture.drv", 'release_image_drv': "/nix/store/fixture.drv"},
                         'publisher': dict(self.run("tinfoil-release-publish.yml", c["release"], c["tag"], "workflow_dispatch"),
                                        id=20, run_attempt=1, build_run="10", build_attempt="1")}
        self.checked: list[Any] = [{"verificationResult": {
            "statement": {"predicateType": r.PREDICATE, "predicate": json.loads(self.raw),
                          "subject": [{"digest": {"sha256": c["manifest"]}}]},
            "signature": {"certificate": {
                "subjectAlternativeName": "https://github.com/fixture/ledger/.github/workflows/tinfoil-release-publish.yml@refs/tags/v0.1.0",
                "buildSignerDigest": c["release"], "sourceRepositoryDigest": c["release"],
                "sourceRepositoryRef": "refs/tags/v0.1.0", "runnerEnvironment": "github-hosted",
                "runInvocationURI": "https://github.com/fixture/ledger/actions/runs/20/attempts/1"}}}}]
        self.operator_policy: dict[str, Any] = {'environment': "fixture-production", 'environment_id': 101, 'operator_id': 102,
                                    'target': "fixture-ledger", 'volume': "fixture-original-disk", 'key_policy': "fixture-attested-unlock",
                                    'unlock_qualified': True, 'replacement_adapter_qualified': True}
        self.policy_view: dict[str, Any] = {'name': "fixture-production", 'id': 101, 'reviewers': [{"type": "User", "id": 102}],
                                'branches': [{"name": "main", "type": "branch"}], 'protected': True}
        self.before: dict[str, Any] = {'target': "fixture-ledger", 'repository': "fixture/ledger", 'cpus': 2, 'memory_mb': 8192, 'gpus': 0,
                           'volume': "fixture-original-disk", 'key_policy': "fixture-attested-unlock", 'configuration': {"opaque": "fixture"},
                           'status': "running", 'tag': "v0.0.1", 'pending': False, 'held': False, 'debug': False, 'confidential': True, 'authority_count': 1}
        self.after: dict[str, Any] = dict(self.before, tag=c["tag"], mutation_complete=True, original_volume_unlocked=True)
        self.plan_view: dict[str, Any] = {'read_only': True, 'strategy': "replacement", 'downtime_required': True, 'hold': False,
                              'target': "fixture-ledger", 'from_tag': "v0.0.1", 'to_tag': c["tag"], 'image': c["image"], 'manifest': c["manifest"],
                              'original_volume': "fixture-original-disk", 'key_policy': "fixture-attested-unlock",
                              'detach_before_attach': True, 'copied_authority': False, 'configuration_changes': [],
                              'unlock_policy_broadened': False, 'initialize_missing': False, 'gateway_replacement': False}
        ph = d.plan_gate(self.plan_view, self.before, c, self.operator_policy)
        self.review: dict[str, Any] = {'authenticated': True, 'operator_id': 102, 'environment_id': 101, 'run': "fixture-run", 'attempt': 1,
                           'state': "approved", 'bypassed': False, 'candidate': copy.deepcopy(c), 'plan_hash': ph,
                           'downtime_approved': True, 'original_volume_key_approved': True}
        self.updates = 0
        self.publications = 0
        self.update_error: Exception | None = None
        self.publish_error: Exception | None = None
        self.update_receipt = {"accepted": True}
        self.ready_value = True
        self.prior = False
        self.absent = True
        self.on_second_evidence: Any = None
        self.evidence_reads = 0

    @staticmethod
    def run(workflow, sha, branch, event):
        return {'repository': {"full_name": "fixture/ledger"}, 'head_repository': {"full_name": "fixture/ledger"},
                    'path': ".github/workflows/" + workflow, 'head_sha': sha, 'head_branch': branch, 'event': event,
                    'status': "completed", 'conclusion': "success"}

    def evidence(self, candidate):
        self.calls.append("evidence")
        self.evidence_reads += 1
        if self.evidence_reads == 2 and self.on_second_evidence:
            self.on_second_evidence()
        return copy.deepcopy(self.data)

    def manifest(self, candidate):
        self.calls.append("manifest")
        return self.raw

    def verify_attestation(self, candidate, raw):
        self.calls.append("verify_attestation")
        return copy.deepcopy(self.checked)

    def policy(self):
        self.calls.append("policy")
        return copy.deepcopy(self.policy_view)

    def get(self):
        self.calls.append("get")
        return copy.deepcopy(self.after if self.updates else self.before)

    def plan(self, candidate):
        self.calls.append("plan")
        return copy.deepcopy(self.plan_view)

    def approval(self, run):
        self.calls.append("approval")
        return copy.deepcopy(self.review)

    def prior_mutation(self, run, candidate):
        self.calls.append("prior_mutation")
        return self.prior

    def update_once(self, candidate, plan):
        self.calls.append("update")
        self.updates += 1
        if self.update_error:
            raise self.update_error
        return self.update_receipt

    def ready(self, candidate):
        self.calls.append("ready")
        return self.ready_value

    def publication_attempt(self):
        return 1

    def publication_absent(self, candidate):
        return self.absent

    def publication_gate(self, candidate):
        r.source_gate(self.source, candidate["repository"], candidate["source"])
        r.build_gate(self.data, candidate["source"], candidate["image"])
        r.relation(self.data["git"], candidate["source"], candidate["release"], candidate["tag"], candidate["image"], candidate["registry"])

    def publish(self, candidate):
        self.publications += 1
        if self.publish_error:
            raise self.publish_error
        return {"published": True}


class DeliveryTests(unittest.TestCase):
    def setUp(self):
        self.f = Fake()

    def deploy(self, **kwargs):
        return d.Deployment(self.f, self.f.operator_policy, self.f.candidate, "fixture-run", enabled=True, **kwargs)

    def stop(self, code, call, outcome="no_effect"):
        with self.assertRaises(r.Stop) as caught:
            call()
        self.assertEqual(caught.exception.code, code)
        diag = r.diagnostic(caught.exception)
        self.assertEqual(diag["code"], "DELIVERY_" + code.upper())
        self.assertEqual(diag["stage"], r.CODES[code][0])
        self.assertEqual(diag["constraint"], r.CODES[code][1])
        self.assertEqual(diag["outcome"], outcome)
        for sentinel in ("fixture/ledger", "fixture-run", "fixture-original-disk", "hostile", "secret-fixture"):
            self.assertNotIn(sentinel, json.dumps(diag))
        return diag

    def test_successful_candidate_is_not_runtime_verification(self):
        self.assertFalse(r.candidate_gate(self.f, self.f.candidate)["runtime_verified"])
        self.assertEqual(self.f.updates + self.f.publications, 0)

    def test_exact_main_successful_ci_only(self):
        for field, value, code in (("main", "9" * 40, "stale"), ("ref", "refs/tags/v0.1.0", "event"),
                                   ("checks", [], "checks"), ("event", "pull_request", "event")):
            with self.subTest(field=field):
                view = dict(self.f.source, **{field: value})
                self.stop(code, lambda view=view: r.source_gate(view, "fixture/ledger", "1" * 40))

    def test_fork_wrong_sha_pr_ci_failure_not_eligible(self):
        for field, value in (("head_sha", "9" * 40), ("head_branch", "feature"), ("event", "pull_request"),
                             ("conclusion", "failure"), ("head_repository", {"full_name": "fork/ledger"})):
            with self.subTest(field=field):
                view = copy.deepcopy(self.f.source)
                view["checks"][0][field] = value
                self.stop("checks", lambda view=view: r.source_gate(view, "fixture/ledger", "1" * 40))

    def test_workflow_run_live_trigger(self):
        view = dict(self.f.source, event="workflow_run", action="completed", trigger=self.f.source["checks"][0])
        r.source_gate(view, "fixture/ledger", "1" * 40)
        view["trigger"] = dict(view["trigger"], event="pull_request")
        self.stop("event", lambda: r.source_gate(view, "fixture/ledger", "1" * 40))

    def test_independent_fresh_same_digest_only(self):
        for field, value in (("runner", "a"), ("image", "sha256:" + "9" * 64), ("fresh", False),
                             ("substitutes", True), ("remote_builders", True), ("success", 1), ("source", "9" * 40)):
            with self.subTest(field=field):
                data = copy.deepcopy(self.f.data)
                data["builds"]["b"][field] = value
                self.stop("build", lambda data=data: r.build_gate(data, "1" * 40, self.f.candidate["image"]))

    def test_generated_archive_qualification_not_source_substitute(self):
        for key in r.QUALIFICATIONS:
            data = copy.deepcopy(self.f.data)
            data["qualification"]["cases"][key] = False
            self.stop("qualification", lambda data=data: r.build_gate(data, "1" * 40, self.f.candidate["image"]))
        self.f.data["qualification"]["archive_sha256"] = "9" * 64
        self.stop("qualification", lambda: r.candidate_gate(self.f, self.f.candidate))

    def test_native_schema_exact_set_and_typed_proofs_at_build_gate(self):
        expected = {"actual_entrypoint", "native_login_create_patch", "shared_key_separation",
                    "held_balance_stock_edit", "sdk_logging_forced_off", "empty_logs_auth_origins_stdio",
                    "frozen_asset_http_hashes"}
        self.assertEqual(r.NATIVE_CASES, expected)
        native = {**dict.fromkeys(expected, True), "browser_inside_image": "not_run"}
        r.build_gate(self.f.data, self.f.candidate["source"], self.f.candidate["image"])
        invalid = [None, [], {}, {**native, "unknown": True}, {**native, "browser_inside_image": True}]
        for key in expected:
            invalid.append({name: value for name, value in native.items() if name != key})
            for value in (False, 1, "true", None):
                invalid.append({**native, key: value})
        old = native.copy()
        old["held_balance_refused"] = old.pop("held_balance_stock_edit")
        old["logs_settings_blocked"] = old.pop("sdk_logging_forced_off")
        invalid += [old, {**native, "held_balance_refused": True},
                    {**native, "logs_settings_blocked": False}]
        for value in invalid:
            with self.subTest(native=value):
                data = copy.deepcopy(self.f.data)
                data["qualification"]["native_operator"] = value
                self.stop("qualification", lambda data=data: r.build_gate(data, self.f.candidate["source"], self.f.candidate["image"]))
        data = copy.deepcopy(self.f.data)
        del data["qualification"]["native_operator"]
        self.stop("qualification", lambda: r.build_gate(data, self.f.candidate["source"], self.f.candidate["image"]))
        self.assertEqual(self.f.publications + self.f.updates, 0)

    def test_previous_release_unknown_not_fake_success(self):
        self.f.data["qualification"]["previous_release_compatibility"] = "passed"
        self.stop("qualification", lambda: r.candidate_gate(self.f, self.f.candidate))

    def test_immutable_tag_parent_tree_config(self):
        for field, value in (("tag_commit", "9" * 40), ("parent", "9" * 40),
                             ("release_tree_without_config", ["changed-source"]), ("config_modes", ["100644", "100755"]),
                             ("release_config", base64.b64encode(b"unexpected config").decode())):
            with self.subTest(field=field):
                self.f = Fake()
                self.f.data["git"][field] = value
                self.stop("relation", lambda: r.candidate_gate(self.f, self.f.candidate))

    def test_release_image_derivation_same(self):
        self.f.data["git"]["release_image_drv"] = "/nix/store/different.drv"
        self.stop("build", lambda: r.candidate_gate(self.f, self.f.candidate))

    def test_manifest_hash_and_config_exact(self):
        self.f.raw += b" "
        self.stop("manifest", lambda: r.candidate_gate(self.f, self.f.candidate))

    def test_tagged_signer_predicate_invocation(self):
        for field, value in (("sourceRepositoryDigest", "9" * 40), ("runnerEnvironment", "self-hosted"),
                             ("sourceRepositoryRef", "refs/heads/main"), ("runInvocationURI", "hostile://secret")):
            with self.subTest(field=field):
                self.f = Fake()
                self.f.checked[0]["verificationResult"]["signature"]["certificate"][field] = value
                self.stop("invocation" if field == "runInvocationURI" else "signature", lambda: r.candidate_gate(self.f, self.f.candidate))

    def test_exact_signed_attempt_not_latest(self):
        self.f.data["publisher"]["run_attempt"] = 2
        self.stop("invocation", lambda: r.candidate_gate(self.f, self.f.candidate))

    def test_supported_signature_command_is_passive(self):
        with patch.object(r, "read_command", return_value=b"[]") as command:
            r.verify_file("manifest.json", "fixture/ledger", "2" * 40, "v0.1.0")
        args = command.call_args.args[0]
        self.assertEqual(args[:3], ["gh", "attestation", "verify"])
        for flag in ("--cert-identity", "--source-ref", "--source-digest", "--signer-digest", "--deny-self-hosted-runners", "--predicate-type"):
            self.assertIn(flag, args)

    def test_disabled_publication_and_deployment(self):
        self.stop("disabled", r.Publication(self.f, self.f.candidate).execute)
        instance = d.Deployment(self.f, self.f.operator_policy, self.f.candidate, "fixture-run")
        self.stop("disabled", instance.protected)
        self.assertEqual(self.f.calls, [])

    def test_publication_once_and_unknown_never_replayed(self):
        self.f.publish_error = RuntimeError("hostile credential body")
        instance = r.Publication(self.f, self.f.candidate, True)
        self.stop("publication_unknown", instance.execute, "unknown")
        self.assertEqual(self.f.publications, 1)
        # Even a stale absence read cannot reopen the consumed local session.
        self.stop("partial", instance.execute)
        self.f.absent = False
        self.stop("partial", r.Publication(self.f, self.f.candidate, True).execute)
        self.assertEqual(self.f.publications, 1)

    def test_published_but_signature_failure_keeps_completed(self):
        self.f.checked[0]["verificationResult"]["statement"]["predicateType"] = "wrong"
        self.stop("signature", r.Publication(self.f, self.f.candidate, True).execute, "completed")
        self.assertEqual(self.f.publications, 1)

    def test_protected_one_replacement_success(self):
        result = self.deploy().protected()
        self.assertEqual(result["mutation_count"], 1)
        self.assertEqual(result["outcome"], "completed")
        self.assertFalse(result["runtime_verified"])
        self.assertEqual(self.f.calls.count("approval"), 2)
        self.assertEqual(self.f.calls.count("policy"), 2)
        self.assertEqual(self.f.calls.count("evidence"), 2)
        self.assertEqual(self.f.calls.count("plan"), 2)
        self.assertEqual(self.f.updates, 1)

    def test_missing_operator_prerequisites_fail_closed(self):
        for key in ("target", "volume", "key_policy", "unlock_qualified", "replacement_adapter_qualified"):
            self.f = Fake()
            self.f.operator_policy[key] = None
            self.stop("prerequisites", self.deploy().protected)
            self.assertEqual(self.f.updates, 0)

    def test_existing_environment_identity_and_branch_restriction(self):
        for field, value in (("id", 999), ("reviewers", []), ("protected", False),
                             ("branches", [{"name": "*", "type": "tag"}])):
            self.f = Fake()
            self.f.policy_view[field] = value
            self.stop("policy", self.deploy().protected)
            self.assertEqual(self.f.updates, 0)

    def test_authenticated_operator_review_not_bypass(self):
        for field, value in (("authenticated", False), ("operator_id", 999), ("state", "rejected"),
                             ("bypassed", True), ("run", "other"), ("attempt", 2), ("plan_hash", "wrong"),
                             ("downtime_approved", False), ("original_volume_key_approved", False)):
            self.f = Fake()
            self.f.review[field] = value
            self.stop("approval", self.deploy().protected)
            self.assertEqual(self.f.updates, 0)

    def test_no_blue_green_or_key_volume_broadening(self):
        for field, value in (("strategy", "blue_green"), ("downtime_required", False), ("hold", True),
                             ("original_volume", "copy"), ("key_policy", "platform-managed"),
                             ("copied_authority", True), ("configuration_changes", ["change"]),
                             ("unlock_policy_broadened", True), ("initialize_missing", True),
                             ("gateway_replacement", True), ("detach_before_attach", False)):
            self.f = Fake()
            self.f.plan_view[field] = value
            self.stop("plan", self.deploy().protected)
            self.assertEqual(self.f.updates, 0)

    def test_recheck_main_after_approval(self):
        self.f.on_second_evidence = lambda: self.f.source.update(main="9" * 40)
        self.stop("stale", self.deploy().protected)
        self.assertEqual(self.f.updates, 0)

    def test_recheck_operator_approval_after_gate(self):
        self.f.on_second_evidence = lambda: self.f.review.update(state="rejected")
        self.stop("approval", self.deploy().protected)
        self.assertEqual(self.f.updates, 0)

    def test_rerun_and_prior_mutation_refused(self):
        self.stop("rerun", self.deploy(attempt=2).protected)
        self.f.prior = True
        self.stop("rerun", self.deploy().protected)
        self.assertEqual(self.f.updates, 0)

    def test_unknown_update_no_second_call_same_session(self):
        self.f.update_error = RuntimeError("hostile upstream secret")
        instance = self.deploy()
        self.stop("update_unknown", instance.protected, "unknown")
        self.stop("rerun", instance.protected)
        self.assertEqual(self.f.updates, 1)

    def test_running_alone_not_terminal_or_ready(self):
        self.f.after.pop("mutation_complete")
        self.stop("completion_unknown", self.deploy().protected, "unknown")
        self.assertEqual(self.f.updates, 1)

    def test_completed_update_readiness_failure_not_unknown_mutation(self):
        self.f.ready_value = False
        self.stop("readiness", self.deploy().protected, "completed")
        self.assertEqual(self.f.updates, 1)

    def test_original_volume_and_opaque_configuration_preserved(self):
        for field in ("volume", "key_policy", "configuration"):
            self.f = Fake()
            self.f.after[field] = "changed"
            self.stop("configuration_changed", self.deploy().protected, "completed")
            self.assertEqual(self.f.updates, 1)

    def test_status_never_plans_retries_or_mutates(self):
        result = self.deploy().status()
        self.assertEqual(self.f.calls, ["get"])
        self.assertEqual(result["readiness"], "unknown")
        self.assertEqual(self.f.updates + self.f.publications, 0)

    def test_diagnostics_suppress_hostile_unexpected_and_preserve_status(self):
        diag = r.diagnostic(RuntimeError("hostile secret URL body"))
        self.assertEqual(diag["code"], "DELIVERY_INTERNAL")
        self.assertNotIn("hostile", json.dumps(diag))
        self.assertEqual(r.diagnostic(r.Stop("transport", 403))["status"], 403)
        self.assertNotIn("status", r.diagnostic(r.Stop("transport", "403")))

    def test_cli_activation_defaults_off_and_prerequisites_not_qualified(self):
        for script, op in (("release.py", "prepare"), ("release.py", "publication-guard"),
                           ("deploy.py", "protected"), ("deploy.py", "preflight")):
            env = {"PATH": os.environ["PATH"], "PYTHONDONTWRITEBYTECODE": "1"}
            result = subprocess.run([sys.executable, str(Path(__file__).parent / script), op], capture_output=True, env=env)
            self.assertEqual(result.returncode, 1)
            self.assertEqual(json.loads(result.stdout)["code"], "DELIVERY_DISABLED")
            self.assertEqual(result.stderr, b"")
            env.update(RELEASE_AUTOMATION_ENABLED="true", PRODUCTION_DEPLOYMENT_ENABLED="true")
            result = subprocess.run([sys.executable, str(Path(__file__).parent / script), op], capture_output=True, env=env)
            self.assertEqual(result.returncode, 1)
            self.assertEqual(json.loads(result.stdout)["code"], "DELIVERY_PREREQUISITES")

    def test_cli_status_has_no_external_subprocess(self):
        for module in (r, d):
            with patch.object(sys, "argv", ["script", "status"]), patch("subprocess.run") as command, patch("builtins.print"):
                module.main()
                command.assert_not_called()

    def test_malformed_evidence_is_boundary_classified(self):
        for field, value, code in (("source", None, "event"), ("builds", None, "build"),
                                   ("git", None, "relation"), ("publisher", None, "invocation")):
            self.f = Fake()
            self.f.data[field] = value
            self.stop(code, lambda: r.candidate_gate(self.f, self.f.candidate))
        self.f = Fake()
        self.f.checked = [None]
        self.stop("signature", lambda: r.candidate_gate(self.f, self.f.candidate))

    def test_hostile_control_plan_approval_failures_do_not_mutate(self):
        for method, code in (("get", "control_read"), ("plan", "plan"), ("approval", "approval"), ("policy", "policy")):
            self.f = Fake()
            with patch.object(self.f, method, side_effect=RuntimeError("hostile body credential")):
                self.stop(code, self.deploy().protected)
            self.assertEqual(self.f.updates, 0)

    def test_source_snapshot_excludes_private_state_without_git(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "checkout"
            root.mkdir()
            for name in ("flake.nix", "flake.lock", "go.mod", "go.sum"):
                (root / name).write_text("synthetic public input")
            for name in ("cmd/intake/main.go", "internal/ledger/core.go", "nix/packages.nix", "scripts/release.py"):
                p = root / name
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_text("synthetic public source")
            for name in (".env", ".devenv/state/private.db", ".git/config", "private.db", "scripts/__pycache__/generated.py"):
                p = root / name
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_text("private-fixture-sentinel")
            a, b = Path(directory) / "a", Path(directory) / "b"
            result_a = source_snapshot.snapshot(root, a)
            result_b = source_snapshot.snapshot(root, b)
            self.assertEqual(result_a["source_identity"], result_b["source_identity"])
            manifest = json.loads((a / "source-manifest.json").read_text())
            self.assertIn("cmd/intake/main.go", manifest["files"])
            self.assertFalse((a / ".git").exists())
            self.assertFalse((a / ".devenv").exists())
            self.assertFalse((a / ".env").exists())
            self.assertFalse((a / "scripts/__pycache__").exists())
            self.assertEqual(result_a["git_commit"], "unavailable")
            self.stop("source_snapshot", lambda: source_snapshot.snapshot(root, a))
            self.assertTrue((a / "source-manifest.json").is_file())
            self.stop("source_snapshot", lambda: source_snapshot.snapshot(root, root / "nested"))

    def website_fixture(self, root):
        """Synthetic website inventory: fixture bytes and a matching manifest."""
        assets, bundle = root / "internal/website/assets", root / "internal/website/bundle"
        assets.mkdir(parents=True)
        bundle.mkdir(parents=True)
        fonts = {name: b"synthetic-font-" + name.encode() for name in ("iosevka-term-regular.woff2", "iosevka-term-bold.woff2")}
        license = b"synthetic-ofl-license-fixture"
        for name, raw in {**fonts, "OFL.txt": license}.items():
            (assets / name).write_bytes(raw)
        declared = {"manifest_version": 1,
                    "assets": [{"path": name, "bytes": len(raw), "sha256": hashlib.sha256(raw).hexdigest()}
                               for name, raw in fonts.items()],
                    "license": {"path": "OFL.txt", "bytes": len(license), "sha256": hashlib.sha256(license).hexdigest()}}
        (assets / "manifest.json").write_text(json.dumps(declared))
        (assets / "NOTICE.md").write_text("synthetic provenance notice")
        for name in ("init.el", "options.el", "pages.el", "routes.el", "shared.el", "style.el"):
            (bundle / name).write_text("; synthetic bundle fixture")
            fixture = root / "pkg/lispsite/testdata" / name
            fixture.parent.mkdir(parents=True, exist_ok=True)
            fixture.write_text("; synthetic library fixture")
        for source in ("cmd/intake/main.go", "internal/website/render.go", "internal/website/assets.go",
                       "pkg/lispsite/runtime.go", "pkg/lispsite/runtime_test.go"):
            path = root / source
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("synthetic public source")
        # Declared-but-unimported and private files that must never be copied.
        for name in ("pkg/lispsite/README.md", "pkg/lispsite/testdata/private.el",
                     "internal/website/bundle/private.el", "internal/website/assets/secret.woff2",
                     "internal/website/assets/manifest.bak.json", "internal/website/private.db",
                     "internal/website/.env"):
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("private-fixture-sentinel")
        return {"root": root, "assets": assets, "bundle": bundle, "fonts": fonts, "declared": declared}

    def test_source_snapshot_includes_pkg_go_and_declared_website_inputs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "checkout"
            root.mkdir()
            for name in ("flake.nix", "flake.lock", "go.mod", "go.sum"):
                (root / name).write_text("synthetic public input")
            self.website_fixture(root)
            self.f = Fake()
            a, b = Path(directory) / "a", Path(directory) / "b"
            result_a = source_snapshot.snapshot(root, a)
            result_b = source_snapshot.snapshot(root, b)
            self.assertEqual(result_a["source_identity"], result_b["source_identity"])
            files = set(json.loads((a / "source-manifest.json").read_text())["files"])
        self.assertIn("pkg/lispsite/runtime.go", files)
        self.assertIn("pkg/lispsite/runtime_test.go", files)
        for name in ("init.el", "options.el", "pages.el", "routes.el", "shared.el", "style.el"):
            self.assertIn("internal/website/bundle/" + name, files)
            self.assertIn("pkg/lispsite/testdata/" + name, files)
        for name in ("iosevka-term-regular.woff2", "iosevka-term-bold.woff2", "OFL.txt"):
            self.assertIn("internal/website/assets/" + name, files)
        self.assertIn("internal/website/assets/manifest.json", files)
        self.assertIn("internal/website/assets/NOTICE.md", files)
        for name in ("pkg/lispsite/README.md", "pkg/lispsite/testdata/private.el",
                     "internal/website/bundle/private.el", "internal/website/assets/secret.woff2",
                     "internal/website/assets/manifest.bak.json", "internal/website/private.db",
                     "internal/website/.env"):
            self.assertNotIn(name, files)

    def test_source_snapshot_rejects_altered_missing_or_undeclared_website_inputs(self):
        cases = {
            "altered_font": lambda f: (f["assets"] / "iosevka-term-bold.woff2").write_bytes(b"altered"),
            "resized_font": lambda f: (f["assets"] / "iosevka-term-regular.woff2").write_bytes(
                f["fonts"]["iosevka-term-regular.woff2"] + b" "),
            "altered_license": lambda f: (f["assets"] / "OFL.txt").write_bytes(b"altered license"),
            "missing_bundle": lambda f: (f["bundle"] / "style.el").unlink(),
            "missing_library_fixture": lambda f: (f["root"] / "pkg/lispsite/testdata/style.el").unlink(),
            "missing_manifest": lambda f: (f["assets"] / "manifest.json").unlink(),
            "missing_notice": lambda f: (f["assets"] / "NOTICE.md").unlink(),
            "missing_bundle_directory": lambda f: __import__("shutil").rmtree(f["bundle"]),
            "manifest_digest": lambda f: (f["assets"] / "manifest.json").write_text(json.dumps(
                {**f["declared"], "assets": [dict(f["declared"]["assets"][0], sha256="0" * 64), f["declared"]["assets"][1]]})),
            "manifest_extra_asset": lambda f: (f["assets"] / "manifest.json").write_text(json.dumps(
                {**f["declared"], "assets": f["declared"]["assets"] + [{"path": "extra.woff2", "bytes": 1, "sha256": "1" * 64}]})),
            "manifest_malformed": lambda f: (f["assets"] / "manifest.json").write_text("not json"),
        }
        for name, alter in cases.items():
            with self.subTest(case=name), tempfile.TemporaryDirectory() as directory:
                root = Path(directory) / "checkout"
                root.mkdir()
                for entry, value in (("flake.nix", "synthetic public input"), ("flake.lock", "synthetic public input"),
                                     ("go.mod", "synthetic public input"), ("go.sum", "synthetic public input")):
                    (root / entry).write_text(value)
                fixture = self.website_fixture(root)
                alter(fixture)
                self.stop("source_snapshot", lambda root=root, directory=directory:
                          source_snapshot.snapshot(root, Path(directory) / "a"))
                self.assertFalse((Path(directory) / "a").exists())

    def test_source_snapshot_rejects_symlinked_website_inputs(self):
        for name, target in (("font", "assets/iosevka-term-regular.woff2"), ("ancestor", "assets"),
                             ("bundle", "bundle"), ("provenance", "assets/NOTICE.md")):
            with self.subTest(case=name), tempfile.TemporaryDirectory() as directory:
                root = Path(directory) / "checkout"
                root.mkdir()
                for entry in ("flake.nix", "flake.lock", "go.mod", "go.sum"):
                    (root / entry).write_text("synthetic public input")
                fixture = self.website_fixture(root)
                if name == "ancestor":
                    moved = Path(directory) / "outside-assets"
                    fixture["assets"].rename(moved)
                    (root / "internal/website/assets").symlink_to(moved, target_is_directory=True)
                elif name == "bundle":
                    moved = Path(directory) / "outside-bundle"
                    fixture["bundle"].rename(moved)
                    (root / "internal/website/bundle").symlink_to(moved, target_is_directory=True)
                else:
                    path = root / "internal/website" / target
                    real = path.with_name(path.name + ".real")
                    path.rename(real)
                    path.symlink_to(real)
                self.stop("source_snapshot", lambda root=root, directory=directory:
                          source_snapshot.snapshot(root, Path(directory) / "a"))
                self.assertFalse((Path(directory) / "a").exists())

    def test_fresh_output_guard_is_passive_and_refuses_cached_output(self):
        drvs = ["/nix/store/fixture-" + str(i) + ".drv" for i in range(7)]
        shown = {"derivations": {drvs[0]: {"outputs": {"out": {"path": "/nix/store/fixture-output"}}}}}
        with patch.object(r, "read_command", side_effect=[json.dumps(drvs).encode(), json.dumps(shown).encode()]) as cmd:
            with patch("pathlib.Path.exists", return_value=True):
                self.stop("fresh_cached", fresh_build.fresh)
            self.assertEqual([c.args[0][1] for c in cmd.call_args_list], ["eval", "derivation"])

    def test_fresh_guard_normalizes_supported_nix_store_basenames_without_building(self):
        drvs = ["/nix/store/fixture-" + str(i) + ".drv" for i in range(7)]
        basename = "0" * 32 + "-fixture-output"
        for version in (3, 4):
            shown = {"version": version, "derivations": {"fixture.drv": {
                "version": version, "outputs": {"out": {"path": basename}}}}}
            replies = [json.dumps(drvs).encode()] + [json.dumps(shown).encode()] * len(drvs)
            with patch.object(r, "read_command", side_effect=replies) as cmd, \
                    patch("pathlib.Path.exists", return_value=False) as exists:
                self.assertTrue(fresh_build.fresh()["passed"])
                self.assertEqual(exists.call_count, len(drvs))
                self.assertTrue(all(call.args[0][1] in ("eval", "derivation") for call in cmd.call_args_list))
            with patch.object(r, "read_command", side_effect=replies), patch("pathlib.Path.exists", return_value=True):
                self.stop("fresh_cached", fresh_build.fresh)
            self.assertEqual(fresh_build.output_paths(shown), ["/nix/store/" + basename])

    def test_fresh_guard_rejects_unknown_or_unresolved_output_metadata(self):
        basename = "0" * 32 + "-fixture-output"
        for version, path in ((5, basename), (True, basename), (4, None), (4, "../outside"),
                              (4, "/nix/store/" + basename), (4, "bad/" + basename), (2, basename)):
            shown = {"version": version, "derivations": {"fixture.drv": {
                "version": version, "outputs": {"out": {"path": path}}}}}
            self.stop("fresh_metadata", lambda shown=shown: fresh_build.output_paths(shown))
        self.assertEqual(fresh_build.output_paths({"fixture.drv": {
            "outputs": {"out": {"path": "/nix/store/fixture-output"}}}}), ["/nix/store/fixture-output"])

    def test_archive_metadata_refuses_embedded_secret_and_mutable_epoch(self):
        settings: dict[str, Any] = {"User": "65532:65532", "WorkingDir": "/",
            "Cmd": ["serve", "--state", "/state", "--listen", "127.0.0.1:8090"],
            "Entrypoint": ["/nix/store/fixture/bin/intake"],
            "Env": ["OTEL_SDK_DISABLED=true", "SSL_CERT_FILE=/nix/store/fixture/ca.crt"]}
        for bad in (None, "secret", "epoch"):
            with tempfile.TemporaryDirectory() as directory:
                archive = Path(directory) / "synthetic.tar"
                config: dict[str, Any] = {"config": copy.deepcopy(settings), "architecture": "arm64", "os": "linux", "created": "1970-01-01T00:00:01Z"}
                if bad == "secret":
                    config["config"]["Env"].append("LEDGER_ADMIN_KEY=secret-fixture")
                if bad == "epoch":
                    config["created"] = "now"
                with tarfile.open(archive, "w") as tar:
                    for name, raw in (("manifest.json", json.dumps([{"Config": "config.json"}]).encode()), ("config.json", json.dumps(config).encode())):
                        info = tarfile.TarInfo(name)
                        info.size = len(raw)
                        tar.addfile(info, io.BytesIO(raw))
                if bad:
                    self.stop("image_archive", lambda archive=archive: smoke.archive_identity(archive))
                else:
                    image, arch = smoke.archive_identity(archive)
                    self.assertTrue(r.pin(image, r.DIGEST))
                    self.assertEqual(arch, "arm64")

    def test_loaded_image_accepts_classic_and_containerd_immutable_ids(self):
        config = "sha256:" + "1" * 64
        for image in (config, "sha256:" + "2" * 64):
            with self.subTest(image=image), patch.object(smoke, "success", side_effect=[(image + "\n").encode(), b""]) as cmd, \
                    patch.object(smoke, "archive_identity", return_value=(config, "arm64")):
                self.assertEqual(smoke.loaded_image_identity(config), image)
                self.assertEqual(cmd.call_args_list[0].args[0],
                                 ["docker", "image", "inspect", "possums-intake-local:qualification", "--format", "{{.Id}}"])
                self.assertEqual(cmd.call_args_list[1].args[0][:3], ["docker", "image", "save"])
                self.assertEqual(cmd.call_args_list[1].args[0][-1], image)

    def test_loaded_image_refuses_different_saved_config(self):
        with patch.object(smoke, "success", side_effect=[("sha256:" + "2" * 64).encode(), b""]), \
                patch.object(smoke, "archive_identity", return_value=("sha256:" + "3" * 64, "arm64")):
            self.stop("image_archive", lambda: smoke.loaded_image_identity("sha256:" + "1" * 64))

    def test_loaded_image_refuses_unpinned_runtime_identity_before_save(self):
        with patch.object(smoke, "success", return_value=b"hostile secret-fixture") as cmd:
            self.stop("image_archive", lambda: smoke.loaded_image_identity("sha256:" + "1" * 64))
        self.assertEqual(cmd.call_count, 1)

    def test_loaded_image_save_failure_cannot_qualify(self):
        with patch.object(smoke, "success", side_effect=[("sha256:" + "2" * 64).encode(), r.Stop("image_archive")]), \
                patch.object(smoke, "archive_identity") as inspect:
            self.stop("image_archive", lambda: smoke.loaded_image_identity("sha256:" + "1" * 64))
        inspect.assert_not_called()

    def test_unknown_generated_image_migration_blocks_acceptance(self):
        evidence: dict[str, Any] = {"cases": dict.fromkeys(r.QUALIFICATIONS, True)}
        evidence["cases"]["migration_failure_refused"] = "unknown"
        with patch.object(sys, "argv", ["image-smoke", "synthetic-archive", "synthetic-probe"]), \
                patch.object(smoke, "qualify", return_value=evidence), patch("builtins.print"):
            self.stop("image_migration", smoke.main)
        self.f.data["qualification"]["cases"]["migration_failure_refused"] = "unknown"
        self.stop("qualification", lambda: r.candidate_gate(self.f, self.f.candidate))

    def test_actual_image_negative_requires_exact_closed_diagnostic(self):
        fixture = smoke.Fixture("sha256:" + "1" * 64, "/synthetic-probe")
        view = {"code": "LEDGER_STARTUP_SCHEMA", **smoke.STARTUP_EXPECTED["LEDGER_STARTUP_SCHEMA"]}
        view["next_step"] = "hostile body secret-fixture"
        result = subprocess.CompletedProcess([], 1, b"", json.dumps(view).encode())
        with patch.object(smoke, "command", return_value=result):
            self.stop("image_privacy", lambda: fixture.once("synthetic-volume", "serve", failure="LEDGER_STARTUP_SCHEMA"))

    def test_image_migration_runs_real_entrypoint_then_checks_original_partial_state(self):
        fixture = smoke.Fixture("sha256:" + "1" * 64, "/synthetic-probe")
        with patch.object(fixture, "volume", return_value="synthetic-partial-state"), \
                patch.object(fixture, "once") as once:
            fixture.migration_failure()
        self.assertEqual(once.call_args_list, [
            call("synthetic-partial-state", "init", failure="LEDGER_COMMIT_UNKNOWN", file_limit=196608),
            call("synthetic-partial-state", "migration-rollback", probe=True),
            call("synthetic-partial-state", "serve", failure="LEDGER_STARTUP_SCHEMA"),
            call("synthetic-partial-state", "init", failure="LEDGER_INIT_NONEMPTY"),
            call("synthetic-partial-state", "logs", probe=True),
        ])

    def test_image_migration_limit_is_on_real_init_not_probe_or_serve(self):
        fixture = smoke.Fixture("sha256:" + "1" * 64, "/synthetic-probe")
        view = {"code": "LEDGER_COMMIT_UNKNOWN", **smoke.STARTUP_EXPECTED["LEDGER_COMMIT_UNKNOWN"]}
        result = subprocess.CompletedProcess([], 1, b"", json.dumps(view).encode())
        with patch.object(smoke, "command", return_value=result) as cmd:
            fixture.once("synthetic-volume", "init", failure="LEDGER_COMMIT_UNKNOWN", file_limit=smoke.MIGRATION_FILE_LIMIT)
        args = cmd.call_args.args[0]
        self.assertIn("fsize=196608:196608", args)
        self.assertNotIn("--entrypoint", args)
        self.assertEqual(args[-3:], ["init", "--state", "/state"])

    def test_bootstrap_failure_does_not_qualify_migration_execution(self):
        fixture = smoke.Fixture("sha256:" + "1" * 64, "/synthetic-probe")
        # Correct closed error, but wrong stage: no actual v1 execution evidence.
        view = {"code": "LEDGER_COMMIT_UNKNOWN", **smoke.STARTUP_EXPECTED["LEDGER_COMMIT_UNKNOWN"]}
        view["stage"] = "startup"
        result = subprocess.CompletedProcess([], 1, b"", json.dumps(view).encode())
        with patch.object(smoke, "command", return_value=result):
            self.stop("image_privacy", lambda: fixture.once("synthetic-volume", "init", failure="LEDGER_COMMIT_UNKNOWN", file_limit=smoke.MIGRATION_FILE_LIMIT))

    def test_missing_rollback_evidence_stops_partial_state_qualification(self):
        fixture = smoke.Fixture("sha256:" + "1" * 64, "/synthetic-probe")
        with patch.object(fixture, "volume", return_value="synthetic-volume"), \
                patch.object(fixture, "once", side_effect=[None, r.Stop("image_migration")]) as once:
            self.stop("image_migration", fixture.migration_failure)
        self.assertEqual(once.call_count, 2)

    def test_image_runtime_has_no_network_pull_or_daemon_logs(self):
        flags = smoke.Fixture("sha256:" + "1" * 64, "/synthetic-probe").flags("synthetic-volume")
        self.assertEqual(flags[flags.index("--network") + 1], "none")
        self.assertEqual(flags[flags.index("--pull") + 1], "never")
        self.assertEqual(flags[flags.index("--log-driver") + 1], "none")
        self.assertIn("--read-only", flags)
        self.assertIn("OTEL_SDK_DISABLED=true", flags)
        self.assertIn("type=volume,src=synthetic-volume,dst=/state", flags)
        self.assertNotIn("--privileged", flags)

    def test_native_operator_uses_real_entrypoint_and_fresh_state(self):
        fixture = smoke.Fixture("sha256:" + "1" * 64, "/synthetic-probe", "/synthetic-assets.json")
        with patch.object(fixture, "volume", return_value="synthetic-new-state"), \
                patch.object(fixture, "once") as once, \
                patch.object(fixture, "start", side_effect=["synthetic-server", "synthetic-restarted"]) as start, \
                patch.object(fixture, "exec") as execute, patch.object(fixture, "stop") as stop:
            fixture.native_operator()
        self.assertEqual(once.call_args_list, [call("synthetic-new-state", "init", operator=True),
                                              call("synthetic-new-state", "operator-privacy", probe=True),
                                              call("synthetic-new-state", "operator-privacy", probe=True)])
        self.assertEqual(start.call_args_list, [call("synthetic-new-state", operator=True)] * 2)
        self.assertEqual(execute.call_args_list, [call("synthetic-server", "operator"),
                                                call("synthetic-server", "website"),
                                                call("synthetic-restarted", "website-restart")])
        self.assertEqual(stop.call_args_list, [call("synthetic-server"), call("synthetic-restarted")])

    def test_native_probe_failure_cannot_qualify_privacy_or_hide_failure(self):
        fixture = smoke.Fixture("sha256:" + "1" * 64, "/synthetic-probe")
        with patch.object(fixture, "volume", return_value="synthetic-new-state"), \
                patch.object(fixture, "once") as once, patch.object(fixture, "start"), \
                patch.object(fixture, "exec", side_effect=r.Stop("image_runtime")):
            self.stop("image_runtime", fixture.native_operator)
        once.assert_called_once_with("synthetic-new-state", "init", operator=True)

    def test_native_init_credentials_runtime_only_and_controls_preserved(self):
        fixture = smoke.Fixture("sha256:" + "1" * 64, "/synthetic-probe", "/synthetic-assets.json")
        result = subprocess.CompletedProcess([], 0, b'{"status":"initialized_empty"}', b"")
        with patch("pathlib.Path.is_file", return_value=True), patch.object(smoke, "command", return_value=result) as cmd:
            fixture.once("synthetic-new-state", "init", operator=True)
        args = cmd.call_args.args[0]
        self.assertNotIn("--entrypoint", args)
        self.assertEqual(args[-4:], ["init", "--state", "/state", "--operator"])
        self.assertIn("LEDGER_OPERATOR_EMAIL=" + fixture.operator_email, args)
        self.assertIn("LEDGER_OPERATOR_PASSWORD=" + fixture.operator_password, args)
        self.assertIn("type=bind,src=/synthetic-assets.json,dst=/asset-manifest.json,readonly", args)
        for flag in ("--read-only", "--network", "--cap-drop", "--security-opt", "--log-driver"):
            self.assertIn(flag, args)
        self.assertEqual(args[args.index("--network") + 1], "none")
        self.assertEqual(args[args.index("--log-driver") + 1], "none")
        self.assertNotIn("0.0.0.0", " ".join(args))  # noqa: S104 - this assertion forbids wildcard binding.

    def test_image_evidence_emits_only_current_native_schema_after_probe(self):
        with tempfile.TemporaryDirectory() as directory:
            archive, probe = Path(directory) / "archive", Path(directory) / "probe"
            archive.write_bytes(b"synthetic archive fixture")
            probe.write_bytes(b"synthetic exec-only probe")
            with patch.object(smoke.shutil, "which", return_value="synthetic-tool"), \
                    patch.object(smoke, "archive_identity", return_value=("sha256:" + "1" * 64, "arm64")), \
                    patch.object(smoke, "loaded_image_identity", return_value="sha256:" + "2" * 64), \
                    patch.object(smoke, "success", side_effect=[b"linux/arm64", b"synthetic manifest", b""]), \
                    patch.object(smoke, "Fixture") as factory:
                evidence = smoke.qualify(str(archive), str(probe))
                factory.return_value.native_operator.assert_called_once_with()
                factory.return_value.cleanup.assert_called_once_with()
        native = evidence["native_operator"]
        self.assertEqual(set(native), r.NATIVE_CASES | {"browser_inside_image"})
        self.assertEqual(native["browser_inside_image"], "not_run")
        self.assertTrue(all(r.boolean(native[key], True) for key in r.NATIVE_CASES))
        self.assertEqual(set(evidence["cases"]), r.QUALIFICATIONS)
        self.assertEqual(evidence["previous_release_compatibility"], "unknown")

    def test_native_operator_requires_frozen_manifest(self):
        fixture = smoke.Fixture("sha256:" + "1" * 64, "/synthetic-probe")
        with patch.object(smoke, "command") as cmd:
            self.stop("image_archive", lambda: fixture.once("synthetic-new-state", "init", operator=True))
        cmd.assert_not_called()

    def test_delivery_workflows_hard_disabled_and_serialized(self):
        root = Path(__file__).resolve().parents[1]
        if not (root / ".github/workflows").exists():
            self.skipTest("Nix isolated script-only check; workflow check runs in checkout")
        for name in ("publish-image.yml", "tinfoil-release-publish.yml", "deploy-production.yml"):
            text = (root / ".github/workflows" / name).read_text()
            self.assertIn("${{ false &&", text)
            self.assertIn("cancel-in-progress: false", text)
            self.assertIn("RELEASE_AUTOMATION_ENABLED: 'false'", text)
        text = (root / ".github/workflows/deploy-production.yml").read_text()
        self.assertIn("PRODUCTION_DEPLOYMENT_ENABLED: 'false'", text)
        self.assertNotIn("TINFOIL_PRODUCTION_ADMIN_KEY", text)
        self.assertNotIn("blue_green", text)


if __name__ == "__main__":
    unittest.main()
