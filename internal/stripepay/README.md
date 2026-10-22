# Stripe adapter boundaries

Synthetic-only source/test packet under `PRIVACY.md`'s **Local synthetic Stripe
top-up implementation** and **Forbidden collection and defaults**, and
`docs/stripe-topup-plan.md`. No ordinary-runtime activation or production client. The separate payments
coordinator owns local synthetic integration; this package remains accounting-free.

## API and ownership

- `NewOffline(OfflineConfig) (*Client, error)` accepts only literal loopback IP
  origins with explicit ports; endpoint is HTTP (in-process fake server), return
  origin is HTTP/HTTPS loopback. Only the two public fixture placeholder constants
  are accepted. DNS, environment credentials, proxies, redirects and production
  backend defaults are unavailable. Never pass real secrets, including real
  Stripe test-mode credentials. A constructor cannot prove that an arbitrary
  loopback listener is a fixture; selecting an isolated fake listener is the
  fixture owner's responsibility.
- `CreateCheckout(context.Context, CreateRequest) (Checkout, error)` accepts an
  original `Purchase` plus its independently random create key. Caller owns
  durable once-only claim, entropy/uniqueness, eligibility and crash recovery;
  repeated method calls are **not** coalesced by this stateless adapter. A failed
  dispatched call or unsafe hosted URL preserves unknown creation and warns
  against create replay. Only the original reference enters `client_reference_id`.
  Success/cancel use the constructor's same fixed `/topup/return`, no selectors.
  Hosted URL is transient and accepted only by the selected mode's fixed policy.
  `CheckoutURLAllowed` exposes that policy; `CheckoutOrigin` returns its fixed
  browser origin (zero/nil client returns empty). Offline remains fixture-only.
- `RetrieveEvidence(context.Context, sessionID, Purchase) (Evidence, error)`
  requires original retained reference, denomination, exact issuance milliseconds
  and issuance-plus-one-hour expiry rounded DOWN to provider seconds. Three GETs maximum: Session/expanded line items, its PaymentIntent,
  its latest Charge/expanded refunds. Validates original create window (minus
  60 seconds through five minutes plus 60 seconds), exact expiry, USD integer
  amounts, one quantity/price, paid/completed Session, succeeded automatic-capture
  PI, succeeded paid/captured card Charge, object bindings and test/direct context.
  Nonnegative bounded refund amounts and all object/amount relationships are
  checked before projection. Only `CodeEvidence/StageCharge/ConstraintRefund`
  deliberately returns **both validated canonical Evidence and a closed error**:
  true Refunded/AmountRefunded/Disputed/RefundObserved fields authorize the core's
  sticky block, never funding. A listed refund includes pending, failed, canceled
  and unknown statuses. Missing/incomplete/malformed refund expansion instead
  returns zero Evidence with `ConstraintRefundUnavailable`; it does not establish
  an observed refund or authorize a block. Every other error returns zero Evidence.
  Returns Charge-created UTC date, not capture/PI/delivery time. No Charge ID,
  customer/card details or raw SDK object escapes. Caller must recheck eligibility
  and identity bindings atomically after the read and map validated fixed
  `payment`/test/`paid`/`succeeded` values into its accounting contract. This is
  neither a funding receipt nor atomic with a concurrent Stripe refund/dispute.
- `DecodeWebhook(raw, signature) (Notification, error)` caps raw bytes at 256 KiB,
  uses supported SDK verification/default five-minute tolerance and version
  enforcement, additionally requires the exact pinned API, rejects connected/live
  context, and projects two Checkout success event kinds to a Session ID and a
  validated reference LOOKUP SELECTOR only. A missing/invalid selector is rejected;
  it cannot recreate purchases. Canonical Session retrieval must independently
  match the retained original reference/terms before binding or funding.
  Notification snapshots never authorize funding. Unsupported/delayed kinds,
  including PI/refund/dispute notifications, return inert `irrelevant`; automation
  is not implemented here. No lookup is triggered by decoding.
- `*Error` carries closed code/stage/constraint/outcome/next-step, known HTTP status
  where available and explicit cause-unknown. No wrapped/raw cause survives. No
  ledger failure metric enum, logs, events, status worker or activity hooks added.

## Distinct local sandbox source boundary

Under `PRIVACY.md`'s **Subsequent local Stripe sandbox authorization** and
`docs/stripe-sandbox-local.md`, `NewSandbox(SandboxConfig)` adds a distinct opt-in
source API, not runtime activation. It fixes API traffic to
`https://api.stripe.com` and navigation separately to
`https://checkout.stripe.com`. ReturnOrigin must be exactly canonical
`https://127.0.0.1:<port>` (1–65535). No endpoint/transport overrides, environment
lookup or constructor network work. `IsSandbox` is constructor-owned mode
information, not remote identity or permission evidence.

Application keys require `sk_test_` or `rk_test_`; signing secrets require
`whsec_`. Each has a nonempty ASCII alphanumeric suffix and at most 256 bytes
total. Public underscore-separated offline placeholders, live keys, controls
and non-ASCII are refused. Shape cannot prove sandbox identity, actual scopes
or correct credentials. `NewOffline` still rejects actual test keys and external
origins, and still accepts only its original public placeholders.

Sandbox navigation requires a canonical `/c/pay/cs_test_...` path (existing
bounded Session ID syntax), no query/userinfo/port/escaped path/other origin.
A fragment of at most 2048 decoded printable ASCII bytes may be preserved exactly
in the transient URL, within the overall 4096-byte limit; decoded controls and
noncanonical serialization are refused. Offline fragment refusal is unchanged.
Neither branch fetches or persists navigation URLs. Evidence, signatures/default
tolerance/exact API pin and test/direct-context validation are unchanged.

Both constructors share the original explicit four-backend privacy settings and
bounded transport. Outgoing `Stripe-Account`/`Stripe-Context` headers (including
empty or differently cased fields) are refused before dispatch, in addition to
existing response context/version refusal. TLS uses Go's default hostname and
certificate checks, without custom trust or verification bypass.

New tests use authored synthetic shape fixtures only. Constructor introspection
checks all four backends; private test-only SDK replacement exercises local fake
responses, fragments, hostile errors, redirects, bounds and no replay. The TLS
negative test routes TCP only to an untrusted local peer, preserving the API
hostname/default TLS checks and verifying no HTTP request reaches it. No actual
provider/key/permission, CLI/listener, browser, 3DS or Dashboard acceptance is
claimed. Ordinary source runtime remains payment-disabled.

Scoped source/offline packet results: both package race suites passed (stripepay
2.046s, payments 16.697s); scoped vet passed; active LSP checks reported no
diagnostics in ten changed Go files. Existing offline suites ran unchanged.
Commands used module-network lookups disabled:

`devenv shell -- env OTEL_SDK_DISABLED=true GOPROXY=off GOSUMDB=off go test -race ./internal/stripepay ./internal/payments -count=1 -timeout=180s`

`devenv shell -- env OTEL_SDK_DISABLED=true GOPROXY=off GOSUMDB=off go vet ./internal/stripepay ./internal/payments`

## Dependency/source qualification

Go module proxy only: queried v86 `@latest` = `v86.4.2`, v87 `@latest` = `v87.0.0`;
no v88 versions were available. Downloaded exact `github.com/stripe/stripe-go/v87`
`v87.0.0` via `proxy.golang.org`, not Stripe/GitHub APIs. Its shipped README,
CHANGELOG, `api_version.go`, generated service/parameter types, `stripe.go`,
`stripe_client.go`, `log.go` and `webhook/client.go` were inspected. API pin:
`2026-09-30.endive`. SDK checksums are in `go.sum`.

Source findings:

- SDK default retries are two; global telemetry is enabled. Every API, Connect,
  Uploads and MeterEvents backend explicitly receives `EnableTelemetry=false`,
  `MaxNetworkRetries=0`, and its own supported `LeveledLogger{Level: LevelNull}`.
  No global logger or global Stripe configuration is mutated. OTel is independent.
- `Stripe-Notice` passes through the backend's warning logger, including detected
  agents (where `STRIPE_SUPPRESS_NOTICES` alone is insufficient). LevelNull emits
  neither warnings nor hostile SDK decode/error bodies. The SDK still constructs
  standard SDK/runtime/agent user-agent metadata; these tests claim suppression of
  request metrics/notices, not removal of all SDK headers or whole-host privacy.
- Endive replaces create's static `payment_method_types` with
  `allowed_payment_method_types`; this adapter filters to card only and rejects
  canonical actual methods/Charge types other than card. No saved-method/future
  usage or customer identifiers are supplied. No allow-redisplay override is
  sent: `never` is not a supported enum; the documented values are `always`,
  `limited`, and `unspecified`. The earlier synthetic wire fixture incorrectly
  accepted `never`; it now asserts that override and saving/customer inputs are
  absent. This does not claim Stripe erases payment methods or payment records.
  Hosted sandbox inspection subsequently found a preselected save-information
  option even with card-only filtering. The request now separately sends the
  documented `wallet_options[link][display]=never` to disable Link display;
  this `never` enum is valid for Link display, not for allow-redisplay.
  The exact SDK wire fixture asserts it. Actual updated hosted UI acceptance,
  other provider/dashboard defaults and tax behavior remain unqualified.
  Offline evidence is not permission for real sales.
- SDK buffers response bodies and can embed body samples in errors. A transport
  wrapper caps each body at 1 MiB before SDK parsing; raw exceptions are discarded
  into closed diagnostics. SDK transient raw objects/last responses remain a
  memory boundary, not an immediate secure-erasure guarantee.
- Zero SDK retries is separate from Go HTTP replay. A private supported
  `http.Transport` uses `DisableKeepAlives=true`, no HTTP/2/proxy inheritance,
  bounded headers and five-second request/deadline bounds. Redirects are refused
  through `CheckRedirect`. A complete evidence chain shares one five-second
  deadline. No custom retry or protocol parser was added.

## Offline evidence and remaining gates

Tests exercise exact wire field allowlist/all four denominations; three GETs and
zero POSTs for canonical reads; signatures, old/altered/oversized payloads, exact
API version/context; wrong amounts/currency/mode/relationships/quantity, partial
capture, partial refunds with `refunded=false`, every listed refund status and
dispute; hostile oversized responses and closed unknown errors; rejected origins
and inputs with zero dispatch; stdout/stderr suppression for detected-agent and
non-agent test/sandbox notice branches with suppression both on/off; three
consecutive requests without prior-request telemetry with OTel on/off; fresh
connections, warm-client disconnect, truncation, cancellation, 3xx, 409/429/500 and
zero automatic mutation replay. Fixtures store synthetic wire observations only
in test memory. No live provider, real credentials, inference or export.

Local results: race tests passed (95.7% statement coverage); vet passed; all
modules verified; active LSP checks found no diagnostics in all twelve Go files.
Scoped whitespace checks passed. No full-repository acceptance is claimed while
other packets are active.

`devenv shell -- env OTEL_SDK_DISABLED=true go test -race -count=1 -cover ./internal/stripepay`

`devenv shell -- env OTEL_SDK_DISABLED=true go vet ./internal/stripepay`

`devenv shell -- env OTEL_SDK_DISABLED=true go mod verify`

Parent owns ledger mapping, once-only attempt/funding state, request-body HTTP
ownership, cutoff/pruning and integrated HTTP/privacy tests. No live account,
provider availability/eligibility, hosted checkout, production identity/transport,
commercial/legal/tax, browser, deployment or financial integration is qualified.
