# Gateway → intake integration handoff

## Authority and delivery boundary

The operator now requests a separate gateway implementation thread/worktree, with
**Astra reviewing this plan and relevant code in both repositories before code
changes**. Implement the gateway's use of this intake for customer authentication
and durable credit accounting. **Intake-attestation verification is explicitly
deferred for this packet.** Preserve ordinary certificate/hostname-verified HTTPS,
existing client→gateway and gateway→inference verification, release provenance,
catalog/quote validation and authenticated completion/tool gates. Do not turn this
exception into an upstream verifier bypass or an automatic insecure fallback.

This is source/local synthetic implementation authority, not live cutover,
credential access/import, real-account/funds use, inference, telemetry export,
client installation, publication, signing or deployment. Prepare reviewable source
and evidence; obtain separate approval for those operational actions. The prior
website setup/serving allowances are consumed. No disk initialization/reset,
credential seeding/rotation, resource creation or replay follows from this work.

The input-face patch is separately merged into intake main at
`853977b9ce996176f0f71cecd84d3a0d3cfa8bb0`; **it has not been deployed**.
The live synthetic intake remains immutable serving release **v0.1.16**, built
from intake source `5fea2c37abbe4a3dd66f30eac37bfc3dbd4c9017`.
Public-page/health checks passed, not authenticated financial/operator acceptance,
real-data readiness, fresh runtime attestation or capacity/durability qualification.

## Read first; review from the correct source

- Intake root: `/Users/mb5/projects/possums-intake`. Read `AGENTS.md`, `README.md`,
  full `PRIVACY.md`, committed `PLAN.md`, and `docs/customer-auth-local-contract.md`,
  `docs/customer-auth-local-evidence.md`, `docs/customer-site-browser.md`,
  `docs/website-image.md`, `docs/permanent-storage.md`, `internal/httpapi/README.md`.
  Read the historical gateway plan at `7ee4604:OVERALL_PLAN.md` as history only.
- Gateway root: `/Users/mb5/projects/possums`. The original checkout is clean on
  `feat/tor-v0`, but **is not current main**. Its local main is also stale.
  New-worktree base: `9bdd33e642db805c4baa924509d275fcd13ec4b3`, verified against
  remote main during handoff preparation. Read the worktree's own current
  `AGENTS.md`, `SPEC.md`, `OVERALL_PLAN.md`, full `PRIVACY.md`, client compatibility,
  accounting, streaming and verification records. Preserve newer main's Tor,
  renewal, diagnostics, resource/backpressure and release changes.
- Relevant intake code: `internal/httpapi/{handler,decode,bind}.go`,
  `internal/ledger/{types,operations,transaction,storage}.go`,
  `internal/siteauth/{customer_key,customer,auth}.go`,
  `internal/operator/{handler,customer_key_failure}.go`, and `cmd/intake/website.go`.
  The committed HTTP/auth/ledger directories and native handler at the input-face
  commit match the deployed source above; that is source comparison, not live API
  acceptance. Follow actual tests and pinned PocketBase v0.40.5 behavior.
- Relevant gateway code: `src/{auth,accounting,api,api_stream,stream_owner,server,main}.rs`,
  generation/inference ownership, catalog/pricing and failure modules; follow their
  actual call sites and tests. Existing auth/admission/accounting are synchronous
  and mutex-coupled. Remote I/O cannot be inserted while holding those locks.

The intake checkout contains unrelated modified `PLAN.md` and untracked operator
work. **Read the committed plan for the baseline; leave all dirty work untouched.**
Review intake read-only in this thread; propose any required intake/API/privacy
amendment explicitly rather than silently editing a sibling checkout. Write
implementation only in the new gateway worktree. Do not infer runtime authority
from older deployment paragraphs in either repository.

## Authentication: verify the supported native route, not a made-up API

The runtime accounting key, customer key and native superuser credentials are
three different authorities. `LEDGER_ADMIN_KEY` authenticates custom accounting
only; it is not a PocketBase superuser token or customer login. Do not provision
superuser credentials into the gateway, use a platform admin key, expose the
runtime key to clients, or add accounting roles/permission tiers.

Current intake has a dedicated SDK auth collection `customer_keys` (ID
`customerkeys001`) and website-private `CustomerKeyAdapter`. There is **no custom
shared-admin-key customer-validation endpoint** in `internal/httpapi`. Native
PocketBase routes are exposed by `internal/operator/handler.go`. Candidate existing
wire surface, to qualify with the actual service before adopting:

- `POST /api/collections/customer_keys/auth-with-password`, JSON `identity` and
  `password`, using the two derivations below, not the accounting Bearer key.
- Stock `POST /api/collections/customer_keys/auth-refresh` using the SDK customer
  auth token as PocketBase expects. This issues a token; it is **not** the private
  Go adapter's non-refreshing `Validate`. Check SDK behavior, AuthRule, response
  fields, MFA and original-expiry handling before choosing revalidation.

Accepted raw customer key: exact ASCII strict unpadded base64url, ≤128 encoded
bytes and ≥32 decoded bytes; no trimming, padding or normalization. Derive from
exact submitted key text, with separate SHA-256 domains:

- Identity: `v1_` plus lowercase hex SHA-256 of
  `possums-intake/customer-key/lookup/v1` + one NUL byte + key text.
- SDK password: unpadded base64url SHA-256 of
  `possums-intake/customer-key/password/v1` + one NUL byte + key text.

The SDK password is a secret. Do not persist it or the raw key; never include either
in accounting bodies, URLs, diagnostics, model prompts or artifacts. Use the
existing compatibility vectors. A digest/record ID is **not** the ledger account
ID: resolve exact current `account_id` from authoritative SDK-authenticated data.
That binding is a required ordinary field; lookup/verifier/tokenKey remain hidden.
No browser-supplied account selector, credential hash fallback, static gateway
allowlist or account auto-provisioning on login. Native auth/collection rules,
password validation and MFA/OTP stay SDK-owned; unsupported customer MFA fails
closed, not disabled.

Astra must explicitly resolve session revalidation/rebinding, revocation/password
changes, deleted records and AuthRule changes. Compare the captured login binding
with the freshly authenticated binding; reassignment must require fresh login,
not silently switch an old session. Keep tokens memory-only and bounded by original
SDK expiry and a documented gateway session policy; do not silently renew forever.
Preserve existing API challenges, admission binding, session/submission isolation
and shipped-client behavior. Existing 12-hour gateway sessions are not evidence
that a one-hour customer SDK token remains valid.

**Privacy/design gate:** the old customer policy is intake-website-only and the
old gateway policy describes local demo auth. This new source request supersedes
the integration deferral, not all real-data/privacy limits. Record the exact
credential/token transit and retention proposal in the gateway policy/plan before
implementation. If the existing native surface cannot preserve supported current
binding/SDK rules and closed diagnostics, stop with the smallest intake contract
amendment needed; do not invent an endpoint, scrape `/login`/`/home`, widen collection
rules or pretend the browser session is an API. Cross-repository amendments and
real-data activation need explicit review/approval.

## Existing shared-key accounting contract

All routes below are **POST**, JSON, `Content-Type: application/json`,
`Authorization: Bearer <runtime accounting key>`. No query parameters, redirects,
compressed requests or duplicate headers. Body limit is 4096 bytes. Send only the
named fields, never prompts, tools, histories, customer credentials, models/token
metadata or content hashes. Source of truth: `internal/httpapi/README.md` and tests.

| Path | JSON request | Successful wrapper |
| --- | --- | --- |
| `/balance` | `account_id` | `account_balance`, `pending_reservations`, `available` |
| `/reservations` | `account_id`, `maximum`, `timeout_seconds` | `reservation` |
| `/reservations/settle` | `reservation_id`, `charged_amount` | `reservation` |
| `/reservations/refund` | `reservation_id` | `reservation` |
| `/reservations/lookup` | `reservation_id` | `reservation` |
| `/accounts/provision` | `account_id` | `account_balance` |
| `/credits` | `account_id`, `amount` | `credit` |

Reserve and credit require a single `Idempotency-Key` header. Provision/credits
are operator/manual-funding capabilities, **not** a new public gateway top-up API
or login/startup side effect. Production remote mode must not seed/reset demo
balances or fall back to local spendable credit on network/auth failure. Payments
remain deferred; synthetic fixture grants are isolated test setup only.

Money is integer **microunits** as canonical decimal strings, uint64 range
`0`–`18446744073709551615`, never JSON numbers/floats. Reserve/credit must be
positive; settle may be `"0"`. Use checked arithmetic and existing authenticated
quote/markup/cached-input pricing; intake trusts the gateway's amount and performs
no catalog/usage validation. Opaque account IDs preserve 1–128 UTF-8 bytes exactly.

`account_balance` is a list of at most one `{account_id,balance,enabled}` row, not
an object. `/balance` additionally returns complete `pending_reservations` (always
an array) and canonical `available`. Derive identity from the current authenticated
session; validate every returned row belongs to that account. Keep the snapshot
atomic and complete, including disabled accounts' reads; never assemble totals
from separate requests or silently truncate rows. A response bound overflow is
typed unavailable, not an empty/partial success. Do not fabricate ledger-global
completed-request counts from this snapshot: the wire supplies no such count.

Every reservation contains `reservation_id`, `idempotency_key`, `account_id`,
`maximum`, integer `timeout_seconds`, `expires_at`, `state`, nullable
`charged_amount`, `terminal_at`, `retain_until`. States: `active`, `settled`,
`refunded`, `expired`. Times use fixed UTC millisecond text. Credit rows contain
`idempotency_key`, `account_id`, `amount`, `balance_after`, `committed_at`,
`retain_until`. Verify the complete receipt and its operation/account binding;
HTTP success alone does not establish that the requested terminal action won.

## Admission, expiry and once-only completion

1. Keep authenticated catalog/quote/context checks and model/conversation/submission
   binding. Preserve existing accepted-work ownership and client compatibility.
   Stage asynchronous admission without holding auth/accounting mutexes across I/O;
   test logout/reset/model/session races during remote reserve and late responses.
2. Before any prompt is sent upstream, confirm a matching **active** reserve receipt
   for the authenticated account and exact maximum/timeout/key. Never launch a
   second generation for a duplicate submission, even if intake returns the same
   active row. A late reserve after cancellation needs owned reconciliation, not
   detached/untracked work or invented refund success.
3. Reserve keys match `v1.<13-digit epoch milliseconds>.<32 lowercase hex digits>`;
   generate independent cryptographic randomness, never derive from content/key.
   New keys are valid for 24h with ≤60s future skew. Existing key reuse returns the
   first row only for matching account, maximum and timeout; changed parameters
   conflict. Preserve one exact key/payload for any expressly permitted recovery;
   never mint a new key to escape uncertainty or replay after retention expiry.
4. Immutable timeout is integer **1–1800 seconds**. Expiry releases a hold and rejects
   late settlement; waiting does not extend it. No renewal/heartbeat/fencing or
   indefinite manual recovery hold. Upstream costs after expiry remain an accepted
   operator risk. Document chosen hold/RPC/drain budgets and slow/backpressured work.
5. Promptly close upstream on observed client disconnect, not mere backpressure.
   Settle the checked charge from trustworthy final or cumulative interrupted usage
   at the submitted quote, capped by the original hold. Never sum cumulative
   snapshots or estimate from text. Refund fully without trustworthy billable usage;
   failures/model rejection before responding are not billable. A partial charge
   does not establish successful completion or authorize tools.
6. Settle/refund by reservation ID: first terminal transition wins atomically;
   conflicting/repeated terminal calls can return **200 with a different existing
   state/charge**. Preserve that observed result rather than reporting the requested
   result. Late settle returns **409 `LEDGER_SETTLEMENT_EXPIRED` plus the authoritative
   expired row**. Lookup/balance may expire holds; they are not passive SQL reads.
7. Retention is bounded, not permanent recovery evidence: terminal/credit rows last
   until at least both issuance+24h and terminal/commit+24h. Lost/pruned reservation
   evidence is **410**, not proof of refund/nonexecution. An uncertain reserve whose
   ID was never received cannot be resolved by ID lookup alone. Review that case
   explicitly; no inference or blind/new-key replay while its result is unknown.
8. Preserve sole terminal ownership through disconnect, cancellation, panic, drain
   and shutdown. Remote outage/malformed response after dispatch is an unknown
   monetary outcome until authoritative evidence, never a local fake settlement.
   Keep in-flight lifecycle state memory-only and acknowledge process-loss limits.
   Do not restart/replace gateway concurrently with ledger replacement; pending
   settlement is not made durable by this adapter.

## Transport, failures and privacy

Use one supported bounded HTTP client and typed serde/domain types, not a generic
SDK/diagnostic framework. Runtime configuration needs the exact intake HTTPS origin
and shared accounting secret; choose/document gateway variable names in the review.
No hardcoded live origin, secret copy into an example, credentials in URLs, proxy
credential forwarding, cross-origin redirects or implicit retry middleware. Local
loopback test transport must be explicit and unavailable as production fallback.
No secret need be read to implement this packet; obtain any later deployment
credential privately through the supported secret-entry path, never chat/tools.

Pin finite connect/operation/response/drain budgets and response-size behavior.
Ledger currently uses 2s writer wait, 1s transaction work budget, 250ms SQLite busy
wait and 5s server read/write timeouts. Do not widen these or introduce an automatic
retry/recovery policy by accident. Bounded replacement waiting is a design target,
not guaranteed recovery, safe replay or permission to extend a hold. If new retry
behavior is required, present it as an explicit accounting-policy decision first.

Read the exact closed error map in `internal/httpapi/README.md`: HTTP shape400,
shared-key401, unknown account404, conflicts/insufficient/disabled/expiry409,
evidence410, storage/work/unknown503. Preserve locally authored typed code, stage,
constraint, actionable next step and evidence-supported outcome. Unexpected transport,
SDK decoding/schema/status/receipt/cleanup/presentation failures need stage-specific
cause-unknown fallbacks. Do not trust/echo arbitrary remote `message`, raw body,
exception or `next_step`; use closed locally authored projections. Treat HTTP401
accounting-key failure as service configuration/auth failure, not bad customer login.

Classification stays mandatory with telemetry disabled. Preserve known receipts
through later delivery/cleanup failure and once-only failure ownership. No new
logging/tracing/metrics family, raw exceptions, URLs, identifiers, money, credentials,
content or provider metadata in diagnostics/support/observability. Test actual
serialized API/SSE/stdio paths with hostile synthetic sentinels. Applicable policies:
[intake boundaries](../PRIVACY.md#accounting-and-processor-boundaries),
[intake customer exception](../PRIVACY.md#customer-key-authentication-and-scoped-website-account-reads),
[intake failure ownership](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates),
and gateway `PRIVACY.md` data boundaries, mandatory typed failures and forbidden data.

## Acceptance and implementation order

1. **Astra first:** read the actual relevant plan/code/tests in both repositories;
   write a concise contract/gap/risk review and smallest implementation plan in the
   gateway worktree. Distinguish verified source facts, proposals and unresolved
   policy/API questions. Resolve consequential auth/admission/terminal design before
   writing the adapter; stop for material policy/API decisions rather than guess.
2. Implement gateway-only in small logical source/test packets. Keep Sol6.1 on
   critical accounting/security implementation if delegation is useful; do not
   delegate those judgments to routine dogfood packets. Do not duplicate active
   work. Do not merge/push/roll out the integration without separate authorization.
3. Test with the **actual Go/PocketBase HTTP service in disposable synthetic state**,
   not just mocked responses. Qualify native customer auth, current binding/rule/
   password/token invalidation, two-account isolation, canonical money boundaries,
   complete snapshots, duplicate/conflicting keys, timeout immutability, concurrent
   gateways/overspend, expiry/late settle and once-only terminal races.
4. Add fault tests for lost reserve/terminal replies, malformed/hostile/oversized
   responses, redirect/TLS/auth failures, offline intake, cancellation during reserve,
   accepted-work handoff, disconnect/backpressure, restart/drain/process-loss limits,
   and bounded evidence loss. No prompt before confirmed reserve; no inference replay,
   fallback balances, invented financial receipts or raw diagnostic leakage.
5. Exercise current and previously shipped client decoders against synthetic
   authentication/discovery/stream/partial/final/error receipts. Preserve default
   wire semantics; any unavoidable breaking contract requires operator approval.
6. Use each repository's pinned environment, telemetry off and no live inference.
   Run Rust and relevant Go race/integration checks, lint/type checks and privacy
   fixtures. Record exact local source/test evidence and remaining API/image/runtime
   gaps. Existing generated-image tests do not qualify a newly implemented gateway.
7. Finish with a reviewed change summary and separately gated activation plan:
   synthetic cross-service acceptance first, exact release/build/provenance/approval
   next, then any explicitly authorized runtime credentials/cutover. Intake attestation
   remains deferred, openly disclosed; do not claim this temporary TLS/shared-key
   channel authenticates the enclave measurement or qualifies real-data operation.
