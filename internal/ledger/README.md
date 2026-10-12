# Packet 1 — standalone local core

## Route/lifecycle handoff

`Open(ctx, absoluteStateDir)` validates **existing** state and returns `*Service`, implementing `Accounting` in `types.go`. `Init` is a separate explicit, empty-directory-only local operation. Neither grants credit or creates superusers. `serve` currently validates/closes existing state and exits with `LEDGER_HTTP_NOT_IMPLEMENTED`; there is deliberately no HTTP listener, stock PocketBase router, installer, metrics exporter or maintenance scheduler yet.

- `Provision`, `Credit(CreditInput)`, `Reserve(ReserveInput)`, `Settle(SettleInput)`, `Refund`, `Balance`, `Lookup`, `Cleanup(prune)` take caller contexts. Every accounting read/effect uses `txApp.DB()` inside supported `RunInTransaction`.
- Inputs use `Money` (canonical decimal string, revalidated by the core). Route parsing can use `ParseMoney`; JSON output remains a string. `Millis` serializes fixed millisecond UTC text. Nullable terminal fields remain null until terminal transition.
- `BalanceSnapshot` contains one account object, the **complete**, reservation-ID-sorted pending row slice and available credit. No generic table/field export.
- On ordinary errors, returned rows are zero values and **not receipts**. The exception is `Settle`: an expired hold returns its authoritative expired row **and** `LEDGER_SETTLEMENT_EXPIRED`. HTTP plumbing must preserve that separate row. Existing settled/refunded outcomes win conflicting terminal attempts; HTTP 200 is not proof the requested action won.
- Errors are closed `*Diagnostic` values (`code, stage, constraint, outcome, next_step`), without wrapped raw causes. Keep schema/key, conflict, insufficient/disabled, overflow, evidence-unavailable, writer wait, cancelled work, begin failure, rollback-confirmed storage failure and uncertain completion distinct. `Failure` is a content-free unexpected-error fallback, not a raw-error formatter. Map these to the status table in `PLAN.md`; authentication/parser errors belong to the route packet.
- `Ready()` is an atomic cached read, performs no accounting and may feed healthz. Observed corrupt/schema/fatal SQLite failure clears readiness. `Close()` waits for actual in-flight completion. No app/DB handle is exposed to route callers.
- Lifecycle scheduling remains a later packet: call `Cleanup(false)` every 10 seconds; use `true` every 60 seconds. One call expires at most 128 rows and prunes at most 128 terminal/credit rows combined. Reads/admissions handle due expiry independently of cleanup backlog.

Do not call PocketBase's stock `Start`/serve command or trigger stock `OnServe` to wire HTTP: that would enable stock routes/cron outside this packet's qualified surface. `BaseApp.Bootstrap` does retain PocketBase's internal system tables/settings and auxiliary database. These are not the four accounting tables, and native superusers remain a separate control plane. Logging is disabled before bootstrap and overridden during settings reload; no superuser bootstrap is performed. Real stock HTTP/error exclusions still require actual route-packet tests.

## Reproducible local verification

Verified on darwin/arm64 with Go **1.27.1**, selected from the following immutable nixpkgs revision. PocketBase **v0.40.5**, dbx **v1.12.0** and modernc SQLite **v1.60.1** downloaded successfully; `go.sum` records actual checksums and `go mod verify` passes. Inspected the downloaded PocketBase `go.mod`, `core/db_connect.go`, `core/db_tx.go`, bootstrap/settings/logger code, dbx transaction/query-context source and modernc cancellation source. PocketBase is pre-v1 and not qualified for production-critical use by these local tests; manual changelog/migration work and real durability remain gates.

Exact tested build command, from the repository root:

`nix shell github:NixOS/nixpkgs/7a0f122f5090cf4c2ade2a13a0e229d4e19ba71f#go_1_27 --command env GOTOOLCHAIN=local go build -o /tmp/possums-intake-packet1 ./cmd/intake`

Qualification commands:

`nix shell github:NixOS/nixpkgs/7a0f122f5090cf4c2ade2a13a0e229d4e19ba71f#go_1_27 --command env GOTOOLCHAIN=local go test ./... -count=1`

`nix shell github:NixOS/nixpkgs/7a0f122f5090cf4c2ade2a13a0e229d4e19ba71f#go_1_27 --command env GOTOOLCHAIN=local go test -race ./... -count=1`

`nix shell github:NixOS/nixpkgs/7a0f122f5090cf4c2ade2a13a0e229d4e19ba71f#go_1_27 --command env GOTOOLCHAIN=local go vet ./...`

`nix shell github:NixOS/nixpkgs/7a0f122f5090cf4c2ade2a13a0e229d4e19ba71f#go_1_27 --command env GOTOOLCHAIN=local go mod verify`

Tests cover canonical/full-u64/overflow/rollback money; zero provisioning and original credit results; last-credit, duplicate credit/reserve and terminal races; complete scoped snapshots racing settlement; disabled admissions and exact expiry; immutable timeout/input/key conflicts and operation scoping; skew/backdated/exact-window boundaries and active/terminal retries; bounded pruning and committed watermark replay protection after restart with SQLite wall time behind the watermark; missing/partial state and schema drift; acknowledged active/terminal persistence; actual connection pragmas, foreign keys and corrupt/impossible holds; actual recursive-query cancellation and external locks; modeled begin/commit stalls and completion evidence loss around real PocketBase transactions; closed hostile error/panic behavior; actual CLI process stderr and logging/superuser exclusions.

LSP probing was inconclusive in this harness; compiler, vet and race tests are the type/runtime evidence. Auxiliary lint warnings about a CLI status print and `context.Done()` are not application logging or WaitGroup misuse.

## Qualified boundaries and remaining constraints

- DSN options apply to **each physical connection**: WAL, FULL synchronization, foreign keys, 250ms busy timeout, memory temp storage; safe upstream cache/journal-limit/defensive options retained. Tests hold/reopen all four read-pool connections and exercise the transaction writer. Schema version 1 checks exact accounting DDL/index definitions, integrity, metadata and all persisted money/rows. Unknown versions/structural drift fail closed; there is no automatic down-migration or financial restore.
- In-process gate waits up to 2 seconds or caller cancellation. All callback queries share a 1-second context, checked before commit; PocketBase `QueryTimeout` is also 1 second. Plain parameterized queries avoid PocketBase model-query lock-retry hooks.
- **A SQLite WAL read-to-write upgrade can reject BUSY immediately**, even with `busy_timeout=250`. The timeout is not guaranteed queueing. Actual first-statement writes exercised the busy handler; cancellation/locks rolled back without later mutation or successful-effect markers.
- `RunInTransaction` begin/commit have **no context parameter**. OS fsync/commit and bootstrap/system migration behavior are not hard real-time bounded. Test-only wrappers model begin/commit stalls; they are not real disk-stall or power-loss proof. Gate ownership remains held until actual completion, without detached accounting goroutines. A cancelled pre-commit callback rolls back; cancellation during commit does not prove rollback. Observed completion can still establish the core receipt after the caller deadline; HTTP delivery remains separately unknown. Lost/error completion evidence yields `unknown`, never invented success or no-effect. Retry only the original identity to resolve it.
- The committed watermark prevents backwards wall time reopening a **pruned original identity**. Forward jumps can expire holds early; backwards jumps leave effective time ahead until wall time catches up. This assumes the original database/watermark, not a restored/copied disk. It supplies no disk-rollback protection.
- Restart tests observe ordinary acknowledged SQLite state; hardware/power-loss durability, encrypted-volume key handling, generated images, prior-release upgrades, real HTTP delivery/route exclusions, lifecycle and telemetry remain later acceptance. One authority only; no live gateway, exports, payments, cloud resources, commits or pushes were used.

Privacy authority: [local accounting/defaults](../../PRIVACY.md#accounting-and-processor-boundaries), [forbidden defaults](../../PRIVACY.md#forbidden-collection-and-defaults), and gateway [data boundaries](../../../possums/PRIVACY.md#data-handling-boundaries) / [forbidden telemetry](../../../possums/PRIVACY.md#telemetry-permitted-signals-and-forbidden-data). Diagnostic/runtime checks here use isolated synthetic data. WAL/deleted pages are retained storage, not secure erasure. Native T3/Pi task history remains a separate persistence boundary.
