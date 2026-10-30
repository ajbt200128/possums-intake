"""Offline signing guard tests: synthetic API/command ports, no Nix acceptance."""
import copy
import importlib.util
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import patch

import release as r
import test_synthetic_publication as old

spec = importlib.util.spec_from_file_location("public_tinfoil_release_tests", Path(__file__).with_name("public-tinfoil-release.py"))
if spec is None or spec.loader is None:
    raise AssertionError("fixture module unavailable")
s = importlib.util.module_from_spec(spec)
spec.loader.exec_module(s)
SOURCE, RELEASE, TAG = old.SOURCE, "2" * 40, "v0.2.0"
DRV = "/nix/store/" + "a" * 32 + "-synthetic-ledger-image.drv"
ROOT = Path(__file__).resolve().parents[1]


def release(number, tag):
    return {"id": number, "tag_name": tag, "draft": False, "prerelease": False}


class FakeAPI(old.FakeAPI):
    def __init__(self, ctx):
        super().__init__()
        self.ctx = ctx
        self.repo_data.update(name="possums-intake", private=False)
        self.main = RELEASE
        self.current: Any = dict(self.current)
        self.checks: Any = self.checks
        self.current.update(path=".github/workflows/" + s.WORKFLOW, head_sha=RELEASE,
                            head_branch=TAG, conclusion=None)
        self.release_check: dict[str, Any] = dict(old.run("check.yml", 11), head_sha=RELEASE)
        self.before = (200, {"id": 200, "name": s.c.PACKAGE, "package_type": "container",
                             "owner": {"login": s.c.OWNER}, "visibility": "public"})
        self.refs: dict[str, Any] = {
            "/git/ref/heads/main": {"ref": "refs/heads/main", "object": {"sha": RELEASE, "type": "commit"}},
            "/git/ref/tags/" + TAG: {"ref": "refs/tags/" + TAG, "object": {"sha": RELEASE, "type": "commit"}}}
        self.signing: list[Any] = [self.current]
        self.signing_pages: Any = None
        self.releases: list[Any] = [[]]

    def repo(self, suffix=""):
        if suffix in self.refs:
            self.reads.append(suffix)
            return copy.deepcopy(self.refs[suffix])
        if suffix.startswith("/actions/runs/11"):
            self.reads.append(suffix)
            return copy.deepcopy(self.release_check)
        if suffix.startswith("/actions/workflows/" + s.WORKFLOW):
            self.reads.append(suffix)
            page = int(suffix.rsplit("&page=", 1)[1])
            batch = self.signing if self.signing_pages is None else self.signing_pages[page - 1]
            return {"total_count": len(self.signing), "workflow_runs": copy.deepcopy(batch)}
        if suffix.startswith("/releases?"):
            self.reads.append(suffix)
            value = self.releases[int(suffix.rsplit("&page=", 1)[1]) - 1]
            if isinstance(value, BaseException):
                raise value
            return copy.deepcopy(value)
        return super().repo(suffix)


class FakeGit:
    """Narrow command port; arbitrary commands (including publishers) refuse."""
    def __init__(self, root, ctx):
        self.root, self.ctx = root, ctx
        self.calls: list[list[str]] = []
        self.head = RELEASE.encode() + b"\n"
        self.parents = (RELEASE + " " + SOURCE + "\n").encode()
        self.change = b"A\0tinfoil-config.yml\0"
        self.mode = b"100644"
        self.template = s.c.read_template()
        self.config = s.c.render(self.template, ctx["image"], phase=s.c.PHASE)
        (root / s.c.TEMPLATE).parent.mkdir(parents=True)
        (root / s.c.TEMPLATE).write_bytes(self.template)
        (root / s.CONFIG).write_bytes(self.config)
        self.trees = {label: {s.c.TEMPLATE: (b"100644", self.template), "flake.nix": (b"100644", b"synthetic")}
                      for label in ("source", "release")}
        self.trees["release"][s.CONFIG] = (b"100644", self.config)
        self.drvs: list[Any] = [DRV.encode(), DRV.encode()]
        self.nix_count = 0
        self.snapshots: list[Path] = []
        self.omit: str | None = None
        self.hash_wrong = False

    def blob(self, name):
        return {s.c.TEMPLATE: "a", "flake.nix": "b", s.CONFIG: "c"}.get(name, "d") * 40

    def tool(self, args, code, timeout=120):
        self.calls.append(args)
        if args[0] == "git":
            a = args[3:]
            if a == ["rev-parse", "HEAD"]:
                return self.head
            if a[0] == "rev-list":
                return self.parents
            if a[0] == "diff-tree":
                return self.change
            if a[0] == "ls-tree":
                if "-rz" in a:
                    label = "source" if a[-1] == SOURCE else "release"
                    return b"".join(mode + b" blob " + self.blob(name).encode() + b"\t" + name.encode() + b"\0"
                                    for name, (mode, _raw) in self.trees[label].items())
                name = a[-1]
                mode = self.mode if name == s.CONFIG else b"100644"
                return mode + b" blob " + self.blob(name).encode() + b"\t" + name.encode() + b"\n"
            if a[0] == "show":
                return self.config if a[-1].endswith(":" + s.CONFIG) else self.template
            if "archive" in a:
                Path(next(item[9:] for item in a if item.startswith("--output="))).write_bytes(b"synthetic archive")
                return b""
            if a[0] == "hash-object":
                if "-w" in a or "--no-filters" not in a:
                    raise AssertionError("fixture object-store mutation")
                hashes = []
                for name in a[a.index("--") + 1:]:
                    path = Path(name)
                    base = next(base for base in self.snapshots if base in path.parents)
                    blob = "0" * 40 if self.hash_wrong else self.blob(path.relative_to(base).as_posix())
                    hashes.append(blob.encode() + b"\n")
                return b"".join(hashes)
        if args[0] == "tar":
            target = Path(args[args.index("--directory") + 1])
            self.snapshots.append(target)
            for name, (mode, raw) in self.trees[target.name].items():
                if name == self.omit:
                    continue
                path = target / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(raw)
                path.chmod(0o755 if mode == b"100755" else 0o644)
            return b""
        if args[:4] == ["nix", "eval", "--offline", "--raw"]:
            value = self.drvs[self.nix_count]
            self.nix_count += 1
            if isinstance(value, BaseException):
                raise value
            return value
        raise AssertionError("unapproved fixture command")


class Fixture(old.Fixture):
    def __init__(self, root):
        root = Path(root)
        accepted = root / "accepted"
        accepted.mkdir()
        super().__init__(accepted)
        self.synthetic["startup_diagnostics"] = dict.fromkeys(s.d.STARTUP_CASES, True)
        self.save()
        self.ctx = {"source": SOURCE, "release": RELEASE, "tag": TAG, "run": 30,
                    "checks_run": 10, "checks_attempt": 1, "release_checks_run": 11, "release_checks_attempt": 1,
                    "qualification_run": 20, "qualification_attempt": 1, "image": self.digest,
                    "archive": self.archive_hash, "phase": s.c.PHASE}
        self.api = FakeAPI(self.ctx)
        self.git = FakeGit(root, self.ctx)
        self.checkout = root

    def command(self, args, code, timeout=120):
        if args[0] == "skopeo":
            if args[1] not in ("--version", "inspect") or any("docker://" in arg for arg in args):
                raise AssertionError("registry command forbidden")
            return self.tool(args, code, timeout)
        return self.git.tool(args, code, timeout)

    def admit(self):
        return s.admit(self.api, self.ctx, self.checkout, self.command)

    def guard(self, downloaded=None):
        return s.guard(self.api, self.ctx, self.checkout,
                       {"a": 100, "b": 101, "archive": 102} if downloaded is None else downloaded,
                       tool=self.command, accepted=self.root)

    def dispatch(self):
        event = {"repository": {"full_name": s.c.REPOSITORY, "private": False, "owner": {"login": s.c.OWNER}},
                 "sender": {"login": s.c.OWNER}, "inputs": {
                     "source": SOURCE, "checks_run": "10", "checks_attempt": "1", "release_checks_run": "11",
                     "release_checks_attempt": "1", "qualification_run": "20", "qualification_attempt": "1",
                     "image_digest": self.digest, "archive_sha256": self.archive_hash, "storage_phase": s.c.PHASE}}
        env = {"PUBLIC_INTAKE_RELEASE_ENABLED": "true", "RELEASE_AUTOMATION_ENABLED": "true",
               "GITHUB_REPOSITORY": s.c.REPOSITORY,
               "GITHUB_ACTOR": s.c.OWNER, "GITHUB_TRIGGERING_ACTOR": s.c.OWNER,
               "GITHUB_RUN_ATTEMPT": "1", "GITHUB_RUN_ID": "30", "GITHUB_SHA": RELEASE,
               "GITHUB_REF": "refs/tags/" + TAG, "GITHUB_EVENT_NAME": "workflow_dispatch",
               "GITHUB_EVENT_PATH": str(self.checkout / "event.json")}
        return event, env


class SigningTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="intake-signing-test-")
        self.addCleanup(self.tmp.cleanup)
        self.f = Fixture(self.tmp.name)

    def stop(self, operation, code=None):
        with self.assertRaises(r.Stop) as caught:
            operation()
        error = caught.exception
        if code:
            self.assertEqual(error.code, code)
        projection = json.dumps(s.diagnostic(error))
        self.assertNotIn("hostile", projection)
        self.assertNotIn("SYNTHETIC_SECRET", projection)
        self.assertEqual(error.outcome, "no_effect")
        return error

    def relation(self):
        return s.relation_gate(self.f.ctx, self.f.checkout, self.f.command)

    def test_exact_public_admission_separates_application_and_current_main_release(self):
        admitted = self.f.admit()
        self.assertEqual(admitted["source"], SOURCE)
        self.assertEqual(admitted["artifacts"], {"a": 100, "b": 101, "archive": 102})
        self.assertIn("/actions/runs/10/attempts/1", self.f.api.reads)
        self.assertIn("/actions/runs/11/attempts/1", self.f.api.reads)
        self.assertEqual(self.f.api.refs["/git/ref/heads/main"]["object"]["sha"], RELEASE)
        self.assertEqual(self.f.git.nix_count, 2)
        self.assertTrue(all(not path.parent.exists() for path in self.f.git.snapshots))
        for args in self.f.git.calls:
            self.assertIn(args[0], ("git", "tar", "nix"))
            if args[0] == "nix":
                self.assertEqual(args[1:4], ["eval", "--offline", "--raw"])
                self.assertTrue(args[4].startswith("path:/tmp/intake-public-signing-"))
                self.assertTrue(args[4].endswith("#packages.x86_64-linux.ledger-image.drvPath"))
                self.assertEqual(args[5:], ["--option", "allow-import-from-derivation", "false"])

    def test_context_exact_inputs_and_public_owner_tag(self):
        event, env = self.f.dispatch()
        path = Path(env["GITHUB_EVENT_PATH"])
        path.write_text(json.dumps(event))
        self.assertEqual(s.context(env), self.f.ctx)
        changes = [("PUBLIC_INTAKE_RELEASE_ENABLED", "false"), ("GITHUB_REPOSITORY", "other/repo"),
                   ("GITHUB_ACTOR", "other"), ("GITHUB_TRIGGERING_ACTOR", "other"),
                   ("GITHUB_RUN_ATTEMPT", "2"), ("GITHUB_REF", "refs/heads/main"),
                   ("GITHUB_REF", "refs/tags/v01.2.3"), ("GITHUB_EVENT_NAME", "push"),
                   ("GITHUB_SHA", "hostile"), ("GITHUB_RUN_ID", "0")]
        for key, value in changes:
            with self.subTest(key=key, value=value), patch.object(s.p, "read_file", side_effect=AssertionError("early read")) as read:
                self.stop(lambda key=key, value=value: s.context({**env, key: value}), "disabled" if key.startswith("PUBLIC_") else "signing_context")
                read.assert_not_called()
        cases = [lambda e: e["repository"].update(private=True),
                 lambda e: e["sender"].update(login="other"), lambda e: e["repository"].update(full_name="other/repo"),
                 lambda e: e["inputs"].update(source="not-sha"), lambda e: e["inputs"].update(source=RELEASE),
                 lambda e: e["inputs"].update(checks_attempt="0"), lambda e: e["inputs"].update(release_checks_attempt="01"),
                 lambda e: e["inputs"].update(qualification_attempt="-1"),
                 lambda e: e["inputs"].update(image_digest="sha256:" + "0" * 64),
                 lambda e: e["inputs"].update(archive_sha256="X" * 64),
                 lambda e: e["inputs"].update(storage_phase="persistent-init"), lambda e: e["inputs"].update(extra="hostile")]
        for mutate in cases:
            modified = copy.deepcopy(event)
            mutate(modified)
            path.write_text(json.dumps(modified))
            self.stop(lambda: s.context(env), "signing_context")
        self.assertEqual(self.f.api.reads, [])
        self.assertEqual(self.f.git.calls, [])

    def test_explicit_repository_readiness_only_reads_fixed_metadata(self):
        with patch.object(s.p, "GitHub", return_value=self.f.api), \
                patch.object(s, "context", side_effect=AssertionError("dispatch context")), \
                patch.object(s.p, "read_file", side_effect=AssertionError("event file")), \
                patch.object(s.p, "command", side_effect=AssertionError("process")), \
                patch.object(s.c, "read_template", side_effect=AssertionError("template")), \
                patch.object(s.Path, "open", side_effect=AssertionError("file")), \
                patch.dict(s.os.environ, {}, clear=True), patch.object(s.sys, "stdout", io.StringIO()) as out:
            self.assertEqual(s.main(["repository-readiness"]), 0)
        self.assertEqual(json.loads(out.getvalue()), {
            "passed": True, "stage": "public_repository_readiness", "authenticated_evidence": True,
            "publication_attempted": False, "runtime_verified": False})
        self.assertEqual(self.f.api.reads, ["", "package"])
        self.assertEqual(self.f.git.calls, [])

    def test_repository_readiness_refuses_private_inaccessible_and_unexpected_metadata(self):
        for status in (403, 404):
            with self.subTest(status=status), patch.object(self.f.api, "package", return_value=(status, None)), \
                    patch.object(s.p, "GitHub", return_value=self.f.api), \
                    patch.object(s.sys, "stdout", io.StringIO()) as out:
                self.assertEqual(s.main(["repository-readiness"]), 1)
                diagnostic = json.loads(out.getvalue())
                self.assertEqual(diagnostic["code"], "DELIVERY_SIGNING_REPOSITORY")
                self.assertEqual(diagnostic["status"], status)
        self.f.api.repo_data["private"] = True
        with patch.object(s.p, "GitHub", return_value=self.f.api), patch.object(s.sys, "stdout", io.StringIO()) as out:
            self.assertEqual(s.main(["repository-readiness"]), 1)
            self.assertEqual(json.loads(out.getvalue())["code"], "DELIVERY_SIGNING_REPOSITORY")
        with patch.object(s.p, "GitHub", return_value=self.f.api), \
                patch.object(self.f.api, "repo", side_effect=RuntimeError("hostile SYNTHETIC_SECRET")), \
                patch.object(s.sys, "stdout", io.StringIO()) as out:
            self.assertEqual(s.main(["repository-readiness"]), 1)
            self.assertEqual(json.loads(out.getvalue())["code"], "DELIVERY_SIGNING_REPOSITORY")
            self.assertNotIn("SYNTHETIC_SECRET", out.getvalue())
        self.assertEqual(self.f.git.calls, [])

    def test_disabled_or_unrecognized_public_selection_refuses_before_reads(self):
        _event, selected_env = self.f.dispatch()
        for flag, value in ((flag, value)
                            for flag in ("PUBLIC_INTAKE_RELEASE_ENABLED", "RELEASE_AUTOMATION_ENABLED")
                            for value in (None, "", "false", "TRUE", "True", "1", "true ", "hostile SYNTHETIC_SECRET")):
            env = dict(selected_env)
            if value is None:
                del env[flag]
            else:
                env[flag] = value
            for mode in ("admit", "guard"):
                with self.subTest(flag=flag, value=value, mode=mode), \
                        patch.object(s.os, "environ", env), \
                        patch.object(s.p, "read_file", side_effect=AssertionError("event read")) as read, \
                        patch.object(s.p, "GitHub", side_effect=AssertionError("auth/API")) as api, \
                        patch.object(s.p, "command", side_effect=AssertionError("process")) as command, \
                        patch.object(s.c, "read_template", side_effect=AssertionError("template")) as template, \
                        patch.object(s.Path, "open", side_effect=AssertionError("file")) as opened, \
                        patch.object(s.sys, "stdout", io.StringIO()) as out:
                    self.assertEqual(s.main([mode]), 1)
                    self.assertEqual(json.loads(out.getvalue())["code"], "DELIVERY_DISABLED")
                    for port in (read, api, command, template, opened):
                        port.assert_not_called()
        self.assertEqual(self.f.api.reads, [])
        self.assertEqual(self.f.git.calls, [])

    def test_invalid_context_refuses_before_external_work(self):
        for key, value in (("source", "bad"), ("tag", "v0.1"), ("phase", "persistent-init"), ("run", True)):
            original = self.f.ctx[key]
            self.f.ctx[key] = value
            self.stop(self.f.admit, "signing_context")
            self.f.ctx[key] = original
        self.assertEqual(self.f.api.reads, [])
        self.assertEqual(self.f.git.calls, [])

    def test_private_wrong_owner_and_package_identity_refuse(self):
        for key, value in (("private", True), ("name", "other"), ("id", True), ("owner", {"login": "other"})):
            original = self.f.api.repo_data[key]
            self.f.api.repo_data[key] = value
            self.stop(self.f.admit, "signing_repository")
            self.f.api.repo_data[key] = original
        package = self.f.api.before[1]
        for key, value in (("visibility", "private"), ("owner", {"login": "other"}), ("id", 0), ("name", "other")):
            original = package[key]
            package[key] = value
            self.stop(self.f.admit, "signing_repository")
            package[key] = original
        for status in (401, 403, 404, 429, 500):
            with patch.object(self.f.api, "package", return_value=(status, None)):
                self.assertEqual(self.stop(self.f.admit, "signing_repository").status, status)
        self.assertEqual(self.f.git.calls, [])

    def test_current_invocation_owner_ref_event_status_attempt_and_path_refuse(self):
        changes = (("id", 31), ("run_attempt", 2), ("run_attempt", True), ("head_sha", SOURCE),
                   ("head_branch", "main"), ("path", ".github/workflows/other.yml"), ("event", "push"),
                   ("status", "completed"), ("conclusion", "success"), ("actor", {"login": "other"}),
                   ("triggering_actor", {"login": "other"}), ("head_repository", {"full_name": "other/repo"}))
        for key, value in changes:
            original = self.f.api.current[key]
            self.f.api.current[key] = value
            self.stop(self.f.admit, "signing_current")
            self.f.api.current[key] = original
        self.assertEqual(self.f.git.calls, [])

    def test_stale_main_and_annotated_or_wrong_tag_refuse(self):
        for suffix, key, value in (("heads/main", "sha", SOURCE), ("tags/" + TAG, "sha", SOURCE),
                                   ("tags/" + TAG, "type", "tag")):
            obj = self.f.api.refs["/git/ref/" + suffix]["object"]
            original = obj[key]
            obj[key] = value
            self.stop(self.f.admit, "signing_source")
            obj[key] = original
        self.assertEqual(self.f.git.calls, [])

    def test_each_exact_check_run_source_attempt_and_success_required(self):
        for check in (self.f.api.checks[0], self.f.api.release_check):
            for key, value in (("id", 99), ("run_attempt", 2), ("conclusion", "failure"), ("event", "pull_request"),
                               ("head_branch", "feature"), ("head_sha", "3" * 40)):
                original = check[key]
                check[key] = value
                self.stop(self.f.admit, "signing_checks")
                check[key] = original
        self.assertEqual(self.f.git.calls, [])

    def test_qualification_attempt_source_artifacts_and_distinct_hosted_runners(self):
        for key, value in (("run_attempt", 2), ("head_sha", RELEASE), ("conclusion", "failure")):
            original = self.f.api.qualified[key]
            self.f.api.qualified[key] = value
            self.stop(self.f.admit, "signing_qualification")
            self.f.api.qualified[key] = original
        for index, key, value in ((0, "expired", True), (0, "id", 101), (0, "name", "wrong"),
                                  (0, "workflow_run", {"id": 20, "head_sha": RELEASE})):
            artifact = self.f.api.artifacts[index]
            original = artifact[key]
            artifact[key] = value
            self.stop(self.f.admit, "signing_qualification")
            artifact[key] = original
        for key, value in (("runner_id", 2), ("labels", ["self-hosted", "ubuntu-latest"])):
            builder = self.f.api.jobs[2]
            original = builder[key]
            builder[key] = value
            self.stop(self.f.admit, "signing_qualification")
            builder[key] = original
        self.assertEqual(self.f.git.calls, [])

    def test_prior_signing_rerun_and_missing_current_refuse(self):
        for signing in ([self.f.api.current, dict(self.f.api.current, id=31)], [],
                        [dict(self.f.api.current, run_attempt=2)]):
            self.f.api.signing = signing
            self.stop(self.f.admit, "signing_current" if signing and signing[0].get("run_attempt") == 2 else "signing_prior")
        self.assertEqual(self.f.git.calls, [])

    def test_release_absence_requires_complete_authenticated_list_not_404(self):
        self.f.api.releases = [[release(1, "v0.1.0")]]
        self.assertIsNone(s.absence_gate(self.f.api, self.f.ctx))
        for status in (401, 403, 404, 429, 500):
            self.f.api.releases = [r.Stop("synthetic_evidence", status=status)]
            self.assertEqual(self.stop(lambda: s.absence_gate(self.f.api, self.f.ctx), "signing_inventory_read").status, status)
        self.f.api.releases = [[release(1, TAG)]]
        self.stop(lambda: s.absence_gate(self.f.api, self.f.ctx), "signing_exists")
        self.assertFalse(any("/releases/tags/" in path for path in self.f.api.reads))

    def test_release_inventory_malformed_duplicate_ids_tags_and_cap_refuse(self):
        cases = [None, {}, [True], [{"id": True}], [release(1, "bad")],
                 [release(1, "v0.1.0"), release(1, "v0.1.1")],
                 [release(1, "v0.1.0"), release(2, "v0.1.0")],
                 [dict(release(1, "v0.1.0"), draft=1)]]
        for batch in cases:
            self.f.api.releases = [batch]
            self.stop(lambda: s.absence_gate(self.f.api, self.f.ctx), "signing_inventory")
        self.f.api.releases = [[release(i + 1, f"v0.1.{i}") for i in range(100)]]
        with patch.object(s, "PAGE_CAP", 1):
            self.stop(lambda: s.absence_gate(self.f.api, self.f.ctx), "signing_inventory")
        self.f.api.releases.append([release(101, "v0.9.9")])
        self.assertIsNone(s.absence_gate(self.f.api, self.f.ctx))
        self.assertIn("/releases?per_page=100&page=2", self.f.api.reads)
        self.f.api.releases[1] = [release(1, "v0.9.9")]
        self.stop(lambda: s.absence_gate(self.f.api, self.f.ctx), "signing_inventory")

    def test_signing_inventory_total_count_duplicates_and_cap_refuse(self):
        self.f.api.signing_pages = [[self.f.api.current, self.f.api.current]]
        self.stop(lambda: s.absence_gate(self.f.api, self.f.ctx), "signing_inventory")
        self.f.api.signing_pages = [[]]
        self.stop(lambda: s.absence_gate(self.f.api, self.f.ctx), "signing_inventory")
        self.f.api.signing_pages = [[dict(self.f.api.current, id=i + 1) for i in range(100)]]
        with patch.object(s, "PAGE_CAP", 1):
            self.stop(lambda: s.absence_gate(self.f.api, self.f.ctx), "signing_inventory")

    def test_config_add_or_modify_only_sole_parent_and_head(self):
        self.f.git.change = b"M\0tinfoil-config.yml\0"
        self.f.git.trees["source"][s.CONFIG] = (b"100644", b"prior public config")
        self.relation()
        for attr, value in (("head", SOURCE.encode()), ("parents", (RELEASE + "\n").encode()),
                            ("parents", (RELEASE + " " + SOURCE + " " + "3" * 40).encode()),
                            ("parents", (RELEASE + " " + "3" * 40).encode()),
                            ("change", b"M\0flake.nix\0"), ("change", b"A\0tinfoil-config.yml\0M\0main.go\0"),
                            ("change", b"D\0tinfoil-config.yml\0"), ("mode", b"120000"), ("mode", b"100755")):
            original = getattr(self.f.git, attr)
            setattr(self.f.git, attr, value)
            self.stop(self.relation, "signing_relation")
            setattr(self.f.git, attr, original)

    def test_config_exact_intent_cvm_disks_template_no_fallback(self):
        original = self.f.git.config
        for value in (original + b"\n", original.replace(b"0.14.14", b"0.14.13"),
                      original.replace(b"--stripe-live", b"--stripe-synthetic"), b"disks: []\n"):
            self.f.git.config = value
            (self.f.checkout / s.CONFIG).write_bytes(value)
            self.stop(self.relation, "signing_relation")
        self.f.git.config = original
        (self.f.checkout / s.CONFIG).write_bytes(original)
        template = self.f.checkout / s.c.TEMPLATE
        template.write_bytes(self.f.git.template + b"\n")
        self.stop(self.relation, "public_config")
        template.write_bytes(self.f.git.template)
        template.unlink()
        template.symlink_to(self.f.checkout / s.CONFIG)
        self.stop(self.relation, "signing_relation")

    def test_local_active_config_symlink_or_dirty_bytes_refuse(self):
        path = self.f.checkout / s.CONFIG
        path.write_bytes(b"hostile")
        self.stop(self.relation, "signing_relation")
        path.unlink()
        path.symlink_to(self.f.checkout / s.c.TEMPLATE)
        self.stop(self.relation, "signing_relation")

    def test_derivation_requires_actual_two_offline_eval_results_and_equality(self):
        for results in ([DRV.encode(), (DRV + "extra").encode()], [b"hostile", DRV.encode()],
                        [DRV.encode() + b"\n", DRV.encode()], [RuntimeError("hostile SYNTHETIC_SECRET"), DRV.encode()]):
            self.f.git.drvs, self.f.git.nix_count = results, 0
            self.stop(self.relation, "signing_drv")
            self.assertTrue(all(not path.parent.exists() for path in self.f.git.snapshots))

    def test_snapshot_rejects_symlink_submodule_missing_and_substituted_blob(self):
        for mode in (b"120000", b"160000"):
            self.f.git.trees["source"]["evil"] = (mode, b"../../outside")
            self.stop(self.relation, "signing_drv")
        del self.f.git.trees["source"]["evil"]
        for name in ("../outside", "/absolute", "-option", "control\nname"):
            self.f.git.trees["source"][name] = (b"100644", b"synthetic")
            self.stop(self.relation, "signing_drv")
            del self.f.git.trees["source"][name]
        self.f.git.omit = "flake.nix"
        self.stop(self.relation, "signing_drv")
        self.f.git.omit = None
        self.f.git.hash_wrong = True
        self.stop(self.relation, "signing_drv")
        self.assertEqual(self.f.git.nix_count, 0)

    def test_snapshot_cleanup_failure_keeps_primary_and_classifies_secondary(self):
        remove = s.shutil.rmtree
        owned: list[Path] = []
        def fail(path):
            owned.append(Path(path))
            raise RuntimeError("hostile SYNTHETIC_SECRET")
        try:
            with patch.object(s.shutil, "rmtree", side_effect=fail):
                self.stop(self.relation, "signing_cleanup")
            self.f.git.nix_count = 0
            self.f.git.drvs = [b"bad", DRV.encode()]
            with patch.object(s.shutil, "rmtree", side_effect=fail):
                error = self.stop(self.relation, "signing_drv")
            self.assertEqual(s.diagnostic(error)["secondary"][0]["code"], "DELIVERY_SIGNING_CLEANUP")
        finally:
            for path in owned:
                remove(path)

    def test_guard_reuses_actual_archive_startup_cases_and_exact_receipt(self):
        result = self.f.guard()
        self.assertTrue(result["passed"])
        self.assertFalse(result["published"])
        self.assertFalse(result["cryptographically_verified"])
        self.assertEqual(len(self.f.calls), 3)
        self.assertTrue(all(args[0] == "skopeo" and "copy" not in args for args in self.f.calls))
        self.assertFalse(any("releases" in arg or "deploy" in arg or "docker://" in arg
                             for args in self.f.git.calls + self.f.calls for arg in args))

    def test_guard_re_admits_artifact_ids_and_refuses_changed_pin_before_archive(self):
        self.stop(lambda: self.f.guard({"a": 999, "b": 101, "archive": 102}), "signing_archive")
        self.assertEqual(self.f.calls, [])
        self.f.api.current["run_attempt"] = 2
        self.stop(self.f.guard, "signing_current")
        self.assertEqual(self.f.calls, [])

    def test_guard_archive_hash_startup_case_manifest_and_expected_receipt_mismatch(self):
        for key, value in (("image", "sha256:" + "9" * 64), ("archive", "9" * 64)):
            original = self.f.ctx[key]
            self.f.ctx[key] = value
            self.f.git.config = s.c.render(self.f.git.template, self.f.ctx["image"], phase=s.c.PHASE)
            (self.f.checkout / s.CONFIG).write_bytes(self.f.git.config)
            self.f.git.trees["release"][s.CONFIG] = (b"100644", self.f.git.config)
            self.stop(self.f.guard, "signing_archive")
            self.f.ctx[key] = original
            self.f.git.nix_count = 0
        self.f.git.config = s.c.render(self.f.git.template, self.f.ctx["image"], phase=s.c.PHASE)
        (self.f.checkout / s.CONFIG).write_bytes(self.f.git.config)
        self.f.git.trees["release"][s.CONFIG] = (b"100644", self.f.git.config)
        for mutation in ("startup", "hash", "manifest"):
            if mutation == "startup":
                self.f.synthetic["startup_diagnostics"]["closed_failure_output"] = False
                self.f.save()
            elif mutation == "hash":
                (self.f.root / "archive/image.tar.gz").write_bytes(b"changed archive")
            else:
                (self.f.root / "archive/image.tar.gz").write_bytes(self.f.archive)
                self.f.raw += b" "
            self.f.git.nix_count = 0
            self.stop(self.f.guard, "signing_archive")
            self.f.synthetic["startup_diagnostics"]["closed_failure_output"] = True
            self.f.save()

    def test_hostile_api_and_archive_failures_keep_stage_status_without_values(self):
        for code, target, method in (("signing_repository", self.f.api, "package"),
                                     ("signing_qualification", self.f.api, "pages")):
            for failure in (r.Stop("synthetic_evidence", status=403), RuntimeError("hostile SYNTHETIC_SECRET"), KeyboardInterrupt()):
                with patch.object(target, method, side_effect=failure):
                    error = self.stop(self.f.admit, code)
                self.assertEqual(error.status, 403 if isinstance(failure, r.Stop) else None)
        receipt = {"publicSourceSHA": SOURCE, "imageDigest": self.f.digest, "archiveSHA": self.f.archive_hash}
        for changed in ({**receipt, "publicSourceSHA": RELEASE}, {**receipt, "extra": True},
                        RuntimeError("hostile SYNTHETIC_SECRET")):
            self.f.git.nix_count = 0
            kwargs: dict[str, Any] = {"side_effect": changed} if isinstance(changed, BaseException) else {"return_value": changed}
            with patch.object(s.d, "archive_gate", **kwargs):
                self.stop(self.f.guard, "signing_archive")

    def test_outputs_only_bounded_authenticated_ids_names_and_run_binding(self):
        admitted = self.f.admit()
        path = self.f.checkout / "output"
        path.touch()
        s.outputs(admitted, {"GITHUB_OUTPUT": str(path)})
        values = dict(line.split("=", 1) for line in path.read_text().splitlines())
        self.assertEqual(set(values), {"qualification_run", "qualification_attempt", "a", "b", "archive", "a_name", "b_name", "archive_name"})
        self.assertEqual(values["a"], "100")
        self.assertEqual(values["qualification_run"], "20")
        self.assertIn(SOURCE, values["a_name"])
        self.assertLess(path.stat().st_size, 1024)
        admitted["artifacts"]["a"] = "hostile\nSYNTHETIC_SECRET=bad"
        self.stop(lambda: s.outputs(admitted, {"GITHUB_OUTPUT": str(path)}), "signing_output")
        self.assertNotIn("hostile", path.read_text())

    def test_status_and_import_do_not_touch_env_auth_process_network_template(self):
        class NoEnv(dict):
            def get(self, *args):
                raise AssertionError("environment access")
            def __getitem__(self, key):
                raise AssertionError("environment access")
        with patch.object(s.os, "environ", NoEnv()), patch.object(s.p, "GitHub", side_effect=AssertionError("auth")), \
                patch.object(s.p, "command", side_effect=AssertionError("process")), \
                patch.object(s.c, "read_template", side_effect=AssertionError("template")), \
                patch.object(s.Path, "open", side_effect=AssertionError("file")), patch.object(s.sys, "stdout", io.StringIO()) as out:
            self.assertEqual(s.main(["status"]), 0)
        status = json.loads(out.getvalue())
        self.assertEqual(status, {"stage": "offline_public_signing_status", "live_activation_observed": False,
                                  "publication_attempted": False, "cryptographically_verified": False,
                                  "runtime_verified": False})
        self.assertNotIn("release_enabled", status)
        with patch.object(subprocess, "Popen", side_effect=AssertionError("process")), \
                patch.object(old.p.urllib.request, "build_opener", side_effect=AssertionError("network")), \
                patch.object(os, "getenv", side_effect=AssertionError("env")):
            s.load("signing_no_work_import", "public-tinfoil-release.py")

    def test_cli_hostile_failures_and_closed_streams_preserve_primary(self):
        for failure in (RuntimeError("hostile SYNTHETIC_SECRET"), KeyboardInterrupt()):
            out = io.StringIO()
            with patch.object(s, "context", side_effect=failure), patch.object(s.sys, "stdout", out):
                self.assertEqual(s.main(["admit"]), 1)
            self.assertEqual(json.loads(out.getvalue())["code"], "DELIVERY_SIGNING_COMMAND")
            self.assertNotIn("hostile", out.getvalue())
        class Closed:
            def write(self, text):
                raise BrokenPipeError("hostile SYNTHETIC_SECRET")
            def flush(self):
                raise BrokenPipeError("hostile SYNTHETIC_SECRET")
        for args, expected in ((["status"], "DELIVERY_SIGNING_OUTPUT"), (["bad"], "DELIVERY_SIGNING_COMMAND")):
            err = io.StringIO()
            with patch.object(s.sys, "stdout", Closed()), patch.object(s.sys, "stderr", err):
                self.assertEqual(s.main(args), 1)
            self.assertEqual(json.loads(err.getvalue())["code"], expected)
            with patch.object(s.sys, "stdout", Closed()), patch.object(s.sys, "stderr", Closed()):
                self.assertEqual(s.main(args), 1)

    def test_actual_closed_pipe_cli_no_raw_shutdown_exception(self):
        script = str(ROOT / "scripts/public-tinfoil-release.py")
        for args in (["status"], ["bad"], ["admit"]):
            result = subprocess.run([sys.executable, "-c", "import os,runpy,sys; rd,wr=os.pipe(); os.close(rd); "
                "os.dup2(wr,1); os.close(wr); sys.argv=sys.argv[1:]; runpy.run_path(sys.argv[0],run_name='__main__')", script, *args],
                cwd=ROOT / "scripts", env={"PATH": os.environ.get("PATH", ""), "PYTHONDONTWRITEBYTECODE": "1"},
                capture_output=True, timeout=20, check=False)
            self.assertEqual(result.returncode, 1)
            self.assertNotIn(b"Traceback", result.stderr)
            self.assertNotIn(b"Error:", result.stderr)
            self.assertTrue(json.loads(result.stderr)["code"].startswith("DELIVERY_"))

    def test_guarded_official_action_only_publisher_least_permissions_and_exact_downloads(self):
        workflow = ROOT / ".github/workflows/tinfoil-release-publish.yml"
        if not workflow.exists():
            self.skipTest("workflow not included in isolated script test source")
        text = workflow.read_text()
        self.assertIn("on:\n  workflow_dispatch:\n", text)
        self.assertIn("    if: ${{ vars.RELEASE_AUTOMATION_ENABLED == 'true' &&\n"
                      "      vars.PUBLIC_INTAKE_RELEASE_ENABLED == 'true' &&\n"
                      "      github.event_name == 'workflow_dispatch' &&\n"
                      "      github.run_attempt == 1 && github.actor == 'ajbt200128' &&\n"
                      "      startsWith(github.ref, 'refs/tags/v') }}", text)
        self.assertNotIn("${{ false &&", text)
        for switch in ("RELEASE_AUTOMATION_ENABLED", "PUBLIC_INTAKE_RELEASE_ENABLED"):
            self.assertIn(f"  {switch}: >-\n    ${{{{ vars.{switch} == 'true' &&\n"
                          f"        fromJSON(vars.{switch}) == true &&\n        'true' || 'false' }}}}", text)
        for required in ("PRODUCTION_DEPLOYMENT_ENABLED: 'false'", "environment: intake-public-release",
                         "group: intake-signed-release-${{ github.ref_name }}", "cancel-in-progress: false", "fetch-depth: 0",
                         "actions/checkout@8e8c483db84b4bee98b60c0593521ed34d9990e8",
                         "DeterminateSystems/nix-installer-action@21a544727d0c62386e78b4befe52d19ad12692e3",
                         "persist-credentials: false", 'diagnostic-endpoint: ""', "mark-as-latest: 'false'",
                         "tinfoilsh/measure-image-action@59f7ab5dbe472b3097c888ebe64e6a4131fc0975",
                         "${{ github.workspace }}/tinfoil-config.yml", "scripts/public-tinfoil-release.py admit",
                         "scripts/public-tinfoil-release.py guard", "nixpkgs#python3 nixpkgs#skopeo"):
            self.assertIn(required, text)
        self.assertIn("packages: read", text)
        self.assertNotIn("packages: write", text)
        self.assertNotIn("skopeo copy", text)
        self.assertNotIn("release.py publication-guard", text)
        self.assertEqual(text.count("uses: actions/download-artifact@d3f86a106a0bac45b974a628896c90dbdf5c8093"), 3)
        self.assertEqual(text.count("run-id: ${{ steps.admit.outputs.qualification_run }}"), 3)
        self.assertEqual(text.count("artifact-ids:"), 3)
        for artifact in ("a", "b", "archive"):
            self.assertEqual(text.count(f"artifact-ids: ${{{{ steps.admit.outputs.{artifact} }}}}"), 1)
            self.assertIn(f"ACCEPTED_{artifact.upper()}_ARTIFACT_ID: ${{{{ steps.admit.outputs.{artifact} }}}}", text)
        self.assertNotIn("artifact-name:", text)
        self.assertEqual(text.count("uses: tinfoilsh/"), 1)
        self.assertLess(text.index("scripts/public-tinfoil-release.py admit"), text.index("uses: actions/download-artifact@"))
        self.assertLess(text.rindex("uses: actions/download-artifact@"), text.index("scripts/public-tinfoil-release.py guard"))
        self.assertLess(text.index("scripts/public-tinfoil-release.py guard"), text.index("uses: tinfoilsh/"))
        check = (ROOT / ".github/workflows/check.yml").read_text()
        self.assertIn("  packages: read", check)
        self.assertNotIn("packages: write", check)
        self.assertIn("GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}", check)
        self.assertIn("scripts/public-tinfoil-release.py repository-readiness", check)
        self.assertIn("cp ${./.github/workflows/check.yml} .github/workflows/check.yml",
                      (ROOT / "flake.nix").read_text())
        self.assertEqual(set(s.INPUTS), {"source", "checks_run", "checks_attempt", "release_checks_run", "release_checks_attempt",
                                      "qualification_run", "qualification_attempt", "image_digest", "archive_sha256", "storage_phase"})


if __name__ == "__main__":
    unittest.main()
