# Local Stripe sandbox extension

## Authority and current evidence

The operator separately authorized sandbox reads/writes, local sandbox testing, adding Stripe CLI to `devenv`, and privately supplying an application key through ignored `.env`. This authorizes neither live-account operations nor real payments, gateway changes, inference probes, publication, commits/pushes or deployment. The completed offline packet remains documented in [local qualification](stripe-topup-local.md).

The MCP selector returned exactly one sandbox, explicitly `livemode=false`, with no live context. That is selector-contract evidence, not a claim about the entire OAuth grant. The exposed catalog has no application/restricted-key creation operation. MCP OAuth must not become the application's SDK credential. Registered webhook endpoints are not local CLI listeners. No business-object call or CLI login/listener has been performed during prerequisite qualification.

Stripe CLI 1.43.2 comes from the existing project pin. `STRIPE_CLI_TELEMETRY_OPTOUT=1` is set by `devenv`; matching upstream source checks it before constructing its analytics client. Use `stripe --version`, not the version subcommand's GitHub update check. Initial pre-opt-out command telemetry is unknown; see the qualification record. Tool availability is not payment acceptance.

## Proposed implementation order

1. Add an explicit sandbox adapter constructor and coordinator admission, with offline negative tests and unchanged offline defaults.
2. Add a separately selected, disposable local sandbox website mode. It must not extend ordinary serving or silently activate from environment presence. Reuse supported authentication, fixture account setup, HTTPS and existing lifecycle helpers where feasible; do not duplicate accounting.
3. Qualify private local credential consumption and listener-secret handoff before starting a CLI listener or application network operation. The agent must never receive secret values through chat, tool output, command arguments, source or diagnostics.
4. Independently review actual diffs and run pinned offline/race/privacy checks before browser-mediated sandbox payment testing. Use synthetic customer/card inputs only. Record actual outcomes and remaining gaps; no automatic attempt replay.

## Adapter boundary

- `NewOffline` remains literal-loopback/public-fixture-only and must still reject actual test credentials and external provider origins.
- A distinct `NewSandbox` accepts only bounded ASCII `sk_test_` / `rk_test_` application keys, excluding fixture placeholders, and a non-fixture webhook secret. No live-key prefix or production constructor, environment lookup or public endpoint/transport override.
- API traffic is fixed to certificate/hostname-verified `https://api.stripe.com`. Browser navigation is separately fixed to `https://checkout.stripe.com`; these origins must not be conflated. Accept only a bounded test Checkout session path. Preserve a provider-returned fragment if required by hosted Checkout, but never accept query-based customer prefill or persist/export the URL. Actual provider URL compatibility remains unqualified until sandbox testing.
- Keep explicit telemetry-off, null logging and zero SDK retries on every backend, fixed timeout/body/header bounds, no proxies/redirects, no reused-connection replay, and no account/context overrides. Preserve closed stage/constraint/status/outcome errors; never copy SDK exception text or payloads.
- Existing canonical Session → PaymentIntent → Charge evidence, test-mode/context checks, exact money/immutable terms, refund-list completeness and webhook signature/exact pinned API checks remain unchanged. Do not disable validation to accommodate a sandbox incompatibility.
- Coordinator sandbox admission must require a sandbox client. Offline admission must refuse it. Both retain explicit initialized-schema admission, overdue purge, durable once-only create claims and atomic once-only funding. No remote work under the ledger writer gate; no payment telemetry or reconciliation worker.

## Local runtime and private input boundary

The planned local mode uses fresh disposable synthetic state and the existing basic fixture login, not real accounts or balances. Ordinary serve and `website-local` remain payment-disabled. No managed volume, persistent production state, native operator administration or application-key-dependent automatic startup action belongs in this mode.

The app receives `STRIPE_API_KEY` and its local listener's signing secret privately in process memory. `.env` is Git-ignored and untracked; its contents were not inspected. A local plaintext file, its backups/editor history, process memory and operator terminal remain persistence boundaries. Do not silently add dotenv loading to ordinary serving or dump inherited environment/configuration. Treat a live key, unsupported configuration or missing secret as a closed pre-network refusal, not grounds to discover/retrieve another key.

CLI login has separate local credential persistence and Stripe audit/network boundaries. The local listener produces a signing secret and event metadata: it must not run with raw output captured by agent history. Its secret is not a registered webhook endpoint's secret. Use supported CLI behavior, no `--live`, debug logs, API-version bypass, TLS-verification bypass, raw event capture or public tunnel. The app's loopback webhook must receive only signature-verified bounded bodies; browser redirects/status cannot fund. The implemented runtime uses HTTPS for the browser and a separate HTTP-only loopback webhook listener; both use the existing joined shutdown coordinator. CLI-to-app private secret handoff remains to be qualified before activation.

### Direct operator-terminal workflow

No wrapper or Python dependency is needed. Run Stripe CLI and the existing Go command in two private operator terminals using the steps below. Neither the agent nor a tool runner should execute the credential-bearing steps or capture the listener output. `.env` remains untouched: privately copy its existing test key into the hidden prompts rather than sourcing the file or putting secrets in commands. These Bash prompts do not echo their input or place it in command history; terminal/clipboard/host retention and child process memory remain independent boundaries. Do not enable shell tracing, terminal recording, debug output, log redirection or environment dumps.

Unlike the removed launcher, this manual workflow uses normal local CLI configuration and inherited shell environment, not an isolated HOME/config or cleared child environment. Use a development terminal without unrelated production credentials, proxies, plugins or Stripe endpoint/account/context overrides. No CLI login is required when supplying the application key. The key-prefix guard before the listener is only a live-key refusal, not proof of sandbox identity or restricted-key scopes. Manually verify the CLI's readiness/API version and keep its actual signing secret private. Keep supported CLI session/reconnection behavior distinct from the application's zero creation retries.

Both processes are manually controlled. Stop the Go app first and wait for it to exit before stopping the listener; never force-kill or erase scratch state to resolve an unknown outcome. Starting either process does not itself create a Checkout or establish payment/funding completion.

## Acceptance gates

- Constructor/mode refusal tests, production key/origin/redirect/context rejection, no live fallback and fake hostile SDK errors.
- Offline fixtures still pass unchanged; synthetic tests prove fixed origins, no telemetry/logging/retry and safe errors without using actual credentials.
- Local CLI/runtime tests cover option refusal, disposable state, missing/live key before network, secret absence from stdout/stderr/diagnostics, authentication/CSRF and loopback-only listeners.
- Full pinned race/vet/module checks and independent review before an actual sandbox purchase. No claim that a non-network constructor test qualifies actual Stripe/3DS or Dashboard defaults.
- Manual sandbox evidence must distinguish Checkout creation, provider completion, canonical webhook funding and browser delivery. Unknown creation is not retry permission. Keep only content-free evidence, never object IDs, hosted URLs, secrets or raw provider events.
- The real-sales legal/currency/tax/retention/support gates remain deferred. The 90-day sales period remains a synthetic fixture, not real-data policy.

## Adapter source qualification

The distinct adapter and coordinator admission are implemented. Parent read the actual constructor, shared transport, URL, diagnostics, mode-admission and synthetic test bodies. Parent checks passed:

`devenv shell -- env OTEL_SDK_DISABLED=true GOPROXY=off GOSUMDB=off go test -race ./internal/stripepay ./internal/payments ./internal/website -count=1 -timeout=240s`

`devenv shell -- env OTEL_SDK_DISABLED=true GOPROXY=off GOSUMDB=off go vet ./internal/stripepay ./internal/payments`

`git diff --check`

Independent bounded read-only review found no introduced scoped P0/P1/P2 and independently passed both adapter/coordinator race suites and scoped vet with cleared inherited environment and module-network lookups disabled. This is not a renewed whole-ledger audit. Private test backend substitution qualifies SDK wire/refusal behavior; constructor/source inspection separately establishes the fixed external destination. The TLS negative test establishes rejection of an untrusted peer before HTTP credentials, not separate independent isolation of hostname and certificate checks. A loopback proxy test alone would not establish proxy independence because Go already bypasses proxies for loopback; explicit `Proxy:nil` and constructor inspection matter.

## Disposable runtime source qualification

`stripe-sandbox-local` is a separate command with only optional `--listen` (127.0.0.1:8443) and `--webhook-listen` (127.0.0.1:8844). Both must be canonical 127.0.0.1, nonzero explicit and distinct ports. Managed-volume secret presence and state/operator/provider/credential overrides are refused before setup. Only this command consumes private process `STRIPE_API_KEY` and `STRIPE_WEBHOOK_SECRET`; it does not load `.env` or invoke CLI.

The command makes fresh disposable state, explicitly initializes optional payments, provisions one zero-balance synthetic account and maps the existing supported SDK demo login to that constructor-fixed account. SDK password/token/current-record/rule validation remains in effect. The browser uses the existing in-memory ephemeral HTTPS certificate; the separate HTTP loopback listener exposes only the existing signature-verifying webhook. Both servers drain and join through the existing maintenance/lifecycle coordinator before storage close. Unconfirmed teardown preserves state/resources, with no shutdown-completion deadline claim. Ordinary serve and `website-local` remain payment-disabled.

Parent closed the integration blocker where the website's offline-only origin guard refused actual Checkout. Only exact `https://checkout.stripe.com` was added beside the existing offline IPv4-loopback guard; other external origins, authority variants and query/path overrides remain refused. CSP uses that fixed origin and redirects still require the adapter capability. New sandbox diagnostic enums preserve their closed categories. The first new CSP test incorrectly assumed a trailing semicolon; its assertion was corrected without changing the source policy.

Parent full race suite passed, including the existing actual no-JavaScript synthetic browser test:

`devenv shell -- env OTEL_SDK_DISABLED=true GOPROXY=off GOSUMDB=off INTAKE_TOPUP_BROWSER=synthetic-only go test -race ./... -count=1 -timeout=240s`

Full `go vet ./...`, `go mod verify` and `git diff --check` passed in the pinned environment. An active LSP probe reported zero errors for the new runtime/lifecycle and changed website source/test paths. Parent inspected runtime, dual-server lifecycle and actual-handler/fault tests. Independent bounded read-only review reported no introduced scoped P0/P1/P2; its own executed checks were listener-free and lifecycle review was static. Parent full tests exercise actual local handlers and dual-server cancellation/drain/error/panic races. The synthetic browser uses a fake provider, not actual Stripe.

## Go-only private startup

After removing the Python wrapper, the operator asked the agent to run setup and approved moving the necessary private handoff into the Go development command. Implement a distinct explicit no-argument `stripe-sandbox-dev`; leave manual `stripe-sandbox-local` and ordinary serving unchanged. This is local development, not a new production payment mode.

Implemented sequence: refuse managed-volume input/options; select and qualify the pinned CLI version; privately parse exactly one bounded literal test-key assignment from ignored `.env` without evaluation or broad dotenv export; start one isolated-config listener with the fixed event/API/forwarding selections; privately consume only bounded reviewed readiness and its actual signing secret, then discard event output. Pass secrets to the existing runtime through its private input closure, never command arguments or a file. A private empty CLI config and temporary HOME contain no credentials; cleanup waits for joined child/runtime completion. CLI's native reconnect behavior remains distinct from no wrapper restart and zero SDK creation retries.

Only after both local listeners bind, make one passive HTTPS `/login` request, verifying the exact ephemeral certificate supplied internally by that runtime plus its loopback hostname. No proxy, redirects, TLS bypass, accounting read or provider request is needed for this check. Emit only `STRIPE_SANDBOX_DEV_READY` on success, not an acceptance/financial receipt. Cancellation/companion failure cancels and joins the app before signaling/reaping CLI; preserve observed typed primary failures and unknown financial outcomes. No forced-kill completion deadline or automatic restart.

Acceptance before any actual startup requires parent inspection of full new bodies, offline fixture/helper-process tests covering hostile inputs/secret boundaries/lifecycle races, scoped race/vet/build checks and independent read-only review. Tests must never read real `.env` or start actual Stripe CLI. Only the parent may subsequently start the reviewed development command once under the operator's local sandbox authority. Hosted Checkout/payment completion remains operator-driven; no automatic purchase or retry.

Applicable privacy boundaries are [local sandbox authorization](../PRIVACY.md#subsequent-local-stripe-sandbox-authorization), [forbidden collection](../PRIVACY.md#forbidden-collection-and-defaults), [typed failures](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates), and the sibling gateway's [data boundaries](../../possums/PRIVACY.md#data-handling-boundaries) and [forbidden telemetry](../../possums/PRIVACY.md#telemetry-permitted-signals-and-forbidden-data). Startup markers/closed errors are transient local feedback, not persistent diagnostic logs or metrics.

## Manual sandbox test (fallback)

Run these commands yourself from this worktree, not through an agent. Use only one app/listener pair. The Go app creates a fresh disposable account/state each start: restarting loses local reconciliation state and must not be used to diagnose an unknown payment.

### Terminal 1: build, then Stripe listener

Enter the pinned environment with Bash (the hidden-input syntax below is Bash-specific):

`devenv shell -- bash --noprofile --norc`

Before entering credentials, build the Go binary into the Git-ignored `bin` directory with module-network lookups disabled:

`env -u STRIPE_API_KEY -u STRIPE_WEBHOOK_SECRET OTEL_SDK_DISABLED=true GOPROXY=off GOSUMDB=off go build -mod=readonly -o bin/intake-sandbox ./cmd/intake`

Stop if the build fails. Check the CLI version; it must be 1.43.2:

`stripe --version`

Privately paste your existing sandbox application key at the hidden prompt. Do not paste it as a shell command or send it to the agent:

`IFS= read -r -s -p 'Stripe sandbox API key: ' STRIPE_API_KEY; printf '\n'; export STRIPE_API_KEY`

Start the listener only for a test-prefixed key. No `--live`, login, debug, secret-printing subcommand, TLS bypass or public tunnel:

`case "$STRIPE_API_KEY" in sk_test_*|rk_test_*) STRIPE_CLI_TELEMETRY_OPTOUT=1 DO_NOT_TRACK=1 stripe --color off --device-name possums-local-sandbox listen --skip-update --latest --events checkout.session.completed,checkout.session.async_payment_succeeded --forward-to http://127.0.0.1:8844/payments/stripe/webhook ;; *) printf '%s\n' 'STRIPE_TEST_KEY_REQUIRED: load a sandbox key; no listener started.' ;; esac`

Wait for Ready and confirm API version **2026-09-30.endive**. Stop if the version differs, authentication is refused, or readiness is absent. Keep this terminal open. Privately copy the `whsec_...` from this running listener, not a Dashboard endpoint secret. Its output includes secrets/event metadata: do not share a screenshot, transcript or raw output.

### Terminal 2: Go app

In another terminal at this same worktree:

`devenv shell -- bash --noprofile --norc`

Paste the same application key privately:

`IFS= read -r -s -p 'Stripe sandbox API key: ' STRIPE_API_KEY; printf '\n'; export STRIPE_API_KEY`

Paste the running listener's signing secret privately:

`IFS= read -r -s -p 'Running listener signing secret: ' STRIPE_WEBHOOK_SECRET; printf '\n'; export STRIPE_WEBHOOK_SECRET`

Start the Go app, with telemetry disabled. It validates credentials before creating state or listeners and does not load `.env`:

`OTEL_SDK_DISABLED=true ./bin/intake-sandbox stripe-sandbox-local`

The app does not print a startup banner. A browser connection is not evidence of payment/funding completion. If it exits, report only its closed diagnostic code/stage/constraint/outcome, never raw third-party output or secret values.

### Browser: one deliberate purchase

1. Open `https://127.0.0.1:8443/topup`. The local ephemeral/self-signed certificate needs deliberate local review; accept an exception only for this local fixture, never for Stripe. Log in with the public fixture key `local-demo-key`.
2. Select **$5 once**. On hosted Stripe Checkout use test card **4242 4242 4242 4242**, any future expiry and synthetic three-digit CVC; use only synthetic other requested inputs, never a real card or customer details. Stop if the page is not in test mode.
3. Complete that one test payment. On return, use **Check local status**, not a new purchase form. Look for **Credit was added once**. The Account page currently renders raw microunits: both balance and available should be **5000000** (= $5). Refresh the status/account pages to check those values stay unchanged; a browser refresh is not duplicate-webhook qualification.
4. Share only whether Checkout opened, the test payment completed, funding was confirmed, and the balance remained $5, or the app's closed diagnostic fields. No secrets, hosted URLs, event/object IDs or raw events. An unknown/missing result is not permission to try another purchase. Keep the processes open for reconciliation.
5. After a known result, Ctrl+C the Go app and wait for exit, then Ctrl+C the listener. Exit both shells to release their exported variables; this is not secure erasure or cancellation/refund of a payment.

## Launcher removal and remaining evidence

At the operator's request, the Python launcher, its 24 authored stub tests and its additional `pkgs.python3` dependency were removed. Their earlier qualification was historical wrapper evidence, not acceptance of the direct manual workflow or of Stripe. The subsequently approved explicit Go development supervisor above replaces the private handoff; accounting/adapter behavior and manual/ordinary serving remain unchanged. Only the development command reads the bounded literal `.env` assignment; no general dotenv evaluator or production feature was added.

After removal, pinned offline `go test -race` and `go vet` passed for `./cmd/intake ./internal/stripepay ./internal/payments ./internal/website`. The documented credential-free Go build passed and produced ignored `bin/intake-sandbox`; the binary was not executed. All standalone documented commands passed Bash syntax checking. An authored shell-function stub verified the exact listener arguments/telemetry opt-outs and rejection of live-prefixed/empty keys before invoking the stub; a public fixture exercised hidden-input parsing. These checks did not read `.env`, start Stripe CLI or contact Stripe. `git diff --check` passed. Manual provider/browser acceptance remains pending.

## Go development-command qualification and first startup

Parent read all new source/test bodies and the modified runtime certificate callback. Pinned `go test -race ./cmd/intake -count=1 -timeout=240s`, `go vet ./cmd/intake`, offline `go build -mod=readonly` and `git diff --check` passed. Independent read-only review independently passed authored fixture/race tests and checked the pinned public CLI output/config behavior. Its alleged closed-certificate-channel issue was refuted against actual source: the channel is never closed; the production probe calls `x509.ParseCertificate` and refuses missing/invalid DER before marker delivery. The options/volume stage concern was not a missing classification: the explicit `no_managed_volume_secret` constraint separately identifies the refusal at pre-start option validation. No scoped startup blocker remained; this is not a whole-payment audit.

Parent attempted the development command once. It completed its credential-free pinned CLI version check, then returned `STRIPE_SANDBOX_DEV_UNAVAILABLE`, stage `stripe_dev_input`, constraint `regular_bounded_local_input`. A metadata-only existence check established that `.env` is absent in this worktree; the process exited. No file contents or key were read, listener started, provider transaction attempted or automatic retry performed. No raw CLI output or persistent diagnostic file was captured. The transient startup pipe carried only the closed application diagnostic; the pipe/control directory were removed.

The operator clarified that the existing `.env` resides in the original repository. A metadata-only check confirmed a regular non-symlink file there. With that explicit direction, parent launched the reviewed worktree binary under its pinned environment with the original repository as working directory; no key file was copied or displayed. The command emitted `STRIPE_SANDBOX_DEV_READY`: observed pinned CLI readiness, private actual-listener secret handoff and exact-certificate-verified local HTTPS `/login` HTTP 200 passed. The app/listener remain running for the operator's browser test. No Checkout or purchase was initiated by the agent. Process memory and Stripe audit/network retention remain independent boundaries.

## First interactive Checkout refusal and source correction

The operator's first browser creation attempt returned `PAYMENT_PROVIDER_UNAVAILABLE`, stage `payment_checkout_create`, constraint `http_success_required`, outcome `creation_unknown_not_replayed`, observed HTTP 400. This is not funding/payment evidence. No provider error body, object ID, hosted URL, key or event was collected; no creation replay or diagnostic provider call was made. Keep the original processes/state for reconciliation; local status remains observational.

Public [pinned-version Checkout documentation](https://docs.stripe.com/api/checkout/sessions/create.md?query=payment_method_data&api-version=2026-09-30.endive) exposed a definite request bug: `payment_method_data[allow_redisplay]=never` is invalid. Supported values are `always`, `limited`, `unspecified`. Parent removed the unsupported override rather than inventing a never-save enum. Guest `customer_creation=if_required`, disabled invoices, absence of Customer/Account identifiers and `setup_future_usage`, and canonical no-Customer/no-future-use checks remain unchanged. No claim that Stripe erases card/payment records follows. Without the original provider body/category, this known source defect is not authoritative proof of the reported 400's sole cause.

The actual SDK wire regression now excludes the redisplay override and customer/saving inputs. Pinned offline race tests and vet passed for `./internal/stripepay ./internal/payments ./internal/website`; `git diff --check` passed. These fake-provider tests do not establish actual Stripe acceptance. The running binary was not replaced/restarted and no new Checkout was submitted. Reconciliation and explicit operator direction precede another purchase; restarting disposable state is not reconciliation.

## Subsequent operator-authorized browser qualification

The operator explicitly authorized agent-driven browser testing with sandbox credentials and synthetic test-card inputs. Credentials remained in private local processes; the agent did not display them. A bounded read-only CLI list query for Checkout Sessions created in the previous 24 hours returned an empty complete list, covering the earlier failed attempt without replaying it. Parent then gracefully interrupted the original app, observed process exit and both ports released, rebuilt with the enum correction and started one new session; actual readiness passed again.

Collaborative preview could not navigate the self-signed local fixture (`ERR_CERT_AUTHORITY_INVALID`). Parent used the already-qualified installed Chromium/Playwright tooling instead, with an isolated nonpersistent browser context and a certificate exception scoped to the current local fixture's public SPKI only, not a global TLS bypass or Stripe exception. Public local certificate acquisition is not attestation; it is deliberate trust of this operator-controlled disposable fixture. No screenshots, HAR, traces, profile, hosted URLs, IDs or provider bodies were emitted or persisted. Temporary ignored `bin` qualification helpers consume secrets privately and emit only locally authored bounded fields; they are not production commands or renewed automated-test evidence.

The browser authenticated with the public fixture key, selected $5 once, navigated to the validated test Checkout origin/path, filled only synthetic email/name/card/expiry/CVC/postal inputs and clicked Pay once. It did not return to the local app within the browser qualification wait. A subsequent complete bounded read-only list showed exactly one test Session, `status=open`, `payment_status=unpaid`. No second creation or payment submission was attempted. Read-only browser inspection of that same Session established the card form and US country selection; no authoritative validation/cause was recovered from the original submission. Reopening a page is not reproduction of its original validation state. Payment completion/funding remain unconfirmed; do not manufacture a decline/root cause or claim no possible charge solely from a lost browser return.

The current Go app/listener and original local accounting state remain running. Another submission requires deliberate authorization, not a diagnostic auto-retry. Actual 3DS, webhook funding, duplicate webhook once-only behavior and final balance acceptance have not passed.

## Non-submitting investigation after expiry

The operator authorized inspecting the existing Session/form and minimal read-only payment evidence, with no new Checkout or Pay submission. A single bounded complete recent-Session list read with expanded `data.payment_intent` found the original test Session now `expired`, `payment_status=unpaid`, `payment_intent=null`. No last-PaymentIntent-error evidence exists in that projection. This records current canonical fields, not a retrospective assertion that no browser/API action occurred. The investigation stopped before opening/filling a new form, creating a Session or submitting payment. The original hosted form is no longer available for reproduction, and the earlier browser context was closed without retaining validation/network artifacts. The original no-return cause therefore remains unknown; do not invent a declined-card, invalid-email or formatting cause. A new bounded form-only test requires explicit authorization to create another sandbox Checkout, and must stop before Pay pending separate approval.

## Authorized fresh form-only check and Link-display correction

After the original Session expired, the operator explicitly selected **Form-only test**: one fresh Checkout, synthetic form fill/validation, stop before Pay. Parent created exactly that one Session, authenticated with the public fixture and reproduced the original field-fill sequence. Both optional name/postal fills succeeded; all six selected fields were nonempty, native validity was true, no `aria-invalid` fields/visible alerts were observed, card/expiry digits matched their synthetic fixtures after formatting, country was US and Pay was enabled. No submit event or payment confirmation was attempted. These checks do not exercise submit-only validation and cannot retrospectively explain the earlier no-return.

The freshly filled page also had one preselected save-information checkbox and an additional empty telephone input. Inspection of the same fresh Session with that checkbox unchecked did not establish a submit failure or cause; do not infer a phone requirement from an empty field. Canonical read-only configuration separately reported phone collection disabled, no shipping/custom fields/required terms, no Customer/SetupIntent and no PaymentIntent. Browser text/field presence is not proof of a provider error, saved card or actual consent outcome.

Card-only method filtering does not by itself disable the Link wallet. Parent checked the [exact pinned API's wallet options](https://docs.stripe.com/api/checkout/sessions/create.md?query=wallet_options&api-version=2026-09-30.endive) and matching generated SDK types, then added the documented `wallet_options[link][display]=never`. This is distinct from the invalid `allow_redisplay=never` removed earlier. The actual SDK wire regression asserts the Link setting; no-Customer/no-future-use assertions and accounting/replay rules remain unchanged. Scoped adapter/payment/website race tests, vet and whitespace checks passed. No runtime restart, new creation beyond the one authorized form-only Session, Pay click, Stripe configuration update or provider-session mutation followed. The Link fix is source-only until separately activated; its actual hosted UI effect remains unqualified. The original failed submission's cause remains unknown. Another payment submission needs explicit authorization with submit-time bounded evidence capture; no screenshots/raw exceptions/URLs/identifiers or automatic retries.

## Completed sandbox payment after Link correction

The operator subsequently authorized activation and continued deliberate sandbox testing. Before replacement, a read-only check of the form-only Session still reported no PaymentIntent; no Pay had been submitted there. Parent gracefully interrupted the prior local app, observed process exit and both ports released, rebuilt with explicit Link display disabled, then started the reviewed Go supervisor once. Actual CLI/private handoff/exact-cert HTTPS readiness passed. This disposable restart did not preserve the previous local synthetic rows; it is not a production recovery policy. No provider Session was modified/expired/canceled by this operation, and previously unused Sessions were not reused.

An isolated browser then created one deliberate $5 test Checkout. Before Pay, the filled page had zero checked boxes and zero `aria-invalid` fields. Parent submitted the same public synthetic card/email/name/expiry/CVC/postal fixtures once, with bounded closed submit-time response/field evidence available on failure; no screenshot/HAR/trace/raw exception/URL/ID was captured. The browser returned to the local `/topup/return`, local status reported `Credit was added once`, and two account reads both showed balance/available `5000000` microunits. This uses the actual signed CLI-forwarded webhook/canonical provider retrieval and ledger path: return/status cannot fund. There was no payment retry.

A final complete bounded read-only recent-Session query, privately expanded to PaymentIntents, reported exactly one `complete`/`paid` test Session with a `succeeded` test PaymentIntent; the two earlier Sessions remained unpaid. No object IDs, keys, exact request times, hosted URLs or provider bodies left the private helpers. This establishes one completed sandbox purchase and once-only observed funding/stable account reads, not live-account eligibility, all restricted-key scopes, 3DS, customer rights/tax/real-data retention, or real-sales readiness. Success after disabling Link does not retrospectively prove the original failed submission's sole cause.

Actual redelivery of that provider event to the existing local listener remains unqualified. Pinned `stripe events resend --help` advertises only account/webhook-endpoint request parameters, not a local `--forward-to` option. No resend, fabricated event, second payment, public tunnel or registered webhook endpoint was created to manufacture evidence. Existing offline signed duplicate-event fixtures remain the scoped duplicate-proof evidence; actual provider duplicate delivery is a separate gap. The current test app/listener remain running, with no live/production/gateway/inference/telemetry activation.

## Status

Source/offline qualification, actual CLI/app readiness, hosted sandbox Checkout creation, one actual sandbox payment, canonical signed-webhook funding and repeated stable balance reads passed. Invalid redisplay override removed and explicit Link-display disable active. Earlier failed attempts were reconciled and not automatically replayed. Actual provider duplicate-event delivery and 3DS remain unqualified; real-sales/legal/tax/retention/deployment gates are unchanged. Ordinary runtime remains payment-disabled. Sandbox success is not a real-sales or whole-grant sandbox-scope certification.
