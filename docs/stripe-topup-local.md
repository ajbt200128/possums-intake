# Stripe top-ups — local synthetic packet

## Authority and scope

The operator authorized local synthetic-only implementation after the [reviewed plan](stripe-topup-plan.md), deferred tax discussion and selected absorbing refund/chargeback losses rather than refund automation or ledger clawback. The business location is San Francisco, California, US. No live processor access, real payment, gateway integration, deployment, publication, new standing resources or telemetry export is authorized. No Stripe credentials are needed for this packet.

See [the scoped intake privacy amendment](../PRIVACY.md#local-synthetic-stripe-top-up-implementation). Accounting state is not telemetry. Tests use fresh local fake accounts and public fixture credentials. Browser/host history and SQLite/WAL/manual snapshots remain separate persistence boundaries.

## Implementation contract

- Four choices: $5/$10/$25/$50 USD; one cent adds 10,000 ledger microunits. Funding does not subtract the inference markup again. These are synthetic units, not proof that existing real balances use USD.
- Explicit `InitializeSyntheticPayments` adds a version singleton, temporary purchases and independent sales. All use `WITHOUT ROWID`. There is no retained provider tombstone table: unique live purchase bindings, canonical Session relationships/create windows and rejection of missing/expired references provide replay protection.
- Ordinary ledger initialization, open and serve do not initialize payment schema. Optional schema must be entirely absent or exactly supported. Case-variant object names cannot disguise partial state.
- Each original form may claim one provider creation dispatch. Unknown completion never permits another dispatch. No queue, automatic create replay, subscriptions, saved-card feature or automatic reload.
- Temporary account/provider mappings become ineligible exactly 20 days after reference issuance. Maintenance logically deletes whole expired rows in bounded batches. Startup payment admission must drain overdue purge batches. Deletion is not secure erasure or a hard wall-clock deadline during downtime.
- Sales retain independent random identity, account, UTC successful Charge creation date, USD gross amount and credited units for 90 UTC days **in synthetic fixtures only**. This is not a real-data legal/business retention policy. Date/amount/account correlation remains possible.
- Funding uses `Service.run`, its transaction-scoped PocketBase app and checked uint64 decimal-text money. The balance write, sale and original funding state commit together. No remote call occurs under the writer gate. Payments never create generic `ledger_credits` markers.
- Before-funding canonical refund/dispute evidence prevents funding; no automatic unblocking or clawback follows. A funded receipt establishes original credit addition, not current available balance. Operator loss acceptance is not waiver of applicable customer rights.

## Offline provider boundary

`internal/stripepay` pins Stripe Go v87.0.0 / API `2026-09-30.endive`. Its only constructor accepts literal loopback fixture origins and public placeholder API/webhook secrets. It has no production/default backend or environment credential lookup. Do not enter real Stripe secrets, including actual test-mode secrets.

All backends explicitly disable SDK telemetry, retries and logging. The HTTP transport rejects redirects, proxies and other origins, bounds response sizes and disables connection reuse to remove reused-connection retry behavior. The supported SDK verifies raw webhook signatures and API compatibility. Canonical retrieval validates original terms, Session → PaymentIntent → Charge relationships, exact amounts and captured/paid/refund/dispute evidence. An event name or browser redirect never establishes funding.

`internal/website` has optional Go-owned `/topup`, `/topup/start`, `/topup/status` and `/topup/return` routes. SDK auth and exact account binding, memory-only sessions, CSRF, stale-publication checks and server-issued bounded form attempts remain mandatory. No browser account/amount/return-URL selector is accepted. References remain private session state, not pages or query strings. Status/return routes cannot create or retry a payment. Trusted Lisp cannot take ownership of `/topup` or `/payments` or acquire a payment/network capability.

The top-up document uses `Referrer-Policy: no-referrer` and permits only the fixed trusted offline provider origin in CSP `form-action`. Browser qualification uses an IPv4 provider origin: Chromium does not support literal IPv6 CSP form-action sources, so website admission refuses that configuration before creation. The adapter itself may still support IPv6 for non-browser fixtures.

**Default runtime remains payment-disabled:** ordinary `newWebsiteHandler` supplies no payment capability and no live webhook backend. Offline integration must be explicitly constructed with the fake provider. This packet is not a Stripe test-mode or production activation switch.

## Explicit local setup

Against an existing **stopped, local synthetic** ledger directory, the command is:

`devenv shell -- go run ./cmd/intake synthetic-payments-init --state /absolute/path/to/local-synthetic-state`

This adds only optional synthetic payment schema. It refuses missing ledger state, extra runtime/volume options and a supplied managed-volume secret. It never initializes/unlocks a disk, creates customers/credentials, configures Stripe or starts a listener. It does not enable payment routes in ordinary serve. A failed/unknown setup must preserve state, not trigger reset/reinitialization.

## Source qualification evidence

Parent inspected the actual delegated changes and ran these final-source commands successfully:

`devenv shell -- env OTEL_SDK_DISABLED=true INTAKE_TOPUP_BROWSER=synthetic-only go test -race ./... -count=1 -timeout=240s`

`devenv shell -- env OTEL_SDK_DISABLED=true go vet ./...`

`devenv shell -- go mod verify`

`git diff --check`

The full race run includes all Go packages, actual CLI subprocess setup/refusal checks, account isolation, concurrent once-only create/funding, rollback/overflow/corrupt holds, missing/expired/replayed mappings, bounded purge/restart, unknown transaction completion and post-commit/delivery failures. Active LSP checks on changed Go paths returned no errors. This is synthetic source evidence, not deployment or real-provider evidence.

`TestTopupHTTPSCanonicalWebhookFundsAuthenticatedAccount` joins real PocketBase authentication, HTTPS/CSRF forms, the actual pinned Stripe SDK against a loopback fake, signed raw-body webhook and accounting transaction. Provider completion alone leaves balance unchanged; the signed event causes canonical retrieval and exactly one $10-equivalent addition to the authenticated account, not another account. Duplicate form/event delivery does not create another payment or credit. Status/return remain local reads; event amount/currency are deliberately wrong and never funding authority.

The installed Chromium test runs with JavaScript disabled: login → form → distinct-host fake Checkout → return. One start and one provider navigation; no account cookies or referrer reach that provider. It qualifies browser/session/CSP navigation separately from the SDK/funding integration test; it does not qualify real Stripe/3DS.

Independent review and parent regressions closed:

- Case-variant payment schema could masquerade as absent; inventory now uses `lower(name)` / `lower(tbl_name)` and rejects malformed partial/full variants.
- Empty/missing/null refund-list data could look like no refunds. Parent reproduced funding through the real SDK + signed webhook fixture before correction. Missing list data now returns unavailable evidence; regressions require zero funding evidence, no binding, no sticky block and no credit. Only validated canonical refund/dispute evidence can persist a block.
- Known Checkout creation could be erased by a later session-commit error/panic. Its outcome now propagates into shared response state before presentation; both actual HTTPS session-failure cases retain known creation without leaking the reference. Final bounded independent verification reported no unresolved P0/P1/P2 in that corrected path; it was not a second full accounting audit.

The first browser fixture failed navigation with an IPv6 provider CSP source; website configuration now refuses that unsupported case. A full race run then caught a test-fixture startup ordering race, corrected by publishing immutable capabilities before starting its server. Final full race/browser checks passed afterward. The Stripe dependency required a vendor-hash update. An attempted extra refund-list object-field check did not compile against the SDK and was removed; no raw-parser workaround was introduced. No failed intermediate build is treated as acceptance.

## Local build and generated-image evidence

`nix build --no-link --print-out-paths 'path:.#default' 'path:.#ledger-image' 'path:.#image-smoke-probe'`

Passed on final source. Public output SHA-256 identities:

- Local host `intake`: `af7215d94cc5eabb8ac6876fc154bf19e6359ad20bafd76b74c48f5653cd3315`.
- Linux archive: `80361cbaa8698b07cc44b97de066034358e2720c98a8553feee85c63dd478bde`.
- Linux probe: `ff866d46561fffd89cc66ca6f27bb6adeb6a2cde574a288ad1443aacad29e1a2`.

The generated archive is `/nix/store/m3ldf6gi8gkyz6fbydzq0ii6j2x0znwy-possums-intake-local.tar.gz`; its probe is `/nix/store/g217r17mc53kzrrsvlv5x4w9627n6vas-intake-image-smoke-probe-0.1.0-local/bin/nix`. These are local outputs, not signed releases or independently matching native Linux build evidence.

The initial existing `scripts/image-smoke.py` attempt stopped at `DELIVERY_IMAGE_TOOLS` because `skopeo` was absent. The operator then explicitly authorized fetching pinned `skopeo` into a temporary Nix shell, with no permanent profile install or deployment. Further preflight refusals established that the unrooted archive/probe were no longer present, not an application boot failure. The final outputs were rebuilt with temporary ignored `result-stripe-qualification*` roots; their paths and hashes matched the earlier final build. No preflight refusal loaded an image or started containers, and no guard was bypassed.

With temporary pinned skopeo 1.24.1 available in the pinned development environment, the **existing qualifier passed on the final archive**, Linux/arm64. Passed cases: explicit empty initialization; migration failure refusal; missing state refusal; no raw logs; read-only/nonroot operation; readiness on original state; persistent restart; schema-drift and unsupported-schema refusal; telemetry disabled. Native operator checks also passed: actual entrypoint, login/create/patch, shared-key separation, accepted stock held-balance edit, logging forced off, empty log/auth-origin/stdio observations and frozen asset HTTP hashes.

Public generated-image identities: manifest `sha256:6b2241c7755a3925d082556abc4a9a060e4b298523e5f7292ee5d6709d6e7f65`; config `sha256:24a2e3618798177797bbbdacf593131fad09c804e9f77db0c2930e7c91a8a91e`; local Docker runtime identity `sha256:b18483ee9ef99346ca2d0970020b72e871015a7ec1466e22ca074d0598d573c8`. Archive SHA-256 matches the final source build above. Temporary root links were removed afterward. The loaded local synthetic image is a build artifact, not a deployed workload.

Limits: this existing image test qualifies baseline runtime/privacy behavior with payments disabled. It does not activate Stripe inside the container. Browser-inside-image was not run; previous-release compatibility is unknown. Native independent Linux matching builds, signing, managed-volume/runtime custody and production/payment activation are not established by this local cross-compiled image check.

## Subsequent local sandbox setup

The operator separately authorized sandbox read/write setup and local sandbox testing, then requested Stripe CLI in the project's pinned `devenv`. This does not activate the offline implementation or authorize live-account operations, real payments, gateway integration, publication or deployment.

`devenv.nix` now includes pinned `pkgs.stripe-cli` (1.43.2 in the current lock) and sets `STRIPE_CLI_TELEMETRY_OPTOUT=1`. Upstream's matching CLI source checks this variable before constructing its analytics client. `devenv shell -- stripe --version` passed with that setting; `git diff --check` passed. Use the version **flag**, not `stripe version`: the subcommand also checks GitHub for updates. The initial qualification used that subcommand before the opt-out was added; GitHub contact was observed and default CLI command telemetry may have occurred. No credentials were supplied to that command; absence of export is not established. No login or forwarding listener has been started.

The MCP selector listed exactly one sandbox with `livemode=false`, and no live context. This establishes the returned selector's sandbox scope under the tool contract, not globally sandbox-only OAuth permissions. Metadata research found no application/restricted-key creation operation in the exposed catalog. Registered webhook-endpoint creation is exposed but is not local listener setup. MCP OAuth is not an application SDK credential.

The operator reports placing an application key in local `.env`; its contents were not inspected. Git confirms `.env` is ignored. This is a plaintext local-file/host persistence boundary, not a secret vault or secure-erasure claim. The intended application variable is `STRIPE_API_KEY`; no key values belong in commands, source, tool output or diagnostics. No provider operation or runtime activation follows merely from this file's presence. A separately qualified sandbox-only runtime path and private listener-secret handoff remain necessary.

## Subsequent first-party UI packet

The operator subsequently approved moving first-party top-up layout and status copy into the existing Lisp UI library, matching the site's shared styling, adding an account-page entry and displaying exact dollar summaries. The [UI packet contract](stripe-topup-ui.md) separates Lisp presentation from Go-owned authenticated actions, fixed amounts, CSRF, once-only funding and financial outcome validation. It also records **polished Tor / JavaScript-disabled UI as future work**, not current end-to-end payment support or Tor authorization.

The earlier setup/remaining-gate statements below are historical offline-stage evidence; [current sandbox evidence](stripe-sandbox-local.md#completed-sandbox-payment-after-link-correction) records the subsequent authorized signed-webhook funding test. The UI packet's local source, offline browser/race checks and independent reviews are complete as recorded in its qualification section; it is not new running-runtime, actual-provider, image or deployment evidence.

## Remaining gates (original offline packet)

- A real runtime activation path, including an account-page entry link, remains disabled/deferred; local fixtures explicitly construct the coordinator and mount its signature-only webhook.
- Separately authorized local sandbox configuration and hosted Checkout/3DS/dashboard-default qualification remain incomplete; the offline evidence above does not establish them. Live-account access remains unauthorized.
- Real-data readiness, currency policy, eligibility, tax/record-retention and public support/legal terms before actual sales; deferred discussion is not exemption.
- Paid gateway integration and verified transport remain separate work. No inference before confirmed reservation; no live inference probes for payment acceptance.
- No claims of production readiness, secure deletion, anonymous card payments, complete PCI compliance or safe financial restore.
