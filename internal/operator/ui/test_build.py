"""Offline recipe boundary tests; no dependencies installed or build initiated."""
import base64
import hashlib
import importlib.util
import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("operator_ui_build", HERE / "build.py")
assert spec is not None and spec.loader is not None
recipe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(recipe)


class BuildEnvironment(unittest.TestCase):
    def test_no_inherited_frontend_or_private_configuration(self):
        hostile = {"PB_BACKEND_URL": "SYNTHETIC_PRIVATE_SENTINEL", "VITE_SECRET": "SYNTHETIC_PRIVATE_SENTINEL",
                   "NPM_CONFIG_USERCONFIG": "SYNTHETIC_PRIVATE_SENTINEL", "NODE_OPTIONS": "SYNTHETIC_PRIVATE_SENTINEL",
                   "LEDGER_ADMIN_KEY": "SYNTHETIC_PRIVATE_SENTINEL", "OTEL_EXPORTER_OTLP_HEADERS": "SYNTHETIC_PRIVATE_SENTINEL"}
        result = mock.Mock(returncode=0, stdout=b"synthetic")
        with mock.patch.dict(os.environ, hostile), mock.patch.object(recipe.subprocess, "run", return_value=result) as command:
            self.assertEqual(recipe.command(["node", "--version"], HERE), "synthetic")
        environment = command.call_args.kwargs["env"]
        self.assertEqual(set(environment), {"PATH", "HOME", "GOENV", "GOTOOLCHAIN", "LC_ALL", "CI"})
        self.assertNotIn("SYNTHETIC_PRIVATE_SENTINEL", str(environment))
        self.assertEqual(environment["GOENV"], "off")
        self.assertEqual(environment["GOTOOLCHAIN"], "local")

    def test_command_failure_is_closed(self):
        result = mock.Mock(returncode=1, stdout=b"SYNTHETIC_PRIVATE_SENTINEL", stderr=b"SYNTHETIC_PRIVATE_SENTINEL")
        with mock.patch.object(recipe.subprocess, "run", return_value=result), self.assertRaises(recipe.BuildFailure) as caught:
            recipe.command(["npm", "run", "build"], HERE)
        self.assertIn("stage=npm_command", str(caught.exception))
        self.assertNotIn("SYNTHETIC_PRIVATE_SENTINEL", str(caught.exception))


class OfflineCache(unittest.TestCase):
    def test_missing_pinned_archive_stops_before_npm(self):
        with tempfile.TemporaryDirectory(dir=recipe.ROOT) as directory:
            work = Path(directory)
            digest = base64.b64encode(hashlib.sha512(b"synthetic-public-archive").digest()).decode()
            (work / "package-lock.json").write_text(json.dumps({"packages": {"node_modules/fixture": {"integrity": "sha512-" + digest}}}))
            with mock.patch.object(recipe, "command", return_value='{"os":"darwin","cpu":"arm64"}') as command, self.assertRaises(recipe.BuildFailure) as caught:
                recipe.cache_dependencies(work, {}, work / "absent-cache")
            self.assertIn("OPERATOR_UI_CACHE_UNAVAILABLE", str(caught.exception))
            self.assertIn("no npm install or fetch attempted", str(caught.exception))
            self.assertEqual(command.call_count, 1)
            self.assertEqual(command.call_args.args[0][0], "node")
            self.assertFalse((work / "node_modules").exists())

    def test_cache_copy_is_lock_scoped_and_checksum_checked(self):
        with tempfile.TemporaryDirectory(dir=recipe.ROOT) as directory:
            work = Path(directory)
            cached = work / "cache"
            raw = b"synthetic-public-archive"
            digest = hashlib.sha512(raw).hexdigest()
            encoded = base64.b64encode(bytes.fromhex(digest)).decode()
            relative = Path("content-v2/sha512") / digest[:2] / digest[2:4] / digest[4:]
            source = cached / relative
            source.parent.mkdir(parents=True)
            source.write_bytes(raw)
            (cached / "unrelated").write_text("SYNTHETIC_PRIVATE_SENTINEL")
            (work / "package-lock.json").write_text(json.dumps({"packages": {"node_modules/fixture": {"integrity": "sha512-" + encoded}}}))
            with mock.patch.object(recipe, "command", return_value='{"os":"darwin","cpu":"arm64"}'):
                recipe.cache_dependencies(work, {}, cached)
                self.assertEqual((work / ".npm-cache/_cacache" / relative).read_bytes(), raw)
                self.assertFalse((work / ".npm-cache/_cacache/unrelated").exists())
                source.write_bytes(b"SYNTHETIC_PRIVATE_SENTINEL")
                with self.assertRaises(recipe.BuildFailure) as caught:
                    recipe.cache_dependencies(work, {}, cached)
            self.assertIn("OPERATOR_UI_CACHE_MISMATCH", str(caught.exception))
            self.assertNotIn("SYNTHETIC_PRIVATE_SENTINEL", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
