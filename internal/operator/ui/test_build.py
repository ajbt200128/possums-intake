"""Offline recipe boundary tests; no dependencies installed or build initiated."""
import importlib.util
import os
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


if __name__ == "__main__":
    unittest.main()
