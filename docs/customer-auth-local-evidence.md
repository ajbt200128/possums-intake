# Customer auth local work — scoped evidence

## Authorization and current limits

The operator explicitly approved [the local contract](customer-auth-local-contract.md)
and the narrow [intake privacy amendment](../PRIVACY.md#customer-key-authentication-and-scoped-website-account-reads)
for isolated synthetic local implementation. No real credentials/data, gateway/client
changes, deployment, installation, export, commits or pushes are authorized.

**Current evidence covers key/native provisioning, explicit auth schema, SDK key
adapter, private session-binding checks and complete scoped snapshot rendering.**
Backend/view/page wiring and the combined stock-admin/customer/publication browser
flow now pass. Only isolated fixtures call the explicit initializer; no ordinary
command creates the customer collection or enables customer website auth. The
original previews/state remain untouched.

Pinned PocketBase is v0.40.5; its pre-v1 production-critical warning still applies.
These passes do not qualify generated-image, production, restart/upgrade or capacity
behavior for customer auth.

## Supported SDK preparation path

`internal/siteauth/customer_key.go` defines the approved domain-separated version-1
lookup/43-character SDK password adapter and a narrow stock-native input middleware.
`internal/operator/handler.go` registers it after SDK authentication, IP policy and
**dynamic body limits**, before stock record form loading. It selects only SDK
superuser customer-record editor POST/PATCH calls; other native authority is not
replaced/restricted. No password mutation in a later record-request hook is used,
so SDK confirmation validation remains intact.

`internal/operator/customer_key_failure.go` projects only reviewed preparation
codes/stages/constraints with locally authored messages, safe HTTP status and known
preparation no-effect outcome. It reuses existing native once-only ownership and
numeric categories. Unknown preparation errors are cause-unknown, never raw errors.
No new failure category, telemetry field, native activity counter or logger.

## Actual synthetic checks passed

- Fixed protocol vector and gateway-compatible 43/71/72/96/128-character keys;
  malformed length, padding, whitespace and trailing-bit refusals.
- Authenticated stock native create/update HTTP through the actual ledger-owned
  logging/AuthAlert protections and operator handler. Long keys become stock SDK
  bcrypt verifiers; final supported SDK authentication accepts the derived secret,
  not the stored lookup or raw key as the SDK password.
- Missing, empty and mismatched confirmations do not save credentials. Bad input
  types/hostile values do not appear in diagnostic bodies. Stale submitted lookup
  cannot override derivation during an explicit key update.
- Supported password update invalidates the prior SDK token; refused update does
  not. Exact external binding bytes, including whitespace, controls/NUL and UTF-8,
  survive the native flow without identity normalization.
- Stock oversized-body refusal occurs before preparation; unauthenticated create
  remains the stock SDK authority refusal, not a project auth gate.
- Raw keys and derived SDK secrets absent from native responses and all inspected
  files under the fresh fixture directory, including SQLite sidecars/WAL. This is
  evidence for exercised fixtures, not secure erasure or arbitrary-admin protection.
- SDK request-log and auth-origin tables remain empty. Closed projection tests
  reject unreviewed code/stage/constraint/outcome claims and hostile next-step text.

One new negative test initially observed HTTP 500 for a known input refusal. The
closed preparation projection now returns 400 for input refusal and 503 for
cause-unknown preparation failure; focused reruns and the full suite below pass.

Pinned environment: `.devenv/profile/bin`, `GOTOOLCHAIN=local`, repository-local
`GOPATH`, `GOPROXY=off`, `GOSUMDB=off`, `OTEL_SDK_DISABLED=true`.

- Focused race/elpscheck key/native create/update and closed projection tests: pass.
- Full `internal/siteauth` and `internal/operator` race/elpscheck suites: pass.
- Root `go test -race -tags elpscheck -p 1 ./... -count=1 -timeout=240s`: pass.
- Root `go vet ./...` and `go mod verify`: pass.
- Active LSP probe of the first seven changed Go paths: no errors. This is not
  whole-workspace diagnostic coverage or a substitute for the test suite.

## Stock editor gate passed

`internal/siteauth/customer_browser_test.go` is an explicit opt-in runner using a
fresh TLS fixture and exact public-certificate SPKI pin. Ordinary Go runs skip it.
It runs the stock-editor script, then verifies the stored verifier/binding, empty
activity tables and absence of raw/derived keys in fresh fixture files. Only an
exact allowlisted child failure protocol can select a public stage; raw tool/
Node/Playwright output is never forwarded.

The actual installed Chromium pass saved the full 128-character synthetic key
through normal password/confirmation/account-ID controls without filling lookup
or verifier fields. Mismatched/missing confirmation saved no extra rows. Exact
SPKI TLS, no external page requests, no unexpected HTTP/page/console errors and
empty local/session storage, IndexedDB, Cache Storage, service workers and cookies
passed. Browser resource errors for expected HTTP 400 refusals are matched by
source metadata and exact HTTP-error population, not exception/message text. An
initial browser-checker false failure from delayed console delivery was corrected
without changing SDK/UI behavior. The corrected browser pass also passed again
using the explicit schema initializer below.

## Explicit customer schema and binding checks passed

`InitializeCustomerAuth` creates only the additive fixed customer auth schema in a
supported SDK transaction. Compatible existing state is unchanged; collisions or
incompatible shape refuse without credential reset. An injected post-create failure
rolled back both collection metadata and table creation; explicit initialization
after inspecting that rollback succeeded. Existing SDK state survived.

`NewCustomer`/`CustomerKeyAdapter` use final supported SDK auth with the versioned
key derivation, return the account binding from the same token-validated record,
and refuse unsupported customer MFA before creating SDK challenge rows. Current
AuthRule and SDK token invalidation remain authoritative; native MFA/OTP are not
disabled. The website caches a binding only to detect reassignment: protected
requests revalidate current SDK identity/binding, and a mismatch destroys the old
browser session instead of silently switching accounts. Fresh login is required.

Actual ledger/SDK restart preserved a populated zero-balance ledger account,
credential verifier/binding and SDK signing state; the same synthetic key still
authenticated. No initialization funding or credential reset occurred. Actual TLS
website tests passed reassignment logout, fresh login and forged-account-field
refusal. These are auth/session tests, not completed account-data integration.

Full auth, website and operator race/elpscheck suites plus root vet passed after
these additions. The earlier full-root pass above predates these additions; a
final combined full-root run remains required. Generated-image/ordinary-runtime
activation remains separately unqualified.

## Complete scoped snapshot rendering passed

`website.Config.Balances` explicitly supplies `Service.Balance` to the disposable
host. Login checks the bound account before granting a website session. Each
protected page obtains exactly one complete Service snapshot using the principal
from that request's current SDK validation, never a client selector or a cached
session binding as authority. Public/anonymous pages make no balance calls. No
source callback, principal, token or SDK record enters Lisp.

`internal/website/account_snapshot.go` copies one snapshot to bounded neutral
strings for `Revision.PageWithView`. No available-credit arithmetic is repeated.
The bundled signed page renders every pending-row field (including null terminal
fields), exact integer microunits, enabled state and available credit. Account and
row identifiers are visibly ASCII-quoted without changing stored identity. No
per-user view is retained by the host or revision.

Actual fresh Go/SDK/SQLite + TLS HTTP tests pass full uint64 balance, complete
holds, control/NUL/Unicode account IDs, HTML escaping, independent customer cookie
jars, no cross-account view, reassignment logout, fresh login, missing-account
refusal without reinitialization, and disabled-spending account reads. Concurrent
settlement yields one internally consistent before/after snapshot. Real immutable
one-second hold expiry concurrent with late settlement yields a released hold and
late-settlement refusal, not a fabricated refund receipt.

An injected complete oversized view produces typed HTTP 503 without a partial
list or balance body; this is presentation-boundary evidence, not financial
capacity evidence. Existing VM/render/document limits may also refuse a view
below its input limit; no truncation or fallback is introduced.

The read owner consumes actual accounting failure reports once, including distinct
completion faults. Missing-account HTTP propagation counts once. Unknown raw
errors, panic payloads and foreign-scope responses expose no supplied text.
Classification remains active with observation disabled. A reported accounting
failure followed by a distinct outer panic retains both categories. Later render
or delivery failure neither repeats the read nor invents accounting rollback.

The neutral `pkg/lispsite` API remains stdlib + pinned ELPS only. New view refusals
have an exact closed public error projection and focused regression. Its view
count/key/byte/UTF-8 bounds, copy/mutation and
concurrent VM-isolation tests pass. Intake separately validates the full neutral
diagnostic tuple before mapping it.

After these additions, the full serial root race/elpscheck suite, root vet,
module verification and whitespace checks passed. Active LSP probes of the 12
changed view/host/test Go paths were clean. The opt-in native editor browser test
also passed after its final checker changes.

## Combined browser gate passed

[Combined browser qualification](customer-site-browser.md) now passes actual
stock-admin provisioning of two keys, isolated customer logins/complete snapshots,
inert draft save, live code/CSS publication, retained old CSS, stale-form refusal,
invalid-candidate refusal, code-only baseline restoration and session continuity.
Storage/cookie/origin checks, parent verifier/file/activity inspections and the
unchanged financial snapshot passed. Native read cancellations are recognized
using Chromium's typed signal, not guessed from raw error text; transport failures
remain refusals. The pinned SDK's publication-hook refusal has observed HTTP 500
with the closed no-effect body; no status normalization or authority bypass occurs.

The full serial root race/elpscheck suite, vet/module verification, separate CLI
build and active changed-path diagnostics passed again after this harness was
added. Ordinary-runtime activation, a supported long-running session lifecycle and
the combined generated image still require qualification. No real-data,
integration or deployment approval is implied.
