"""Offline fixed-package projection: injected API only; no credentials or requests."""
import ast
import copy
import importlib.util
import io
import json
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from typing import Any
from unittest.mock import patch

import release as r

spec = importlib.util.spec_from_file_location("inspect_synthetic_package", Path(__file__).with_name("inspect-synthetic-package.py"))
assert spec is not None and spec.loader is not None
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
SOURCE = "a" * 40
HOSTILE = "PRIVATE_SENTINEL_secret_url_identifier_exception"
ROOT = Path(__file__).resolve().parents[1]
KEYS = {"packageobject", "packageid", "packagename", "packagetype", "packageownerpresence", "packageownermatch",
        "packagepublicvisibility", "sourceRepoobject", "sourceRepoid", "sourceReponame", "sourceRepoowner",
        "sourceRepoprivate", "sourceRepodefaultmain", "repositorypresence", "repositoryidpresent", "repositoryidmatch",
        "repositoryfullnamematch", "repositoryprivatefieldpresence", "repositoryprivatebooltrue", "versionLIST",
        "versionschema", "allidpositive", "allidunique", "digestvalid", "tagslist", "tagstringbounds", "tagsunique",
        "completeinventory", "baselinepresent", "targetsourceTagabsent"}


def version(number, digest=m.BASELINE, tags=None):
    return {"id": number, "name": digest, "metadata": {"container": {"tags": [] if tags is None else tags}}}


class API:
    def __init__(self):
        self.repository = {"id": 23, "full_name": m.p.REPO, "owner": {"login": m.p.OWNER},
                           "private": True, "default_branch": "main"}
        self.package_body: Any = {"id": 71, "name": m.p.PACKAGE, "package_type": "container",
                             "owner": {"login": m.p.OWNER}, "visibility": "public",
                             "repository": {"id": 23, "full_name": m.p.REPO, "private": True}}
        self.pages: list[Any] = [[version(1)]]
        self.calls = []
        self.head = SOURCE
        self.failure: tuple[str, BaseException] | None = None

    def fail(self, stage):
        if self.failure and self.failure[0] == stage:
            raise self.failure[1]

    def repo(self, suffix=""):
        self.calls.append(("repo", suffix))
        self.fail("source")
        self.assert_suffix(suffix)
        return copy.deepcopy(self.repository if not suffix else {"object": {"sha": self.head}})

    @staticmethod
    def assert_suffix(suffix):
        assert suffix in {"", "/git/ref/heads/main"}

    def package(self):
        self.calls.append(("package",))
        self.fail("package")
        return 200, copy.deepcopy(self.package_body)

    def get(self, path, *, package=False):
        self.fail("versions")
        page = int(path.rsplit("=", 1)[1])
        assert 1 <= page <= 100
        assert package is True
        assert path == "/users/" + m.p.OWNER + "/packages/container/" + m.p.PACKAGE + f"/versions?state=active&per_page=100&page={page}"
        self.calls.append(("versions", page))
        return 200, copy.deepcopy(self.pages[page - 1])


class InspectTests(unittest.TestCase):
    def setUp(self):
        self.api = API()

    def projection(self):
        value = m.inspect(self.api, SOURCE)
        self.assertEqual(set(value), KEYS)
        self.assertTrue(all(type(v) is bool for v in value.values()))
        text = json.dumps(value)
        for forbidden in (HOSTILE, SOURCE, m.BASELINE, m.p.OWNER, m.p.REPO, m.p.PACKAGE):
            self.assertNotIn(forbidden, text)
        return value

    def refused(self, operation, code, status=None):
        with self.assertRaises(r.Stop) as caught:
            operation()
        error = caught.exception
        self.assertEqual((error.code, error.status, error.outcome), (code, status, "no_effect"))
        diagnostic = r.diagnostic(error)
        self.assertIn("unknown", str(diagnostic["next_step"]))
        self.assertNotIn(HOSTILE, json.dumps(diagnostic))
        return diagnostic

    def test_all_true_closed_output_and_fixed_reads(self):
        self.assertTrue(all(self.projection().values()))
        self.assertEqual(self.api.calls, [("repo", ""), ("repo", "/git/ref/heads/main"), ("package",), ("versions", 1)])
        with patch.object(m, "context", return_value=SOURCE), patch.object(m.p, "GitHub", return_value=self.api):
            stream = io.StringIO()
            with redirect_stdout(stream):
                m.main(["inspect"])
        self.assertEqual(len(stream.getvalue().splitlines()), 1)
        self.assertEqual(set(json.loads(stream.getvalue())), KEYS)

    def test_association_private_presence_distinct_from_value(self):
        link = self.api.package_body["repository"]
        del link["private"]
        value = self.projection()
        self.assertFalse(value["repositoryprivatefieldpresence"])
        self.assertFalse(value["repositoryprivatebooltrue"])
        for private in (False, None, 1, "true", {}, HOSTILE, True):
            with self.subTest(private=private):
                link["private"] = private
                value = self.projection()
                self.assertTrue(value["repositoryprivatefieldpresence"])
                self.assertEqual(value["repositoryprivatebooltrue"], type(private) is bool and private is True)

    def test_package_null_hostile_and_required_fields(self):
        original = copy.deepcopy(self.api.package_body)
        for body in (None, HOSTILE, [], True, {}, {"owner": None, "repository": None}):
            self.api.package_body = body
            value = self.projection()
            self.assertFalse(value["packageid"])
            self.assertFalse(value["repositoryidmatch"])
        for field, key, bad in (("id", "packageid", True), ("name", "packagename", HOSTILE),
                                ("package_type", "packagetype", None), ("owner", "packageownermatch", []),
                                ("visibility", "packagepublicvisibility", "private"), ("repository", "repositorypresence", None)):
            self.api.package_body = copy.deepcopy(original)
            self.api.package_body[field] = bad
            self.assertFalse(self.projection()[key])
        self.api.package_body = original
        self.api.package_body["repository"].update(id=True, full_name=HOSTILE)
        value = self.projection()
        self.assertFalse(value["repositoryidpresent"])
        self.assertFalse(value["repositoryidmatch"])
        self.assertFalse(value["repositoryfullnamematch"])

    def test_authenticated_source_identity_and_current_main_guard(self):
        for field, key, bad in (("id", "sourceRepoid", True), ("full_name", "sourceReponame", HOSTILE),
                                ("owner", "sourceRepoowner", None), ("private", "sourceRepoprivate", 1),
                                ("default_branch", "sourceRepodefaultmain", "other")):
            self.api = API()
            self.api.repository[field] = bad
            value = self.projection()
            self.assertFalse(value[key])
            self.assertFalse(value["completeinventory"])
            self.assertFalse(value["targetsourceTagabsent"])
            self.assertEqual(self.api.calls, [("repo", "")])
        self.api = API()
        self.api.head = "b" * 40
        self.refused(lambda: m.inspect(self.api, SOURCE), "inspect_source")
        self.assertNotIn(("package",), self.api.calls)
        self.api = API()
        self.refused(lambda: m.inspect(self.api, HOSTILE), "inspect_context")
        self.assertEqual(self.api.calls, [])

    def test_complete_pagination_later_baseline_and_no_early_success(self):
        self.api.pages = [[version(i, "sha256:" + "b" * 64) for i in range(1, 101)], [version(101)]]
        self.assertTrue(all(self.projection().values()))
        self.assertEqual(self.api.calls[-1], ("versions", 2))
        self.api = API()
        self.api.pages = [[version(i) for i in range(1, 101)], [version(101, tags=[SOURCE])]]
        self.assertFalse(self.projection()["targetsourceTagabsent"])
        self.assertEqual(self.api.calls[-1], ("versions", 2))
        self.api = API()
        self.api.pages = [[version(i) for i in range(1, 101)], None]
        self.refused(lambda: m.inspect(self.api, SOURCE), "inspect_versions")

    def test_full_cap_refused_even_with_baseline(self):
        self.api.pages = [[version(page * 100 + i + 1) for i in range(100)] for page in range(100)]
        self.refused(lambda: m.inspect(self.api, SOURCE), "inspect_versions")
        self.assertEqual(self.api.calls[-1], ("versions", 100))

    def test_version_schema_predicates_and_empty_inventory(self):
        cases = [([version(1), version(1)], "allidunique"), ([version(True)], "allidpositive"),
                 ([version(1, HOSTILE)], "digestvalid"), ([version(1, tags=["tag", "tag"])], "tagsunique"),
                 ([version(1, tags=[None])], "tagstringbounds"), ([version(1, tags=["x" * 129])], "tagstringbounds"),
                 ([version(1, tags=[{}])], "tagstringbounds"), ([None], "versionschema"),
                 ([{"id": 1, "name": m.BASELINE, "metadata": None}], "tagslist")]
        for batch, key in cases:
            with self.subTest(key=key):
                self.api = API()
                self.api.pages = [batch]
                self.assertFalse(self.projection()[key])
        self.api = API()
        self.api.pages = [[]]
        value = self.projection()
        self.assertTrue(value["completeinventory"])
        self.assertFalse(value["baselinepresent"])
        for batch in (None, {}, HOSTILE, [version(i) for i in range(101)]):
            self.api.pages = [batch]
            self.refused(lambda: m.inspect(self.api, SOURCE), "inspect_versions")

    def test_known_denials_and_unexpected_failures_keep_stage(self):
        for stage in ("source", "package", "versions"):
            for status in (403, 404, 502):
                self.api = API()
                self.api.failure = (stage, r.Stop("synthetic_namespace" if stage != "source" else "synthetic_evidence", status=status))
                diagnostic = self.refused(lambda: m.inspect(self.api, SOURCE), "inspect_" + stage, status)
                self.assertEqual(diagnostic["stage"], "inspect_" + stage + "_read")
            self.api = API()
            self.api.failure = (stage, RuntimeError(HOSTILE))
            self.refused(lambda: m.inspect(self.api, SOURCE), "inspect_" + stage)
        with patch.object(self.api, "package", return_value=(404, None)):
            self.api.failure = None
            self.refused(lambda: m.inspect(self.api, SOURCE), "inspect_package", 404)
        with patch.object(self.api, "package", return_value=None):
            self.refused(lambda: m.inspect(self.api, SOURCE), "inspect_package")
        known = r.Stop("inspect_versions", status=429)
        self.api.failure = ("versions", known)
        with self.assertRaises(r.Stop) as caught:
            m.inspect(self.api, SOURCE)
        self.assertIs(caught.exception, known)

    def test_unexpected_processing_and_output_errors_keep_stage(self):
        with patch.object(m, "project", side_effect=RuntimeError(HOSTILE)):
            self.refused(lambda: m.inspect(self.api, SOURCE), "inspect_source")
        with patch.object(m, "inventory", side_effect=RuntimeError(HOSTILE)):
            self.refused(lambda: m.inspect(self.api, SOURCE), "inspect_versions")
        with patch.object(m, "context", return_value=SOURCE), patch.object(m.p, "GitHub", return_value=self.api), patch("builtins.print", side_effect=RuntimeError(HOSTILE)):
            self.refused(lambda: m.main(["inspect"]), "inspect_output")
            with self.assertRaises(SystemExit) as caught:
                m.cli()
            self.assertEqual(caught.exception.code, 1)

    def test_commands_refuse_before_context_or_network_and_cli_is_closed(self):
        for args in (["status"], ["report"], [], ["inspect", HOSTILE], ["publish"]):
            with patch.object(m, "context") as context, patch.object(m.p, "GitHub") as getter:
                self.refused(lambda args=args: m.main(args), "inspect_command")
                context.assert_not_called()
                getter.assert_not_called()
        with patch.object(m, "main", side_effect=RuntimeError(HOSTILE)):
            stream = io.StringIO()
            with redirect_stdout(stream), self.assertRaises(SystemExit):
                m.cli()
            self.assertEqual(len(stream.getvalue().splitlines()), 1)
            self.assertEqual(json.loads(stream.getvalue())["code"], "DELIVERY_INSPECT_COMMAND")
            self.assertNotIn(HOSTILE, stream.getvalue())

    def test_manual_exact_context_requires_no_environment_secrets(self):
        event = {"inputs": {"inspect": "true", "expected_source": SOURCE}, "sender": {"login": m.p.OWNER},
                 "repository": {"full_name": m.p.REPO, "private": True}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "event.json"
            env = {"GITHUB_EVENT_PATH": str(path), "GITHUB_SHA": SOURCE, "GITHUB_REPOSITORY": m.p.REPO,
                   "GITHUB_REF": "refs/heads/main", "GITHUB_EVENT_NAME": "workflow_dispatch",
                   "GITHUB_ACTOR": m.p.OWNER, "GITHUB_RUN_ATTEMPT": "1"}
            path.write_text(json.dumps(event))
            self.assertEqual(m.context(env), SOURCE)
            for key in env:
                if key != "GITHUB_EVENT_PATH":
                    self.refused(lambda key=key: m.context(dict(env, **{key: HOSTILE})), "inspect_context")
            variants = [None, {}, {"inputs": None}, copy.deepcopy(event), copy.deepcopy(event), copy.deepcopy(event),
                        copy.deepcopy(event), copy.deepcopy(event), copy.deepcopy(event)]
            variants[3]["inputs"]["inspect"] = "false"
            variants[4]["inputs"]["expected_source"] = "b" * 40
            variants[5]["inputs"]["extra"] = HOSTILE
            variants[6]["repository"]["private"] = 1
            variants[7]["sender"]["login"] = HOSTILE
            variants[8]["repository"]["full_name"] = HOSTILE
            for variant in variants:
                path.write_text(json.dumps(variant))
                self.refused(lambda: m.context(env), "inspect_context")
            path.write_text(HOSTILE)
            self.refused(lambda: m.context(env), "inspect_context")

    def test_workflow_read_only_manual_pinned_and_no_automatic_triggers(self):
        workflow = (ROOT / ".github/workflows/inspect-synthetic-package.yml").read_text()
        self.assertEqual([line for line in workflow.splitlines() if line.startswith("  workflow_")], ["  workflow_dispatch:"])
        self.assertIn("default: false", workflow)
        self.assertIn("  contents: read\n  packages: read\n", workflow)
        self.assertEqual(workflow.count("permissions:"), 1)
        for guard in ("inputs.inspect == true", "inputs.expected_source == github.sha", "github.run_attempt == 1",
                      "github.event_name == 'workflow_dispatch'", "github.ref == 'refs/heads/main'",
                      "github.actor == 'ajbt200128'", "github.repository == 'ajbt200128/possums-intake'"):
            self.assertIn(guard, workflow)
        self.assertIn("actions/checkout@8e8c483db84b4bee98b60c0593521ed34d9990e8", workflow)
        self.assertIn("persist-credentials: false", workflow)
        self.assertIn("ref: ${{ github.sha }}", workflow)
        self.assertIn("GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}", workflow)
        self.assertIn("run: python3 scripts/inspect-synthetic-package.py inspect", workflow)
        self.assertIn("native Actions logs", workflow)
        self.assertNotIn("write", workflow.replace("without publication or grant changes", ""))
        for forbidden in ("push:", "pull_request:", "schedule:", "workflow_run:", "upload-artifact", "nix ", "docker", "skopeo", "curl", "install", "status", "report"):
            self.assertNotIn(forbidden, workflow)
        source = (ROOT / "scripts/inspect-synthetic-package.py").read_text()
        tree = ast.parse(source)
        self.assertEqual({node.value for node in ast.walk(tree) if isinstance(node, ast.Constant) and isinstance(node.value, str) and node.value.startswith("/")}, {"/git/ref/heads/main", "/users/", "/packages/container/", "/versions?state=active&per_page=100&page="})
        for forbidden in ("urllib", "subprocess", "Authfile", "Publication(", "api.pages", "GITHUB_TOKEN", "GITHUB_OUTPUT", "write_text", "skopeo"):
            self.assertNotIn(forbidden, source)
        self.assertIn("inspect-synthetic-package.yml", (ROOT / "flake.nix").read_text())


if __name__ == "__main__":
    unittest.main()
