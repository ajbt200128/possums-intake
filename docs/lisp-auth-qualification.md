# Independent website authentication/session qualification

For the subsequent parent-owned three-page SCS/nosurf/SDK TLS fixture and full
root regression results, see [current implementation evidence](lisp-website-evidence.md).
Historical unchanged-root/CSRF-not-run statements below describe their original
nested or delegated packets, not the later parent fixture. Full production
website authentication acceptance remains open.

The current local preview accepts one public fake key through
`FixtureKeyAdapter`, which supplies a fixed synthetic SDK identity and delegates
to this unchanged adapter. It introduces no real-key lookup, storage or
provisioning contract. See the current evidence for the operator-selected local
scope; historical identity/password tests below still qualify the underlying SDK
bridge, not a proposed two-field customer experience.

## Current decision: keep SCS — concurrency risk accepted

**The operator explicitly accepts stock SCS and its overlapping-request logout/session-resurrection risk.** The former no-resurrection stop gate is superseded; do not add a workaround, custom store, revocation registry, serialization or replacement library for this accepted risk. Explain further security trade-offs and ask before expanding security scope. Remaining SDK auth, CSRF, cookie, token-privacy and runtime requirements are unchanged and still need qualification.

**Historical qualification result:** the proposed ordinary SCS v2 memory-session composition failed the then-required concurrent logout/invalidation gate. Passing reproducer tests below confirm the behavior, not full authentication acceptance. Preserve those tests as evidence of the accepted limitation. No website, alternative session framework, custom store, revocation registry, serialization workaround or service/auth/privacy change was implemented in that qualification packet. ELPS qualification is independent.

On SCS **v2.9.0**, a request that loaded an authenticated session before `Destroy` can commit that session afterward. The stock memory store recreates the deleted entry. With `IdleTimeout > 0`, even a read-only request is automatically marked modified; `LoadAndSave` commits it and reissues the old cookie when that request eventually writes its response. A subsequent request presenting the old cookie sees the restored authentication marker. This is **future access after completed logout**, not merely an already-authorized request finishing.

An actual PB bridge could revalidate the underlying PB token and current authorization on every protected request, as required. That is not evidence of logout revocation: ordinary session destruction does not itself revoke the PB bearer token. The reproducer uses only a boolean stand-in, not a PB token. It demonstrates the accepted session-layer limitation; it does not claim to have exercised actual PB authorization or demonstrated any deployed vulnerability.

**Next step:** resume the remaining supported PocketBase auth-bridge and SCS/nosurf qualification with stock SCS. The accepted concurrency limitation is no longer a blocker. Do not represent risk acceptance as passing SDK MFA/AuthRule, idle expiry, token privacy or other unrun gates.

## Reviewed releases and environment

- Existing root pin: `github.com/pocketbase/pocketbase v0.40.5`, module checksum `h1:D1EKhU96dVII80Rt6lHuCOEBTt2xTVlZG3r2gXYM6VE=`; root dependency files untouched. Reviewed cached `apis/record_auth_with_password.go` and the final-auth portion of `apis/record_helpers.go`, plus existing `internal/operator/handler.go` SDK-router construction. Password controller calls the SDK final response; AuthRule is checked before the auth hook, and MFA is checked inside its finalizer before final token JSON. A pre-MFA hook/token creation is not final authentication. The existing `apis.NewRouter(app)` / `BuildMux` pattern is a supported internal HTTP-handler candidate, **not a qualified website bridge**. PocketBase is pre-v1 with its production-critical/breaking-change warning; no new customer collection was created.
- Selected/tested session release: `github.com/alexedwards/scs/v2 v2.9.0`, public tag commit `ab20b3feb5e9981c1f79cee8a97a289810134163`, module checksum `h1:xa05mVpwTBm1iLeTMNFfAWpKUm4fXAW7CeAViqBVS90=`, go.mod checksum `h1:ToaROZxyKukJKT/xLcVQAChi5k6+Pn1Gvmdl7h3RRj8=`. This is the only nested-module dependency. Reviewed `session.go`, relevant `data.go` lifecycle methods, `store.go` and `memstore/memstore.go`. Library declares Go 1.12.
- Reviewed CSRF candidate only: `github.com/justinas/nosurf v1.2.0`, public tag commit `ec9bb776d8e5ba9e906b6eb70428f4e7b009feee`, module checksum `h1:yMs1bSRrNiwXk4AS6n8vL2Ssgpb9CB25T/4xrixaK0s=`, go.mod checksum `h1:ALpWdSbuNGy2lZWtyXdjkYv4edL23oSEgfBT1gPJ5BQ=`. Reviewed `handler.go` origin/token path and cookie/failure configuration; declares Go 1.12. Public dependency acquired for source review only, not imported or pinned in a module because the earlier gate failed. **No CSRF acceptance claimed.**
- SCS and nosurf release lists were queried through the public Go proxy; v2.9.0 and v1.2.0 respectively were the highest listed releases at inspection. Exact versions above, not moving documentation, determine this evidence.
- Toolchain: existing `.devenv/profile/bin/go` resolves to `/nix/store/3gmc5vqk07d0z5qdqdm3g9cdmbnfnyr7-go-1.27.2/bin/go`; `go version go1.27.2 darwin/arm64`. `.envrc`, `devenv.nix`, `devenv.yaml` and `devenv.lock` were inspected; nixpkgs revision `6510408d147d8c5e6d76043e899e9251756f6a75`. Used the existing pinned devenv profile directly, `GOTOOLCHAIN=local`, project-local GOPATH and `GOWORK=off`; no global installation or `.env`/secret reading. Race build uses the available system C compiler. Dependency acquisition used Go proxy/sum verification; tests/vet/verification ran with network dependency resolution off and `OTEL_SDK_DISABLED=true`.

## Small deterministic synthetic evidence

Files: `qualification/siteauth/go.mod`, `qualification/siteauth/go.sum`, `qualification/siteauth/session_test.go`. This is an independent nested module; root `go test ./...` does **not** run it.

1. `TestStockMemoryLateCommitRecreatesDestroyedSession/explicit_mutation`: idle timeout off; seed synthetic authentication; load two independent contexts; modify a non-auth value in one; destroy the other; prove a fresh load is anonymous; commit the stale context; prove the original token loads authentication again.
2. `TestStockMemoryLateCommitRecreatesDestroyedSession/idle_touch_only`: same ordering with idle timeout enabled and **no handler mutation**; `Load` alone marks the pending context modified. Proves `Destroy`-based explicit invalidation has the same session-layer limitation as logout. It does not simulate a PB credential-change event.
3. `TestStockMiddlewareReadOnlyIdleRequestUndoesLogout`: genuine `LoadAndSave` middleware, `RenewToken` on synthetic seeding, `Destroy` on POST logout, `httptest` HTTPS request/response objects and a channel-controlled overlapping read-only request. Wait until the late request loaded authentication, complete logout and its deletion-cookie response, verify old-cookie protected access returns 401, then release/join the late request. It returns 204, reissues the same cookie, and future old-cookie protected access returns 204. No sleep or scheduler race decides ordering. The sole fixture goroutine is always released and joined, including assertion failures; no listener, network request or detached work.

The middleware test also checks the generated session cookie is `__Host-` named, host-only, Path `/`, Secure, HttpOnly, SameSite=Lax and session-only (no Expires/MaxAge). This is HTTP response-object evidence, **not** a browser/TLS transport/no-JS acceptance test. The synthetic seed handler does not authenticate anyone; no password or PB token enters these fixtures.

Fixtures construct a single `SessionManager` per context lineage using exported fields, stock GobCodec and the **unmodified** `memstore.NewWithCleanupInterval(0)`. Zero interval is a supported test setting avoiding the constructor's default background cleanup goroutine; it does not change `Find`, `Commit` or `Delete`. No cleanup lifecycle or production memory-bound claim follows. SCS's `New` defaults (24-hour lifetime, persistent/non-Secure cookie, no idle timeout) are deliberately not treated as the proposed policy. Fixture lifetime one hour and idle ten minutes are synthetic settings, not approved product lifetimes. A closed `ErrorFunc` replaces raw default logger behavior; assertions never print raw errors, cookies, tokens or session values.

### Why the stock result follows

Versioned SCS source:

- `data.go`: `Load` decodes a separate request snapshot and marks it modified for idle touch; `Destroy` deletes the store entry and resets **only its own** request context; `Commit` serializes the stale snapshot under its original token.
- `session.go`: `LoadAndSave` commits Modified state at first response write (or handler return), then writes a cookie.
- `memstore/memstore.go`: `Delete` removes the map entry; `Commit` unconditionally inserts/replaces it. Mutexes protect map operations, not the multi-request invalidation invariant. No tombstone, generation or conditional update fences a late commit. `store.go` explicitly defines adding/overwriting on commit.

Thus a clean Go race-detector run is compatible with the reproduced **logical** race. Disabling idle timeout would neither satisfy the selected idle-expiry contract nor remove the explicit-mutation case. No such workaround was made.

## Commands and observed results

Run from repository root (standalone inline commands; they touch only this nested module's dependency files when acquisition/tidy is requested):

`GOWORK=off GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org .devenv/profile/bin/go list -m -versions github.com/alexedwards/scs/v2 github.com/justinas/nosurf`

`GOWORK=off GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org .devenv/profile/bin/go mod download -json github.com/alexedwards/scs/v2@v2.9.0 github.com/justinas/nosurf@v1.2.0`

`export PATH="$PWD/.devenv/profile/bin:$PATH" GOPATH="$PWD/.devenv/state/go" GOTOOLCHAIN=local GOWORK=off OTEL_SDK_DISABLED=true; cd qualification/siteauth && go mod tidy && gofmt -w session_test.go && GOPROXY=off GOSUMDB=off go test -race ./... -count=1 -v -timeout=30s`

Observed: two top-level tests, three cases PASS; closed output explicitly states `recommendation FAILS`. No race report.

`export PATH="$PWD/.devenv/profile/bin:$PATH" GOPATH="$PWD/.devenv/state/go" GOTOOLCHAIN=local GOWORK=off OTEL_SDK_DISABLED=true; cd qualification/siteauth && GOPROXY=off GOSUMDB=off go test -race ./... -count=100 -timeout=60s && GOPROXY=off GOSUMDB=off go vet ./... && GOPROXY=off GOSUMDB=off go mod verify`

Observed: 100 repetitions PASS; vet exit 0; all modules verified. No race report.

`git diff --check`

Observed: exit 0. `git diff -- go.mod go.sum` empty. Existing unrelated modifications/untracked files, including `PLAN.md` and the original website plan, were not changed by this packet. No root dependency, service, auth, privacy, schema or gateway edits; no commits/pushes, inference, exports, deployment, real data or customer persistence.

## Bounded PocketBase adapter — local source qualification

The separately authorized implementation adds only `internal/siteauth/auth.go`, `auth_test.go`, `validation_test.go` and this evidence update. Root dependencies and website/session/routing work belong to the parent; they were not edited by this packet. Earlier nested-module SCS reproducers are unchanged. PB remains pinned at **v0.40.5**; this adapter does not import SCS/nosurf or select their policy. Reviewed target releases remain SCS **v2.9.0**, nosurf **v1.2.0**.

Small private Go API:

- `New(app core.App, collection string) (*Adapter, error)` requires an explicit existing collection **name**, non-superuser auth type, zero SDK log retention and disabled customer AuthAlert. It fixes collection ID as well as name; no collection/schema/credential creation or settings mutation. It constructs `apis.NewRouter` / `BuildMux`, unbinds only SDK activity/raw-panic middleware, and consumes dispatch errors before the SDK default error serializer/logger. No `Serve`, listener, `OnServe`, accounting-hook changes or `OperatorApp` call.
- `(*Adapter).Login(ctx, identity, password) (string, Principal, error)` constructs one internal JSON password-auth request, forwards no browser headers/query/collection selector, and consumes the **final** response privately. The returned token and `Principal{RecordID, CollectionID}` are private Go data, never Lisp/render/error data. It accepts neither a pre-MFA hook nor a challenge as success. MFA-enabled customer configuration is unsupported even when its rule would exempt a record: no session grant, no native MFA/OTP disabling. No customer identity/provisioning/recovery contract is selected.
- `(*Adapter).Validate(ctx, token) (Principal, error)` calls SDK `FindAuthRecordByToken(..., core.TokenTypeAuth)`, checks current expected non-superuser collection, then SDK `CanAccessRecord` with current AuthRule. RequestInfo is equivalent to **auth-refresh**: POST, authenticated record, default context, empty body/query, private Authorization header. It does not dispatch refresh or issue a token. Password-only request-data rules therefore cannot authorize later access. This is deliberate SDK request semantics, not an invented disabled-account field or cached authorization.

Inputs are bounded to 255 UTF-8 bytes each, final response to 64 KiB and token to 16 KiB; operations derive a five-second context and observe cancellation before admitting a result. These are local synthetic bounds, not approved Tor usability/capacity evidence. SDK methods without context parameters and arbitrary custom hooks cannot be forcibly preempted; production query/lifecycle bounds and cooperating hooks still need wiring qualification. No detached timeout goroutines, retries or diagnostic-triggered work.

Failures use existing `ledger.Diagnostic`/numeric `failuretype` categories, closed `SITE_AUTH_*` codes, fixed stages/constraints/outcomes/next steps; no enum expansion, observer/export or retained raw causes. Supported typed causes retain their existing numeric projection. PB has untyped claim/token-type errors: these deny with the stage-specific cause-unknown fallback rather than invent a credential cause. No customer session is created by this adapter; SDK auth-side effects may already exist (notably MFA challenge rows). The parent remains responsible for preserving auth outcomes through subsequent session/render/delivery failures.

### Actual synthetic evidence

Tests use an independent supported `core.NewBaseApp` in disposable `t.TempDir()` storage, SDK settings saved with `Logs.MaxDays=0`, and an explicit synthetic email/password collection with AuthAlert disabled and all five public CRUD rules nil. Temporary credentials are fixtures only; no production migration, automatic production credentials, real data, mail, inference or export. SDK bootstrap state is closed at test cleanup.

- `TestSDKFinalPasswordAndNeutralRefusal`: actual SDK final token succeeds and validates; absent identity and wrong password return identical closed diagnostics and no token/principal. No timing-equivalence/enumeration-resistance claim.
- `TestSDKMFAChallengeIsNotFinalAuthentication`: SDK auth hook has already seen a token, but final response requires MFA; no token/principal is returned. A real `_mfas` row proves the SDK challenge ran. OTP/MFA remain enabled; no mail is invoked.
- `TestCredentialAndRuleRevalidation`: deletion, password change, record token-key and collection auth-secret change invalidate credentials; AuthRule nil/restriction/context changes deny. PB v0.40.5 **rotates its collection auth secret when AuthRule changes**. Fresh SDK-minted fixture-only tokens independently prove current `CanAccessRecord` enforcement rather than confusing signature invalidation with rule evaluation. A refresh-compatible request-info rule succeeds; password-context-only rule denies subsequent validation.
- `TestSDKAuthRuleLoginAndOtherTokens`: final password login preserves nil/restricted AuthRule; expired auth, password-reset type and other-collection tokens cannot authorize. Superuser collection construction is refused.
- `TestHostileSDKAndFinalResponse`: hostile SDK errors/panics before and after final response, malformed/oversized response and fabricated success cannot release a token/principal. Detailed diagnostics contain no fixture identity/password/hostile sentinel. `_logs` and `_authOrigins` remain empty in the selected fresh-fixture paths.
- `TestValidateUnexpectedSDKFailuresStayClosed`: supported App-boundary injected token lookup/rule errors and panics retain stage-specific closed unknown classifications; no raw exception survives.
- `TestBoundsCancellationAndQuietConfiguration` and `TestCancellationAfterSDKFinalResponseDoesNotReleaseToken`: bounded input, pre-cancelled calls, cancellation after SDK final response and enabled AuthAlert fail closed without a returned credential. No session or native MFA policy is rewritten.
- `TestActualTLSLoginFinalResponsePrivacy`: **real local TLS** via `httptest.NewTLSServer` and its certificate-verifying client; actual SDK password login inside the handler. Success returns only 204, absent/wrong credentials the same neutral 401/body; no PB token/identity/password or cookies in those HTTP responses. This is an auth-only fixture handler, not website/SCS/nosurf or browser acceptance.

Pinned Go **1.27.2**, project-local GOPATH, `GOTOOLCHAIN=local`, dependency resolution off, telemetry disabled:

`GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true .devenv/profile/bin/go test -race ./internal/siteauth -count=1 -timeout=90s`

Observed final run: PASS, no race report (27.063s). Earlier draft tests exposed a wrong expired-token fixture assumption (negative duration selects the SDK default) and the SDK AuthRule-secret rotation; corrected with an already-expired nanosecond token and current-key rule-isolation fixtures. Those draft failures are not passing evidence.

`GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true .devenv/profile/bin/go vet ./internal/siteauth`

Observed: exit 0.

`GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true .devenv/profile/bin/go test ./internal/operator -run 'TestLoggingOffDefaultAndNativeActualHTTP|TestNativeSQLCannotReenableSDKAuthActivity|TestNativeLoggingSettingsPersistOffAcrossRestart' -count=1 -timeout=60s`

Observed: PASS (1.391s). These existing actual-ledger/native tests qualify their original logging/settings/restart boundaries, **not** customer-adapter integration. The new adapter deliberately does not obtain the app through `OperatorApp`; production reuse/accessor and actual-ledger customer fixture wiring remain with the parent. Independent fixture logging-off evidence cannot close that gap. No whole-SDK swallowed-error/stdio completeness claim; no historical snapshot scrubbing claim.

Final focused non-race repeat `go test ./internal/siteauth -count=1 -timeout=60s` with the same pinned/offline environment passed (3.149s). Active LSP error probes covered all three new Go files: clean=3, no compiler diagnostics. `git diff --check` exited 0; new Go files are gofmt-formatted. No full root suite, compiled artifact or parent-owned session tests were run by this packet.

## Historical adapter-only gaps (superseded locally below)

At the end of the adapter-only packet, full website authentication remained **UNQUALIFIED**. This adapter packet does not qualify SCS/nosurf final composition, idle/absolute expiry, fixation, CSRF/origin/missing cookies/stale forms, stock-store errors, restart/logout cleanup/memory bounds, no-JS/Tor Browser, actual compiled-service/generated-image or complete wire/stdio/privacy behavior. The real TLS test is auth-only, not a browser flow. The earlier stock SCS concurrency behavior remains explicitly accepted and unchanged. Parent-owned wiring must keep tokens private, revalidate on protected requests, cap session lifetime to token validity and preserve known outcomes; no custom store/revocation/serialization/replacement is requested.

The adapter packet's policy review covered full `docs/lisp-website-plan.md`, `AGENTS.md`, `README.md`, `PRIVACY.md`, `docs/operator-dashboard.md`, and sibling `AGENTS.md`, `SPEC.md`, `OVERALL_PLAN.md`, full `PRIVACY.md`, plus historical `7ee4604:OVERALL_PLAN.md` (superseded historical context only). Applicable boundaries: [intake forbidden defaults](../PRIVACY.md#forbidden-collection-and-defaults), [typed failure ownership](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates), [gateway data boundaries](../../possums/PRIVACY.md#data-handling-boundaries). The retained SCS tests use transient synthetic session state; new PB fixtures use disposable temporary SDK databases containing synthetic credential hashes, settings and MFA state until cleanup. No project telemetry/export is instantiated; the SDK logger is constructed with retention off, not absent. Go caches and native development-host history remain separate persistence boundaries; this is not a whole-host transience or secure-erasure claim.

## Completed disposable HTTP/session/diagnostic qualification

This section supersedes the historical adapter-only session/HTTP gaps above **at
local source/test scope only**. Production/customer identity, persisted website
publication, real browser/accessibility/Tor, generated-image and deployed-service
qualification are not established. Parent-owned `cmd/intake` local TLS/synthetic
wiring and the separate runtime/render qualification have their own evidence;
this packet did not edit those files or select production/customer policy.

### Integration and lifetime contract

`NewFixture(revision, auth, fallback, Config)` retains its signature.
`Config.FailureObserver` is an optional existing `failureowner.Observer`; use keyed
configuration literals. No observer/exporter is created here, nil is supported,
and there are no success/activity counts. Existing numeric categories stay
**1–39**. No accounting/native fallback observation is added. The SDK adapter and
runtime currently have no observer; the outer website boundary owns their HTTP
failure propagation, not a second SDK/runtime count. An integration that adds
lower-level observers must retain this sole-owner arrangement.

The selected simplest local-only lifecycle remains **disposable process-owned
sessions without a cleanup worker**, using the SCS literal and unmodified
`memstore.NewWithCleanupInterval(0)`. No `Close`/cleanup goroutine, custom store,
revocation registry, serialization or replacement was added. The server owner
must stop admission/listening and join active requests before releasing the host.
Expired entries cannot authorize access but are retained until host release and
Go garbage collection/process exit; this is not secure erasure. Memory is not
bounded across arbitrarily many sessions or an indefinitely running local server.
Use only disposable synthetic sessions and end the process when finished. No
long-running memory/cleanup qualification or product lifetime approval is claimed.
The default SCS cleanup/StopCleanup startup-race concern is not worked around or
relabelled as passing. **Stock SCS's accepted overlapping-logout/invalidation
resurrection risk is exactly unchanged.** Its independent nested reproducers still
pass, demonstrating the accepted limitation rather than revocation guarantees.

TLS is checked directly (`r.TLS`), not inferred from forwarded headers. Customer
session/CSRF cookies remain Secure, HttpOnly, host-only `__Host-`, Path `/`,
SameSite=Lax and session-only. No JWT enters HTML, cookies, redirects, Lisp,
browser storage or diagnostics; only the opaque SCS cookie reaches the browser.
Native/accounting fallback routes remain outside the customer session boundary.
Synthetic SDK databases contain synthetic hashes/auth state until test cleanup;
this does not approve real customer persistence.

### Boundary, projection and ownership inventory

| Boundary | Closed diagnostic / numeric fallback | Sole owner and outcome rule |
| --- | --- | --- |
| Path/method/form/revision/TLS refusal | `SITE_ROUTE_REFUSED`, `SITE_METHOD_REFUSED`, `SITE_FORM_REFUSED`, `SITE_REVISION_STALE`, `SITE_HTTPS_REQUIRED`; RequestInvalid | HTTP admission/route owner, once before auth action |
| CSRF / capability refusal | `SITE_CSRF_REFUSED`, `SITE_ACTION_REFUSED`; PolicyRefused; `SITE_AUTH_REQUIRED`; AuthenticationRefused | CSRF or independent action owner, once; public Lisp policy cannot grant logout |
| Dynamic admission/resource refusal | `SITE_BUSY`; AdmissionRefused; missing page/asset uses RequestInvalid | HTTP/asset owner, once; no activity counts |
| SDK final auth/current credential check | closed `SITE_AUTH_*`; AuthenticationRefused, PolicyRefused, Cancelled/DeadlineExceeded or NativeAuthUnknown | Website adapter-call owner; preserve supported numeric causes and allowlisted SDK stages, never arbitrary diagnostic fields |
| SCS load/renew/delete/commit | `SITE_SESSION_UNAVAILABLE`; NativeReadUnknown for load, NativeWriteUnknown for renew/delete/commit, or preserved typed numeric cause | Direct operation or stock SCS ErrorFunc, once; closed load/renew/delete/commit stages; cause-unknown fallback does not invent storage/revocation success |
| ELPS page/HTML rendering | `SITE_RENDER_UNAVAILABLE`; ResponseEncodingUnknown fallback or preserved typed cause | Page boundary, once; known auth effects are not replaced with `no_action` |
| Embedded resource read | `SITE_ASSET_UNAVAILABLE`; NativeAssetUnknown | Asset boundary, once; no raw filesystem exception |
| HTTP write/error/short-write/panic | `SITE_DELIVERY_UNAVAILABLE`; ResponseDeliveryUnknown | Outer response writer, once per response, distinct from primary failure; no retry/report recursion |
| Unexpected dispatch/auth/store/render panic | stage-specific closed unavailable diagnostic; RequestDispatchUnknown only if no narrower boundary is known | Outer request recovery, no panic value/log/stack; no replay |

All authored HTTP errors are `ledger.Diagnostic` before presentation, with code,
stage, constraint, outcome and actionable next step. Known types survive wrappers;
unknown error text and hostile `As`/`Error` methods cannot become diagnostic data.
No new enum, diagnostic persistence, logging, telemetry construction/export or
failure registry exists. A failed primary operation plus independently failed
render/delivery can count separately; wrapping/presentation of that same primary
does not recount it. Counts receive numeric kinds only. Classification still runs
with telemetry disabled and a nil observer. There is no promise that an
undeliverable response yields a user-visible report or that every exception is
exported. Process abort/OOM/termination, framework-swallowed failures and arbitrary
non-cooperating external code remain outside this interception guarantee.

SCS v2.9.0 calls its error callback on commit failure but can then continue writing
the originally requested status/body. The small outer response guard writes a
closed 503 directly (avoiding recursive session commit), removes the original
Location and suppresses subsequent original response delivery. It changes no
stock store/session concurrency semantics. Successful SDK auth is recorded as
`sdk_auth_completed_website_session_unconfirmed` before later session work;
`Destroy` success records `session_destroyed_not_credential_revocation`. A later
commit/render/delivery failure cannot turn either into `no_action` or claim
credential revocation. Observed write failures are classified without a second write or replay.
`net/http.WriteHeader` has no error result; buffered flush/connection failure
after handler return can remain outside this boundary. A 303 alone is not proof
of browser receipt. Session persistence/cookie delivery after interrupted login remains
unconfirmed; a successful follow-up protected request is separate evidence.

### Synthetic test evidence

Owned implementation: `internal/website/handler.go`, `assets.go`,
`diagnostic.go`. Owned tests: `handler_test.go`, `session_test.go`,
`expiry_test.go`, `diagnostic_test.go`, `diagnostic_projection_test.go`.

- Actual certificate-verifying local TLS client: public/login/signed home,
  final SDK auth, protected access and POST logout; cookie flags, CSRF/origin,
  missing origin/cookies, stale forms, neutral absent/wrong identity, reserved
  fallback, asset allowlist and exact pinned fonts. No JavaScript is executed.
- `TestSessionFixationRestartAndExpiry`: a real stored anonymous token rotates
  at login and its old entry disappears; stock Commit selects the idle/absolute
  minimum; an expired stock entry cannot authorize; a new host/store rejects the
  old cookie. No process-restart artifact claim follows from new-host evidence.
- `TestActualIdleAbsoluteAndSDKTokenExpiration`: actual short idle expiry,
  past absolute deadline and an already-expired SDK-minted auth token each deny
  subsequent protected HTTP access. `TestTokenExpiryCapsSessionAndRejectsExpiredGrant`
  separately isolates host expiry capping/rejection with a stub; it is not SDK
  signature-verification evidence. Revalidation through the actual SDK denies
  deletion, password/token-key change and AuthRule removal and destroys the
  current session. Concurrent resurrection remains accepted, not fixed.
- Public Lisp logout policy cannot authorize anonymous logout. Missing,
  duplicated, unknown, oversized identity/password and invalid UTF-8 fields fail
  before login. Identity/password bounds are 255 UTF-8 bytes each, total body
  4096 bytes; no product/Tor usability approval is implied.
- Hostile SDK/auth and stock-store boundary errors/panics remain closed in HTTP
  and captured stdout/stderr/standard logger output (empty for selected negative
  fixtures). Tests inject stores only as test fault seams; production still uses
  the stock memory store. Find/Commit/Delete errors and panics, typed cancellation
  causes and hostile diagnostic fields are covered. Commit failure emits one
  503 without the original redirect/page/cookie and is observed once.
- Write errors, short writes and panics preserve prior login/logout effects and
  count delivery once. A session-commit failure plus error-response delivery
  failure counts exactly two independent failures. Rendering after an auth
  failure counts that primary once plus its distinct render failure; a dedicated
  post-action render fixture preserves known SDK-auth/logout outcomes. Normal
  successful login/logout uses a 303, not that synthetic render branch.
- No auth/browser storage or bearer secret is present in tested public HTML;
  selected fresh actual-SDK login/logout flows leave `_logs`/`_authOrigins` empty.
  No whole-SDK/whole-process stdio completeness or secure-erasure claim follows.

Pinned Go 1.27.2, local dependency cache, resolution off, telemetry disabled:

`env GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true .devenv/profile/bin/go test -race -tags elpscheck ./internal/website ./internal/siteauth -count=1 -timeout=150s`

Observed final rerun: PASS website (42.657s), siteauth (28.266s); no race report. This includes the final session-stage projection, captured store-output checks and stored-session-effect assertions after failed delivery.

`env GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true .devenv/profile/bin/go vet -tags elpscheck ./internal/website`

Observed final vet: exit 0. The unchanged nested stock-SCS reproducers also
passed once under `-race` (1.450s). All eight owned Go files passed active LSP
error probes; no diagnostics. Owned Go files are gofmt-formatted and
`git diff --check` passed. Parent owns full-root, compiled CLI/local server and
image evidence; no such acceptance is inferred here.

Applicable policies: [intake forbidden defaults](../PRIVACY.md#forbidden-collection-and-defaults),
[typed ownership](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates)
and [gateway data boundaries](../../possums/PRIVACY.md#data-handling-boundaries).
No root dependency, siteauth, accounting, PLAN/AGENTS, gateway, production schema,
resource, deployment, commit/push, inference smoke or export change was made by
this HTTP packet. Native development-host history/caches and the operator's
browser/history/password-manager boundaries remain independent persistence risks.
