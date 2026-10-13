# Classified interface failures and aggregate error counts

## Status and authority

The operator approved mandatory typed classifications at every project-controlled intake↔PocketBase and intake↔gateway failure boundary, including native operator and background failures. The intake side is in scope; gateway implementation remains deferred. See [the privacy extension](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates), [forbidden collection](../PRIVACY.md#forbidden-collection-and-defaults), [failure/export rules](../PRIVACY.md#export-retention-and-failure-behavior) and the sibling gateway's [closed error-category requirement](../../possums/PRIVACY.md#telemetry-permitted-signals-and-forbidden-data).

**This is the selected implementation contract, not complete coverage or export evidence.** The metric foundation passed parent synthetic source/window/SDK/wire checks. Boundary mappings, observation ownership and integrated image/runtime qualification remain pending. The ordinary runtime statically withholds this family until coverage is qualified; neither accepted exporter configuration nor an HTTP-only test fixture can enable unqualified error zeros. Existing qualified metrics are documented in [the telemetry contract](telemetry-contract.md). Live Honeycomb export remains unauthorized; the disposable synthetic startup remains unconditionally telemetry-off.

## Classification contract

Classify from supported typed errors, actual protocol/completion evidence and the locally known failing stage. Never classify by formatting exception text or guessing its contents. Preserve known financial outcomes independently of the error category: a successful commit followed by a hook or socket failure is not a failed commit. A missing completion receipt is not proof of rollback.

Detailed diagnostics retain locally authored code/stage/constraint/outcome/next-step fields, not arbitrary fields from an error claiming to be a known diagnostic. Known classifications survive wrappers. Unexpected faults use the applicable stage-specific cause-unknown fallback. Detailed codes are not automatically exported; telemetry accepts only the numeric closed enum below. No raw causes survive the operation boundary.

A numeric classification is mandatory even when collection is disabled. Early startup, shutdown, process termination, incomplete windows and dropped batches can prevent metric delivery. Never promise that every exception reaches Honeycomb, weaken the off switch, flush partial windows, or initiate work to diagnose a failure.

## Fixed metric schema

Selected fifth instrument: `possums.intake.interface.errors`, Int64 observable **gauge**, unit `{error}`, sole point attribute `error_type`. Export one complete cell set for each aligned, non-overlapping UTC five-minute window, including zero cells. No cumulative counter, total gauge, endpoint/status/operation/outcome cross-breakdown, exemplars, trace links, error messages, identifiers or financial values.

The data-only `failuretype.Type` uses explicit stable assignments. Zero and values outside 1–39 are invalid, not healthy or a generic unknown category. Labels are fixed locally, never supplied by callers or exposed as a mutable registry.

| Number | Label | Number | Label |
| --- | --- | --- | --- |
| 1 | `request_invalid` | 21 | `transaction_rollback_unknown` |
| 2 | `authentication_refused` | 22 | `transaction_completion_unknown` |
| 3 | `policy_refused` | 23 | `transaction_after_completion_unknown` |
| 4 | `account_missing` | 24 | `initialization_unknown` |
| 5 | `evidence_unavailable` | 25 | `storage_open_unknown` |
| 6 | `idempotency_refused` | 26 | `storage_validation_unknown` |
| 7 | `admission_refused` | 27 | `storage_close_unknown` |
| 8 | `amount_refused` | 28 | `native_auth_unknown` |
| 9 | `not_ready` | 29 | `native_read_unknown` |
| 10 | `cancelled` | 30 | `native_write_unknown` |
| 11 | `deadline_exceeded` | 31 | `native_asset_unknown` |
| 12 | `writer_wait_exhausted` | 32 | `request_read_unknown` |
| 13 | `sqlite_busy` | 33 | `request_dispatch_unknown` |
| 14 | `sqlite_constraint` | 34 | `response_encoding_unknown` |
| 15 | `sqlite_storage_failure` | 35 | `response_delivery_unknown` |
| 16 | `schema_rejected` | 36 | `maintenance_unknown` |
| 17 | `state_rejected` | 37 | `runtime_setup_unknown` |
| 18 | `identity_unavailable` | 38 | `runtime_serve_unknown` |
| 19 | `transaction_begin_unknown` | 39 | `runtime_shutdown_unknown` |
| 20 | `transaction_work_unknown` | — | — |

Expected mappings include key/key-window/input conflicts → `idempotency_refused`, insufficient credit/disabled admission → `admission_refused`, overflow/charge-over-hold → `amount_refused`, and late settlement → `policy_refused`, preserving its authoritative expired row. A successful read proving schema/state mismatch is different from a failed read. SQLite busy/locked, constraint and storage-failure categories require supported driver codes; they do not establish a physical root cause. A native HTTP status alone does not establish bad credentials, expired credentials, missing data or a storage cause. SQL no-rows means missing account/evidence only where that lookup contract establishes it.

## Window, serialization and lifecycle rules

The error family has independent validity from the custom HTTP and process families. Reuse the independent one-second heartbeat and existing gap/late/backwards/skipped-window rules. An error observation cannot establish heartbeat coverage. Discard partial startup/restart/shutdown windows; no catch-up zeros.

Each count and the checked family total must remain in 0…2^53−1. Invalid enums, overflow or known observation-integrity failure withhold the error family, not unrelated valid families. Complete sparse windows remain correlatable; this is not anonymity or a distinct-user guarantee.

The final sanitizer must require exactly 39 distinct cells, correct unit/type/sole attribute, aligned boundaries and exact agreement with the released source counts. Reconstruct clean points from constants; reject unknown/missing/duplicate cells and overflow streams. Preserve fixed resources/scope, one sender, one pending batch, no retries/spool/replay and joined shutdown without a final flush. Maximum complete batch is 81 scalar points: existing 42 plus 39. The per-instrument cardinality bound remains 40. The existing 16 KiB transport cap must be demonstrated with actual maximal synthetic supported-serializer bytes; do not silently increase it or split/replay exports.

## Observation ownership — integration requirements

- The custom HTTP owner counts auth/input/accounting failures once. Ledger/diagnostic/rendering wrappers must not recount them. Existing status/latency observations remain only the seven custom accounting routes.
- The restricted native request owner counts native gate/auth/read/write/framework failures. This is error-only, not native success/activity/latency or financial-mutation instrumentation. No generic CRUD hooks.
- The maintenance owner counts each failed cleanup invocation after it returns, including nonfatal failures; fatal propagation must not recount it or alter existing scheduling/recovery policy.
- Startup/lifecycle owners retain primary and distinct secondary setup/cleanup/closure classifications. Failure after telemetry shutdown remains classified, without a metric-delivery claim.
- An independently observed post-completion hook fault must remain visible even when the accounting result is successful. Reporting must occur after the writer gate is released, through a minimal bounded transient numeric seam; no telemetry callbacks in transactions, identifier registry or retained request record.
- Distinguish a propagated fault from an independently observed subsequent delivery fault. Do not count a deliberate `http.ErrAbortHandler`, expected `ErrServerClosed`, or normal shutdown cancellation as a new failure. Do not double-count a cancellation and the same resulting delivery interruption.
- Exporter/sanitizer/telemetry machinery failures remain closed transient classifications and must not recursively enter their own metric pipeline.

Pin exact call-site/type/owner mappings and test duplicate/lost observations before claiming complete coverage. A heartbeat alone cannot detect an omitted observation hook.

## Known limits and acceptance

PocketBase can consume errors internally before project hooks receive them. The stock token-loading middleware, dummy-password lookup and auxiliary cleanup paths require scoped supported-API inspection; do not recover categories by intercepting logger text or recreate authentication. Native PATCH's preliminary SDK lookup precedes the accounting hook, so transaction-scoped negative tests do not qualify that lookup's readiness behavior. dbx's panic rollback path does not expose every rollback error. Process termination, fatal runtime failures and pre-handler transport failures are external interception limits, not generic-success cases. Preserve the existing disclosed SDK default-resource evaluation boundary.

Required remaining evidence: canonical hostile-diagnostic tests; typed SQLite/context and unknown begin/work/rollback/completion tests; known-commit/later-hook/delivery/cleanup races; native authentication/read/write and preliminary lookup tests; once-only propagation; actual HTTP/stdio/auxiliary-state privacy; complete sparse/zero/invalid/partial windows; actual maximal OTLP protobuf/TLS/one-attempt wire checks; off/outage/shutdown independence; generated-image/runtime qualification. All fixtures must be isolated and synthetic, with no live export or gateway inference.

### Parent-observed foundation evidence

The parent inspected the enum, window, runtime, SDK sanitizer and tests. Full Go race tests, vet and module verification passed with the initial foundation and compiled synthetic startup. After the production coverage gate and HTTP-only fixture correction, parent failuretype/telemetry race tests passed three times, with focused vet. The actual supported loopback-TLS protobuf fixture emitted 81 points, five gauges, exactly 39 error labels, **6,205 bytes** and one origin attempt, below the unchanged 16 KiB cap; hostile metadata/credentials were absent from its datapoints.

Independent source review identified misleading zero-error release without boundary hooks. Production construction now disables this family before worker startup; observations do not read its clock or change its state, and ticks do not advance it. Follow-up review confirmed that correction and identified one-shot invalidation in the HTTP-only fixture as insufficient across future windows. That fixture now uses the same disabled-coverage construction; a parent-passed two-window regression checks both paths while preserving existing request metrics. These are source/test-only results, not live Honeycomb or generated-image evidence. No boundary hooks or complete interface coverage follow from this foundation.

Repeatable checks:

`devenv shell -- env OTEL_SDK_DISABLED=true GOFLAGS=-mod=readonly go test -race ./internal/failuretype ./internal/telemetry -count=3 -timeout=120s`

`devenv shell -- env OTEL_SDK_DISABLED=true GOFLAGS=-mod=readonly go vet ./internal/failuretype ./internal/telemetry`

`devenv shell -- env OTEL_SDK_DISABLED=true GOFLAGS=-mod=readonly go test ./internal/telemetry -run '^TestFailureMaximumSupportedTLSProtobufOneRequest$' -v -count=1`

### Parent-observed typed helper evidence

`ledger.Diagnostic.Kind`, `FailureKind` and opt-in `CanonicalFailure` now carry only private numeric classification metadata. Supported context and SQLite causes are inspected without calling `Error`; unknown, typed-nil and panicking `As`/`Is`/`Unwrap` fixtures use closed fallbacks. Canonicalization reconstructs core public templates instead of copying hostile exported fields. `CodeWork` alone does not establish cancellation. Unknown non-completion boundaries use `INTAKE_BOUNDARY_UNAVAILABLE` with an owner's closed stage, not a misleading commit-failure code; the conservative unknown outcome does not erase previously observed accounting results. Legacy `Failure`, initialization/runtime variants and HTTP callers have not been switched to this helper.

The parent inspected the helper implementation and actual SQLite BUSY/LOCKED/extended-CONSTRAINT/NOTADB tests. Full sequential-package race tests, vet and module verification passed; independent source review confirmed the non-accounting fallback correction. One concurrent-package race run returned `LEDGER_WORK_CANCELLED` during an operator fixture's initialization; its cause was not established from that diagnostic alone. The isolated test passed three times and the complete `-p 1` race run passed without changing accounting budgets. This is source-only helper evidence, not whole-boundary observation coverage or new image/export qualification. Transaction reporting evidence is scoped below; outer observation owners remain unwired.

`devenv shell -- env OTEL_SDK_DISABLED=true GOFLAGS=-mod=readonly go test -p 1 -race ./... -count=1 -timeout=180s`

### Parent-observed transaction reporting evidence

`WithFailureReport(ctx)` creates a transient numeric report for **one** `Service.run` invocation; `Take()` consumes at most three classifications once. Do not reuse its context across multiple transactions or count the returned primary diagnostic again. Pre-transaction API validation remains the outer owner's responsibility. Reports publish only after actual transaction completion and writer-gate release, with no exporter callback, additional SQL or retry. Invalid categories, overflow, premature/duplicate consumption, reuse and unsupported completion shapes invalidate evidence rather than yielding healthy zeros.

Parent race tests exercised actual context/SQLite work failures, authoritative rollback, known commit followed by an error or panic, and real PocketBase completion hooks. A disposable deferred-foreign-key fixture verified `foreign_keys=1` on the actual transaction connection, allowed the INSERT callback to succeed and observed typed `SQLITE_CONSTRAINT_FOREIGNKEY` at commit. Its primary fault and each returned subsequent hook fault are counted separately, with the public financial outcome still unknown. Repeated focused races and full sequential-package race tests, vet and module verification passed. Independent review confirmed the missed-secondary-fault correction and removal of unintended readiness changes: reporting newly visible rollback causes does not expand recovery policy.

Failed-rollback cause extraction is qualified only with a labeled synthetic `dbx.Errors` evidence seam, not an induced driver rollback failure. Actual PocketBase hook tests reproduced earlier returned hook errors disappearing when a later hook panics: the boundary can report only the observed primary and panic, not certify every internal hook exception. dbx likewise discards panic-path rollback causes. PocketBase internally formats returned hook errors; no logger interception, raw diagnostic export or SDK replacement was added. These external visibility limits remain explicit.

The report seam is **not connected to HTTP/native/maintenance/lifecycle observation owners** and records no metrics by itself. Mandatory transaction classifications are computed even without a report. The production error-family gate remains closed; this source qualification is not complete interface coverage, generated-image qualification or permission for live export.

### Confirmed local prerequisite fix

An actual regression test reproduced arbitrary non-comparable callback errors becoming `LEDGER_COMMIT_UNKNOWN` because comparison of error interfaces panicked after an observed rollback. A comparable callback-abort token now preserves the supported SDK's rollback evidence without formatting the cause. `TestNonComparableCallbackFailurePreservesObservedRollback` uses an error whose `Error()` panics, verifies `LEDGER_STORAGE_UNAVAILABLE`/`no_effect`, retained readiness and absence of the rolled-back row. The parent passed the full ledger race suite and ledger vet afterward. This scoped fix does not establish new metric coverage or whole-service/image acceptance.
