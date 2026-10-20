# Minimal website activation — implementation handoff

## Local implementation result

**Subsequent default-runtime authorization:** the operator selected default-on
website serving, explicit setup, stock process-lifetime cleanup and the existing
Tinfoil HTTPS ingress assumption. The local source/runtime extension is recorded
in [default website runtime](website-runtime.md). It supersedes ordinary-command
and fixture-only statements below for this synthetic local scope; image, real-data,
integration and deployment acceptance remain separate.

The SDK ordering gate and focused source/persistence-fixture slice are now
implemented and locally checked. See [activation evidence](website-activation-evidence.md)
for storage fields, native operation ordering, race/fault/privacy tests and
remaining gates. Ordinary commands and the running preview remain unchanged;
this is not production or real-auth activation.

Subsequent operator approval extended the isolated synthetic implementation to
single-key customer auth, current-binding checks and complete scoped snapshots.
That focused extension and its combined stock-admin/customer/publication browser
pass are recorded in [local evidence](customer-auth-local-evidence.md) and
[combined browser qualification](customer-site-browser.md). This supersedes this
handoff's older planning-only auth boundary for that local slice, not real data,
ordinary-runtime activation, integration or deployment.

## Operator authorization and scope

The latest operator instruction authorizes ensuring a reusable ELPS library
boundary, then handing this plan to a new T3 thread **to implement locally**.
This supersedes the earlier plan-only restriction for the focused activation
slice below. It does not authorize real customer credentials/data, gateway/client
changes, production activation, deployment, installation, commits or pushes.

Primary plan: [customer-auth/data plan, section 5](customer-auth-data-plan.md#5-minimal-live-website-activation).
Implement that minimal live-activation slice. The surrounding real-key auth,
account-binding and financial-view plan is context, not permission to expand this
packet. Resolve any broader intended scope with the operator before doing it.

Read repository guidance, README, local/gateway privacy policies and applicable
SDK/hosting evidence before changing behavior. `PLAN.md`, `AGENTS.md` and unrelated
work remain untouched. The checkout contains substantial uncommitted prototype
and unrelated work; preserve it. Leave the running preview untouched unless the
operator authorizes replacing it. Use isolated synthetic test state instead.

## Library boundary — keep it small

- Reusable core: `pkg/lispsite`, within the existing Go module. ELPS capture/load/
  preparation, isolated request VMs, bounded node copying and neutral declaration
  types/errors. No intake-internal imports, PocketBase, ledger, sessions, customer
  fixture assets or hard-coded financial route policy in the library.
- Intake adapter: `internal/website` owns reserved namespaces, the three-page/
  two-auth-action contract, HTTP/session/CSRF, application renderer/CSS/assets and
  projection into existing closed diagnostics. SDK auth stays `internal/siteauth`;
  accounting stays the ledger Service.
- Persistence/native activation wiring belongs on the application side. Do not
  put a PocketBase adapter or metrics/diagnostic framework inside the ELPS core.
  Do not create a separate repository/module, plugin system or interpreter fork.
- Preserve the exact qualified ELPS pin, fresh VM per request and VM-local entry
  lookup. No captured principals, SDK records, credentials or mutable view data in
  shared callbacks. No shared serialized VM, supervisor or abandoned worker.

Current API: `lispsite.Prepare(ctx, Bundle, Policy)` returns a revision with
`Page`, bounded copied-string `PageWithView`, `Named` and `Ready`; the intake wrapper preserves application diagnostics
and `Render`/`CSS`. See [library README](../pkg/lispsite/README.md). Public revision
metadata is read-only by serving contract, not deeply immutable Go data; do not
mutate it while requests run. Rerun relevant checks after your own changes.

Parent verification before handoff: inspected core/policy/diagnostic and adapter
source; full-root race/elpscheck suite passed, repeated core race suite ×3 passed,
root vet/module verification and separate compiled CLI build passed. Ten changed
Go paths were actively probed: no Go/type errors; three existing generic HTTP
sink advisories were checked against validated CSS, escaped complete HTML and
closed plain-text diagnostics; one two-string-concatenation hint was left alone.
These are local source/build checks, not publication/persistence acceptance.

## Selected activation behavior

1. Operator edits a draft through stock PocketBase admin. Saving source alone
   never activates it. No new admin screen or provisioning/publication CLI.
2. Explicit publish through a field/action in that UI. Use supported native SDK
   authentication/rules, not a new authority gate or permission tier.
3. Capture candidate bytes once. Serialize publishers, not request VMs. Check only
   essentials: valid bounded eager Lisp, compatible host version, allowed routes/
   capabilities and bounded renderable output. Initialization has no effects.
4. Prepare outside the SQLite write transaction. Save the exact candidate and
   active/previous selection in one short supported transaction. Coordinate new
   website admission across confirmed commit and the prepared pointer swap.
5. One request captures one immutable route/page/style revision, constructs a
   fresh VM and discards it after rendering. Existing requests finish on their
   captured revision; stale POST forms refuse before effects. Do not rebuild SCS
   or discard sessions on publication.
6. Restart reads/verifies/prepares the selected persisted bundle. Invalid/missing
   active code yields a small Go-owned website 503, not a demo fallback, automatic
   selection of older policy or financial database restore.

Only necessary state: bounded source/selection (one draft, active and previous),
one prepared active pointer and temporary bounded candidate/in-flight references.
No queue, watcher, activation-history table, per-user presentation store, automatic
retry/recovery worker, persisted diagnostics or elaborate state machine.
Preserve session-only 1h/10m volatile SCS policy and the explicitly accepted stock
concurrent-logout resurrection limitation; do not add revocation machinery.

**First technical gate:** inspect and qualify pinned PocketBase v0.40.5 hook/
transaction ordering for a stock-native publish field/action. Preparation must
precede the write transaction and the runtime swap must follow observed commit.
An earlier validation/save hook is not completion evidence. If supported SDK
facilities cannot provide this small safe workflow, stop and explain the exact
boundary; ask before adding another operation/UI or changing the contract.

Bad preparation leaves active unchanged. Unknown commit/activation is not success:
close new website admission rather than guess/replay, re-establish authoritative
selection before reopening, or return 503 pending explicit publish/restart. No
background reconciliation. Native/accounting availability is independent where
storage/SDK health permits. Rollback explicitly republishes retained code only;
never restore balances, credentials, reservations or replay state. Trusted native
admins can still directly edit/delete/restore records/schema; preserve their
accepted unrestricted authority rather than claim tamper protection.

Content-address actual generated CSS bytes, including the host scaffold. Never
serve changed CSS under an old URL. Preserve the accepted light buffer appearance,
literal source whitespace, tooltip face, hover/focus and body gutters. Retain
compatible active/previous styles; honestly missing unsupported old assets are
better than mismatched bytes. Fonts/licenses remain embedded, not database code.

## Persistence and host integration

Use supported PocketBase APIs and the transaction-scoped app. Define only the
necessary bounded source/selection fields and intentional migration/validation;
no automatic initialization in ordinary serve, financial reset or weakening of
financial SQL constraints. Synthetic isolated persistence tests are authorized;
opening real operator state or enabling production routes is not.

Current disposable `website-local` has no persistent state-path option or native
admin surface: it embeds a bundle and routes through website plus accounting.
`internal/operator/handler.go` supplies the existing SDK native router. Qualify
actual native saves/publication with isolated synthetic SDK HTTP fixtures; do
not silently turn the public demo into an unrestricted real admin deployment.
Preserve `CustomerAuthApp()` hook behavior and independent reserved-route dispatch.
Do not add an accounting permission tier or change custom accounting shared keys.

Document the narrow source/selection storage inventory, bounded retention and
SQLite/WAL/deleted-page/manual-snapshot boundaries under local privacy policy.
No source record contains runtime/customer view values, secrets, request history
or errors. Logical pruning is not secure erasure. Preserve SDK logging off,
AuthAlert origin recording off, other native auth controls and the 39-category
once-only closed failure projection. No activity/success/financial counters,
raw exceptions, logs/traces, exports or diagnostic-triggered work.

## Acceptance

- Prove stock-native draft save does not publish, explicit publish changes pages
  live without restarting/logging out, invalid candidate leaves old code usable.
- Exercise concurrent publication/draft edits, old in-flight requests, stale forms,
  coherent CSS, commit/swap interruptions, explicit code-only rollback and cold
  restart from isolated persistent state. No guessed success or financial changes.
- Keep full isolated VM/cancellation/limits/privacy tests and standalone non-intake
  library-consumer/dependency-boundary tests. Preserve native/accounting regressions.
- Use actual pinned SDK/HTTP and compiled binary where feasible; synthetic fixtures
  only, no real inference/payment calls or external telemetry.
- Pinned offline toolchain: `.devenv/profile/bin/go`; set PATH to include its bin,
  `GOTOOLCHAIN=local`, `GOPATH=$PWD/.devenv/state/go`, `GOPROXY=off`, `GOSUMDB=off`,
  `OTEL_SDK_DISABLED=true`. Run `go test -race -tags elpscheck ./... -count=1 -timeout=240s`,
  relevant repeated fault/race tests, `go vet ./...`, module verification, build and
  changed-path diagnostics. No install or preview restart is required for tests.
- Record source/build/local evidence and remaining gaps honestly. This is not
  production, capacity, Tor, attestation or gateway-integration acceptance.
