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

Source/image integration, public config publication, the paid 2 CPU / 8192 MB no-volume synthetic instance and platform acceptance remain parent-owned. This packet neither performs nor establishes those operations, storage-key/durability qualification or real-data readiness.
