# Stripe adapter boundaries

Explicit offline, sandbox and live SDK adapters under `PRIVACY.md`'s **Local
synthetic Stripe top-up implementation**, **Subsequent live Stripe payment
direction** and **Forbidden collection and defaults**, and
`docs/stripe-live-activation.md`. The authoritative gateway `PRIVACY.md`'s **Data
handling boundaries**, **Telemetry: permitted signals and forbidden data** and
**Processors, retention, access and shutdown** also apply. Source/offline
qualification is not runtime activation or provider acceptance. The separate
payments coordinator owns integration; this package remains accounting-free.

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
  PI, succeeded paid/captured card Charge, object bindings and constructor-selected
  live/test direct context.
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
  enforcement, additionally requires the exact pinned API, rejects connected or wrong-mode
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

## Distinct live source boundary and exclusive tax

`NewLive(LiveConfig{ReturnOrigin, APIKey, WebhookSecret, TaxCode})` is inert and
has no environment lookup or configurable provider endpoint. It fixes API and
hosted navigation separately to `https://api.stripe.com` and
`https://checkout.stripe.com`, using the same four-backend privacy wiring as the
other modes. No constructor, accessor, URL check or webhook decode dispatches.
Only bounded `sk_live_`/`rk_live_` and `whsec_` shapes are admitted (ASCII
alphanumeric suffix, 256-byte maximum). Shape is not proof of remote mode,
permissions, endpoint-secret ownership or valid tax setup. The caller privately
supplies inputs; this package never reads credentials from files or environment.

ReturnOrigin must be a canonical lowercase HTTPS DNS origin with at least two
labels, alphabetic TLD, no port/userinfo/path/query/fragment/opaque form, and no IP
or recognized special-use local/test suffix. Validation performs no DNS lookup:
public routing, ownership and deployed TLS are operator qualification, not
constructor evidence. Live hosted URLs require the existing canonical bounded
`/c/pay/cs_live_...` path and the same finite printable-fragment policy as sandbox.
Sandbox still permits only `cs_test_...`; offline remains fixture-origin only.
Nil/zero/unselected/contradictory mode flags fail closed. `IsLive()` and
`ProductTaxCode()` expose trusted constructor-owned configuration, with an empty
code outside configured live mode. `IsSandbox()` is unchanged.

There is **no default product classification**: the operator must supply a
canonical `txcd_` plus eight digits. `Purchase.TaxCode` is the original immutable
trusted classification, never a browser input. Live create requires it to equal
current configuration before dispatch; retrieval instead checks the original
purchase code, so earlier pending purchases survive a later configuration change.
Test/offline purchases must have an empty code. No Product ID escapes projection
or is stored by this package.

Live create requests automatic tax with explicit self liability, minimum `auto`
billing-address collection, an inline exclusive Price, the operator Product tax
code, and name `Prepaid inference credit`. Card-only, Link display `never`, guest
`if_required`, invoices disabled, no future usage/save inputs, no Customer or
metadata inputs, no recovery and no replay stay intact. Test/sandbox wire fields
and their existing expansion remain unchanged. Live create/retrieve expand
`line_items.data.price.product` on their existing SDK calls (no fourth GET).
The pinned SDK's `Product.TaxCode` is `*stripe.TaxCode`; its supported unmarshaller
accepts either a tax-code ID or expanded TaxCode. No custom JSON decoder or
separate Product/TaxCode fetch is used.

Binding validates immutable principal/subtotal, one quantity/unit price, expanded
live Product/original code, exclusive behavior, automatic-tax enablement, context,
timing and prohibited features. An open/unpaid Session may have unresolved tax
and nullable provisional totals; these are **not a stable quote** and are not
projected. `Session.GrossCents` always means **credit principal/subtotal**, never
the total charged. Session also projects `LiveMode` and original `TaxCode`.

Final evidence additionally requires complete automatic tax, explicit `self`
liability with no Account, explicit `stripe` provider, and nonnil total details.
Unknown/absent/null provider or liability cannot prove those facts and is refused
for funding; nullable open-state fields may remain unresolved. This is a narrow
supported-flow admission rule, not a claim that Stripe never returns null.
SDK nonpointer integer fields follow the documented API semantics (for example,
line tax defaults to zero when no tax is applied); no second raw-body presence
parser is introduced. Checked integer arithmetic enforces principal + nonnegative
tax = line total = Session total, equal Session/line tax, and zero shipping and
discounts. PaymentIntent amount/received and Charge amount/captured must equal
that **final total**, retaining all prior binding/currency/time/refund checks.
Zero tax is accepted only with the same complete canonical context; no local rate
calculation or legal taxability conclusion follows. Managed-payments,
performance-location and incompatible tax contexts are refused rather than
inventing support.

Only after the complete Session → PI → Charge/refunds chain validates is optional
`Evidence.Tax` projected as `TaxEvidence{AmountCents, TotalCents, Complete,
Exclusive, LiabilitySelf}`. All ordinary errors return zero Evidence. The sole
existing typed refund/dispute error retains validated evidence (including tax)
for the owner's sticky block, **never funding or receipt clawback**. No ledger
mapping, cutoff/retention, runtime selection, website, deployment, metrics family,
background task or provider retry is added here.

### Live packet offline evidence and remaining gates

Public synthetic fixtures replace the private SDK with a bounded loopback SDK
**before any operation**. No exported bypass or live endpoint override exists.
Tests cover inert valid/invalid constructors and four-backend settings, strict
mode/URL refusals, exact live request allowlist versus unchanged V1 requests,
unresolved open tax binding, all four principals with zero/nonzero tax, original
code after configuration change, tax/amount/context mismatches and unknowns,
actual SDK signed-event roundtrips, three-GET bounds, one-POST/zero-retry failures,
validated refund partial outcomes, hostile bodies and no raw SDK output. Fixtures
contain deliberately private-looking synthetic customer/card fields that must not
survive projections or diagnostics. Host-native test history remains a separate
boundary; no whole-process immediate-erasure claim follows.

Actual Stripe expansion/provider/nullable-field responses, restricted-key scopes,
hosted guest/address/Link/3DS behavior, tax registration/classification/remittance
and active public notices remain provider/operator qualification gates. This
packet performs **no provider calls, key access, real charges or deployment**.
The already operator-accepted gateway integration is not reclassified as missing;
new live coordinator/runtime mapping and full-root functional qualification are
separate packets. No real-data activation follows from offline success.

Scoped live-packet results: package race suite passed (1.930s), scoped vet/build
passed, and full-root compile-only tests passed with no positional-caller gaps.
Nine changed Go files had no active LSP Go diagnostics. An auxiliary Semgrep
warning flags inspecting the SDK-returned Event on an error; pinned SDK inspection
confirms Event is a value, populated before its API-version error, so the existing
closed version classification is intentional (no nil dereference or raw error use).
No full-root functional suite or provider qualification is claimed.

`devenv --offline shell -- env OTEL_SDK_DISABLED=true GOPROXY=off GOSUMDB=off PYTHONDONTWRITEBYTECODE=1 go test -race -p 1 ./internal/stripepay -count=1`

`devenv --offline shell -- env OTEL_SDK_DISABLED=true GOPROXY=off GOSUMDB=off PYTHONDONTWRITEBYTECODE=1 go vet ./internal/stripepay`

`devenv --offline shell -- env OTEL_SDK_DISABLED=true GOPROXY=off GOSUMDB=off PYTHONDONTWRITEBYTECODE=1 go build ./internal/stripepay`

`devenv --offline shell -- env OTEL_SDK_DISABLED=true GOPROXY=off GOSUMDB=off PYTHONDONTWRITEBYTECODE=1 go test -p 1 ./... -run '^$' -count=1`

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
