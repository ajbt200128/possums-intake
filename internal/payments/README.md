# Synthetic payment integration

Local synthetic-only coordinator under `PRIVACY.md`'s **Local synthetic Stripe
top-up implementation** and **Forbidden collection and defaults**. No ordinary
runtime, CLI, listener registration, production client, secrets, gateway changes,
telemetry or worker is introduced. Selecting an isolated fake loopback provider
remains the fixture owner's responsibility.

## Host API

- `NewOffline(ctx, *ledger.Service, *stripepay.Client) (*Service, error)` requires
  **already explicitly initialized** synthetic payment schema. It drains bounded
  `PurgeSyntheticPayments` batches before returning admission, under a shared
  ten-second context budget. Failure returns no usable coordinator. Existing
  PocketBase non-cancellable begin/commit limitations still apply; this is not a
  guaranteed wall-clock completion bound. No initialization, reset or repair.
  It rejects sandbox clients **before purge**.
- `NewSandbox(ctx, *ledger.Service, *stripepay.Client)` is a distinct source-only
  admission API under `PRIVACY.md`'s **Subsequent local Stripe sandbox
  authorization** and `docs/stripe-sandbox-local.md`. It requires the adapter's
  authoritative `IsSandbox` predicate, refusing offline/nil/zero clients before
  purge. Both modes share the same explicit-schema and overdue-purge admission;
  neither constructor calls Stripe. Dispatch, once-only claims, atomic funding,
  typed storage/unknown outcomes and all accounting decisions remain unchanged.
  This adds no runtime selection, environment/secret lookup or CLI listener.
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
  fixed navigation policy: canonical fixture origin for offline, fixed test
  Checkout origin/path with bounded transient fragment for sandbox. Zero/nil
  clients grant nothing. No browser-selected origin or allow-any override exists.
  Website owns CSP,
  form-action qualification, secure departure and fixed return routes.

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

Latest scoped results: race tests passed for payments (93.5% statement coverage),
stripepay (95.9%), ledger (87.0%) and website (84.3%). Scoped vet passed. Active
LSP checks covered 18 touched Go files with no errors; one Semgrep warning in
adapter webhook verification flags using the SDK Event value on error. The SDK
returns that struct by value (including version-mismatch cases); it is not a nil
pointer dereference. No version/signature bypass was introduced.

`devenv shell -- env OTEL_SDK_DISABLED=true go test -race -count=1 -cover ./internal/payments ./internal/stripepay ./internal/ledger ./internal/website`

`devenv shell -- env OTEL_SDK_DISABLED=true go vet ./internal/payments ./internal/stripepay`
