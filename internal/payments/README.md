# Explicit payment coordination

Source-only coordinator under `PRIVACY.md`'s **Local synthetic Stripe top-up
implementation**, **Subsequent live Stripe payment direction** and **Forbidden
collection and defaults**, plus the gateway's `PRIVACY.md` **Data handling
boundaries**, **Telemetry: permitted signals and forbidden data** and **Processors,
retention, access and shutdown**. See `docs/stripe-live-activation.md` for the live
contract and activation gates. No ordinary runtime selection, CLI, schema creation,
listener registration, secret lookup, gateway change, telemetry or worker is
introduced. Ordinary serving remains payments-off. Selecting an isolated fake
loopback provider remains the fixture owner's responsibility.

## Host API

- `NewOffline(ctx, *ledger.Service, *stripepay.Client) (*Service, error)` requires
  **already explicitly initialized** synthetic payment schema. It drains bounded
  `PurgeSyntheticPayments` batches before returning admission, under a shared
  ten-second context budget. Failure returns no usable coordinator. Existing
  PocketBase non-cancellable begin/commit limitations still apply; this is not a
  guaranteed wall-clock completion bound. No initialization, reset or repair.
  It rejects sandbox and live clients **before purge**, then requires the exact
  persisted `PaymentIdentitySynthetic`; initializer success is not mode proof.
- `NewSandbox(ctx, *ledger.Service, *stripepay.Client)` is a distinct source-only
  admission API under `PRIVACY.md`'s **Subsequent local Stripe sandbox
  authorization** and `docs/stripe-sandbox-local.md`. It requires the adapter's
  authoritative `IsSandbox` predicate, refusing offline/live/nil/zero clients
  before purge, then requires `PaymentIdentitySynthetic`. All modes share the
  explicit-identity and overdue-purge admission; no constructor calls Stripe.
  Dispatch, once-only claims, atomic funding,
  typed storage/unknown outcomes and all accounting decisions remain unchanged.
  This adds no runtime selection, environment/secret lookup or CLI listener.
- `NewLive(ctx, *ledger.Service, *stripepay.Client)` requires authoritative
  `IsLive`, not sandbox, a nonempty configured product tax code and fixed
  `https://checkout.stripe.com` navigation origin. It requires the exact persisted
  `PaymentIdentityLiveStripeTax` before draining `PurgeLiveStripePayments` batches
  under the same ten-second startup context. Every overdue purchase/sale pair
  must drain before admission; failure/cancellation returns nil service. It never
  initializes/relabels schema or obtains credentials. The immutable service
  identity is not a global cache; the ledger still validates persisted schema and
  original terms at each accounting operation. Configuration/accessor checks
  verify constructor/adapter mode agreement without provider or database work.
- `*Service` implements `website.TopupActions`: `StartTopup`, `TopupStatus`,
  `CheckoutURLAllowed` and `CheckoutOrigin`. Website must validate the exact live
  server-issued session form on **every** start, including duplicate submissions.
  Ledger validates its original window/account/terms. Prepare and durable claim
  precede the one SDK create call, outside accounting transactions. A false claim
  returns local status only, never another URL, create or retrieval. Attempted
  and unknown claims survive restart and never authorize automatic replay.
- A create failure preserves the typed Stripe primary/creation-unknown outcome;
  best-effort marking uses the same request context, without detached recovery.
  The durable attempted claim already forbids replay if marking cannot complete.
  A successful provider create followed by failed local binding returns a typed
  `payments.Error` with `checkout_created_local_binding_unconfirmed` and its
  preserved typed ledger cause (`errors.As`), not a provider rollback claim.
- `website.TopupResult.Reference` is private session data, not public JSON or
  markup. A known reference is retained on start errors. URL is transient and
  never persisted/cached. Status returns no reference or URL. Missing/expired or
  cross-account mappings yield `unavailable_after_unlink`; malformed inputs and
  other known errors remain typed failures. Status performs only `GetPayment`:
  no provider calls, charge creation, binding or credit. The core may advance its
  clock watermark; this is not a promise of zero SQLite writes.
- `CheckoutOrigin` and `CheckoutURLAllowed` forward only the adapter's configured
  fixed navigation policy: canonical fixture origin for offline, fixed Checkout
  origin and mode-specific Session path with bounded transient fragment for
  sandbox/live. Zero/nil
  clients grant nothing. No browser-selected origin or allow-any override exists.
  Website owns CSP,
  form-action qualification, secure departure and fixed return routes.

## Admission failure ownership

`NewLiveObserved(ctx, *ledger.Service, *stripepay.Client, failureowner.Observer)`
has the same concrete configuration, identity and bounded-drain requirements as
`NewLive`. It owns **all admission primary and secondary failure observations**,
including configuration, mode decisions, actual context cancellation/deadline and
unexpected admission panics. Runtime callers must **not recount its propagated
error**. It performs normal admission only, never diagnostic work or retry.

Each identity lookup and each purge batch gets a fresh `ledger.WithFailureReport`
context, shadowing any caller report. The owner immediately consumes that report
with `ledger.ObserveRunFailures` after the call returns and the writer gate is
released. Known commits remain successful even when the report contains distinct
after-completion faults; equal categories do not collapse distinct occurrences.
Invalid reports use the existing observer invalidation policy, not healthy zeros.
Legacy constructors share these per-call boundaries with a nil observer; core
classification and report consumption do not require collection/export. No new
category, runtime enablement, SDK request, permission tier or payment counter.
This follows intake `PRIVACY.md`'s **Mandatory interface failure classification
and error aggregates** and the gateway's **Typed interface failures** policy.

`admission_observed_test.go` uses actual disposable ledger state and inert SDK
clients, without listeners or provider requests. It covers preflight/context and
post-identity decisions, schema/corrupt/not-ready primaries, observer panics,
partial-result clearing, multi-batch paired purge and untouched caller reports.
The ledger's `failure_report_primary_test.go` additionally covers known commits
with completion error/panic and distinct primary/secondary roles. Admission's
SQL-only runs expose no supported public completion-hook injection through the
concrete ledger service; the core tests use its private app seam. This packet
adds no unsafe access or production injection API, so composed admission with an
injected after-completion fault remains an explicit coverage limit.

Scoped ownership-packet checks passed in the pinned offline environment with
`OTEL_SDK_DISABLED=true`, `GOPROXY=off`, `GOSUMDB=off` and
`PYTHONDONTWRITEBYTECODE=1`: focused `TestLiveObserved` race tests (8.041s), the
whole payments race suite (69.361s), and the three supplemental core tests
`TestFailureReportPrimaryTracksFinalRunResult`,
`TestFailureReportPrimaryAPICompatibilityAndPrivacy` and
`TestFailureReportPublishedOnlyAfterTransactionAndGateRelease` (3.438s).
Payments-only vet/build and an active LSP probe of the three changed Go files
also passed. No root functional run, runtime/provider acceptance, export or
activation is claimed.

## Webhook and evidence ownership

`WebhookHandler()` returns an **unmounted** `http.Handler`. Only exact
`POST /payments/stripe/webhook`, without query or RawPath, is accepted. The raw
body is capped at 256 KiB before supported SDK signature/version verification.
No customer CSRF/session/shared accounting key is used by this handler. Parent
must mount it separately and must not put payment traffic in accounting metrics.

A relevant Checkout notification's signed reference is only a lookup selector.
Absent/expired/pruned mappings never trigger provider reads or recreate state.
Prepared attempts or a known different Session are ineligible. Canonical SDK
retrieval independently checks the original stored reference/terms/window and
Session→PaymentIntent→Charge relationships. No event payment fields are used.
The core rechecks identities and expiry at the serialized funding decision.

Only nil-error canonical evidence authorizes normal funding. The adapter's exact
`CodeEvidence/StageCharge/ConstraintRefund/OutcomeReadUnavailable` error with
explicit canonical refund/dispute fields also authorizes calling the core, solely
for its sticky block (or an already-funded receipt). Missing, invalid or negative
refund evidence never authorizes blocking or funding. Decimal mapping uses
`strconv`, never floats. No generic `Credit` or provider-marker table is used.

Relevant canonical refunds/disputes block before funding; later success cannot
unblock. Already-funded credit is never clawed back. Standalone refund/dispute
notification kinds remain inert in the adapter: there is no Charge→Session search
or automatic refund workflow. Current refund state is checked on supported
Checkout notifications. A remote refund racing the read/commit remains an
accepted distributed-transaction limit, not an atomic Stripe/SQLite guarantee.

Closed JSON statuses distinguish funded, already_funded, blocked, ineligible,
ignored, rejected and unavailable, with no references/provider IDs/raw text.
Ineligible references get 2xx, not a funding/non-charge receipt. Eligible read,
storage and unknown-completion failures get 5xx; no early acknowledgement or
queue. Body/signature/version errors get 4xx. Known core/Stripe classifications
survive safe HTTP projection. Unexpected panics become stage-specific
cause-unknown failures. No metrics/logs/error observers are invoked.

A response-write error/panic is classified transiently with its observed outcome;
there is no remaining HTTP delivery channel, log/export or retained artifact for
that diagnostic. It cannot erase a confirmed commit or cause another mutation.
Tests exercise successful funding followed by delivery panic and safe redelivery.
The ledger packet owns actual transaction completion fault injection; this packet
checks typed commit-unknown projection, real storage closure, cancellation and
post-commit delivery failures, not a new production fault hook.

## Live principal, tax and original-term mapping

Only live `StartTopup` supplies the trusted adapter's `ProductTaxCode` to
`PreparePaymentInput`; the browser cannot supply it. Existing purchase reads
always project the **stored original** tax code, even after configuration changes.
Issuance preserves milliseconds, Checkout expiry preserves the original rounded
provider time, and `GrossCents`/legacy `GrossMinor` always mean credit principal.
Session evidence carries actual `LiveMode` and original `TaxCode`, never invented
test-mode defaults. Nullable final tax evidence is value-copied, including all
completion/exclusive/self-liability facts; missing tax is not synthesized as zero.
For validated live evidence only, PaymentIntent/Charge amounts use final tax
`TotalCents`; received/captured amounts remain the adapter's actual validated DTO
amounts. Synthetic intent/charge amounts remain principal. The ledger rechecks
mode, final tax arithmetic and exact totals, and credits only $5/$10/$25/$50 base
principal. A known refund error remains solely sticky-block/known-receipt authority,
never new funding authority.

The live schema owns the immutable issuance-plus-20-day paired purchase/sale
cutoff. Payment, duplicate notification, status and support never refresh it.
Logical deletion does not establish immediate physical erasure or Stripe deletion.
Live configuration failures use closed live-specific diagnostics, not misleading
offline instructions. No error text, identifiers, amounts, URLs, new counters or
permission tiers enter diagnostics/observability.

## Live offline coordinator evidence and limits

`live_test.go` and `live_boundary_test.go` exercise actual disposable Go/PocketBase
v2 state with a **private, package-internal** adapter port. Public constructors
still accept only concrete `*stripepay.Client`; there is no exported custom adapter,
transport or authority constructor. The live fixtures replace only create/retrieve
methods with authored canonical DTOs, not SDK private fields or fixed endpoints.
Mode/navigation accessors and raw-body signature/API/mode verification use the
actual SDK client with authored synthetic credential shapes. No network-capable
live/sandbox SDK operation or existing local sandbox is contacted. Existing V1
fixtures retain actual SDK request/response coverage against their fake loopback
provider; these live DTO tests do **not** qualify the live SDK three-GET chain or
actual Stripe Tax expansion/API behavior.

Coverage includes the absent/v1/v2 versus nil/zero/offline/sandbox/live admission
matrix, paired multi-batch startup drain, canceled admission, immutable original
tax code after configuration change, nonzero/zero completed tax, malformed/missing
mode/tax refusal, one fund under concurrent duplicate signed live notifications,
canonical sticky refund/dispute handling, no clawback of known credit, uncertain
creation/binding without URL receipts or replay, expiry after canonical retrieval,
pruned replay refusal, inert status across retained states and inert mode-aware
diagnostics. No SDK errors or event fields become funding authority.

Coordinator packet verification: the full payments race suite passed in 59.889s;
scoped `go vet` and `go build` passed; all root packages/tests compiled with
`go test -p 1 ./... -run '^$'` (no root functional tests executed). Active LSP
checks reported all eight changed Go files clean. These commands used the pinned
`devenv --offline` shell with telemetry and module downloads disabled. No coverage
percentage or live provider result is claimed for this packet.

`devenv --offline shell -- env OTEL_SDK_DISABLED=true GOPROXY=off GOSUMDB=off PYTHONDONTWRITEBYTECODE=1 go test -race -p 1 ./internal/payments -count=1`

Remaining activation gates are separate: provider/API/hosted behavior, actual
product-tax classification, registrations/filing, legal retention and active durable
website processor/pricing/tax/support disclosures, explicit existing-state setup
and runtime wiring, parent full-root acceptance and native image/provenance/signing
qualification. The live account and gateway test are operator-confirmed; no new
gateway retest is requested here. Operator controls actual deployment. No real
configuration, provider call, payment, inference, export or deployment follows.

## Sandbox source-only evidence

`TestSandboxAndOfflineModeMismatchRefusedBeforePurge` checks both mismatches
against an overdue retained purchase, verifies refusal leaves it untouched, then
checks sandbox admission drains purge. `TestSandboxAdmissionRequiresExplicitSchemaAndValidContext`
checks disabled schema, nil/zero/offline adapters, missing ledger/context and
canceled/expired admission. These tests use authored synthetic credential shapes
and perform no network-capable sandbox operations. Adapter tests substitute a
private local fake SDK backend; ordinary offline once-only tests remain intact.
No provider identity/scope, actual sandbox purchase, runtime/browser/CLI/private
secret handoff, real sales or deployment acceptance follows.

## Scoped synthetic evidence

Tests use actual temp Go/PocketBase state, the actual pinned Stripe SDK, an
in-process loopback provider and SDK-signed HTTP webhook fixtures. They cover
fractional issuance, concurrent starts/events, one sale/credit effect, no generic
credit marker, local-only status, invalid terms/ref/objects, lost/canceled create,
post-create binding failure, durable unsent claims across restart, sticky refunds
(including pending/failed/canceled/unknown statuses), disputes, missing/expired/
pruned references, post-read expiry and multi-batch startup purge. Provider-side
callbacks successfully perform local ledger work, checking no remote call holds
the accounting writer gate. HTTP size/signature/version/context/route failures,
panic/delivery faults, closed diagnostics, logical database field/bridge inventory
and integrated stdout/stderr silence are exercised.

Logical-row inspection is not secure erasure or a whole-process/browser/platform
privacy guarantee. SDK objects/request memory, native host history, SQLite/WAL,
free pages, snapshots, provider records and browser hosted URLs remain separate
boundaries. Parent owns route/browser/CSP, full-runtime/image/retention/release
qualification. No real provider, funds, live configuration, deployment, inference
or export was used or authorized.

Historical synthetic scoped results (not live activation evidence): race tests passed for payments (93.5% statement coverage),
stripepay (95.9%), ledger (87.0%) and website (84.3%). Scoped vet passed. Active
LSP checks covered 18 touched Go files with no errors; one Semgrep warning in
adapter webhook verification flags using the SDK Event value on error. The SDK
returns that struct by value (including version-mismatch cases); it is not a nil
pointer dereference. No version/signature bypass was introduced.

`devenv shell -- env OTEL_SDK_DISABLED=true go test -race -count=1 -cover ./internal/payments ./internal/stripepay ./internal/ledger ./internal/website`

`devenv shell -- env OTEL_SDK_DISABLED=true go vet ./internal/payments ./internal/stripepay`
