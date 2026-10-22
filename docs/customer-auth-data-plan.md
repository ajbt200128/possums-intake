# Customer key authentication and account data — joint draft

**Subsequent schema decision:** the operator selected and locally implemented [one Accounts auth collection](merged-account-schema.md), using its generated record ID for both authentication and ledger ownership. Earlier separate-auth/binding/migration proposals below are historical; no migration or compatibility bridge is required. Raw-key adaptation and local-only/privacy gates remain applicable.

**Status: local synthetic customer-auth/account-data implementation approved.**
The reusable ELPS boundary and focused live publication slice have local evidence
(section 5). The operator subsequently approved the
[exact customer auth contract](customer-auth-local-contract.md) and its scoped
[intake privacy amendment](../PRIVACY.md#customer-key-authentication-and-scoped-website-account-reads).
This supersedes the older planning-only gates below only for isolated synthetic
local source/tests; it does not establish runtime qualification.
Real credentials/data, gateway/client edits, production activation, deployment,
exports and commits remain unauthorized. Do not edit `PLAN.md` or `AGENTS.md`;
existing unrelated work remains untouched.

The operator subsequently requested completing customer auth/account data locally
with synthetic accounts before combined image qualification and deployment approval.
The [approved local contract](customer-auth-local-contract.md) pins the exact
storage, key protocol, native-provisioning feasibility gate and acceptance order.
Earlier proposal/unresolved wording below is historical where that contract now
resolves it; technical qualification and real-data gates remain mandatory.

## Selected product direction

The operator selected:

1. One user key shared between website and Pi/API, not email plus password and
   not an independent website credential.
2. Admin-provisioned accounts; no public signup.
3. Signed-in home shows the user's balance and complete current pending holds.
4. No lost-key recovery flow. Users must retain their key; this is not a claim
   that trusted native administrators lack the authority to alter accounts.
5. Transient same-key processing with verifier/lookup-only durable storage, subject
   to the explicit scoped policy amendment and implementation approval below.
6. New explicitly provisioned accounts first; no existing-account enrollment or
   real credential import in this phase. Preserve the gateway's accepted key range.
7. Browser sessions: one-hour absolute maximum, ten-minute idle timeout, capped by
   SDK token validity; volatile across restart and no remember-me.
8. **Persisted Lisp bundles/publication now**, not embedded-only page deployment.
9. Retain one draft plus active and previous code revisions, excluding bounded
   temporary in-flight references. Pruning is not secure erasure.
10. Manual creation/editing through stock PocketBase admin is sufficient for now.
    Do not assume a new provisioning CLI, custom dashboard or browser action UI.
11. **Live publication**, not activation requiring an intake restart. Publication
    must preserve current sessions and existing captured requests.

Keep the accepted buffer presentation, visible italic tooltip face, inverse
hover/focus, single-newline layout and 1rem top/left gutters. Dark mode, payments,
public onboarding, saved preferences and account/request history remain deferred.

## Existing facts, not proposed behavior

- Gateway `src/auth.rs` accepts unpadded base64url credential text, at most 128
  encoded bytes and at least 32 decoded bytes. It hashes the **encoded text** with
  SHA-256 and compares the registered digest in constant time. Account identity
  is an independent opaque configured ID, not derived from the key.
- Gateway credentials/accounts are explicitly provisioned at startup. Its current
  accounting is memory-only. Pi sends the credential in its authenticated login
  flow; no new client key format is needed merely to add this website.
- Intake `internal/ledger/types.go` preserves external account IDs as 1–128 UTF-8
  bytes. They must not be replaced with PocketBase record IDs, trimmed, normalized
  or subjected to a new character-class restriction.
- `Service.Provision` in `internal/ledger/operations.go` creates zero balance and
  is idempotent without resetting an existing account. There is no website
  credential-to-ledger binding today. `FixtureKeyAdapter` is demonstration only.
- `Service.Balance` already returns `AccountBalance`, complete
  `PendingReservations` and `Available` through the serialized transaction path.
  It can expire due holds and advance accounting time evidence; it is not a
  side-effect-free raw SQL read.
- The existing SDK bridge consumes the final PocketBase authentication response,
  revalidates current SDK credentials/authorization, and keeps tokens in private
  Go/SCS state. The browser gets only the opaque session cookie.
- Pinned PocketBase v0.40.5 `core/field_password.go` uses bcrypt and constrains
  configured password lengths to at most 71 characters. Directly treating every
  gateway-valid key (up to 128 ASCII bytes) as an SDK password is **not** a
  compatible general solution. The usual newly generated 32-byte key encodes to
  43 characters, but that does not cover every accepted existing credential.

These are current source facts, not deployed identity, imported-account or
synchronized-balance evidence. No private provisioning configuration was read.

**Same credential does not mean shared accounting yet.** Showing intake's balance
must not imply Pi is spending that balance while the gateway still uses memory
accounting. Gateway integration/cutover remains a separate authorized packet;
never operate copied or competing spendable ledger authorities.

## Policy proposals requiring explicit approval

Current [intake privacy](../PRIVACY.md#accounting-and-processor-boundaries) and
`PLAN.md` exclude client recovery credentials. The same-key choice requires a
narrow amendment; it does not silently lift that restriction.

The operator selected the following boundary, now made precise and approved in
[the local contract](customer-auth-local-contract.md). Implementation remains
synthetic-only and must pass its SDK/native provisioning feasibility gate:

- Receive the key transiently only for the bounded, same-origin HTTPS login or
  explicit admin provisioning operation. No raw key in persistent storage,
  sessions, Lisp, HTML, URLs, diagnostics, logs, metrics or model/tool history.
  Runtime/SDK memory cannot promise immediate secure erasure.
- Persist only a necessary SDK verifier, non-secret lookup material and minimal
  binding to the existing ledger account, plus explicitly inventoried SDK auth
  fields. No email requirement, login timestamps/origins or activity history.
- Permit this authenticated intake website to return its own account snapshot.
  This deliberately extends the older gateway-only customer-read policy; it does
  **not** make custom accounting HTTP endpoints public or remove their shared key.
- SQLite/WAL/deleted pages, optional manual snapshots, unrestricted trusted native
  administration and platform/operator access remain disclosed boundaries. A
  verifier or key-derived lookup is sensitive first-party auth data, never a
  telemetry label or support artifact.

The separately approved [intake amendment](../PRIVACY.md#customer-key-authentication-and-scoped-website-account-reads)
now permits only the specified synthetic local auth/account-read work. Other
privacy restrictions remain unchanged.

## Reusable library requirement

Keep ELPS hosting/declaration/node work in a reusable `pkg/lispsite` package with
standard-library/ELPS dependencies only and closed neutral errors. Intake passes
its reserved-route/action policy and owns SDK auth, sessions, accounting, rendering/
branding and failure-category projection. PocketBase persistence/activation wiring
stays application-side. No financial names, intake-internal imports, fixture
assets, SDK dependencies or per-user state belong in the core. Prove the boundary
with a standalone non-intake consumer and import-dependency tests; do not invent
an extensibility framework or separate repository/module.

## Proposed implementation shape

### 1. One credential/account contract, supported SDK authentication

- Preserve the exact existing key-text semantics and external account identity.
  Do not invent key-to-account derivation or silently remap existing balances.
- Define and review a small versioned compatibility adapter before implementing
  persistent auth. It must handle the gateway's accepted key range without
  truncation and still use final supported SDK authentication, not manual token
  minting or a replacement password verifier.
- If bounded key derivation is needed for the SDK password limit, use supported
  cryptographic primitives with explicit domain separation and synthetic vectors.
  Stored lookup material must not itself be sufficient to compute the SDK login
  secret. Do not simply reuse one stored key digest as both identifier and password.
  Exact derivation/lookup/schema are now pinned by the approved local contract;
  this does not establish native provisioning or production qualification.
- Use a separate customer auth collection, never `_superusers` or a new accounting
  permission tier. Initial public registration/CRUD rules remain nil; trusted
  native admin authority and SDK controls remain intact.
- Resolve the account from the currently verified credential/authoritative
  binding on each protected request. No browser-selected account ID or stale
  session binding may choose the financial account. Reuse current SDK validation;
  fail closed on missing/deleted/inconsistent binding.
- Ledger `enabled=false` continues to block new spending, not authorized scoped
  balance reads or closing holds. Do not silently redefine it as credential
  revocation. Keep customer auth authorization distinct.

### 2. Explicit admin provisioning and persistent startup

- Use explicit stock-native admin record operations for the zero-balance ledger
  account and customer auth binding. These are separate writes, not an atomic
  custom provisioning receipt. Document ordering and partial setup: missing or
  inconsistent binding denies login/account access and never creates an account
  or funds it. Native financial writes intentionally bypass Service safeguards;
  do not claim custom API idempotency or serialization for this manual path.
- SDK credential processing uses supported APIs and preserves SDK constraints.
  If any custom accounting operation is later added, it retains the shared-key,
  bounded Service writer and transaction-scoped-app contract. Expensive key
  preparation must not needlessly hold that writer.
- Qualify a normal native-record entry recipe compatible with the one-key adapter;
  do not require hand-computed verifier fields or put raw keys in an identity/
  lookup field. Any necessary narrow SDK hook needs its own ordering/privacy tests.
  Do not assume the native generic password generator meets the gateway key format
  or entropy requirement. Private key generation/entry/delivery remain a technical
  design gate, not permission to expose a real key to the agent.
- Do not describe partial native setup as successful end-to-end provisioning.
  No secrets in command arguments, model-visible tool results, logs or artifacts.
- Existing accounts/balances are never reset or credited by credential setup.
  Funding remains a separate explicit idempotent credit operation.
- Serve/login never create, upsert, reset or rotate credentials as a fallback.
  Missing/corrupt auth state is unavailable, not permission to seed a demo account.
- Plan explicit schema changes, supported migrations and strict startup validation
  without dropping financial SQL constraints. Test existing populated state,
  interrupted migration and prior-code compatibility; no database restore as code
  rollback and no automatic empty-state recovery.

### 3. A scoped first-party home snapshot

- Go authenticates and resolves ownership, then calls existing `Service.Balance`
  for that account. No duplicated SQL/arithmetic or loopback admin-key HTTP client.
  Custom accounting endpoints retain their existing shared-key requirement.
- Show total unspent balance, available balance and pending holds; include account
  enabled state and the complete defined pending-row fields, including expiration
  and idempotency key. No terminal/credit/request history or cross-account lists.
- One complete authoritative snapshot, not separate racing balance/hold queries.
  Expiry/settlement/refund remain existing ledger decisions, never Lisp effects.
- Pass only copied bounded first-party view data into the fresh request VM. No SDK
  records, credentials, tokens, services or mutable accounting objects enter Lisp.
  Data remains per-request, never a global bundle/default or a stored page.
- Render exact money without floats or silent rounding. Preserve original account
  IDs while visibly escaping embedded control characters in structured data, so
  legitimate identifier bytes cannot inject text-buffer rows or markup.
- Respect the current bounded tree/text/document limits. If a complete snapshot
  cannot be displayed, return a typed unavailable result, not a silently truncated
  top-N list. Pagination or broader limits require a separate explicit design.
- No automatic polling, funding/settlement buttons, financial editor, exports or
  diagnostic-triggered work. Snapshot failures must not imply a refund/rollback.

### 4. Keep qualified session and presentation machinery

Reuse SCS/nosurf, real HTTPS, secure first-party session-only cookies, fixation
rotation, current SDK revalidation and token-capped expiry. Use the selected 1h
absolute / 10m idle lifetimes. Restart logs browser sessions out while
approved durable credentials/accounting survive. No remember-me or silent refresh.
The accepted stock SCS overlapping-logout resurrection limitation stays accepted;
no custom stores, revocation registries, serialization or replacement library.
Customer MFA remains unsupported until separately scoped; never disable native
MFA/OTP to make the customer flow work.

### 5. Minimal live website activation

**Operator-approved minimal design, now handed off for local implementation.**
The [focused handoff](website-implementation-handoff.md) defines authorization and
remaining SDK feasibility checks. This simplified flow supersedes the activation detail in the
[earlier plan, section 5](lisp-website-plan.md#5-publication-one-bundle-one-pointer).
The focused source and isolated SDK/HTTP persistence fixtures now pass local
checks; see [activation evidence](website-activation-evidence.md). Ordinary
commands/preview and all real-auth/data/production gates remain unchanged.

1. **Edit the draft in stock PocketBase admin.** Saving source does not activate
   it. Use existing native SDK authentication and collection rules; no new admin
   screen, CLI, customer editor or permission tier.
2. **Explicitly publish** through a field/action in that same UI. Capture the
   candidate bytes once and serialize publication operations, not request VMs.
3. **Prepare before committing.** Reuse the qualified captured ELPS runtime.
   Check only essentials: source/host compatibility and existing size/execution
   bounds, valid eager Lisp loading, allowed routes/capabilities and bounded
   renderable output. Preparation has no backend effects and does not run inside
   the SQLite write transaction. A bad candidate leaves the old website active.
4. **Commit, then swap.** Save the exact prepared source and active/previous
   selection in one short supported transaction. Briefly coordinate new website
   request admission across confirmed commit and the in-memory pointer swap.
   Never activate uncommitted or different bytes. Existing requests finish with
   their captured revision; publication does not restart intake or reset sessions.
5. **Serve stateless presentation.** Each request captures one immutable template,
   creates its own VM, renders, then releases the VM. Route/page/style belong to
   that same revision; stale POST forms reload before effects. Go still owns
   authentication, independent capability authorization and escaping.
6. **Reload on process restart.** Read the selected persisted bundle and prepare
   it before serving website traffic. Missing/invalid active code gives a small
   Go-owned 503, not automatic demo initialization or fallback to older policy.

**Only necessary state:** bounded persisted source/selection, with one draft,
active and previous revisions; one prepared active runtime pointer; temporary
bounded candidate/in-flight references. Suggested storage remains
`website_bundles` plus a singleton selection record, not a workflow/history store.
Fonts/licenses stay embedded. No queue, watcher, activation-history table,
per-user presentation state, automatic retry/recovery worker or elaborate state
machine. Existing volatile SCS authentication sessions are separate: this does
not claim the whole authenticated website is stateless.

**Small failure boundary:** unknown commit/swap outcome is not publication success.
Stop new website admission rather than guess or replay. Re-establish authoritative
selection before reopening; if that cannot be done, return 503 pending an explicit
publish/restart. No background reconciliation machinery. Native/accounting
availability stays independent where storage/SDK health permits.

Keep the existing safety boundaries: content-address actual generated CSS bytes
(including host scaffold), never changed CSS under an old URL; republish retained
previous code for rollback, never restore financial/auth data or migrate schemas
through Lisp. Logical pruning is not erasure from SQLite/WAL/manual snapshots.
Trusted native edits/deletion/restore can still violate logical invariants; those
accepted admin-authority risks are not prevented by a new authority gate.

**One SDK feasibility gate remains:** verify a supported native save/hook path
can prepare outside the write transaction and observe/cooperate with commit before
swapping. Do not invent that guarantee from an early hook. If unavailable, ask
before adding any separate operation or changing this workflow.

## Acceptance order — no live work implied

1. Approve the policy/storage scope, unresolved choices and exact key/account
   adapter contract. Pin synthetic compatibility vectors, schema and supported
   migration/provisioning behavior before authorizing implementation.
2. Implement only after authorization, using isolated persistent **fake** accounts
   first. Prove key compatibility, restart survival, failed/interrupted provisioning
   and migrations, neutral refusals, current SDK AuthRule/token invalidation and
   session expiry without raw credential persistence or fixture fallbacks.
3. Exercise at least two accounts: no selectors, crafted keys/forms, stale
   sessions, binding changes or rendering faults may expose the other account.
   Race snapshots against expiry/settlement/refund; check all fields and full-u64
   formatting, control-character escaping, empty and over-limit pending lists.
4. Qualify persisted publication with competing publishers, draft/native edits,
   stale forms, old in-flight requests, page/style coherence, session continuity,
   commit/swap interruptions, restart, pruning and explicit code-only rollback.
   Financial/auth data must survive unchanged except independently authorized
   operations. Include the actual host-CSS cache identity regression.
5. Retain accounting/native/logging-off regressions and closed typed diagnostics,
   once-only failure ownership and the 39-category projection. SDK/store/render/
   delivery/cleanup failures preserve known outcomes; no extra financial/activity
   counters, request logs, raw causes, telemetry export or retries.
6. Qualify the actual compiled persistent service and generated image, restart/
   migration/lifecycle, HTTP/browser behavior and synthetic privacy boundaries.
   Existing local prototype passes do not qualify these new paths automatically.
7. Only then propose separate gateway/client integration and real-data activation.
   Verify supported deployment/TLS termination, release/enclave/endpoint identity,
   customer-controlled disk-key unlock and original disk/key continuity. Ordinary
   HTTPS/readiness or exposed boot evidence alone does not satisfy these gates.

## Open decisions for the next conversation round

- Approve the explicit privacy-policy wording and exact necessary SDK/auth fields;
  source/bundle storage must not accidentally capture runtime/customer values.
- Qualify the stock-native explicit publish-intent operation and precise supported
  SDK hook/transaction ordering. Do not invent SDK atomicity or a successful
  activation receipt from an earlier validation/save hook.
- Specify the private key generation/entry/delivery recipe and manual account/auth
  record sequence, using stock SDK administration without a new dashboard/CLI.
- Review the exact SDK-compatible key lookup/derivation, account binding, schema
  and migration/provisioning contract. No raw-key import, silent key-length
  restriction, invented identity mapping or automatic credential creation.
- Set evidence-based production concurrency/session-store/capacity bounds and
  preserve the separate gateway/real-data activation gates. Product choices above
  do not establish these bounds or authorize implementation.

References: [website evidence](lisp-website-evidence.md),
[auth qualification](lisp-auth-qualification.md),
[earlier website plan](lisp-website-plan.md),
[intake forbidden defaults](../PRIVACY.md#forbidden-collection-and-defaults),
[typed ownership](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates),
[gateway data boundaries](../../possums/PRIVACY.md#data-handling-boundaries).
