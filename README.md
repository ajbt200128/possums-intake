# Possums intake

**Current status:** The [merged-account release record](docs/merged-account-deployment.md) records the operator-reported v0.1.21/v0.1.22 rollout. [Live Stripe Tax preparation](docs/stripe-live-activation.md) records the locally qualified, explicitly opt-in payment source and remaining provider, public-terms, publication and activation gates. The operator reports gateway integration already tested; no recheck or gateway change follows. Historical baseline/deferred-scope passages below do not override those newer scoped decisions. Ordinary serving remains payment-disabled unless explicitly configured and admitted.

Design and local implementation repository for the external Possums credit service. Current scope is the **ledger MVP only**: durable internal prepaid credits, not cryptographic e-cash. Payment intake is deferred. The original standalone synthetic baseline passed source/race/curl/restart/privacy and actual generated-image gates, as recorded in [docs/verification.md](docs/verification.md). The restricted native dashboard/account-collection packet also passed parent source/browser and fresh image gates; it uses the genuine upstream editor, not a custom account form. Legacy image/deployment templates remain hard-disabled; the newer intake signer is a guarded manual path described below. Local acceptance is not permission for real data, integration or deployment.

Selected stack: **Go + PocketBase/SQLite in a separate Tinfoil container**, with one encrypted persistent volume and one ledger instance. The operator accepts no backups and manual migration/admin work for the MVP. Multiple gateways can share this ledger; PocketBase does not provide active-active ledger instances. This same node will eventually host Stripe and Monero payment intake; those integrations remain deferred.

The ledger must never receive prompts, responses, conversation history or tool payloads. It necessarily observes account-linked accounting operations. Its separate attestation and verified gateway transport remain targets requiring qualification—not guarantees inherited from the gateway. For the permanent-storage synthetic packet, the operator explicitly selected a Tinfoil-managed disk-key secret and automatic startup unlock, accepting platform access to the key and disk plaintext; retain an independent original-key copy in the operator's password manager. Tinfoil encrypted volumes are beta, lack disk-rollback protection, and do not hide network/access patterns.

Resource target: Tinfoil's published minimum **2 CPUs / 8 GB RAM** (`cpus: 2`, `memory: 8192`), CPU-only. A 1-CPU / 2–4-GB enclave is not currently a supported public shape. For the synthetic persistent packet, use the smallest supported volume; the operator waived quote-first approval. Supported capacity/host availability still require evidence, not a guessed size or price.

Telemetry target: supported Go OpenTelemetry metrics → bounded local aggregation/allowlist → direct Honeycomb US. Complete five-minute accounting HTTP aggregates and one-minute process CPU/RSS means are locally implemented. The operator additionally approved mandatory typed interface-error classification and complete five-minute closed-category failure counts, including native/operator/background failures. The fixed 39-category enum, error gauge and window/sanitizer foundation are implemented and passed parent source and synthetic wire qualification, but the production error family stays gated off and complete boundary/owner coverage remains unqualified; no complete error metric coverage or export is claimed. No logs/traces, account/payment identifiers or monetary values. See `PRIVACY.md`; `OTEL_SDK_DISABLED=true` disables export. Sparse aggregates remain a correlation risk, not anonymity. Place explicit middleware on custom accounting routes after final outcome classification and use separate lifecycle/process sampling hooks; no generic CRUD/admin hooks or auto-instrumentation. The approved error-only native/background family does not authorize activity, success or financial-mutation counters; see [interface failure policy](PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates).

Delivery direction: **two public source repositories**, not a monorepo. `ajbt200128/possums` keeps gateway/clients; this intake repository will own its measured config and signing, eliminating the future private-source/public-config handoff. Preserve exact-main qualification, independently matching builds, signed provenance, protected approval and no mutation replay; intake replacement/downtime preserves its disk/key, not gateway blue-green. The separate `possums-intake-deploy` now has historical signed releases and deployment records; retain that evidence and retire future publication only after a qualified transition. The intake signer requires both exact-true repository switches, protected operator approval and authenticated evidence; production deployment remains hard-disabled. Remote activation and final release acceptance remain unqualified, and legacy publisher admission retains its private-only defaults. The updated [staged plan and offline public-release contract](docs/two-public-repositories.md) record the operator-authorized full gated cutover, preserving the newer live Stripe serving profile, current disk/key/state and gateway contract. No new license is selected. The latest selection is a **new stopped instance, moving the original disk/domain later**, not an in-place retarget or fresh ledger. Source/history/Actions-output review, exact-source native qualification/signing and supported create-only/secret-readiness checks remain gates. The CLI auto-deploys volume-declaring configurations, so it is not a stopped-create path. No current-instance/disk/domain mutation or future transfer/boot follows now; the cutover is not yet executed. Historical failed boots and consumed setup/replacement allowances are not current readiness or retry authority.

Latest persistent-storage packet: supported managed-secret startup and explicit blank-disk initialization are implemented and passed scoped local source/image checks. The operator authorized small logical commits, pushes and the protected build/signing pipeline toward synthetic persistent deployment. Native Linux release qualification, signed artifacts and actual Tinfoil unlock/replacement remain gates; no live disk or service was changed by these local checks. See [permanent storage](docs/permanent-storage.md) for current authority, exact evidence and remaining limits; this supersedes older no-volume/customer-only-key choices for this packet, not real-data or gateway restrictions.

Attestation target: reuse the platform's public evidence exposure with minimal app glue only if needed. New client/gateway verification work is deferred; exposure is not verification, and encrypted-volume key unlock still needs its own qualified mechanism.

Local-first acceptance: implement and confirm the standalone intake service with local curl smoke tests, synthetic concurrency/fault tests, restart/migrations, image readiness and privacy checks **before any gateway changes**. Gateway adapters/admission waits/client verification and live integration are later, separately authorized work.

Implementation orchestration mixes Possums DeepSeek v4.1 Flash for bounded routine packets with Sol 6.1 for accounting/privacy-critical work and verification. Manually record observed DeepSeek failures with safe diagnostic/reproduction evidence only in the external, local-only `/Users/mb5/projects/possums-intake-agent-failures.md`. Only that file may hold development task/thread/run IDs and verified native transcript pointers; never copy transcripts, prompts, responses, raw exceptions, secrets or customer data. `docs/agent-failures.md` is guidance only. Logging initiates no inference, retry, telemetry export or gateway changes; production privacy remains unchanged. The design-review packet itself launches no implementation agents.

`PLAN.md` pins the local execution contract (PocketBase v0.40.5, exact money/API schema, immutable timeouts, writer/RPC budgets, replay-safe bounded keys, storage and startup policy). `PRIVACY.md` governs accounting/telemetry boundaries. The operator has separately authorized commits/pushes to `ajbt200128/possums-intake` and preparation of a new **disposable synthetic-only Tinfoil test enclave**, including native operator dashboard login and direct native fake-account creation/editing. Local account-storage/accounting/browser/image qualification passed. An explicit disposable synthetic-serve startup/listener now exists and passed local compiled-CLI/source checks, but no image, release or deployed qualification covers it; see [disposable synthetic startup](docs/synthetic-startup.md) and [synthetic preparation](docs/synthetic-tinfoil.md). The operator authorized one CPU-only 2 CPU / 8192 MB no-volume disposable test instance at actual cost without another price-confirmation gate; no exact rate or spending ceiling is claimed and additional instances remain unauthorized. Do not activate production release/deployment templates by inference. No gateway cutover, real funds, real telemetry export or payments are authorized. Real-data/operator trust and access decisions remain separate gates.

## Local synthetic payment work

The operator authorized local synthetic-only fixed-denomination Stripe top-ups after the [reviewed plan](docs/stripe-topup-plan.md). The optional ledger schema, offline-only supported SDK adapter, signed webhook coordinator and authenticated host-owned top-up surfaces are implemented and source-tested. Final full race/browser/vet/module checks, local app/Linux-image builds and the existing isolated generated-image runtime/privacy qualifier pass. No real Stripe credentials or processor calls are needed; ordinary serving remains payment-disabled. Explicit `synthetic-payments-init --state ...` adds only optional feature schema to an existing stopped local synthetic ledger. Refund/chargeback automation and ledger clawbacks are out of scope; losses are accepted. See [scope, evidence and remaining gates](docs/stripe-topup-local.md). No real payments, gateway integration or deployment follows.

## Renewable-hold predecessor and retained behavior

**Historical rollout:** the v1-to-v2 upgrade described here belongs to the accepted predecessor release. The current merged-account schema v3 retains renewals and the twelve-hour native token default but has no upgrade/migration command; use the [current release record](docs/merged-account-deployment.md). Existing incompatible state still refuses, and no reset authority is inferred.

The approved [renewable-hold contract](docs/gateway-hold-renewal.md) adds conditional
lease extension and a narrow explicit stopped-service `ledger-upgrade --state`
path from schema v1 to v2, preserving existing test state. Serve never upgrades or
resets state; prior binaries refuse v2. [Customer native auth](docs/gateway-session-lifetime.md)
uses 12-hour tokens for fresh setup, with an explicit supported duration-only
native update for existing collections; website sessions remain 1h absolute/10m idle.

The operator selected **uninterrupted accepted streams**, accepting uncollected
usage if intake is unavailable or the hold expires. One unconfirmed renewal stops
further renewals, not accepted work; terminal billing is attempted once at normal
completion/genuine disconnect. This supersedes the earlier accounting cutoff,
not new-admission reservation/auth checks. Gateway consumer work and all runtime
configuration/migration/deployment gates remain separate. See the contract's
qualification section for source evidence; policy approval is not live acceptance.

The operator subsequently approved the guarded intake-only rollout, including one
stopped existing-disk schema upgrade and the native existing-collection duration
update. [Deployment decisions and pending gates](docs/gateway-renewal-deployment.md)
track the minimal managed-unlock prerequisite and exact-source release work. No
activation is claimed; gateway/client rollout remains separate.

## Local customer website

**Latest local schema:** [one merged Accounts auth collection](docs/merged-account-schema.md) now holds SDK authentication, exact balance and enabled state. The generated record ID is the sole account ID; the older separate credential/account creation recipe is superseded. Fresh synthetic initialization only—no migration tooling or implicit reset. Existing old layouts refuse before SDK bootstrap. This source change is not activated in the currently running sandbox and has no new image/deployed/gateway qualification.

The three-page ELPS website is now wired into ordinary `serve` and disposable
`synthetic-serve` by default in local source. Explicit `init` adds the initial
website/auth schema; `website-init --state ...` adds compatible website setup to
an existing stopped synthetic ledger without resetting balances, credentials or
valid published code. Optional `--tinfoil-volume ledger` unlocks existing managed
storage before that additive setup; it never initializes the disk or ledger.
Serve/login never initialize or seed state. Stock native
key provisioning, scoped account snapshots and live publication passed the
[combined browser qualification](docs/customer-site-browser.md).

The ordinary runtime assumes Tinfoil's HTTPS ingress and uses stock memory-only,
process-lifetime session cleanup; secure cookies and HTTPS same-origin CSRF remain.
See [runtime behavior and limits](docs/website-runtime.md). Combined local
[image/vendor/runtime qualification](docs/website-image.md) passed; exact-main
native release and deployment gates remain separate from real-data acceptance.

The separate `website-local` loopback HTTPS demo remains disposable, uses a public
fake key and never opens existing state. Its [walkthrough](docs/lisp-website-local.md)
and [evidence](docs/lisp-website-evidence.md) remain separate from ordinary customer
provisioning. Complete accessibility/Tor checks remain open.

## Existing decisions

Read the sibling gateway's `../possums/AGENTS.md`, `../possums/SPEC.md`, `../possums/OVERALL_PLAN.md` and `../possums/PRIVACY.md`. The original store design is preserved in gateway Git history at `7ee4604:OVERALL_PLAN.md`; historical browser, buffering, weak account-number, post-completion debit and privacy-guarantee assumptions are superseded by current policy.

- Separate the ledger workload from the measured inference gateway. Defer new consumer verifier implementation in this packet; the eventual real-data integration's own identity/endpoint verification gate is not satisfied merely by exposing evidence.
- All ledger accounting endpoints use one runtime admin key shared with the gateway; no RBAC. Users never receive that key. Their authenticated gateway balance view returns only their account row and pending reservation rows, with every defined field including expiration and idempotency key.
- Preserve high-entropy recovery credentials; no 16-digit account numbers, public signup or payment processing in this MVP.
- Existing commercial policy is a $5 minimum and 30% markup over upstream inference cost; payment intake remains future work.
- Reserve the authenticated catalog's maximum quote before upstream prompt transmission; settle authenticated actual usage at that quote and refund the remainder exactly once.
- Planned gateway integration follows the approved [accounting update](t3-thread://v1/0da9419e-574d-47bd-a8c9-21b679f04704): promptly close upstream on observed client disconnect, charge trustworthy final or partial usage, and refund when no trustworthy usage exists. The ledger only receives the monetary settlement amount. This gateway change was not fully implemented or deployed at thread review; do not confuse the target with current runtime behavior.
- Durable balances must not reset on restart or silently initialize after disk/key loss. PocketBase write transactions serialize reservations, settlement and expiry; wait within a bounded writer budget, then require available credit to cover the requested maximum. Use exact decimal text storage and checked Go uint64 arithmetic, not float numeric fields.
- Reservations require immutable timeouts and reserve idempotency keys. Settlement/refund use the reservation ID; short-lived terminal results confirm lost-response retries. Expiry releases credit and prevents late settlement; the operator accepts possible upstream cost after expiry.
- Retain only necessary prompt-free accounting/idempotency state with expired-key replay protection. Owner/fencing machinery and financial restore reconciliation are deferred. No scheduled backups; optional manually downloaded PocketBase snapshots introduce a sensitive local persistence boundary. WAL/deleted pages remain a privacy boundary, not a secure-erasure claim.
- Latest operator policy trusts native PocketBase admins fully: expose stock financial/identity/schema edits, deletion, settings, backups/restore and file-token downloads under SDK authentication/rules. This supersedes older restricted native UI/input and backup-token bans for intake manual administration only. Stock writes can bypass Service idempotency, held-balance and concurrent-write safeguards, corrupt accounting or create financial rollback; they are not funding/available-balance receipts or a safe restore guarantee. Custom shared-key accounting is unchanged. SDK logging and AuthAlert activity recording stay forced off; browser auth/preferences/drafts/token cache stay page-memory only. No gateway privacy change, real-data/production/integration/persistence/new-resource, inference/export or deployment authorization follows. [Current source evidence and UI-build blocker](docs/operator-dashboard.md#trusted-native-admin-policy-reversal--current-local-packet).
- Gateway updates may remain blue-green against this shared ledger after integration. The persistent-volume ledger itself uses replacement with downtime, preserving the original disk/key; brief funding unavailability and delayed new inference admissions are accepted. Gateways may wait within bounded budgets for reservation/settlement confirmation; no inference before confirmed reservation, no inferred billing success and no automatic inference replay. One–two minutes is a recovery target, not a guarantee; hold expiry still applies. Avoid simultaneous gateway replacement. Code rollback is not database restore. See `PLAN.md` for research evidence and unqualified deployment requirements.
- Tor, Monero, Stripe, new clients, blind signatures/e-cash, replicas and an external audit are not this phase.
