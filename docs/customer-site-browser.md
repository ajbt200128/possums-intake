# Combined synthetic customer website browser qualification

## Scope and result

**Passed locally**, with a fresh synthetic Go/PocketBase/SQLite service and the
existing installed Chromium/Playwright tools. The latest rerun uses ordinary
runtime setup/host constructors behind a synthetic HTTPS frontend and HTTP
backend. This is not deployed Tinfoil transport, generated-image, capacity,
real-data, gateway or deployment acceptance.

The opt-in harness is `cmd/intake/website_customer_browser_test.go`; the normal-UI
script is `internal/website/customer-site-browser-test.mjs`. Ordinary Go tests skip
the browser run. No existing state-path argument is accepted. Original preview
processes/artifacts/state are untouched; every run creates its own temporary
ledger. No credentials/accounts are created by ordinary serve or login.

Run from the repository root, using only existing installed dependencies:

`PATH="$PWD/.devenv/profile/bin:$PATH" GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true INTAKE_CUSTOMER_SITE_BROWSER=synthetic-only go test -race -tags elpscheck ./cmd/intake -run '^TestCustomerSite' -count=1 -timeout=180s`

The browser uses the existing sibling checkout's Playwright dependency read-only
and the installed Chromium executable already used for native-editor acceptance.
No installation/download, gateway change, inference, export, publication outside
this temporary fixture, commit or deployment occurs.

## Actual flow checked

1. Explicitly initialize fresh operator/financial, customer-auth and publication
   schemas through the ordinary explicit website initializer. Seed only known
   initial code and synthetic financial accounts/holds. The ordinary host runs
   behind a synthetic TLS-terminating reverse proxy with an actual HTTP backend,
   exercising the fixed offload policy with secure cookies and CSRF checks.
   The HTTP service uses actual shared-key accounting, native SDK administration,
   `CustomerKeyAdapter`, `Service.Balance` and persisted live publication together.
2. Through the stock admin UI, create two customer records with normal password,
   confirmation and account-ID inputs: a full 128-character synthetic key and a
   separate 64-character key. No lookup/verifier is entered or computed in the
   browser. SDK validation and collection rules remain in force.
3. In independent browser contexts, use each single key in the customer form.
   Account A displays its balance of 100 integer microunits, available 90, enabled
   state and both complete pending holds. Account B displays its own zero balance
   and no holds, never A's fields. These are temporary intake-ledger fixtures, not
   receipts for gateway funding.
4. Save a stock-editor draft without publishing: public code remains unchanged.
   Explicit publish changes public copy and actual CSS live. The old stylesheet
   remains available. An old customer logout form receives HTTP 409 before its
   effect; the existing session still reads the same scoped complete snapshot.
5. Submit invalid Lisp through the stock editor. The pinned SDK wraps this hook
   refusal as **HTTP 500**, with the closed `SITE_PUBLICATION_REFUSED` body and
   no-effect preparation evidence. Status alone is not publication evidence.
   Active code remains usable; no status normalization or SDK bypass was added.
6. Deliberately republish the known initial code through the editor. Existing
   customer sessions and financial snapshots remain intact. This is code-only
   publication, never a financial database restore or automatic recovery.

## Privacy and diagnostic evidence

- Exact ephemeral TLS SPKI comes directly from the parent fixture; no blanket
  HTTPS-verification bypass. Page-controlled network requests are restricted to
  that fixture origin. Child/browser environments do not inherit credentials,
  proxy configuration or exporters.
- All browser contexts have empty local/session storage, IndexedDB, Cache Storage
  and service-worker registration lists. Only secure, HttpOnly, SameSite=Lax,
  session-only website/CSRF cookies are permitted; SDK JWTs are not cookie values.
- No unexpected page errors, warnings, HTTP failures or console-resource errors.
  The exact expected stale-form 409 and invalid-publication 500 populations are
  matched using metadata, never free-form console/exception text.
- Chromium's transient CDP network observation uses its **typed cancellation
  bit**, not `errorText`. Client-cancelled native read-only GETs are distinguished
  from transport failures. The stock SDK cancels duplicate requests through its
  AbortController, and `recordsList` cancels on unmount; this behavior is present
  in the pinned SDK source and built UI. Uncancelled failures, cancelled writes,
  website navigations/assets and unmatched failures still refuse acceptance.
  No SDK auto-cancellation/retry behavior is changed. This is not a general
  browser-network health or whole-process transience claim.
- No screenshots, traces, response/page text, browser errors, tokens or raw child
  output are retained as test artifacts. The parent accepts only an exact success
  marker or a closed stage protocol; arbitrary runtime/tool output is never echoed.
  Host-native conversation history remains a separate persistence boundary.
- Parent checks both SDK verifiers and exact bindings, absence of raw/derived
  customer-key bytes in fresh fixture files (including SQLite/WAL), exactly the
  two explicitly provisioned customer records, zero request-log/auth-origin rows,
  persisted logging-off settings and an unchanged authoritative financial snapshot.
  These scoped scans are not secure-erasure or encrypted-volume qualification.

## Regression/build evidence and remaining gate

After adding the combined harness, the full serial root race/elpscheck suite,
root vet, module verification, whitespace checks and a separate CLI build passed.
Active LSP probes of the changed Go paths were clean. The browser acceptance above
also passed independently with its explicit opt-in.

The separate build artifact is
`.devenv/state/customer-site-qualification/intake`; it does not replace the old
preview binary. The subsequent [default-runtime packet](website-runtime.md)
wires customer/publication serving in by default, adds explicit setup and uses
the stock process-lifetime cleanup worker. This browser rerun passed through
those constructors and the synthetic HTTPS-to-HTTP boundary. The disposable
`website-local` demo remains separate.

Next: qualify the combined generated image, then separately seek real-data/
integration and coordinated deployment approval. No such approval is implied by
this local pass. See [local contract](customer-auth-local-contract.md),
[auth/data evidence](customer-auth-local-evidence.md) and the runtime document's
explicit transport, memory and packaging limits.
