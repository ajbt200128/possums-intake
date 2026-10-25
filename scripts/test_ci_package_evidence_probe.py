"""Synthetic offline tests only; never contact API/registry/runtime/provider.

Privacy evidence: intake PRIVACY.md#required-acceptance-evidence and gateway
PRIVACY.md#required-evidence-for-every-telemetry-change. These tests prove only
local projection/annotation behavior, NOT the actual CI token's package view.
"""
import copy
import email.message
import importlib.util
import io
import json
import os
import unittest
import urllib.error
from contextlib import ExitStack, redirect_stdout, redirect_stderr
from pathlib import Path
from typing import Any
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("ci_package_probe", Path(__file__).with_name("ci-package-evidence-probe.py"))
if spec is None or spec.loader is None:
    raise AssertionError("local fixture unavailable")
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)
d = probe.load_delivery()
CANARY = "HOSTILE_SECRET_%\r\n::error::https://invalid.example/private"


def version(number=1, digest=d.BASELINE, tags=None):
    return {"id": number, "name": digest, "metadata": {"container": {"tags": [] if tags is None else tags}}}


class FakeAPI:
    def __init__(self):
        self.repository = {"id": 42, "full_name": d.p.REPO, "owner": {"login": d.p.OWNER},
                           "private": True, "default_branch": "main"}
        self.current: Any = {"object": {"sha": probe.EXPECTED_SOURCE}}
        self.package_value = {"id": 81, "name": d.p.PACKAGE, "package_type": "container",
                              "owner": {"login": d.p.OWNER}, "visibility": "public",
                              "repository": {"id": 42, "full_name": d.p.REPO, "private": True}}
        self.pages: list[Any] = [[version()]]
        self.reads = []
        self.failure = {}

    def read(self, key, value):
        self.reads.append(key)
        if key in self.failure:
            raise self.failure[key]
        return copy.deepcopy(value)

    def repo(self, suffix=""):
        if suffix not in ("", "/git/ref/heads/main"):
            raise AssertionError("non-allowlisted endpoint")
        return self.read("main" if suffix else "repository", self.current if suffix else self.repository)

    def package(self):
        return self.read("package", (200, self.package_value))

    def version_page(self, page):
        if type(page) is not int or not 1 <= page <= 100:
            raise AssertionError("page bound")
        return self.read("versions:" + str(page), (200, self.pages[page - 1]))


class ProbeTests(unittest.TestCase):
    def setUp(self):
        self.api = FakeAPI()
        stack = self.enterContext(ExitStack())
        # No test can accidentally fall through into network, process, copy or
        # any release-authority boundary; only injected fixed fake reads run.
        for target in ("socket.create_connection", "urllib.request.build_opener", "subprocess.Popen"):
            stack.enter_context(patch(target, side_effect=AssertionError(CANARY)))
        for module, names in ((d, ("context", "admit", "public_gate", "package_identity", "Publication", "archive_gate", "post_gate")),
                              (d.p, ("context", "admit", "admit_qualified", "Publication", "command", "archive_gate"))):
            for name in names:
                guard = stack.enter_context(patch.object(module, name, side_effect=AssertionError(CANARY)))
                self.addCleanup(guard.assert_not_called)

    def run_probe(self):
        output, errors = io.StringIO(), io.StringIO()
        with redirect_stdout(output), redirect_stderr(errors):
            value = probe.sweep(self.api, d)
            line, success = probe.annotation(value)
        self.assertEqual(output.getvalue(), "")
        self.assertEqual(errors.getvalue(), "")
        self.assertNotIn(CANARY, line)
        self.assertNotIn("HOSTILE_SECRET", line)
        self.assertNotIn("https://", line)
        self.assertNotIn("\n", line)
        self.assertNotIn("\r", line)
        self.assertNotIn(probe.EXPECTED_SOURCE, line)
        self.assertNotIn(d.BASELINE, line)
        for fixed_identity in (d.p.OWNER, d.p.REPO, d.p.PACKAGE):
            self.assertNotIn(fixed_identity, line)
        self.assertLessEqual(len(line.encode()), probe.MAX_RESULT_BYTES + len("::warning::"))
        self.assertEqual(json.loads(line.split("::", 2)[2]), value)
        self.assertFalse(value["release_eligible"])
        self.assertFalse(value["runtime_verified"])
        self.assertEqual(success, value["code"] == "CI_PACKAGE_PROBE_COMPLETE")
        return value

    def predicate_false(self, key):
        value = self.run_probe()
        self.assertFalse(value["predicates"][key])
        self.assertEqual(value["code"], "CI_PACKAGE_PROBE_PREDICATES_NOT_ESTABLISHED")
        return value["predicates"]

    def test_all_predicates_and_fixed_single_sweep(self):
        value = self.run_probe()
        self.assertTrue(all(value["predicates"].values()))
        self.assertEqual(set(value["predicates"]), set(probe.PREDICATES))
        self.assertEqual(self.api.reads, ["repository", "main", "package", "versions:1"])
        self.assertEqual(probe.EXPECTED_SOURCE, "1946aa43f21f3ccd6ceaee7819aa53a5f8221f6f")

    def test_package_and_canonical_repository_predicates_missing_and_wrong(self):
        vectors = [
            ("repository", "id", "repository_id", (True, 0, CANARY)),
            ("repository", "full_name", "repository_full_name", (CANARY,)),
            ("repository", "owner", "repository_owner", ({}, None, CANARY, {"login": CANARY})),
            ("repository", "private", "repository_private", (False, 1, CANARY)),
            ("repository", "default_branch", "repository_default_main", (CANARY,)),
            ("package_value", "id", "package_id", (True, 0, CANARY)),
            ("package_value", "name", "package_name", (CANARY,)),
            ("package_value", "package_type", "package_type", (CANARY,)),
            ("package_value", "owner", "package_owner", ({}, None, CANARY, {"login": CANARY})),
            ("package_value", "visibility", "package_public", ("private", CANARY)),
        ]
        for target_name, field, predicate, wrong in vectors:
            for value in (*wrong, None):
                with self.subTest(target=target_name, field=field):
                    self.api = FakeAPI()
                    getattr(self.api, target_name)[field] = value
                    self.predicate_false(predicate)
            self.api = FakeAPI()
            del getattr(self.api, target_name)[field]
            self.predicate_false(predicate)
        for target_name, key in (("repository", "repository_object"), ("package_value", "package_object")):
            for bad in (None, [], CANARY):
                self.api = FakeAPI()
                setattr(self.api, target_name, bad)
                self.predicate_false(key)

    def test_embedded_missing_false_and_canonical_private_independent(self):
        for bad in (None, [], CANARY):
            self.api = FakeAPI()
            self.api.package_value["repository"] = bad
            self.predicate_false("embedded_repository_object")
        for field, key, bad in (("id", "embedded_repository_id_integer", True),
                                ("id", "embedded_repository_id_equal", 43),
                                ("full_name", "embedded_repository_name", CANARY),
                                ("private", "embedded_private_true", False),
                                ("private", "embedded_private_true", 1)):
            self.api = FakeAPI()
            self.api.package_value["repository"][field] = bad
            flags = self.predicate_false(key)
            self.assertTrue(flags["repository_private"])
        for field, key in (("id", "embedded_repository_id_present"),
                           ("full_name", "embedded_repository_name"),
                           ("private", "embedded_private_field_present")):
            self.api = FakeAPI()
            del self.api.package_value["repository"][field]
            flags = self.predicate_false(key)
            if field == "private":
                self.assertFalse(flags["embedded_private_true"])
        self.api = FakeAPI()
        self.api.package_value["repository"]["private"] = False
        flags = self.predicate_false("embedded_private_true")
        self.assertTrue(flags["embedded_private_field_present"])
        self.api = FakeAPI()
        self.api.repository["private"] = False
        flags = self.predicate_false("repository_private")
        self.assertTrue(flags["embedded_private_true"])

    def test_current_main_shape_and_source_binding(self):
        for bad in (None, [], {}, {"object": None}, {"object": {"sha": CANARY}}):
            self.api = FakeAPI()
            self.api.current = bad
            self.predicate_false("current_main_matches_expected")
        self.api.current = {"object": None}
        self.predicate_false("current_main_object")

    def test_each_version_predicate_and_hostile_nested_shapes(self):
        for bad, key in ((CANARY, "version_entry_object"),
                         ({"id": True}, "version_entry_id"),
                         (version(digest=CANARY), "version_digest"),
                         (version(tags=[CANARY, CANARY]), "version_tags_unique"),
                         (version(tags=[probe.EXPECTED_SOURCE]), "source_tag_absent")):
            self.api = FakeAPI()
            self.api.pages = [[bad]]
            self.predicate_false(key)
        for tags in (None, CANARY, {}, [True], [[]], [""], ["x" * 129]):
            self.api = FakeAPI()
            raw = version()
            raw["metadata"]["container"]["tags"] = tags
            self.api.pages = [[raw]]
            flags = self.predicate_false("version_tags_shape")
            self.assertFalse(flags["source_tag_absent"])
        for field in ("id", "name", "metadata"):
            self.api = FakeAPI()
            raw = version()
            del raw[field]
            self.api.pages = [[raw]]
            self.predicate_false({"id": "version_entry_id", "name": "version_digest", "metadata": "version_tags_shape"}[field])
        for metadata in (None, CANARY, {"container": None}, {"container": {}}):
            self.api = FakeAPI()
            raw = version()
            raw["metadata"] = metadata
            self.api.pages = [[raw]]
            self.predicate_false("version_tags_shape")
        for pages in ([[]], [[version(digest="sha256:" + "2" * 64)]]):
            self.api = FakeAPI()
            self.api.pages = pages
            self.predicate_false("baseline_present")

    def test_paging_complete_duplicate_and_no_early_exit(self):
        self.api.pages = [[version(n) for n in range(1, 101)], [version(101, tags=[probe.EXPECTED_SOURCE])]]
        self.predicate_false("source_tag_absent")
        self.assertEqual(self.api.reads[-1], "versions:2")
        self.api.pages[1] = [version(1)]
        self.predicate_false("version_ids_unique")
        self.api.pages[1] = []
        self.assertEqual(self.run_probe()["code"], "CI_PACKAGE_PROBE_COMPLETE")
        self.api.pages = [[version(1), version(1)]]
        self.predicate_false("version_ids_unique")
        # Baseline anywhere, not just first page.
        self.api.pages = [[version(n, digest="sha256:" + "2" * 64) for n in range(1, 101)], [version(101)]]
        self.assertTrue(all(self.run_probe()["predicates"].values()))

    def test_page_cap_never_silently_successful(self):
        self.api.pages = [[version(page * 100 + n) for n in range(1, 101)] for page in range(100)]
        flags = self.predicate_false("versions_complete")
        self.assertFalse(flags["source_tag_absent"])
        self.assertTrue(flags["baseline_present"])
        self.assertEqual(self.api.reads[-1], "versions:100")
        self.assertEqual(len(self.api.reads), 103)
        for bad in (None, {}, CANARY, [version(n) for n in range(101)]):
            self.api = FakeAPI()
            self.api.pages = [bad]
            self.predicate_false("version_pages_shape")
            self.assertEqual(self.api.reads[-1], "versions:1")

    def test_observed_http_and_unknown_failures_no_invented_status_or_retry(self):
        for read, stage in (("repository", "repository"), ("main", "current_main"),
                            ("package", "package"), ("versions:1", "versions")):
            for failure in (d.r.Stop("synthetic_evidence", status=403),
                            d.r.Stop("synthetic_namespace"), RuntimeError(CANARY), KeyboardInterrupt(CANARY)):
                self.api = FakeAPI()
                self.api.failure[read] = failure
                value = self.run_probe()
                self.assertEqual(value["stage"], stage)
                self.assertEqual(self.api.reads.count(read), 1)
                if isinstance(failure, d.r.Stop) and getattr(failure, "status", None) is not None:
                    self.assertEqual(value["http_status"], 403)
                    self.assertEqual(value["cause"], "observed_http_status")
                else:
                    self.assertNotIn("http_status", value)
                    self.assertEqual(value["cause"], "cause_unknown")
        for status in (401, 403, 404, 429, 500):
            self.api = FakeAPI()
            with patch.object(self.api, "package", return_value=(status, CANARY)):
                value = self.run_probe()
            self.assertEqual(value["http_status"], status)
            self.assertFalse(value["predicates"]["package_read"])
        self.api = FakeAPI()
        self.api.pages = [[version(n) for n in range(1, 101)], []]
        self.api.failure["versions:2"] = RuntimeError(CANARY)
        value = self.run_probe()
        self.assertTrue(value["predicates"]["baseline_present"])
        self.assertFalse(value["predicates"]["versions_complete"])
        self.assertFalse(value["predicates"]["source_tag_absent"])

    def test_status_validation_and_module_arguments_fallback(self):
        for status in (True, 0, 600, CANARY):
            self.api = FakeAPI()
            failure = d.r.Stop("synthetic_evidence")
            failure.status = status
            self.api.failure["repository"] = failure
            self.assertNotIn("http_status", self.run_probe())
        with patch.object(probe, "load_delivery", side_effect=RuntimeError(CANARY)):
            value = probe.sweep()
        self.assertEqual(value["code"], "CI_PACKAGE_PROBE_MODULE_CAUSE_UNKNOWN")
        with patch.object(probe, "load_delivery") as load:
            value = probe.sweep(arguments_valid=False)
            load.assert_not_called()
        self.assertEqual(value["stage"], "arguments")

    def test_exact_schema_rejects_backend_values_and_emits_single_safe_fallback(self):
        good = self.run_probe()
        mutations = [lambda v: v.update(extra=CANARY), lambda v: v.update(code=CANARY),
                     lambda v: v.update(stage=CANARY), lambda v: v.update(constraint=CANARY),
                     lambda v: v.update(cause=CANARY), lambda v: v.update(next_step=CANARY),
                     lambda v: v.update(schema=CANARY), lambda v: v.update(http_status=403),
                     lambda v: v.update(release_eligible=True), lambda v: v.update(runtime_verified=True),
                     lambda v: v["predicates"].update(extra=False),
                     lambda v: v["predicates"].update(package_name=CANARY)]
        for mutation in mutations:
            bad = copy.deepcopy(good)
            mutation(bad)
            with self.assertRaises(ValueError):
                probe.compact(bad)
            line, success = probe.annotation(bad)
            self.assertFalse(success)
            self.assertNotIn("HOSTILE_SECRET", line)
            self.assertEqual(json.loads(line.split("::", 2)[2])["code"], "CI_PACKAGE_PROBE_PROJECTION_CAUSE_UNKNOWN")
        self.assertEqual(probe.escape_command("%\r\n::warning::"), "%25%0D%0A::warning::")
        for result_value in (good, probe.sweep(self.api, d, arguments_valid=False)):
            output, errors = io.StringIO(), io.StringIO()
            with patch.object(probe, "sweep", return_value=result_value), redirect_stdout(output), redirect_stderr(errors):
                code = probe.main([])
            self.assertEqual(len(output.getvalue().splitlines()), 1)
            self.assertEqual(errors.getvalue(), "")
            self.assertEqual(code, 0 if result_value is good else 1)

    def test_schema_limits_and_metadata_identifiers_never_emitted(self):
        for code, (_, _, cause) in probe.CLASSIFICATIONS.items():
            value = probe.result(dict.fromkeys(probe.PREDICATES, False), code,
                                 599 if cause == "observed_http_status" else None)
            self.assertLessEqual(len(probe.compact(value).encode()), 4096)
        private_id = 7777333399991888
        self.api.repository["id"] = private_id
        self.api.package_value["id"] = private_id + 1
        self.api.package_value["repository"]["id"] = private_id
        self.api.pages = [[version(private_id + 2, tags=[CANARY])]]
        value = self.run_probe()
        line = probe.annotation(value)[0]
        for number in (private_id, private_id + 1, private_id + 2):
            self.assertNotIn(str(number), line)
        self.assertEqual(value["code"], "CI_PACKAGE_PROBE_COMPLETE")

    def test_actual_inherited_transport_with_fake_opener_only(self):
        class Response:
            status = 200
            raw = b""
            limit = 0

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

            def read(self, limit):
                self.limit = limit
                return self.raw

        class Opener:
            request: Any = None
            timeout = 0

            def open(self, request, timeout):
                self.request, self.timeout = request, timeout
                return response

        response, opener = Response(), Opener()
        response.raw = json.dumps(self.api.repository).encode()
        output, errors = io.StringIO(), io.StringIO()
        with patch.dict(os.environ, {"GITHUB_TOKEN": "SYNTHETIC_TOKEN"}), \
                patch.object(d.p.urllib.request, "build_opener", return_value=opener) as build, \
                redirect_stdout(output), redirect_stderr(errors):
            self.assertEqual(d.GitHub().repo(), self.api.repository)
            self.assertEqual(opener.timeout, 30)
            self.assertEqual(response.limit, (8 << 20) + 1)
            self.assertEqual(opener.request.get_method(), "GET")
            self.assertEqual(opener.request.get_header("Authorization"), "Bearer SYNTHETIC_TOKEN")
            self.assertIsInstance(build.call_args.args[0], d.p.NoRedirect)
            self.assertIsNone(build.call_args.args[0].redirect_request(None, None, 302, CANARY, {}, CANARY))
            # The actual transport collapses hostile invalid JSON and oversized
            # successful bodies to no-status; never infer decoding vs transport.
            for raw in (CANARY.encode(), b"x" * ((8 << 20) + 1)):
                response.raw = raw
                value = probe.sweep(d.GitHub(), d)
                self.assertEqual(value["code"], "CI_PACKAGE_PROBE_REPOSITORY_CAUSE_UNKNOWN")
                self.assertNotIn("http_status", value)
            failure = urllib.error.HTTPError(CANARY, 403, CANARY, email.message.Message(), io.BytesIO(CANARY.encode()))
            with patch.object(opener, "open", side_effect=failure):
                value = probe.sweep(d.GitHub(), d)
            self.assertEqual(value["http_status"], 403)
            self.assertTrue(failure.closed)
        self.assertEqual(output.getvalue(), "")
        self.assertEqual(errors.getvalue(), "")
        self.assertNotIn("HOSTILE_SECRET", probe.annotation(value)[0])
        self.assertNotIn("SYNTHETIC_TOKEN", probe.annotation(value)[0])

    def test_unknown_outer_failure_single_annotation_and_no_raw_output(self):
        output, errors = io.StringIO(), io.StringIO()
        with patch.object(probe, "sweep", side_effect=RuntimeError(CANARY)), redirect_stdout(output), redirect_stderr(errors):
            self.assertEqual(probe.main([]), 1)
        self.assertEqual(len(output.getvalue().splitlines()), 1)
        self.assertIn("CI_PACKAGE_PROBE_PROJECTION_CAUSE_UNKNOWN", output.getvalue())
        self.assertNotIn("HOSTILE_SECRET", output.getvalue())
        self.assertEqual(errors.getvalue(), "")

    def test_workflow_manual_permissions_and_no_execution_authority(self):
        workflow = (Path(__file__).resolve().parents[1] / ".github/workflows/check.yml").read_text()
        self.assertIn("permissions: {}", workflow)
        self.assertIn("  workflow_dispatch:\n", workflow)
        self.assertEqual(workflow.count("uses:"), 1)
        self.assertEqual(workflow.count("run:"), 1)
        for required in ("contents: read", "actions: read", "packages: read", "timeout-minutes: 5",
                         "github.event.repository.private == true", "github.run_attempt == 1",
                         "github.actor == 'ajbt200128'", "github.repository_owner == 'ajbt200128'",
                         "github.repository == 'ajbt200128/possums-intake'",
                         "github.event_name == 'workflow_dispatch'", "ref: ${{ github.sha }}",
                         "actions/checkout@8e8c483db84b4bee98b60c0593521ed34d9990e8",
                         "persist-credentials: false", "GITHUB_TOKEN: ${{ github.token }}",
                         'PYTHONDONTWRITEBYTECODE: "1"', 'OTEL_SDK_DISABLED: "true"',
                         "run: python3 scripts/ci-package-evidence-probe.py"):
            self.assertIn(required, workflow)
        for forbidden in ("  push:", "  pull_request:", "inputs:", "write", "id-token:",
                          "secrets.", "upload-artifact", "download-artifact", "nix-installer", "skopeo"):
            self.assertNotIn(forbidden, workflow)


if __name__ == "__main__":
    unittest.main()
