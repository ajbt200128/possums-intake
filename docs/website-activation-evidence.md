# Minimal website activation — local source and persistence fixtures

Subsequent operator-authorized [isolated browser acceptance and walkthrough](website-activation-browser.md)
now qualify the stock editor's draft/publish path with actual Chromium. The
original preview/ordinary commands remain untouched; human visual inspection and
production/image/real-auth gates remain open. The sections below retain the
original source-fixture packet's scope and evidence.

Scope: the focused [handoff](website-implementation-handoff.md) and
[section 5](customer-auth-data-plan.md#5-minimal-live-website-activation), not real
customer auth/account data. `pkg/lispsite` and its dependency boundary are unchanged.
This packet adds application-side publication APIs and isolated SDK/HTTP fixtures.
It does **not** enable publication/native administration in ordinary serve or
`website-local`, add a command/admin screen, open operator state, change the
running preview, or overwrite its executable. No installation, real credentials,
gateway edits, inference, export, deployment, commit or push occurred.

## SDK feasibility gate

Pinned PocketBase **v0.40.5** remains pre-v1 with its production-critical/breaking
change warning. Inspected cached versioned implementation:

- `ui/src/records/recordUpsertModal.js`: `exportPayload` copies all record fields;
  the normal existing-record editor calls `collection.update(id, payload)`, not
  batch. A boolean field and JSON field use that stock editor; no UI extension.
- `apis/record_crud.go`: `OnRecordUpdateRequest` runs after native authentication,
  collection-rule checks and form loading, before `form.Submit`. Its normal PATCH
  uses `recordUpdate(true, nil)` and defers JSON through `execAfterSuccessTx`.
- `apis/record_helpers.go`: deferred responses use `TxInfo().OnComplete` and write
  only on a nil transaction error.
- `core/db_tx.go`: `RunInTransaction` completes dbx's transaction **before** calling
  completion callbacks, with the actual transaction error. Callback errors are
  joined into its return value; a nonnil return alone does not establish rollback.
- `core/db.go` / `record_model.go`: success hooks inside a transaction also defer
  until completion. A validation/save hook is not commit evidence.

`TestNativePublishTransactionOrdering` uses actual native authenticated PATCH:
preparation observes a nontransactional app, completion reads the committed value
through the original app, and a response-writer probe verifies swap precedes the
stock JSON response. This is a passing SDK gate, not a browser click recording.
Batch/other already-transactional publish intents are refused **before** preparation:
there is no safe prepare-first interpretation inside their existing transaction.
Native batch CRUD otherwise remains stock. A direct transactional-event regression
checks that refusal; no claim of a complete batch-client qualification follows.

## Source, storage and ordering

`internal/website/publication.go` supplies explicit `InitializePublication(app)`
and host `BindPublication(app)` APIs, currently exercised only with synthetic
fixtures. Initialization is one supported SDK transaction, not an ordinary-serve
migration, upsert or reset. Repeating it fails without resetting selected code.
A host binds/reloads before serving; a failed load leaves website admission closed
while native/accounting reserved routes still bypass the website boundary.

Necessary initial inventory (both collections have nil SDK rules):

| Collection / singleton | Fields | Purpose |
| --- | --- | --- |
| `website_bundles` / `websitebundle01` | `draft` JSON; `publish` boolean | One bounded editable candidate; explicit true intent on a native PATCH |
| `website_selection` / `websitebundle01` | `active`, `previous` JSON | Selected code and one retained code-only rollback candidate |

Each code value is `{"host":1,"files":{"init.el":"…",…}}`. JSON fields are
bounded to 256 KiB encoded; preparation additionally enforces the existing 1–8
files / 32 KiB total source and runtime/route/render bounds. Active/previous contain
only the exact captured source strings and host version, not Go VMs, errors,
customer views, auth/session values or runtime services. There are no source
history rows, activation timestamps, queue, watcher, recovery worker, saved user
presentation or telemetry fields. Fonts/licenses remain embedded.

Draft and selection are separate because the **stock editor sends whole records**:
a stale draft editor must not overwrite newer selection fields. Ordinary draft
saves do not publish. `publish=true` captures/decodes source once and serializes
publishers with a mutex. Preparation, three page evaluations and escaped-render
preflight all precede the write transaction and website admission boundary.
The short supported transaction uses its transaction-scoped app for reading the
current selection, moving active to previous, saving captured active, and the
native draft save/reset of publish=false. No Lisp preparation runs in that write
transaction. A registered completion callback swaps only after observed SQL commit
and before the deferred native response. Transactions do not encompass inference,
financial operations or session-store rebuilding.

GETs capture one revision under a short read lock, then run independently. POSTs
recheck captured/current revision and form digest before SDK validation/login/logout
and retain a shared admission lock through the entered operation. Concurrent POSTs
are not serialized; publication waits for already-entered effects before its short
commit/swap boundary. SDK calls that do not cooperate with cancellation can delay
that wait: this is not a hard latency/capacity guarantee. Every page gets its normal
fresh VM. Sessions are not reconstructed or reset by publication; restart still
loses volatile SCS state. The accepted stock overlapping-logout resurrection risk
and disposable no-cleanup-worker memory limitation are unchanged.

`Revision.CSSPath()` hashes **actual complete generated CSS bytes**, including the
host scaffold. HTML and asset lookup use that identity, not the source digest.
Only one prepared active runtime is retained; previous styling is copied CSS bytes,
not a second retained VM template. Cold load prepares persisted active
and optionally retained previous code; missing/unsupported older assets are absent,
never served with different bytes. References held by older in-flight GETs may
outlive two newer publications; their now-unretained style may honestly be missing.
Literal text, gutters, tooltip face and hover/focus CSS are otherwise unchanged.

## Failure and authority boundaries

Bad preparation leaves active usable and never begins the publication transaction.
Unexpected publication errors are closed, stage-specific diagnostics; no exception
text/source/credentials are copied into diagnostics. Native publication failures
are observed once by the existing native owner, preserving the existing numeric
**39-category** projection. The tiny operator serializer accepts only the closed
publication code/stage/constraint/outcome protocol and authors messages locally;
it ignores supplied diagnostic guidance. It adds no native success/activity or
financial counters. Website admission owns its separate 503, not the earlier
native publication failure. Bootstrap callers own returned setup/load errors.
No observer/exporter, logs, diagnostic persistence or retries are created here.

Unknown commit/completion or an interruption before swap closes new website
admission. The native failure is not a success receipt. Captured GETs can finish;
new admission returns a Go-owned 503. An explicit publish or startup reload must
re-establish authoritative selection; there is no reconciliation worker, guessed
rollback or replay. Confirmed commit/swap evidence survives later completion
failure as `website_selection_committed_response_unconfirmed`, never a rollback
claim. Arbitrary external hooks can prevent response delivery; process loss cannot
be caught by this code. Supported completion ordering is not crash-proof delivery.

Native administration remains unrestricted under the
[trusted manual-admin policy](../PRIVACY.md#trusted-intake-manual-admin-exception).
Admins can directly alter/delete selection, sources, rules/schema or restore state,
add records outside this initial singleton inventory, and bypass this workflow.
Those writes are not live-publication receipts; they can cause persisted/runtime
divergence until explicit publish/restart. This is accepted authority, not tamper
protection. No new permission tier, native route gate or financial SQL changes.
Code-only rollback copies retained previous code to draft and explicitly publishes;
never restore the database to roll back presentation.

Privacy inventory follows [forbidden defaults](../PRIVACY.md#forbidden-collection-and-defaults),
[typed ownership](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates)
and the gateway's [data boundaries](../../possums/PRIVACY.md#data-handling-boundaries).
Only isolated synthetic source/auth/accounting fixtures are persisted by these
tests. SQLite/WAL/deleted pages and optional manual snapshots can retain overwritten
source/state; logical replacement is not secure erasure. Native development-host
history and build/dependency caches remain independent persistence boundaries.
Nothing here approves real customer credentials, runtime secrets in source, or
observability export. Logging-off/AuthAlert-origin/MFA/OTP/accounting code remains
unchanged and covered by full-root regressions.

## Local acceptance evidence

Pinned Go via `.devenv/profile/bin` in PATH; `GOTOOLCHAIN=local`, project-local
GOPATH, `GOPROXY=off`, `GOSUMDB=off`, `OTEL_SDK_DISABLED=true` throughout.

Focused publication suites passed **three repetitions under race/elpscheck**:

- Native draft save is inert; explicit intent activates without host restart;
  final SDK login/session remains usable; stale form returns 409 before auth effects.
- Invalid/hostile candidate leaves old code usable; actual distinct active/previous
  CSS bytes match content hashes; retained previous can be explicitly republished.
- Competing native publishers end with matching persisted/prepared selection;
  later/stale whole-record draft edits cannot rewind selection.
- Actual channel-controlled overlapping authenticated GET finishes on captured old
  code while native publication completes; subsequent admission gets new code.
- Hostile transaction write failure leaves persisted old source, closes admission,
  and explicit publish recovers. Once-only native error counts remain unchanged.
- Actual postcommit SDK success-hook panic interrupts **before swap**: persisted new
  source exists, HTTP is not successful, admission closes, and authoritative reload
  selects committed source without replay. Exactly one native failure observation.
- Actual SDK close/rebootstrap reloads persisted source; missing active never uses
  an embedded demo. Actual ledger open/close/reopen preserves balance and pending
  hold state with added code collections; SDK logging and auth-origin tables stay
  empty in selected fixtures. An early test queried auxiliary `_logs` as a record
  collection; its fixture inspection was corrected to the supported auxiliary DB.
  The strengthened funded/held-state fixture initially used invalid synthetic
  idempotency keys; it now uses the existing timestamp-prefixed key contract.
- Existing core consumer/isolation/cancellation, native, accounting, session and
  compiled disposable CLI tests remain in the full-root suite.

Latest coherent-source checks passed, including both serial and exact parallel
full-root execution. One intervening parallel run failed: existing native fixture
initializations reported `LEDGER_WORK_CANCELLED` at the fixed one-second init
budget, and the existing recorded-startup/open fixture's primary-contract assertion
failed. That run is not passing evidence or a proven root-cause diagnosis. No
accounting/init budget or fixture was changed to mask it. A subsequent full
`-p 1` race/elpscheck run and then the exact parallel command both passed with the
final CSS-only retention change.

Final checks:

- `go test -race -tags elpscheck ./... -count=1 -timeout=240s`: all root packages,
  including the compiled CLI tests, core consumer and native/accounting suites.
- Focused website/operator publication and diagnostic tests: three more
  race/elpscheck repetitions passed after final diagnostic/privacy assertions.
- `go vet ./...`, `go mod verify`, pinned gofmt and `git diff --check`: passed.
- Separate build: `.devenv/state/website-activation-check/intake`; the running
  `.devenv/state/website-local/intake` was not overwritten or restarted.
- Active LSP probe of all 11 touched Go paths: no Go/type errors. Nine auxiliary
  audit warnings were inspected: three HTTP sinks carry validated CSS, complete
  html/template-escaped documents or closed plaintext; five existing native panic
  sites preserve HTTP abort semantics; the new serializer rethrows delivery panics
  to that same owner rather than swallowing them or writing twice.
- The actual postcommit interruption fixture captures stdout/stderr/standard
  logger output and asserts empty output. Failed native HTTP responses additionally
  assert a closed publication code/stage and absence of hostile source/credentials.

These commands used the pinned offline environment above, not host bare Go.
Nested-module qualification was not newly rerun; root library consumer and
isolation tests were rerun. No generated-image or deployed acceptance is inferred.

## Remaining gates

This is source plus actual SDK/HTTPS persistence-fixture evidence, **not an enabled
persistent operator-facing command or production release**. The separately built
CLI artifact retains ordinary command policy and the existing disposable website;
publication is exercised through SDK/HTTP fixtures, not a new CLI. Browser clicks,
full visual/accessibility/Tor behavior, generated-image publication/restart,
production schema rollout/access/TLS/storage keys, long-running session capacity
and real-auth/account data integration remain unqualified/separately authorized.
The running local preview was intentionally untouched. No runtime or deployment
activation is implied by passing these tests.
