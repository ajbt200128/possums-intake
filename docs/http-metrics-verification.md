# Standalone HTTP + metrics integration — local verification

## Status, authority and scope

**PASS: scoped local integration of execution packets 2/3/4. Not generated-image,
Linux-runtime, gateway, real-data, Honeycomb or deployment acceptance.**
The historical blocked packet-3 record remains in
[runtime verification](runtime-verification.md); this record supersedes only
its missing-HTTP/full-suite blocker. Package metrics details and historical
packet-4 results remain in [telemetry contract](telemetry-contract.md).

Authority reviewed: full [PLAN.md](../PLAN.md), local [accounting boundaries](../PRIVACY.md#accounting-and-processor-boundaries),
[first metrics scope](../PRIVACY.md#first-telemetry-scope-metrics-only),
[forbidden defaults](../PRIVACY.md#forbidden-collection-and-defaults),
[export/failure behavior](../PRIVACY.md#export-retention-and-failure-behavior),
[acceptance evidence](../PRIVACY.md#required-acceptance-evidence), and full gateway
[privacy](../../possums/PRIVACY.md), especially its
[data boundaries](../../possums/PRIVACY.md#data-handling-boundaries),
[forbidden telemetry](../../possums/PRIVACY.md#telemetry-permitted-signals-and-forbidden-data),
[aggregation](../../possums/PRIVACY.md#aggregation-is-necessary-not-sufficient),
[processor/shutdown](../../possums/PRIVACY.md#processors-retention-access-and-shutdown),
[verification](../../possums/PRIVACY.md#required-evidence-for-every-telemetry-change).
Gateway AGENTS/SPEC/current overall plan and historical `7ee4604:OVERALL_PLAN.md`
were read; current policy supersedes historical browser/accounting/privacy claims.

No agents, inference, commits, pushes, installation, cloud operations, live export,
gateway edits or delivery-file edits were performed. Existing delivery-owned
`scripts/smoke.py` was read and executed, not edited. All data/keys/state were
disposable synthetic fixtures. Native host/task history remains a separate
persistence boundary; no raw test output, transcripts or payloads are copied here.

## Exact integration changes

- `cmd/intake/main.go`: construct `telemetry.New(ctx)` only in validated `serve`
  startup after storage opens; pass it to the custom handler and defer joined,
  non-flushing `Shutdown`. `init` does not initialize telemetry. HTTP/maintenance
  drain still finishes before the deferred metrics shutdown and storage close;
  parent cancellation also cancels metrics work.
- `cmd/intake/process_test.go`: subprocess environment strips all ambient
  `OTEL_*` and `INTAKE_METRICS_*` configuration, then forces
  `OTEL_SDK_DISABLED=true`; the parent's corrected short-key/list fixtures were
  preserved. New `cmd/intake/telemetry_test.go` forces the off switch for direct
  lifecycle tests and verifies subprocess isolation under hostile ambient config.
- `internal/httpapi/handler.go`: add the narrow `TerminalObserver` constructor
  argument (nil is allowed). All seven request schemas now require all fields,
  including settle/refund/lookup. Reject duplicated Idempotency-Key headers on
  every custom route, alongside existing auth/content-header duplicate rejection.
  Buffer starts without a default success status. Complete writes, short writes,
  errors and writer panics are distinguished; supported final flush errors are
  surfaced via `http.ResponseController`, before observation.
- `internal/httpapi/decode.go`: validate JSON syntax/raw UTF-8 and UTF-16 escape
  pairing before the standard decoder can replace lone surrogates with U+FFFD.
  A high surrogate requires an immediately following low-surrogate escape;
  standalone/reversed/mismatched surrogates reject with the closed 400 input
  diagnostic. Valid pairs, literal U+FFFD, escaped literal backslashes and
  decomposed/composed Unicode are preserved without normalization. The duplicate
  header diagnostic now describes all relevant singleton headers.
- New `internal/httpapi/terminal.go`: request-local once closure, closed numeric
  classes only, no request identity registry. Duplicate/missing classification,
  invalid timing and observer failure invalidate request-window evidence.
  Observation/invalidation panics cannot change the already-written response or
  money. Negative/unavailable timing never becomes a fabricated duration.
- New `internal/httpapi/integration_test.go`: strict boundaries, actual HTTP
  singleton-header tests, final-class/flush spy, delivery/panic/cancellation,
  once/invalidation, real money under observer failure/disabled/shutdown, and
  committed-credit delivery/evidence-loss resolution by the original key.
  `handler_test.go` / `boundary_test.go` only adapt existing constructor calls.
  `internal/httpapi/README.md` describes the integrated API and boundaries.
- `internal/telemetry/runtime.go`: comment qualified to distinguish no explicit
  detector registration from upstream internal resource evaluation. No runtime
  metrics schema, transport, aggregation, resource cache or environment mutation
  was introduced. New `http_fixture_test.go` exposes a **test-only in-memory
  exporter/manual clock seam**, not a runtime endpoint override; new
  `http_integration_test.go` joins actual HTTP/accounting with the real SDK/runtime.
- This record is new; `docs/runtime-verification.md` gains only a current-record
  link. Core accounting, migrations, dependency versions, go.mod/go.sum, delivery
  flake/nix/workflows/scripts and image-smoke files were not edited.

The checkout's source is untracked, so an empty `git diff` is **not** absence of
these changes. Qualified Go source/module manifest SHA-256:
`103e753622acde0c253b89f8f7dce966a72bed818415416a14e024943cfaddbb`.
It hashes the SHA-256 lines for `go.mod`, `go.sum` and every `.go` file under
`cmd/intake`, `internal/httpapi`, `internal/telemetry`, `internal/ledger`, paths
sorted with `LC_ALL=C`. It excludes documents and independent delivery artifacts.

Observed toolchain: **Go 1.27.2, darwin/arm64**, supplied by devenv. The existing
`go 1.27` / `toolchain go1.27.1` directives need no change when Go 1.27.2 is
already selected. Dependencies/checksums unchanged: PocketBase v0.40.5,
dbx v1.12.0, modernc SQLite v1.60.1, OTel SDK/exporter v1.44.0,
gopsutil v4.26.9, OTLP protobuf v1.10.0 / protobuf v1.36.11.

## Final-outcome and privacy contracts

Only POST dispatch to the seven exact accounting paths is observed, including
schema/auth/accounting errors. Health, wrong methods, default/native routes and
pre-handler transport failures are excluded. No `e.Next`, generic CRUD hook,
auto-instrumentation, logger, tracer, identifiers, endpoint dimensions or financial
mutation counters are added. Every financial decision remains in the core's
supported `RunInTransaction` callback using its `txApp`; exporters/hooks run
outside that callback and cannot initiate accounting.

One observation follows the explicit final write and supported flush. Successful
2xx/4xx/5xx classes describe **HTTP**, not requested financial effects. A delivered
unknown-commit 503 is 5xx; its body still says outcome unknown and contains no
receipt. Route/writer/flush panic, write error/short write, cancellation or missing
classification produces `Unknown`, never assumed 2xx. Known classification loss
also withholds the affected window. On writers without supported Flush, only the
Write result is available; even a successful network flush does not establish peer
receipt or prevent a later disconnect. Neither success nor failure invents a
refund, rollback, cancellation or safe new-key retry.

Duplicate auth/content/encoding/idempotency header fields return 400. Missing or
wrong auth returns 401; a comma-joined single Authorization value is one wrong
credential, not two accepted keys. Every absent required body field returns 400
before operations run; a panic-on-dispatch fixture proves this rather than relying
on core validation. Success still serializes exact fields, canonical money,
complete account-scoped row lists and UTC millisecond/null time fields. Expired
settlement retains the authoritative expired row separately from its 409 diagnostic.

The existing metrics family remains unchanged: complete aligned five-minute
request classes plus matched exclusive latency bins, complete one-minute process
CPU/RSS means from six ten-second readings, fixed resource/scope allowlists,
no individual timestamps/durations/exemplars/traces. Partial/inconsistent windows
are unavailable, not zero; shutdown drops partial/pending state and never flushes.
Sparse aggregates remain correlatable, not anonymous. Valid reviewed configuration
can enable the real exporter; **none was supplied here**. All full-service tests
force the startup off switch; synthetic enabled tests inject an in-memory exporter
or the existing test-only loopback TLS wire fixture, never Honeycomb.

### Installed SDK boundary — explicit remaining limitation

Inspected installed OTel v1.44.0 sources under the module cache:

- `sdk/metric/config.go:newConfig` unconditionally evaluates `resource.Default()`;
  `WithResource` merges `resource.Environment()` even with our explicit resource.
- `sdk/resource/resource.go:DefaultWithContext` caches default service-name,
  environment and SDK detection with `sync.Once`; an experimental resource option
  can add default instance-ID detection. Runtime rejects ambient OTEL configuration
  before construction, including that experimental option.
- `sdk/resource/builtin.go:defaultServiceNameDetector.Detect` calls `os.Executable`
  to derive a fallback name. This is internal SDK metadata evaluation, even though
  the package requests no process/host/cloud resource detector.

The supported API does not bypass these evaluations. Explicit resources plus the
final allowlist remove unused metadata before supported serialization, and actual
synthetic OTLP bytes qualify the **exported schema**, not literal zero SDK-internal
discovery. No SDK fork, global cache/environment mutation or global error-handler
replacement was added. This strict no-discovery interpretation remains unqualified
and must be resolved before real export/data; do not claim it passed. Concurrent
runtime environment mutation is not an approved configuration path. Standard DNS,
OS numeric reads and whole-process shutdown timing remain upstream/runtime limits.

## Commands and synthetic evidence

Final **PASS**, with the off switch applied to all full-service commands:

`devenv shell -- go version`

`devenv shell -- env OTEL_SDK_DISABLED=true go test ./... -count=1 -timeout=180s`

`devenv shell -- env OTEL_SDK_DISABLED=true go test -race ./... -count=1 -timeout=180s`

`devenv shell -- env OTEL_SDK_DISABLED=true go vet ./...`

`devenv shell -- env OTEL_SDK_DISABLED=true go mod verify`

Build/curl smoke used a temporary executable, removed after joined normal service
shutdown. Reproduction creates no repository binary and supplies no real config:

`devenv shell -- bash -c 'set -e; export OTEL_SDK_DISABLED=true; artifact=$(mktemp -d); trap '\''rm -rf "$artifact"'\'' EXIT; go build -o "$artifact/intake" ./cmd/intake; python3 scripts/smoke.py --self-test; python3 scripts/smoke.py --binary "$artifact/intake"'`

Final full suite package results: cmd/intake 62.031s, httpapi 2.711s, ledger 5.918s,
telemetry 3.036s; race suite: 74.311s / 5.173s / 15.189s / 4.835s respectively.
The production 10s-expiry/60s-prune subprocess schedule ran in both suites.
Smoke reported seven harness checks and the compiled CLI/curl/restart/normal
shutdown gate passing. No generated image was built by this packet.

Evidence highlights:

- `TestInvalidUnicodeAndMissingFieldsNeverDispatch`,
  `TestPairedUnicodePreservesOpaqueAccounts`, `TestSingletonHeadersOnActualHTTP`:
  parser identity preservation, complete schemas and actual loopback duplicate-header
  statuses, with closed secret-free diagnostics.
- `TestFinalClassesAndRouteExclusions`, `TestUnknownDeliveryAndPanicObservations`,
  `TestLocalOnceGuardAndObserverFailureIsolation`: final 200/400/401/409/410/503
  after flush; write/flush error, short-write, header/write/flush/route panic,
  canceled context and unclassified route; exactly once, no default/health/native
  observation, invalidation and no manufactured latency.
- `TestDeliveryLossAndUnknownCommitNeverInventReceipt`: real credit commits even
  when delivery fails or a synthetic wrapper loses completion evidence; no fabricated
  receipt, same-key original result and altered-input conflict, no double credit.
  The wrapper is modeled evidence loss, not an OS disk-fault claim.
- `TestHTTPCompleteWindowOutageAndShutdownPreserveAccounting`: actual core/handler,
  real Runtime/SDK, matched five-terminal counts/bins and aligned five-minute
  boundaries; missing process readings export no process values. In-memory exporter
  blocks outside transactions, credit still commits, shutdown cancels/joins once,
  no flush/replay, and post-shutdown accounting remains correct.
- Existing HTTP suite proves expired-row preservation, complete scoped rows,
  canonical money/timeouts, shared auth on every route and secret-free errors.
  Existing core suite independently proves txApp effects, races, exact replay/key/
  pruning/clock boundaries, unknown commit, rollback, locks, restart and migration
  refusal. This integration changes none of those policies.
- Actual subprocess tests (`TestProcessAuthNativeRoutesPrivacyAndRestart`,
  `TestProcessLocksLostResponseRacesAndExpiry`,
  `TestProcessScheduledExpiryPruningAndCachedHealth`, CLI failure tests) pass through
  production main, real loopback HTTP and PocketBase. Normal stdout/stderr stays
  empty, native surfaces unreachable, `_logs`/superusers empty, no private sentinels
  in inspected auxiliary DB/WAL/SHM bytes. Necessary ledger/WAL state persists;
  this is not secure erasure or a whole-platform no-logs claim.
- Existing protobuf wire tests pass with supported SDK serialization: closed
  resources/scopes/points/window times, no private sentinels/credentials/free
  metadata in data; auth header separate, TLS verified, no redirects/retries under
  hostile partial-success/error/disconnect/outage fixtures. Errors remain transient
  and initiate no accounting/inference.

An early focused compile failed only because the new external telemetry test had
an unused import; it was removed before passing final suites. No DeepSeek packet
or provider failure occurred in this task. An automated runner briefly reported
Python diagnostics in independent `scripts/smoke.py`; an active LSP error probe of
that script plus main/handler returned clean, and the actual smoke passed. The
script was not edited or claimed to have been repaired here.

Active probes covered the 12 changed Go paths. Seven auxiliary warnings were
reviewed: fixed init stdout is intentional (not an application logger), direct
writes are closed JSON with application/json/nosniff rather than HTML rendering,
and WaitGroup.Done belongs to the actual clock/sender goroutine bodies launched
after Add. No compiler/vet/race failure remains. These warnings were not silenced
by adding logs, templates or changing worker ownership.

## Remaining gates

Separate delivery owner: generated image/root/state/readiness/privacy qualification,
Linux current-process behavior and reproducible artifact identity. No prior released
ledger exists, so prior-release compatibility is not yet demonstrated.

Before real data/export/integration: resolve the SDK-internal no-discovery policy
boundary; qualify OS durability/commit stalls/power loss, encrypted original
volume/key retention and attested unlock, verified gateway/admin transport,
provider/platform/operator logging/access, Honeycomb schema/key scope/effective
retention and infrastructure-slot semantics; explicitly authorize gateway changes,
export, provisioning/publication/replacement. Provider-default retention is accepted,
not measured; seven days remains a future target. All gateway/payment/live deployment
work remains deferred. Passing this scoped local gate authorizes none of it.
