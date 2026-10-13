# Disposable synthetic startup

`intake synthetic-serve` is an explicit standalone native-dashboard test mode. It defaults to `0.0.0.0:8090` for container reachability. The only option is `--listen`: a numeric loopback or wildcard IP and canonical decimal port (including `0` for OS selection). Hostnames, interface zones and other interface addresses are rejected. Port zero is not advertised; choose a known port for interactive use.

`--state` is rejected even when empty. `--operator` is also rejected, including `--operator=false`: synthetic mode always selects the restricted native operator surface. Credentials cannot be CLI options. Unknown commands/options or positional arguments fail closed.

Inject throwaway runtime `LEDGER_ADMIN_KEY` (at least 32 bytes), `LEDGER_OPERATOR_EMAIL` and `LEDGER_OPERATOR_PASSWORD` (valid email; valid UTF-8 password of at least eight characters and at most 71 bytes). They are validated, along with the listener, before scratch creation. Never use real credentials or accounts. No setup URL, credential, state path or success banner is printed.

Each invocation uses `os.MkdirTemp` under the standard runtime temporary directory (`TMPDIR`, ordinarily `/tmp` in the container). It explicitly initializes new empty state and one native operator with the supported `InitOperator` transaction path, then opens that same state. It does not reuse, reset or select persistent state, and never grants seed credit. Native operator tokens cannot authorize custom accounting routes; those still require the shared accounting key. The accounting key cannot authorize native operator routes.

Synthetic startup never constructs the telemetry runtime/exporter, regardless of inherited Honeycomb or generic OTel configuration, and does not mutate the process environment. Request/activity logging remains disabled through the existing restricted service and operator handlers. See [intake forbidden collection](../PRIVACY.md#forbidden-collection-and-defaults), [export behavior](../PRIVACY.md#export-retention-and-failure-behavior) and [gateway telemetry policy](../../possums/PRIVACY.md#telemetry-permitted-signals-and-forbidden-data). This says nothing about platform logging or native agent-host history.

SIGINT/SIGTERM uses the existing joined HTTP/maintenance shutdown, then storage close. Only confirmed safe teardown allows recursive removal of this invocation's scratch directory. Init/open failures, lifecycle panics or unconfirmed shutdown/close preserve scratch state for private runtime inspection; it must not become reusable ledger state. Removal failure reports a closed cleanup diagnostic with unknown removal outcome. Earlier observed failures retain diagnostic precedence. No worker is detached or retried for cleanup. Process crash/forced kill and runtime storage destruction are not graceful-cleanup evidence. TMPDIR permissions, tmpfs sizing and container runtime configuration remain operator/parent-owned.

Ordinary `init` and `serve` defaults remain unchanged: explicit absolute state, no automatic initialization by serve, numeric loopback-only serving, and separately explicit `--operator` opt-in.

## Local verification scope

`TestSyntheticCompiledCLI` builds the actual CLI and probes subprocess HTTP: validation before filesystem effects, temporary-creation diagnostics, wildcard reachability, native login/account creation, key separation, zero initial balance, dashboard reachability, disabled auxiliary request logging, valid/hostile inherited telemetry configuration, graceful shutdown/removal, bind-failure removal and ordinary serve refusals. `TestSyntheticListenerAndConservativeCleanup` covers listener constraints, retained uncertain teardown state and classified removal failure. Existing lifecycle tests cover joined maintenance and storage shutdown. Short HTTP tests are not a five-minute exporter wire proof; exporter exclusion follows the explicit startup construction branch as well as local runtime checks.

Run from the repository with the pinned environment:

`devenv shell -- go test ./cmd/intake -count=1`

`devenv shell -- go test -race ./cmd/intake -count=1`

`devenv shell -- go vet ./cmd/intake`

## Generated-image packaging baseline

The parent qualified the new disposable fixture against committed source **`d804e53`**, before the separate native/lifecycle failure-owner changes. This is a **local Linux/arm64 packaging baseline**, not the final integrated deployment image or independent native Linux A/B release reproduction.

The actual generated image passed native login/create/PATCH, held-balance refusal, key/token separation, blocked logs/settings, all frozen asset response hashes, empty auxiliary logs/auth origins and attached stdout/stderr, and fresh accounting state after restarting the same container. A request to `127.0.0.2` inside the network-none namespace distinguished the wildcard listener from an exact `127.0.0.1` bind; this does not qualify a public shim or verified remote transport. The fixture used a read-only root, nonroot image user, dropped capabilities, no-new-privileges, no core dumps, log driver none, bounded 128-MiB `/tmp` tmpfs, no volume and telemetry off. Credentials were child-environment values, not command arguments. Owned containers and temporary source copies were confirmed removed; public Nix/Docker artifacts may remain.

- Frozen allowlisted source identity: `7966bc31a8e97c36fd7b10a145279d514c86ab275c09ae06726f346443fd0afd`.
- Archive: `/nix/store/hqry07nx83yrlx1rhshpb13i03pcxjg4-possums-intake-local.tar.gz`; SHA256 `ea0e33ff349e87465d39a921e7416fe75aa014e4674c486b491406edebdff525`.
- Image config: `sha256:c117d14bbbb72c7af6c1cb921c622e6e6ff2001ac920ff4975e7c48f4e1223c5`.
- Verified local Docker identity: `sha256:562229f7d5873b882d26facf196d795113c7c4fffc46cc67671bc73c27f60637`.
- Exec-only probe: `/nix/store/4j407pjih85b2ca2pz2113d725vqvswr-intake-image-smoke-probe-0.1.0-local/bin/nix`; not shipped.

All **78** offline packaging/workflow tests and pinned probe compilation/vet passed. Independent review confirmed bounded teardown, refusal of unconfirmed cleanup and preservation of earlier failures. These checks do not replace actual image execution; the packaging baseline above records that separate execution.

The manual `qualify-synthetic-image.yml` workflow now prepares read-only exact-current-main admission, separate fresh native Linux A/B builds, actual A-image ordinary/disposable fixtures and manifest comparison. It publishes no image or release and creates no instance. It has not yet supplied release qualification evidence. Final integrated source must be requalified after the background changes are accepted; the baseline archive above must not be deployed as that final image.

Public config publication, the paid 2 CPU / 8192 MB no-volume synthetic instance and platform acceptance remain pending. No storage-key/durability qualification or real-data readiness follows from this packaging pass.
