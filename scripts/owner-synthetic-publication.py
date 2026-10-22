#!/usr/bin/env python3
"""Manual, once-only owner publication of the already qualified PUBLIC image.

Authority is the saved native github.com login plus explicit source-bound CLI
pins/quiet approval, NOT an invented Actions publisher run. No login, grants,
rebuild, deployment or retry. PRIVACY.md forbidden-collection/export rules apply.
Only public accepted artifacts enter an owned /tmp workspace. Failed sessions
retain that workspace; unconfirmed joins also retain auth material for private
operator inspection. Native gh/keychain/history and platform retention are separate
boundaries; environment restoration/removal is not memory erasure. p.command joins
Skopeo, but its final OS wait has no hard deadline; no whole-process bound claimed.
Unavailable output sinks cannot deliver diagnostics; their errors are never echoed.
"""
import hashlib
import importlib.util
import json
import os
import shutil
import stat
import sys
import tempfile
import urllib.error
import urllib.request
from contextlib import contextmanager, suppress
from pathlib import Path

import release as r
import smoke

_spec = importlib.util.spec_from_file_location("owner_diagnostic_publication", Path(__file__).with_name("synthetic-diagnostic-publication.py"))
if _spec is None or _spec.loader is None:
    raise r.Stop("internal")
d = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(d)
p = d.p
# Locally qualified immutable package paths, not caller PATH resolution. This
# trusts the host/root and Nix store; it is not runtime attestation.
NATIVE_GH = Path("/nix/store/a1i791nra2ivwww08klq5q97wngql6yn-gh-2.88.1/bin/gh")
NATIVE_SKOPEO = Path("/nix/store/dl4adb8hifsh3gk65qcsxlbnpzk9iwq6-skopeo-1.24.1/bin/skopeo")
NATIVE_PATH = f"{NATIVE_GH.parent}:{NATIVE_SKOPEO.parent}:/usr/bin:/bin:/usr/sbin:/sbin"
# This qualified native Nix package ships its policy outside Skopeo's default
# /etc lookup. This path is the offline-evaluated Skopeo output of the pinned
# nixpkgs revision; its policy bytes match the historically qualified digest.
# Image trust remains the independent qualification/archive gates, not OCI
# signature evidence or a TLS/authentication override.
NATIVE_POLICY = Path("/nix/store/dl4adb8hifsh3gk65qcsxlbnpzk9iwq6-skopeo-1.24.1/etc/containers/default-policy.json")
NATIVE_POLICY_SHA = "cddfaa8e6a7e5497b67cc0dd8e8517058d0c97de91bf46fff867528415f2d946"
r.CODES.update({
    "owner_tool": ("native_tool_preflight", "qualified_fixed_binary_regular_nonsymlink_executable",
        "The qualified native tool is unavailable or invalid; underlying cause unknown. Inspect the pinned local tool package privately; do not substitute caller tools or retry publication."),
    "owner_policy": ("native_copy_policy", "qualified_sdk_bundled_policy_exact_bytes",
        "The qualified native SDK policy is unavailable or changed. Inspect the pinned local tool package; do not disable TLS, source qualification or publication guards."),
    "owner_auth": ("native_owner_authentication", "saved_github_com_login_and_same_token_authenticated_owner",
        "Owner authentication is unavailable; underlying cause unknown. Inspect the existing native github.com login privately; do not upload credentials or add grants."),
    "owner_download": ("native_accepted_artifacts", "exact_qualified_names_ids_and_closed_public_files",
        "Accepted artifact download is unavailable; underlying cause unknown. Inspect the exact qualification privately; do not substitute archives or retry publication."),
    "owner_join": ("native_tool_cleanup", "positively_joined_child_before_owned_material_removal",
        "Tool completion/join is unconfirmed; underlying cause unknown. Owned temporary material was retained. Inspect native processes and owned temporary material privately; never retry publication."),
    "owner_workspace": ("native_public_workspace", "owned_public_temporary_workspace_removal",
        "Owned public workspace cleanup is unavailable; underlying cause unknown. Inspect owned temporary material privately; do not retry publication."),
})


def parse(args):
    names = {"--sourceSHA", "--qualification-run", "--qualification-attempt", "--expected-image",
             "--expected-archive", "--writers-quiet-confirmed"}
    r.require(len(args) == 13 and args[0] == "publish", "arguments")
    values = {}
    for key, value in zip(args[1::2], args[2::2], strict=True):
        r.require(key in names and key not in values, "arguments")
        values[key] = value
    r.require(set(values) == names and values["--writers-quiet-confirmed"] == "true"
              and r.pin(values["--sourceSHA"], r.SHA) and r.pin(values["--expected-image"], r.DIGEST)
              and r.pin(values["--expected-archive"], r"[0-9a-f]{64}"), "arguments")
    return {"source": values["--sourceSHA"], "qualification_run": p.positive(values["--qualification-run"]),
            "qualification_attempt": p.positive(values["--qualification-attempt"]), "baseline": d.BASELINE,
            "expected_image": values["--expected-image"], "expected_archive": values["--expected-archive"],
            "writers_quiet_confirmed": True}


def native_env():
    env = {key: os.environ[key] for key in ("HOME", "GH_CONFIG_DIR") if key in os.environ}
    r.require(bool(env.get("HOME")), "owner_auth")
    env.update(PATH=NATIVE_PATH, GH_HOST="github.com", GH_PROMPT_DISABLED="1", OTEL_SDK_DISABLED="true",
               DO_NOT_TRACK="1", GH_NO_UPDATE_NOTIFIER="1", GH_NO_EXTENSION_UPDATE_NOTIFIER="1")
    return env


@contextmanager
def owner_environment(env, token):
    # Single manual process only; do not inherit proxy/provider/admin/Actions authority.
    previous = dict(os.environ)
    try:
        os.environ.clear()
        os.environ.update(env, GITHUB_TOKEN=token, GITHUB_ACTOR=p.OWNER)
        yield
    finally:
        os.environ.clear()
        os.environ.update(previous)


def verify_owner(token):
    """Separate fixed GET: never widen the accounting publisher API allowlist."""
    def check():
        request = urllib.request.Request("https://api.github.com/user", headers={
            "Authorization": "Bearer " + token, "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "intake-owner-publication"})
        try:
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), p.NoRedirect())
            with opener.open(request, timeout=30) as response:
                raw = response.read(p.MAX_READ + 1)
                r.require(response.status == 200 and len(raw) <= p.MAX_READ, "owner_auth")
                user = json.loads(raw)
                r.require(type(user) is dict and user.get("login") == p.OWNER, "owner_auth")
        except urllib.error.HTTPError as error:
            status = error.code
            error.close()
            raise r.Stop("owner_auth", status=status) from None
    p.boundary("owner_auth", check)


def require_native_tool(path):
    try:
        valid = stat.S_ISREG(path.lstat().st_mode) and os.access(path, os.X_OK)
    except (Exception, KeyboardInterrupt):
        raise r.Stop("owner_tool") from None
    r.require(valid, "owner_tool")


class Tools:
    def __init__(self, env):
        self.env, self.joined = env, True

    def gh(self, args, code, token=None):
        r.require(bool(args), "owner_tool")
        require_native_tool(NATIVE_GH)
        env = dict(self.env)
        if token is not None:
            env["GITHUB_TOKEN"] = token  # Obtained token, NEVER an inherited override.
        self.joined = False
        try:
            rc, stdout, _ = smoke.run_bounded([str(NATIVE_GH), *args], env, code, 30 if args[0] == "auth" else 600)
        except smoke.SmokeError as error:
            # Only these supported helper failures establish joined process/readers.
            self.joined = error.constraint in {"child_start_unavailable", "child_deadline_unknown_outcome"}
            raise r.Stop(code) from None
        self.joined = True
        r.require(rc == 0, code)
        return stdout

    def skopeo(self, args, code, timeout=120):
        r.require(bool(args) and args[0] == "skopeo", "owner_tool")
        require_native_tool(NATIVE_SKOPEO)
        args = [str(NATIVE_SKOPEO), *args[1:]]
        if args[1:2] == ["copy"]:
            r.require(p.boundary("owner_policy", lambda: NATIVE_POLICY.is_file() and not NATIVE_POLICY.is_symlink()),
                      "owner_policy")
            policy = p.boundary("owner_policy", NATIVE_POLICY.read_bytes)
            r.require(hashlib.sha256(policy).hexdigest() == NATIVE_POLICY_SHA, "owner_policy")
            args = [*args[:2], "--policy", str(NATIVE_POLICY), *args[2:]]
        self.joined = False
        # Conservative on ANY failure: p.command doesn't expose OS-join failures.
        result = p.command(args, code, timeout)
        self.joined = True
        return result


def admit(api, ctx):
    r.require(ctx.get("baseline") == d.BASELINE and ctx.get("writers_quiet_confirmed") is True,
              "diagnostic_approval")
    return p.boundary("diagnostic_evidence", lambda: p.admit_qualified(api, ctx, p.repository_gate(api)))


def artifact_names(ctx):
    stem = f"{ctx['qualification_run']}-{ctx['qualification_attempt']}-{ctx['source']}"
    return {"a": "intake-synthetic-" + stem + "-a", "b": "intake-synthetic-" + stem + "-b",
            "archive": "intake-synthetic-archive-" + stem}


def download(api, ctx, admitted, root, tools, token):
    # gh's supported native extraction, never a custom ZipFile.extractall. Exact
    # unique names select immutable artifacts; full admission binds IDs both sides.
    r.require(admit(api, ctx) == admitted, "owner_download")
    for key, name in artifact_names(ctx).items():
        tools.gh(["run", "download", str(ctx["qualification_run"]), "--repo", "github.com/" + p.REPO,
                  "--name", name, "--dir", str(root / key)], "owner_download", token)
    r.require(admit(api, ctx) == admitted, "owner_download")
    expected = {"a": {"digest", "archive-sha256", "qualification.json", "synthetic-qualification.json"},
                "b": {"digest", "archive-sha256"}, "archive": {"image.tar.gz"}}
    r.require({item.name for item in root.iterdir()} == set(expected), "owner_download")
    for key, names in expected.items():
        folder = root / key
        r.require(folder.is_dir() and not folder.is_symlink()
                  and {item.name for item in folder.iterdir()} == names, "owner_download")
        r.require(all(item.is_file() and not item.is_symlink() for item in folder.iterdir()), "owner_download")


class Publication:
    def __init__(self, api, ctx, root, admitted, tools, auth=None):
        self.api, self.ctx, self.root, self.admitted = api, dict(ctx), Path(root), admitted
        self.tools, self.auth = tools, auth if auth is not None else p.Authfile()
        self.used, self.outcome = False, "no_effect"

    def archive(self, admitted):
        receipt = d.archive_gate(self.root, admitted, self.tools.skopeo)
        r.require(receipt == {"publicSourceSHA": self.ctx["source"], "imageDigest": self.ctx["expected_image"],
                              "archiveSHA": self.ctx["expected_archive"]}, "synthetic_archive")
        return receipt

    def execute(self):
        if self.used:
            raise p.PublicationStop("diagnostic_used", outcome=self.outcome)
        primary = None
        try:
            r.require(admit(self.api, self.ctx) == self.admitted, "diagnostic_evidence")
            receipt = p.boundary("synthetic_archive", lambda: self.archive(self.admitted))
            identity = p.boundary("diagnostic_package", lambda: d.public_gate(self.api, self.ctx))
            authfile = p.boundary("synthetic_auth", self.auth.prepare)
            refreshed = admit(self.api, self.ctx)
            r.require(refreshed == self.admitted, "diagnostic_evidence")
            r.require(p.boundary("synthetic_archive", lambda: self.archive(refreshed)) == receipt, "synthetic_archive")
            r.require(p.boundary("diagnostic_package", lambda: d.public_gate(self.api, self.ctx)) == identity,
                      "diagnostic_package")
            r.require(p.boundary("diagnostic_evidence", lambda: self.api.repo("/git/ref/heads/main"))["object"]["sha"]
                      == self.ctx["source"], "stale")
            self.used, self.outcome = True, "unknown"
            p.boundary("diagnostic_copy", lambda: self.tools.skopeo([
                "skopeo", "copy", "--preserve-digests", "--retry-times", "0", "--authfile", str(authfile),
                "docker-archive:" + str((self.root / "archive/image.tar.gz").resolve()),
                "docker://" + p.REGISTRY + ":" + receipt["publicSourceSHA"]], "diagnostic_copy", timeout=600))
            self.outcome = "completed"
            p.boundary("diagnostic_public", lambda: d.post_gate(self.api, identity, receipt, authfile, self.tools.skopeo))
            return receipt
        except (Exception, KeyboardInterrupt) as error:
            primary = p.PublicationStop(error.code, status=error.status, outcome=self.outcome) if isinstance(error, r.Stop) else p.PublicationStop("diagnostic_evidence", outcome=self.outcome)
            raise primary from None
        finally:
            try:
                r.require(self.tools.joined, "owner_join")
                self.auth.cleanup()
            except (Exception, KeyboardInterrupt):
                if primary is None:
                    raise r.Stop("owner_join" if not self.tools.joined else "synthetic_cleanup", outcome=self.outcome) from None
                primary.cleanup_failed = True
                primary.join_unconfirmed = not self.tools.joined


def diagnostic(error):
    value = p.diagnostic(error)
    if getattr(error, "join_unconfirmed", False):
        value["secondary"] = [r.diagnostic(r.Stop("owner_join", outcome=error.outcome))]
    return value


def publish(ctx):
    env = native_env()
    tools = Tools(env)
    token = p.boundary("owner_auth", lambda: tools.gh(["auth", "token", "--hostname", "github.com"], "owner_auth").decode().strip())
    r.require(r.pin(token, r"[A-Za-z0-9_]{1,4096}"), "owner_auth")
    with owner_environment(env, token):
        verify_owner(token)
        api = d.GitHub()
        admitted = admit(api, ctx)
        p.boundary("diagnostic_package", lambda: d.public_gate(api, ctx))
        root = Path(p.boundary("owner_workspace", lambda: tempfile.mkdtemp(prefix="intake-owner-public-", dir="/tmp")))
        # mkdtemp is owner-only; do not use inherited TMPDIR or caller paths.
        p.boundary("owner_workspace", lambda: root.chmod(0o700))
        try:
            p.boundary("owner_download", lambda: download(api, ctx, admitted, root, tools, token))
        except r.Stop as error:
            if tools.joined:
                raise
            primary = p.PublicationStop(error.code, status=error.status)
            primary.join_unconfirmed = True
            raise primary from None
        receipt = Publication(api, ctx, root, admitted, tools).execute()
        try:
            print(json.dumps(receipt, sort_keys=True, separators=(",", ":")), flush=True)
        except (Exception, KeyboardInterrupt):
            raise r.Stop("synthetic_receipt", outcome="completed") from None
        try:
            shutil.rmtree(root)
        except (Exception, KeyboardInterrupt):
            raise r.Stop("owner_workspace", outcome="completed") from None


def main(args=None):
    args = sys.argv[1:] if args is None else args
    if args in ([], ["status"], ["report"]):
        print(json.dumps({"stage": "source_only", "publication": "not_observed", "runtime_verified": False}))
        return
    publish(parse(args))


def cli():
    try:
        main()
    except (Exception, KeyboardInterrupt) as error:
        if not isinstance(error, r.Stop):
            error = r.Stop("diagnostic_internal")
        # Unavailable output cannot deliver diagnostics; never echo its exception.
        with suppress(Exception, KeyboardInterrupt):
            print(json.dumps(diagnostic(error)))
        sys.exit(1)


if __name__ == "__main__":
    cli()
