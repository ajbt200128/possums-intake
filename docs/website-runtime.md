# Default website runtime — synthetic local acceptance

## Selected behavior

The operator selected default-on website serving, explicit setup, stock
process-lifetime session cleanup, and Tinfoil's existing HTTPS ingress as the
transport assumption. This extends the prior isolated fixture implementation to
ordinary **local synthetic runtime** source/testing. It does not qualify that
platform boundary or authorize deployment, real credentials/data, gateway
changes, inference, installation or telemetry export. Existing preview binaries,
processes and state have not been replaced.

- Ordinary `serve` includes the website without an enable flag. Native
  administration still uses the existing `--operator` option; custom accounting
  keeps its shared runtime key. `synthetic-serve` includes both native admin and
  the website on fresh temporary state.
- Explicit `init` initializes empty financial state, then customer-auth schema and
  bundled initial code. It creates no customer key, account or credit. Existing
  `init --operator` remains the only credential bootstrap, using runtime-only
  operator environment values. Serving never consumes these to reset a login.
- For an existing stopped synthetic ledger, `website-init --state ...` explicitly
  adds compatible customer auth and initial code. It does not overwrite existing
  valid published/draft code or financial rows. Partial/incompatible publication
  state and invalid selected code refuse setup, rather than guessing recovery.
- Customer-auth setup and publication setup are separate transactions; failure
  can leave the auth schema initialized. Publication collections and their
  initial code are created atomically. Preserve partial state and inspect it;
  no whole-command rollback, financial restore or reset is claimed.
- Ordinary serve refuses absent/incompatible customer auth. Publication cold-load
  failure instead closes website admission with `SITE_PUBLICATION_UNAVAILABLE`
  HTTP 503, preserving native/accounting availability for explicit code repair.
  No embedded or previous-code fallback is selected. `/healthz` remains ledger
  readiness, not proof that website code is available.

Commands below are examples for deliberately selected synthetic state only,
with required runtime credentials already injected privately. They were not run
against existing operator state:

`intake init --operator --state /absolute/new-synthetic-state`

`intake website-init --state /absolute/existing-stopped-synthetic-state`

`intake serve --operator --state /absolute/initialized-synthetic-state`

## HTTPS boundary

The ordinary runtime sets the website's fixed `TLSOffloaded` owner policy. It
assumes an HTTPS-only ingress supplies the original request host over the private
backend connection. It does **not** interpret `Forwarded`, `X-Forwarded-Proto`,
client IPs or request text as evidence of TLS, and does not fabricate Go TLS state.
Do not expose this HTTP backend directly to customers. Tinfoil's actual ingress,
backend isolation/host preservation and platform retention were assumed at the
operator's request, not newly inspected or verified by this packet.

Secure, HttpOnly, SameSite=Lax, host-only session cookies and HTTPS same-origin
CSRF checks remain required. Browser HTTP login is not an authorized alternative.
`website-local` and fixture defaults still require direct TLS. No certificate-file
management, insecure cookie mode or new public listener was added.

## Session cleanup and shutdown

`website.New` constructs one stock SCS v2.9.0 memory store per intake process,
with the supported one-minute expired-entry cleanup interval. Sessions remain
RAM-only, with one-hour absolute and ten-minute idle limits, additionally capped
by the SDK token lifetime. Restart clears sessions. Publication does not recreate
the store or log users out. Existing binding revalidation and accepted stock
concurrent-logout resurrection semantics are unchanged.

The cleanup worker intentionally lives until process exit. The pinned SDK's
`StopCleanup` startup synchronization is unsafe and it exposes no joined teardown
API; this implementation never calls it and promises no in-process host restart
or cleanup-worker join. The worker touches memory only, not SDK/storage. Existing
HTTP/accounting drain and storage shutdown stay unchanged. Expiry pruning is not
a fixed bound on active-session count, memory capacity or shutdown duration.
Disposable `NewFixture` retains its zero-worker behavior.

## Evidence and remaining gates

Passed focused race/elpscheck tests cover explicit/idempotent setup, atomic
publication rollback, partial/corrupt state refusal, preservation of acknowledged
balance/holds across setup/restart, no credential seeding, independent reserved
routes during cold code failure, default CLI startup, fixed offload policy,
spoofed forwarded-header refusal in direct-TLS mode, secure cookies and HTTPS
same-origin CSRF. Expired-session access is tested through stock public APIs;
physical map pruning is based on inspection/use of the pinned SDK cleanup
implementation, not an invented private-map observation.

The full serial root race/elpscheck suite passed in the pinned offline environment:

`go test -p 1 -race -tags elpscheck ./... -count=1 -timeout=240s`

After the independent read-only review, both confirmed gaps were corrected:
TLS-only hosts now gate CSS/fonts/licenses before their fast paths, and synthetic
website initialization/host construction uses a distinct closed `website_setup`
startup phase/catalog. Regression tests cover asset GET/HEAD refusal, cross-phase
code rejection, known/hostile cause projections, and an actual website-checkpoint
panic with unchanged cleanup/once-only ownership. The empty-publication fixture
initializer remains intentional and shares its schema helper with default setup;
no unrelated API cleanup was taken on.

The final full serial root suite and combined Chromium pass were rerun after
these corrections and passed. Final root vet, module checksum verification,
whitespace checks, separate CLI build and active error-level LSP probes of all
thirteen changed Go paths passed. The separate binary is
`.devenv/state/website-runtime-qualification/intake`; original previews are
untouched. The old process test's root-404 expectation was updated for deliberate
default website availability; native exclusion/shared-key checks remain.

One preceding full-suite attempt observed a generic synthetic-init refusal in
`TestProcessKeyRejectedBeforeListenerAndPrivateStartup`; its original underlying
cause was not retained and remains unknown. That test passed five isolated
repeats, and the subsequent full suite passed. The fixture now reports only an
allowlisted closed diagnostic code on recurrence, never raw output. No execution,
accounting or initialization budget was widened, and no automatic runtime retry
was added. This does not establish absence of timing/capacity limits.

The combined Chromium qualification passed using the **ordinary setup and host
constructors** behind a synthetic TLS frontend and actual HTTP backend. Stock
provisioning, independent single-key customer sessions, complete snapshots,
inert draft/live publication/CSS, stale-form refusal, code-only restoration and
browser/SQLite/logging privacy checks all passed. See
[combined browser evidence](customer-site-browser.md).

The source-only packet initially left generated-image acceptance open because
`nix/packages.nix` excluded `pkg/lispsite` and website Lisp/static inputs and the
vendor closure was stale. The subsequently authorized packaging extension fixed
those inventories and passed actual local image/vendor/runtime checks; see
[combined image evidence](website-image.md). Exact-main native AMD64 A/B,
publication and deployment gates remain separate. Historical deployed evidence
is not evidence for these website edits.

Real-data integration, gateway balance cutover, capacity, encrypted persistence,
attestation, release/publication and deployment remain separately unqualified.
Applicable privacy boundaries are [customer auth/reads](../PRIVACY.md#customer-key-authentication-and-scoped-website-account-reads),
[trusted manual administration](../PRIVACY.md#trusted-intake-manual-admin-exception)
and [forbidden diagnostics](../PRIVACY.md#forbidden-collection-and-defaults).
