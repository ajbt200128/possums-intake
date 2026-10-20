# A small Lisp-authored customer website

**Planning only.** A quiet, readable website: ordinary HTML, one small stylesheet, three pages. No browser JavaScript, no frontend framework, no simulated terminal. Change trusted page code in the database; change Go only to introduce a backend capability.

**Interpretation of “first three pages”: public home → login → signed-in home.** The signed-in home initially says that the customer is signed in and offers sign-out. It contains no balance, recent statement, payment or inference interface. Login errors remain on the login page; logout is a POST action, not a fourth page.

This is a newly requested **future intake website and customer-auth direction**, not an account of today's implementation. Intake is to become the customer authentication source of truth; a future gateway will defer to intake. That does not restore a gateway Web UI, select an integration protocol, migrate existing credentials or authorize gateway edits. Operator superuser authentication, customer authentication and the custom accounting shared key remain distinct. Customers never automatically become superusers.

This document authorizes no implementation, dependency acquisition, schema migration, actual new persistence, inference/payment tests, deployment or production rollout. [Repository guidance](../AGENTS.md), [README](../README.md), [intake privacy](../PRIVACY.md) and [current operator evidence](operator-dashboard.md#trusted-native-admin-policy-reversal--current-local-packet) remain authoritative for existing work. The gateway's [guidance](../../possums/AGENTS.md), [specification](../../possums/SPEC.md), [current roadmap](../../possums/OVERALL_PLAN.md) and full [privacy policy](../../possums/PRIVACY.md) were read. Its `7ee4604:OVERALL_PLAN.md` was read as historical context only: weak account numbers, post-completion debit, browser gateway, buffered inference and absolute privacy claims are not adopted.

## 1. Design in five decisions

1. **Make documents, not an application shell.** Left-aligned prose, semantic headings, unmistakable links and native forms. Beauty comes from type, contrast and space.
2. **One Go host, one small trusted bundle.** Reuse the current service/PocketBase app and lifecycle. No second server, service, queue, browser IDE or plugin system.
3. **Lisp declares the page and its configurable choices; Go validates, authenticates and renders; user preferences are just data.** Routes, public/session requirements, composition, option defaults/documentation and style values are editable. Password handling, sessions, CSRF, escaping, capability authorization and accounting are not Lisp policy.
4. **Load before serving; isolate every request.** Evaluate `init.el` when preparing a revision, eagerly load its helpers/pages, resolve routes and publish an ELPS template. Instantiate a fresh VM from that template for every request. Do not re-evaluate source or lazy-load files for each request.
5. **Cancellation and request isolation are mandatory.** Select ELPS instead of SteelSeries GoLisp. Qualify cancellation, execution limits, mutable-state isolation and the SDK-to-browser auth bridge before feature work. Do not substitute serialized shared state, abandoned goroutines or an interpreter fork for these requirements.

## 2. Grounded reuse and its limits

### Existing local facts

- [go.mod](../go.mod) pins **PocketBase v0.40.5**. PocketBase remains pre-v1; its upstream warning about production-critical use and breaking changes still applies. Existing ledger evidence does not qualify a new customer-auth surface.
- [internal/operator/handler.go](../internal/operator/handler.go) builds the native SDK router with `apis.NewRouter` on the same app, without `apis.Serve`, `OnServe`, cron or another listener. It forwards paths outside `/api…` and `/_…` to the accounting handler. Its CSP permits the separate JavaScript native UI; it is not the customer-page CSP.
- [internal/httpapi/handler.go](../internal/httpapi/handler.go) owns `/healthz` and the shared-key accounting POST routes. [internal/ledger/operator.go](../internal/ledger/operator.go)'s `OperatorApp` deliberately removes a named native account-validation hook. **Do not call it merely to obtain an app for the website.** Future wiring must preserve the selected native-admin behavior without making customer enablement change accounting hooks.
- [internal/ledger/storage.go](../internal/ledger/storage.go) enforces SDK logging off and disables AuthAlert origin recording/alert mail. [cmd/intake/server.go](../cmd/intake/server.go) owns bounded HTTP handling, quiet server errors and joined shutdown. Reuse these boundaries; the existing short local timeouts are not proof of usability over Tor.
- No website Lisp runtime, bundle publication or customer cookie-session implementation is established by these files. The native operator UI remains a separate JS control plane, with its existing page-memory auth/preferences/drafts/file-token policy unchanged. Preserve unrestricted trusted stock administration, including financial/schema edits, deletion and manual backups/restore/token downloads; this does not make financial restore safe.

### ELPS: selected runtime, with required qualification

The operator selected [**Luther Systems ELPS**](https://github.com/luthersystems/elps) instead of SteelSeries GoLisp because **cancellation and individual state per request are required**. ELPS is an embedded Lisp-1, not Emacs Lisp. `init.el` remains a deliberate filename convention. Historical GoLisp product use does not outweigh these hosting requirements; neither GoLisp nor Zygomys is an automatic fallback.

Current upstream documentation describes actual embedding facilities, not our proposed website framework:

- [VM templates](https://github.com/luthersystems/elps/blob/main/docs/templates.md): load a program in a quiescent source environment, publish it with `lisp.NewTemplate`, then instantiate independent VMs with `Template.NewVM`. Mutable interpreter state is reconstructed per VM; context, active stacks and step accounting are not inherited. `lisp.VMWithContext` binds request context at instantiation.
- [Embedding](https://github.com/luthersystems/elps/blob/main/docs/embed.md): context-aware evaluation/loading, host callback context checks and step charging. A host callback doing blocking work must itself honor cancellation; a VM cannot forcibly stop arbitrary Go code.
- [Language reference](https://github.com/luthersystems/elps/blob/main/docs/lang.md): configurable execution limits and built-in function/macro documentation strings. These do not supply `defcustom`, website routing, rendering or a preference store.

These are moving upstream references, not a selected dependency pin or local acceptance evidence. First choose a reviewed release/commit containing the required features; do not assume an older release implements the current docs.

**Hosting model:** prepare one side-effect-free template per published bundle; create a new VM for each request and bind its deadline/cancellation plus fresh safe view/preference data. No VM pool or serialized shared interpreter. Copy the completed node tree into Go-owned render data, then discard request VM references. Immutable source and template metadata may be shared; mutable request state may not.

Audit every registered host callback and native payload. ELPS cannot inspect hidden mutable Go closure state. Bind request services per VM rather than capturing a principal, request, mutable account record or preference map in shared callbacks. Use template admission policies deliberately, not approve-all predicates. Share only audited immutable values; initialization receives no auth/accounting or general file/network/process capability. Expose a minimal runtime library, no mutable testing registry, eval of customer input, debug REPL, printing/logging, runtime file/network loads or background work. Replace diagnostic writers with a quiet boundary; retain closed typed transient failures without raw ELPS errors, source snippets or stacks.

**Cancellation contract:** request cancellation/deadlines and configured step/stack/nesting/allocation-size limits must terminate supported Lisp evaluation and cooperating host work promptly, release resources and leave other requests usable. Apply separate bounded contexts to publication/loading and limits to input, concurrency and output too. Interpreter allocation-size limits are not a whole-process memory quota. A handler timeout or abandoned goroutine is not cancellation; no accounting/action success is inferred or retried when delivery is interrupted. Initial request-time Lisp is presentation-only.

**STOP gate:** on the exact pin prove concurrent and sequential isolation, mutable closures/containers, template-source independence, context cancellation of loops/recursion, step exhaustion, blocking callback cancellation, candidate rejection and cleanup/shutdown. Test that another VM continues normally after one is cancelled and that a new VM sees defaults, not a prior user's data. Validate the reader/source-library sharing contract and callbacks, not merely the absence of races. If these requirements cannot be met with small supported embedding, stop and revise runtime choice; do not relax them or quietly add a fork/process supervisor.

## 3. The small host boundary

A future outer dispatcher preserves existing endpoints **before** considering a bundle route. Reserve the current `/api…` and `/_…` namespaces (including native UI/control/assets), `/healthz`, `/accounts/provision`, `/credits`, `/reservations` and its subtree, and `/balance`; reserve `/attestation` for control-plane use even though this is not a claim that it is currently served. Use `/_site/` for host-owned website assets/publication plumbing; it is intercepted explicitly before the current `/_…` native dispatch. No new public publication endpoint is necessary initially.

Validate canonical method/path pairs at publication; reject collisions, ambiguous encodings and duplicate route names. Never let Lisp shadow reserved routes, and never convert a refused reserved request into a website request. Ordinary website routes are **data**, not one Go handler per page. For example, moving signed-in home from `/home` to `/welcome` changes the bundle only.

- **Go:** dispatch; resolve a customer principal; enforce route session requirements; execute a closed backend action/capability; validate forms; construct safe render data; escape HTML text/attributes; enforce URLs, headers and CSS restrictions; call the existing ledger Service if a later slice is authorized.
- **Lisp:** register ordinary GET routes and approved POST actions, choose `public` or `session` page policy, and compose a small semantic node tree. It sees only necessary safe view values. Login passwords and SDK/session tokens bypass Lisp entirely.
- **Independent capabilities:** any future account read/write derives identity from the authenticated principal and checks ownership regardless of a page's `public` declaration. No arbitrary SQL, collection queries, account-ID selectors or shared-key forwarding. The initial three pages need no financial capability. Accounting keeps exact arithmetic, transaction/idempotency/expiry and shared-key contracts unchanged. No permission-tier framework is introduced.

Render through Go-owned `html/template` templates for a small node vocabulary, not concatenated HTML or Lisp-provided `template.HTML`. Keep semantic elements, labels and form structure intact; do not accept event attributes, raw markup, arbitrary resource URLs or script nodes. Render a bounded complete document before writing it. A render/delivery failure must not replay a successful auth action.

### Illustrative bundle — proposed host API, not a runnable ELPS framework

Every `site:*` and `ui:*` name below would be a **new, deliberately small host API**. ELPS supplies the language and VM, not these UI primitives, routes or bundle loading. The semantic document tree is our small buffer model, not an Emacs buffer/overlay emulator. Examples use ELPS-style `defun`; host registration details remain to be qualified.

```lisp
;; init.el — evaluated once when this revision runtime is prepared
(site:load "shared.el")
(site:load "options.el")
(site:load "pages.el")
(site:load "routes.el")
(site:load "style.el")

;; shared.el
(defun page (title content)
  "Compose a page with a semantic heading and shared document shell."
  (ui:document title
    (ui:main (ui:h1 title) content)))

;; pages.el — request-specific messages/forms are Go-owned render slots
(defun public-home (view preferences)
  (page "Possums"
    (ui:group
      (ui:p "Private inference, with clear boundaries.")
      (ui:link (site:url 'login) "Sign in"))))

(defun login-page (view preferences)
  (page "Sign in"
    (ui:group
      (ui:notice view)
      (ui:login-form 'sign-in)
      (ui:link (site:url 'public-home) "Back to Possums"))))

(defun signed-home (view preferences)
  (page "Home"
    (ui:group
      (home-introduction preferences)
      (ui:post-form 'sign-out "Sign out"))))

;; routes.el — names keep links/actions independent of paths
(site:get 'public-home "/"      'public  'public-home)
(site:get 'login       "/login" 'public  'login-page)
(site:get 'signed-home "/home"  'session 'signed-home)
;; name, path, page policy, Go action, success destination, error page
(site:post 'sign-in  "/login"  'public  'auth/login  'signed-home 'login)
(site:post 'sign-out "/logout" 'session 'auth/logout 'public-home 'signed-home)

;; style.el — values only; Go emits the small stylesheet
(site:style '((paper "#f7f6f2") (ink "#202320")
              (link "#174f3b") (border "#73766e")
              (space-unit "0.5rem")))
```

`site:load` resolves only a named text entry from the captured revision, not disk or network; reject missing/cyclic/duplicate loads. Eager preparation checks every page entry name, action and destination. Export only immutable routing/schema metadata to Go; resolve the entry name in each new request VM, never invoke a retained function/closure from the source environment. Registration is initialization-only. `ui:document` adds language, viewport, escaped title and this revision's stylesheet URL. Go form nodes generate labels, native inputs, action URLs, hidden CSRF and revision fields; no session/bearer credential is a hidden field. The login identity label/fields are finalized only after the credential decision below.

**GET:** reserved-path check → capture active revision → load/validate customer session → require session if declared → instantiate a fresh request-context ELPS VM from that revision's template → invoke its prepared page function with safe view data and validated effective preferences → Go render → release VM state. Anonymous access to `/home` redirects to the bundle's login route without encoding a credential or arbitrary `return_to` URL.

**POST:** bounded URL-encoded body, not multipart/upload → session and CSRF validation, including login/logout → match current revision/action → Go validates fields and independently authorizes action → supported auth/session operation → 303 to its named destination. Refusals render the appropriate existing page with a closed message, empty password and useful status. Stale-revision forms fail before effects and ask for a fresh page; never dispatch stale or arbitrary historical handlers. GET/HEAD never change authentication or money. Wrong methods get a closed 405/404, not an eval path.

### Customize-style choices, documentation and preferences

**Lisp declares the page and its configurable choices; Go validates, authenticates and renders; user preferences are just data.** Use one small option schema, not a second UI system or executable per-user configuration.

The following `defgroup` and `defcustom` declarations are **proposed application macros**, not built-in ELPS or imported Emacs code. They register initialization-time metadata that Go validates alongside routes. The example is deliberately non-financial and does not add a fourth page:

```lisp
;; options.el — loaded by init.el before pages/routes
(defgroup home
  "Choose how your signed-in home is presented.")

(defcustom home-show-introduction true
  "Show the short introduction on your home page."
  :type 'boolean
  :group 'home)

(defcustom home-section-order '(introduction navigation)
  "Order the approved sections on your home page."
  :type '(ordered-subset introduction navigation)
  :group 'home)

;; A future page can consume effective values through a small host accessor.
;; Preferences never grant access to a backend capability.
(defun home-introduction (preferences)
  "Render the introduction only when the effective preference enables it."
  (if (site:option preferences 'home-show-introduction)
      (ui:p "You are signed in.")
      (ui:group)))
```

All page entry functions receive `(view preferences)`; `home-introduction` is loaded from `options.el` in the example. The ordered-section option illustrates future composition; the first signed-in page only consumes the introduction toggle and always retains sign-out.

Each declaration has a stable name, group, default, closed type/constraints and plain-text documentation. Start with booleans, finite choices and bounded ordered subsets of approved section names; reject invalid defaults, duplicate names and unknown types at publication. Go can generate native labeled fields/help from the same schema when a customization interface is separately authorized. Ordinary page composition and generated option controls use the same node renderer, not a generic form framework. No arbitrary predicates, backend selectors or code values in preference submissions.

- **Published code:** option declarations, documentation, defaults and page composition belong to one bundle revision. Public/session route requirements are trusted application policy, never customer preferences.
- **Effective preferences:** Go resolves the authenticated user's permitted overrides against the captured revision's schema and defaults, then passes a fresh data snapshot into that request VM. Never eval submitted text, intern user-controlled variable names or let a preference enable unauthorized sections/data. Initial three-page acceptance uses defaults only—no preference collection, editor or extra settings route yet.
- **Later saved preferences:** explicit scoped persistence approval is needed before adding a minimal account-scoped option-value store. A supported Go POST handler revalidates names/values against the active schema and checks principal ownership/CSRF. No arbitrary account IDs, per-user source files, credentials, request history or sensitive free text. This is distinct from stock operator page-memory preferences and does not change their policy.
- **Revision compatibility:** missing or incompatible values resolve to the current declared default; unknown/removed names are ignored. Never silently rewrite stored data during GET. Removed values can remain in storage until an explicitly reviewed save/cleanup policy; document this retention honestly. A stale form is refused before effects. Code rollback re-resolves preferences against that code's schema; it does not roll back customer data.
- **Documentation:** `;;` comments explain source decisions and do not become UI content. ELPS function/macro docstrings document helpers; the explicit `defcustom` string is option help metadata. Render only deliberately exposed help as escaped plain text with proper field associations. Do not dump interpreter introspection, private source or raw error traces to customers. No Markdown or raw HTML engine is needed.

## 4. Authentication: reuse PocketBase, fill only the browser gap

### What the pinned SDK actually supplies

[PocketBase authentication documentation](https://pocketbase.io/docs/authentication/) explicitly describes stateless bearer-token APIs and no server logout endpoint. Clearing a browser token does not revoke a copied token; auth refresh does not invalidate the previous token.

Inspection of the locally cached **v0.40.5** source confirms:

- [`apis/record_auth_with_password.go`](https://github.com/pocketbase/pocketbase/blob/v0.40.5/apis/record_auth_with_password.go) binds native form fields, looks up configured identity fields, validates passwords and performs a dummy password check for absent records. The controller is unexported; do not invent a supported `Login()` Go method.
- [`apis/record_helpers.go`](https://github.com/pocketbase/pocketbase/blob/v0.40.5/apis/record_helpers.go)'s `RecordAuthResponse` enforces AuthRule and runs MFA before the final JSON token response. An `OnRecordAuthRequest` hook occurs **before** the final MFA checks: observing that hook is not proof of completed authentication.
- [`apis/middlewares.go`](https://github.com/pocketbase/pocketbase/blob/v0.40.5/apis/middlewares.go) loads an Authorization header, not a customer cookie session. [`core/record_query.go`](https://github.com/pocketbase/pocketbase/blob/v0.40.5/core/record_query.go)'s `FindAuthRecordByToken(..., core.TokenTypeAuth)` checks record existence, token type, signature and expiry. It is not by itself every account/route authorization check.

**One recommendation:** PocketBase customer auth collection + **SCS v2 memory sessions + nosurf form-CSRF middleware**, subject to the early auth gate. Keep the existing `net/http` host; add no general web/auth framework. Use PB's password/token/authentication machinery, SCS's session lifecycle and nosurf's masked form tokens. Each fills a distinct demonstrated gap. Pin compatible reviewed releases before implementation; no module change is made here.

Why not simply put a PB JWT in a cookie? That is fewer pieces, but cookie deletion is not revocation and copied tokens survive until expiry/invalidation. Why not replace PocketBase with a second auth framework? It duplicates identity/password/reset/MFA responsibilities. SCS alone does not supply CSRF; nosurf supports no-JS hidden fields and a closed failure handler. Do not handroll these protocols. Standard-library origin protection is useful but is not a replacement for the selected explicit form-token contract.

### The bridge that must be proven, not assumed

Use a **separate customer auth collection**, never `_superusers` or the financial account collection. Initially keep public registration/CRUD unavailable with supported collection rules; trusted native admins retain their existing authority. Customer provisioning, identity type, recovery and MFA enrollment remain explicit product decisions, not a reason to add email/OAuth flows now.

Candidate bridge: dispatch a bounded, internally constructed request through the supported SDK-built auth HTTP handler for the **fixed customer collection**, consuming its final response privately in Go. No loopback network request or new listener, no client-selected collection/query/header forwarding. Reuse the full SDK validation/AuthRule/MFA path rather than calling `ValidatePassword` and issuing a token manually. Establish a browser session only after the final successful authentication result, never from a pre-MFA hook or challenge. This adapter is project work still needing qualification, not existing PocketBase middleware. Preserve the existing quiet SDK/error boundaries inside it; configure SCS/nosurf failure callbacks to produce closed transient diagnostics, never raw library errors or default error logging.

SCS stores only the necessary PB auth token and minimal customer binding in memory; the browser receives an opaque SCS cookie, **not the PB token**. On protected requests validate the PB token through the SDK, the expected customer collection and current customer authorization/disabled policy. Do not trust an old cached record or client-selected identity. Invalid/deleted/expired credentials fail closed. Cap session lifetime to underlying credential validity; initially require a fresh login rather than silently refreshing indefinitely. Qualification must cover password/token-key changes and current AuthRule/account disablement; token verification alone does not enforce all of these.

Use SCS `RenewToken` on successful login, `Destroy` on CSRF-protected logout, idle and absolute expiry, and explicit invalidation on failed credential revalidation. Memory-store restart logs everyone out; a code-bundle publish does not. Set no “remember me” cookie. **Latest operator decision:** keep stock SCS and explicitly accept that an overlapping request may commit an old session snapshot after logout/invalidation, recreate the session and reissue its cookie, permitting future access while underlying credentials remain valid. This supersedes the earlier no-resurrection stop gate; it does not claim that SCS guarantees revocation. Preserve the reproducer and [qualification evidence](lisp-auth-qualification.md), and retain SDK credential/current-authorization revalidation. No custom store, revocation registry, serialization workaround or replacement library is requested for this accepted risk. A request already authorized before logout is not retroactively undone. Explain further security trade-offs and ask before expanding scope or adding security machinery; keep the smallest supported implementation within the remaining agreed boundaries.

For unsupported customer MFA/configuration, deny session creation with a closed next step; never disable native MFA/OTP to force the three-page flow through. Do not select mail providers, reset flows, recovery credentials or MFA product behavior here. No secrets pass to Lisp, HTML, redirects, diagnostics or logs. A masked CSRF token is intentionally present in a hidden form field; it is not a login credential.

### Tor and no-JS contract

- HTTPS; server-set, host-only **Secure, HttpOnly, SameSite=Lax** session and CSRF cookies, with explicit scope and session-only persistence. Prefer `__Host-` names where library configuration supports them. Do not inherit SCS/nosurf persistence defaults. Native operator cookies/tokens remain separate; do not interpret customer cookies on native/accounting routes.
- Tor Browser supports first-party cookies during browsing. New Identity clears them; browser exit normally ends their lifetime. No promise of surviving New Identity/restart, and no IP/circuit/user-agent binding or fingerprinting. If all cookies are disabled, the form explains that first-party cookies are required for sign-in and CSRF; no redirect loop or URL-token fallback. Public home remains usable.
- Qualify real Tor Browser with JS disabled/Safest, including its header behavior. nosurf checks origin/fetch metadata and can require Referer as fallback. Propose `Referrer-Policy: same-origin` for these credential-free route URLs, no external referrers, and test that precise combination. Do not exempt login/logout or weaken CSRF when a privacy setting removes all usable origin evidence; explain the unsupported setting honestly.
- No third-party auth, fonts, scripts, browser storage or automatic resource dependencies. No localStorage, sessionStorage, analytics, client fingerprinting or IP-keyed abuse policy. Bound transport/auth work; qualify a small non-IP abuse-control policy before real users rather than silently inheriting IP rate limits.
- Login refusals use identical account-neutral text/status for absent identity and wrong secret; malformed form messages mention fields/constraints, not record existence. Verify timing/error behavior rather than promising perfect enumeration resistance. `Cache-Control: no-store` on login, authenticated pages, redirects and errors; history/password managers/browser memory remain independent boundaries.

## 5. Publication: one bundle, one pointer

**Proposed storage, requiring scoped persistence approval first:** one `website_bundles` collection holding bounded text-entry maps (`init.el`, helpers/pages/routes/style), content digest and host-API version; one singleton `website_state` record containing active and previous digests. A sealed revision is insert-only through the publication operation. Keep at most one draft plus active and previous revisions; no request history, user content, credentials, diagnostics or automatic audit trail in bundles. Font binaries/notices stay pinned static assets, not database code. Retention/deletion and SQLite WAL/deleted-page/manual-snapshot exposure require explicit review; deletion is not secure erasure.

This is logical publication immutability, **not** protection against trusted native admins changing/deleting records or schema. Those admins already possess unrestricted authority. Editing executable Lisp is trusted-code authority, not a customer text editor. No browser editor is required: a manual trusted-admin import/validate/publish operation is enough. Native draft edits do not auto-activate; neither do out-of-band active-record edits hot-reload a running copy. Restart/publication must revalidate persisted bytes against the selected digest.

1. **Draft → prepare:** read a consistent copy; compute its digest; check host version, bounded files, full syntax, eager loads, reserved paths, actions, option defaults/types/documentation, style values and synthetic page fixtures. Build an ELPS template and render fixtures in disposable independent VMs without changing the serving revision or running backend mutations. Initialization must be side-effect-free under the hosting gate.
2. **Publish:** serialize publishers; compare the draft digest and expected active pointer. In one short supported PocketBase transaction, seal the exact candidate bytes and update active/previous together. No evaluation inside the write transaction. Briefly hold website revision admission across commit and the prebuilt in-memory pointer swap; existing requests retain their captured revision. Accounting keeps its own unchanged path/budgets.
3. **Coherent serving:** each request uses one route/auth-policy/page/style revision. Serve generated CSS at `/_site/styles/<digest>.css`; never serve new CSS under an old digest. Retain previous CSS with the previous bundle. Very old tabs may get an honest missing stylesheet after pruning, never mismatched new styles; content remains usable unstyled. Stale POST forms ask for reload before any effect.
4. **Failure/restart:** failed preparation leaves active unchanged. An uncertain DB commit or interrupted activation closes new website admission until authoritative state is reread/reconciled—no guessed success. Restart reads the active pointer, verifies bytes/host version and prepares it before admitting website traffic. Missing/corrupt/incompatible active state serves a small Go-owned 503 page; it does not initialize empty financial state or silently choose older auth policy. Existing native/accounting availability is independent where storage health allows it.
5. **Rollback:** explicitly revalidate and republish the retained previous **code/style** revision. Never restore the database, balances, reservations, credentials or financial replay state to roll back website code. A bundle cannot carry schema migrations. A schema/host-capability change needs a separately authorized Go change.

Content addressing identifies bundle bytes; ELPS template construction supplies the documented VM construction boundary. Qualify both separately, including host-callback/native admission and retirement of old template references. A request retains its captured template and schema until completion; neither publication nor cancellation may reuse its mutable VM state for another request.

## 6. Compact visual contract

**Iosevka**, self-hosted. Start the visual review with **Iosevka Term**, not a finalized variant choice. Ship actual upright **400 and 700** weights with the relevant copyright/OFL notices; no synthesized bold. Pin the font release/files during implementation. Use `font-display: swap` and a generic monospace fallback. Tor's higher security settings may disable fonts: the page must still work and read well.

- One text size everywhere: `1rem`, comfortable line-height around `1.6`. Semantic `h1`/`h2` remain headings, distinguished by **bold and space**, not larger type. Keep ordinary body text regular.
- Warm light background, near-black text, one dark green link/focus accent; verify contrast rather than assuming the sample palette passes every combination. No theme switch or elaborate palette.
- Left-aligned reading column, approximately `max-width: 42rem`, with a small responsive outer margin. Spacing uses one `0.5rem` unit and a few multiples. No forced character grid or fixed-height content.
- Inputs/buttons inherit font and size; square corners, thin borders, generous usable padding, visible labels and normal password-manager semantics. Underline links at rest. Visible high-contrast keyboard outline with offset, never removed. Keep touch targets usable without inflating typography.
- Semantic landmarks, a skip link, logical heading/focus order, associated field errors and a readable error summary. No color-only meaning. Allow wrapping, browser zoom, text-spacing overrides and forced colors. Test narrow screens and 200–400% zoom without horizontal page scrolling.
- **No mode line, cards, shadows, rounded pills, ornamental icons, animation, terminal emulator or JavaScript.** Do not trade legibility for aesthetic alignment.

For the smallest privacy-safe dynamic styling, expose only the tiny style-value declaration shown above and generate a fixed small CSS scaffold in Go. Validate colors/lengths as values; no arbitrary CSS text, selectors, `@import`, resource `url()`, SVG or inline styles. Broader styling syntax is a later capability review, not a CSS sanitizer project. Fixed `@font-face` URLs reference only the pinned local font assets. No user/account data appears in selectors, attributes used for styling or resource paths.

Customer CSP proposal: `default-src 'none'; script-src 'none'; connect-src 'none'; style-src 'self'; font-src 'self'; img-src 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'`. Go also constrains actual link/form/resource targets to registered website routes and fixed assets; `'self'` alone would not stop same-origin URL-based exfiltration. Use `nosniff`; no CSP reporting endpoint. Confirm headers and actual requests in the supported browsers. Do not apply this customer CSP to the separate stock native UI or weaken native auth to make it match.

## 7. Shortest phased local acceptance

All work below is **future, separately authorized**, in the existing pinned devenv/Nix environment; no installs or tests were run for this planning packet. Synthetic credentials only, metrics export disabled, no external auth/mail/inference/payment calls.

### A. Two early gates, before feature work

- **ELPS hosting:** pin a reviewed version with the required template/context/limit APIs; prove eager `init.el` preparation and independent VM creation, primitive and host-capture restrictions, candidate isolation and cleanup. Run sequential/concurrent cross-user mutation sentinels and template-source mutation tests under the race detector, with repeated publish/restart. Cancel infinite loops/recursion and cooperating blocking callbacks; exercise step exhaustion, bounded malformed/deep source, output limits and shutdown in a disposable test process. Assert bounded termination and resource release, no background evaluation left running, a healthy concurrent VM and clean defaults in the next VM. Test publication/loading cancellation too. **Stop** if supported embedding cannot meet mandatory cancellation and request isolation; a shared lane, goroutine timeout or assumed deep clone does not pass.
- **Auth bridge:** prove SDK final-auth completion → SCS → protected GET → CSRF-protected logout with actual local HTTPS/native forms. Test absent/wrong identity neutrality, MFA challenge not granting a session, deletion/password/token-key/authorization changes, idle/absolute expiry, fixation, concurrent logout, stale forms, missing cookies and hostile SDK/store errors. Verify no tokens escape the private adapter. Qualify cookie/origin/proxy behavior and memory-store bounds. **Stop** if full SDK behavior cannot be preserved with small supported glue.

These gates are independent of choosing a final font variant. Neither passing one nor an existing operator login test passes the other.

### B. One three-page vertical slice

Use an in-memory fixture bundle and disposable synthetic customer fixtures first. Implement only public home, login, signed-in home and logout; no financial view or production provisioning/recovery flow. Exercise curl cookie-jar/form navigation, no-JS browser navigation, named-route changes and public/session policy changes. Confirm a public-page declaration never grants a protected capability. Test reserved-route collisions/encodings, method/body bounds, HTML escaping, closed unknown-cause errors, no open redirects and no state-changing GETs. Qualify option declaration validation, default resolution, page use of effective values, escaped help/docstrings, unknown/removed/incompatible preference values and per-request mutation isolation with synthetic in-memory preferences only; no new preference UI or persistence is implied. Inspect all three pages with keyboard, screen reader, narrow viewport, zoom, forced colors and fonts blocked; compare actual regular/bold rendering.

### C. Reviewed persistence and coherent publication

Obtain the scoped storage/retention approval for customer auth and code bundles before adding collections/migrations. Current intake privacy excludes client recovery credentials; do not silently import them or treat the auth direction as blanket approval to retain secrets. Specify necessary PB auth fields/hashes, minimal customer-to-ledger binding if later needed, SDK metadata and any future MFA/OTP/reset state explicitly. Sessions remain volatile by default; persistence across restart would be a new decision.

Then qualify draft validation, simultaneous publishers, corrupt/incompatible bundles, publication races, crash points around commit/swap, restart reloading, stale POSTs and explicit code-only rollback on disposable persisted state. Verify pages/styles share a revision and bad candidates never contaminate active behavior. Preserve financial state across every case and retain the existing accounting/native logging-off regression checks. Do not weaken strict startup schema validation to accept the new collections; plan the intentional migration separately.

### D. Local privacy and final acceptance

Run the complete three-page flow against the actual built intake service, including Tor Browser JS-off/Safest in a separately approved local test arrangement—not a new onion service. Capture only synthetic assertions: no external browser fetches, SDK/access/AuthAlert recording remains off, `_logs`/`_authOrigins` stay empty in fresh fixtures, and fake secret/user/body/error sentinels never reach HTTP diagnostics, stdio or telemetry. Include load/render/auth/session/CSRF/publication failures and cleanup races. No request history is stored in bundles or sessions. Record code, dependency, browser and built-artifact versions plus gaps; do not relabel local results as deployment, attestation or real-data evidence.

Use [intake forbidden defaults](../PRIVACY.md#forbidden-collection-and-defaults), [typed failure ownership](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates) and gateway [data boundaries](../../possums/PRIVACY.md#data-handling-boundaries). Keep stage-specific, closed transient errors even with telemetry off; examples: `SITE_CSRF_REFUSED` — reload the form, no action ran; `SITE_RENDER_UNAVAILABLE` — rendering cause unknown, no action replayed; `SITE_PUBLICATION_UNCONFIRMED` — inspect active state privately before republishing. These are proposed detailed codes, not new metric labels. Reuse once-only ownership; preserve known auth/publication effects through later delivery failure. Do not expand the existing 39-category projection or count website/native activity. No automatic logs, support bundles, diagnostic-triggered work or new observability system.

## 8. Consequential decisions still needed

1. **ELPS qualification and pin:** select a reviewed version and prove required cancellation, independent request state and host callback ownership with small supported embedding. Set bounded request/publication/concurrency limits from synthetic evidence; do not treat interpreter allocation-size limits as process memory isolation.
2. **Customer credential contract:** identity/provisioning, recovery and any MFA requirement; do not presume email or import gateway recovery credentials. Exact session lifetimes, invalidation behavior and restart logout must be approved after qualification.
3. **Persistence scope:** minimal customer auth/binding and bundle schema, retention and migration policy under the existing WAL/manual-snapshot/admin boundaries. Saved per-user option values need a later scoped approval; initial pages use declared defaults. No durable browser sessions by default.

Iosevka variant and final spacing are a small visual review, not an architectural dependency. Gateway delegation protocol, financial pages, payments, onboarding/recovery product flows, Tor infrastructure and production rollout are separate future packets.

## Evidence and verification of this planning packet

Read-only inspection covered the required policies/history, the focused local server/operator/accounting wiring and the cached pinned PocketBase password/auth-response/token implementation. Public primary sources were read; no interpreter/library was installed or executed and no hosting, browser/auth, concurrency or restart acceptance is claimed.

Update evidence: the ELPS selection and customization model supersede the earlier GoLisp hosting proposal. Current upstream [template](https://github.com/luthersystems/elps/blob/main/docs/templates.md), [embedding](https://github.com/luthersystems/elps/blob/main/docs/embed.md) and [language/docstring](https://github.com/luthersystems/elps/blob/main/docs/lang.md) documentation was read for this revision. No ELPS dependency was acquired, pinned or executed; cancellation and isolation remain mandatory acceptance checks, not completed evidence. The option macros and website APIs are proposals.

Additional primary references:

- [PocketBase upstream status warning](https://github.com/pocketbase/pocketbase/blob/v0.40.5/README.md); versioned implementation links above take precedence over moving documentation for API claims.
- [SCS documentation](https://github.com/alexedwards/scs/blob/209de6e426de9259665975ce16b91331d228f052/README.md): middleware, memory-store loss at restart, cookie settings, expiry, `RenewToken` and `Destroy`.
- [nosurf documentation](https://github.com/justinas/nosurf/blob/ec9bb776d8e5ba9e906b6eb70428f4e7b009feee/README.md) and [handler source](https://github.com/justinas/nosurf/blob/ec9bb776d8e5ba9e906b6eb70428f4e7b009feee/handler.go): masked hidden tokens, cookie customization, origin checks, failure handler; its default persistent cookie is not this plan's policy. These inspected commits are evidence, not selected dependency pins.
- Tor Project: [cookies/session lifetime](https://support.torproject.org/tor-browser/getting-started/about-tor-browser/), [New Identity](https://support.torproject.org/tor-browser/features/managing-identities/), [security levels](https://tb-manual.torproject.org/security-settings/).
- Iosevka: [family/weight/spacing documentation](https://github.com/be5invis/Iosevka#features), [SIL OFL 1.1 license](https://github.com/be5invis/Iosevka/blob/main/LICENSE.md).

The deliverable is this new document only. No separate visual mockup, implementation, actual new persistence or change to `PLAN.md`, `AGENTS.md` or sibling gateway work is included.
