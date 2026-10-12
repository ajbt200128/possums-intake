# Packet 3 — local lifecycle and fault verification

## Status and scope

Historical packet-3 status below is preserved. For subsequent integrated HTTP,
metrics and full/subprocess verification, see
[HTTP/metrics verification](http-metrics-verification.md).

**Partial local verification; not integrated acceptance.** Runtime wiring and tests are written, but the independently owned `internal/httpapi` package was absent when full test/vet/build commands ran. The expected dependency is exactly `NewHandler(accounting ledger.Accounting, ready func() bool, adminKey string) (http.Handler, error)`. No substitute/stub handler was created and no route-owned files were edited. Subprocess HTTP tests below are **pending**, not passing evidence.

This packet changes only `cmd/intake/*.go`, the ledger's supported termination path, ledger lifecycle tests, and this record. It does not change accounting/key policy, dependencies, development configuration, other planning documents, gateway code, telemetry, deployment or payments. No delegation, inference, export, installation, cloud operation, commit or push was performed. The packet-1 README's no-listener statement is historical; this record describes packet-3 wiring and its current dependency blocker.

Privacy authority: local [accounting/persistence boundaries](../PRIVACY.md#accounting-and-processor-boundaries), [forbidden collection/defaults](../PRIVACY.md#forbidden-collection-and-defaults) and [acceptance evidence](../PRIVACY.md#required-acceptance-evidence); gateway [data boundaries](../../possums/PRIVACY.md#data-handling-boundaries), [forbidden telemetry](../../possums/PRIVACY.md#telemetry-permitted-signals-and-forbidden-data) and [required evidence](../../possums/PRIVACY.md#required-evidence-for-every-telemetry-change). Fixtures use disposable local databases, invented accounts and a test-only admin key, never real credentials/data. Captured process output remains test memory, is not printed on failure, and is not copied into this record. Native host/task history is a separate boundary.

## Runtime implementation

- Explicit `init --state <absolute-directory>` remains the only empty-state creator. It refuses nonempty/partial state, starts no listener, grants no credits and creates no superuser. `serve` requires the original directory plus both existing nonempty databases; no working-directory default or auto-init recovery.
- `serve --state <absolute-directory> [--listen <numeric-loopback:port>]`; default listener is `127.0.0.1:8090`. Only numeric IPv4/IPv6 loopback is allowed; wildcard, remote, hostname, zoned IPv6 and invalid ports fail closed. Port zero is usable by local fixtures. No secret CLI flags exist.
- `LEDGER_ADMIN_KEY` is read as raw environment bytes without normalization. Fewer than 32 bytes fails before storage opening or listener creation. Operator entropy is required, but cannot be established by a length check. The route packet separately owns constant-time shared-key authentication.
- The HTTP server uses read-header 2s, whole-read 5s, write 5s, idle 30s and `MaxHeaderBytes=8192`. Standard `net/http` parser buffering has its own allowance; this setting is not evidence of an exact raw-wire byte cutoff. The server's `ErrorLog` discards upstream transport/panic output. Closed HTTP diagnostics/recovery belong to the custom route packet; framework stock routes are never started.
- Cached readiness combines request-lifecycle cancellation with the core's atomic storage readiness; no health request performs accounting. Core readiness is only exposed after existing-state validation. Observed fatal SQLite/corrupt-schema state clears it.
- One joined maintenance worker invokes bounded `Cleanup` on a real 10s ticker. Pruning is selected at most once per 60s, anchored before ticker creation so goroutine scheduling cannot delay the first prune to the seventh tick; there is no backlog catch-up loop or overlapping cleanup workers. Each call expires at most 128 rows and prunes at most 128 terminal/credit rows combined. Reads/admissions enforce due expiry independently. Contention is not a fatal readiness failure and produces no request/error log.
- SIGINT/SIGTERM cancels request and maintenance work, stops admission, drains actual HTTP handlers and joins maintenance/server workers before storage closure. No deadline is reported as proof a transaction stopped. A committing transaction retains the writer gate until authoritative completion. Shutdown may therefore exceed HTTP budgets if OS begin/commit/fsync stalls.
- `Service.Close` clears readiness, acquires the same writer gate, triggers the supported PocketBase `OnTerminate` hook and uses supported `ClearBootstrap` as its final action. Existing `OnBootstrap`/`OnSettingsReload` hooks remain; **no `OnServe`, stock router, installer, native API, cron or PocketBase CLI is invoked**. Closure errors are retained rather than discarded by a best-effort CLI callback. Startup/listener/maintenance failures take precedence over subsequent cleanup errors; cancellation does not erase an already observed fatal maintenance failure.
- Runtime diagnostics use closed codes for invalid runtime key/listener, unavailable listener startup, handler construction, serving, shutdown and unexpected startup failure. They include a stage/constraint/outcome/action without paths, addresses, arguments, credentials, exception text or stacks. Listener failure says no accounting HTTP operation ran, not that framework migrations/watermark validation never happened. Interrupted delivery is not a financial receipt.

## Observed toolchain and commands

Observed locally: **Go 1.27.2, darwin/arm64**, through the existing devenv shell. Pinned modules remain PocketBase **v0.40.5**, dbx **v1.12.0**, modernc SQLite **v1.60.1**; no `go.mod`/`go.sum` edits. The parent's earlier complete test/module verification pass is context, not evidence that this packet builds with the missing route dependency.

**PASS**:

`devenv shell -- go version`

`devenv shell -- go test ./internal/ledger`

`devenv shell -- go test -race ./internal/ledger`

`devenv shell -- go test cmd/intake/server.go cmd/intake/server_test.go`

`devenv shell -- go test -race cmd/intake/server.go cmd/intake/server_test.go`

`devenv shell -- go vet ./internal/ledger`

`devenv shell -- go vet cmd/intake/server.go cmd/intake/server_test.go`

**BLOCKED by missing `internal/httpapi`**, not a passing build:

`devenv shell -- go test ./...`

`devenv shell -- go vet ./...`

`devenv shell -- go build ./cmd/intake`

File-list testing deliberately excludes `main.go` and subprocess tests: it qualifies server lifecycle code with a synthetic HTTP handler, not authentication or accounting routes. Full test/race/vet/build, actual executable/curl smoke, generated-image qualification and the independent route review remain required after the dependency lands. An eight-file active LSP probe confirmed one file clean and timed out on seven; zero returned diagnostics is not proof those files compile. Compiler/vet/race checks above are the scoped evidence.

## Passing synthetic evidence

New lifecycle tests:

- `TestLocalListenAndServerBudgets`: loopback-only validation, selected HTTP budgets and discarded raw HTTP logger.
- `TestMaintenanceCadenceAndCancellationJoinsActualWork`: selected cleanup/prune cadence, no overlapping/abandoned maintenance or new work on cancellation. Numeric tick fixtures do not prove the production wall-clock schedule.
- `TestFatalMaintenancePreservesKnownCategory`: known failure remains the original diagnostic.
- `TestShutdownWaitsForActualHTTPHandlerCompletion`: a real loopback `net/http` request is cancelled, but the service waits until the test handler actually returns. No accounting/auth handler is involved in this scoped test.
- `TestCloseWaitsForCommitAndTriggersCoreTermination`: supported real PocketBase transaction with a modeled stalled-completion wrapper; close waits, readiness clears, termination occurs after completion, and reopening the same database resolves the original credit exactly once.
- `TestTerminationFailureIsClosedAndDoesNotReopenAccounting`: hostile hook failure becomes the closed shutdown category; storage does not become ready again.

The complete existing core suite was rerun with race detection. Relevant evidence includes:

- `TestWriterWaitFastReleaseAndBoundedCancellation`, `TestCancelledWorkRollsBackAndQueryBudget`, `TestExternalSQLiteLockBusyBudgetAndNoLaterEffect`: real writer waits, actual recursive-query cancellation, raw synthetic SQLite locks, rollback/no later effects and same-key retry. WAL read-to-write upgrades can return BUSY immediately; 250ms is a maximum busy wait, not guaranteed queueing.
- `TestBeginFailureAndObservedFatalStorage`, `TestBeginStallCancelledCallbackMakesNoEffect`, `TestCommitStallRetainsGateUntilActualCompletion`, `TestCompletionEvidenceLossAndSameIdentityResolution`, `TestRollbackAndPanicNeverExposeHostilePayload`: known no-effect versus unknown completion, observed commit versus later failure, no abandoned transaction, and closed hostile failure handling. Stalls and evidence loss are test wrappers around the real supported transaction, **not real OS disk-stall/power-loss evidence**.
- `TestLastCreditAndDuplicateRaces`, `TestTerminalRacesDisabledAndExactExpiry`, `TestSnapshotCompleteSortedScopedAndExpiry`, `TestSnapshotRacingSettlementNeverReturnsMixedState`, `TestBoundedCleanupAndReadsIndependentOfBacklog`: serialized accounting, first terminal outcome, complete consistent scoped snapshots and expiry independent of cleanup backlog.
- `TestKeyPruningRetentionRollbackClockAndRestart`, `TestNewKeyAdmissionBoundariesAndFailedReserveRetry`, `TestBackdatedActiveRetryAfterKeyValidityEnds`, `TestIdempotencyOriginalResultsAndInputConflict`: immutable original inputs/deadlines, retained retries, window/skew/retention boundaries and pruned-key rejection after restart with wall time behind the committed watermark. This assumes the original database; it is not disk-rollback protection.
- `TestExplicitInitMissingPartialAndUnknownSchema`, `TestSchemaDriftRejectedAndMigrationRollback`, `TestRestartPreservesAcknowledgedBalancesActiveAndTerminal`, `TestCorruptStateFailsClosedWithoutRepair`: explicit v1 migration, transactional DDL rollback, no silent repair/reset, acknowledged-state restart and unknown/drifted state rejection.
- `TestConnectionPragmasAndForeignKeys`, `TestNoCollectionsSuperusersOrActivityLogs`: actual pooled/reopened connections, constraint/storage checks and overridden saved logging configuration on restart, including framework logger calls leaving `_logs` empty.

## Written subprocess tests — not yet executed

These use a fresh subprocess invoking the production `main` path through the compiled Go test helper, real loopback HTTP and disposable PocketBase storage. They are not generated-image or deployed-service evidence.

- Updated `TestCLIExplicitInitExistingServeAndPrivateErrors`: missing-state refusal, zero-state init, nonempty init refusal, actual held-port listener failure and malformed CLI/corrupt database diagnostics without hostile values.
- `TestProcessKeyRejectedBeforeListenerAndPrivateStartup`: missing/short key rejection, reusable unopened listener and remote-listener refusal.
- `TestProcessAuthNativeRoutesPrivacyAndRestart`: absent/wrong key on every accounting route, hostile input, unreachable stock installer/dashboard/collection/auth/file/realtime/settings/backup paths, authoritative settlement plus active-hold restart and exact-key retry. Asserts silent normal stdout/stderr, zero superusers/activity logs and no synthetic private sentinels in framework auxiliary DB/WAL/SHM bytes after shutdown.
- `TestProcessLocksLostResponseRacesAndExpiry`: actual HTTP storage-lock failure with known no-effect, same-key retry, incomplete response delivery resolved by original credit identity, last-credit race, settle/refund race and expired settlement returning the authoritative row without debit. Reading one response byte deliberately does not establish a complete caller receipt.
- `TestProcessScheduledExpiryPruningAndCachedHealth`: actual production 10s expiry / 60s pruning schedule, read-only inspection without accounting endpoints doing cleanup, and cached health while a synthetic writer lock is held. This test takes about one minute; it advances only the disposable persisted clock and exposes no production clock setting.

## Storage/default and migration boundaries

Inspected installed PocketBase v0.40.5 `core/db_connect.go`, `core/db_tx.go`, `core/base.go` and termination-event definitions. Upstream default connection pragmas are WAL, **NORMAL**, foreign keys, **10000ms** busy timeout and memory temporary storage. The core's supported `DBConnect` overrides these to **FULL/250ms** on each physical connection while preserving safe cache/journal-limit/defensive options. System tables/settings and `auxiliary.db` still exist; the four ledger tables are not collections. Bootstrap runs PocketBase system migrations and settings reload even if later ledger validation fails.

The upstream batch logger normally has an in-memory ticker and can asynchronously write to the auxiliary database; `IsDev` also permits terminal output. This service uses `IsDev=false`, sets `Logs.MaxDays=0` before bootstrap and overrides loaded settings before the outer logger hook sees them. The installed source stops its logger ticker on bootstrap clear and starts its stock cron only through `OnServe`, which is not invoked. Passing core tests exercise saved settings, restart, actual logger calls and auxiliary log counts; **actual request/error/panic and whole-process auxiliary-state qualification remains blocked on the HTTP packet**. Disabling request logs alone would not suffice.

Version 1 is the only ledger migration/schema. There is **no previous released ledger code**, so previous-release binary compatibility/upgrade/downgrade cannot be demonstrated. Current-code restart, actual v1 initialization, migration rollback and unsupported-schema refusal passed; no fabricated prior-release claim or down-migration was added. PocketBase remains pre-v1 with its production-critical warning. Future dependency/schema changes require manual review/qualification against the original database.

Remaining unknowns: real OS commit/fsync and power-loss durability; encrypted-volume/customer-key unlock and rollback; installed/generated-image read-only root and writable state; prior-release compatibility once a release exists; actual HTTP package behavior/curl smoke; provider/network/operator collection; and all live integration/deployment. No telemetry middleware/exporter was added. WAL/deleted pages remain persistent accounting/storage boundaries, not secure erasure. Passing these scoped tests authorizes neither integration nor deployment.
