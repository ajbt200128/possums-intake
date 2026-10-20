# Local customer authentication and account snapshot — approved contract

**Status: operator-approved for isolated synthetic local implementation.** The
operator explicitly approved this scoped storage/privacy/implementation contract;
[the intake policy](../PRIVACY.md#customer-key-authentication-and-scoped-website-account-reads)
now records the exception. Source approval is not runtime qualification. This pins
the remaining contract in [the customer plan](customer-auth-data-plan.md). The operator requested
completion locally with isolated synthetic accounts/keys before image qualification
and one separately approved deployment. Current scoped evidence and remaining gates
are tracked in [the local evidence record](customer-auth-local-evidence.md).
No real credentials, gateway/client changes,
installs, exports, deployment, commits or pushes are authorized. Existing previews
and their data directories remain untouched. `PLAN.md` and `AGENTS.md` stay unchanged.

## 1. Exact key adapter

Use the existing gateway key **text**, without trimming, normalization, case changes
or deriving an account ID from it. Accept strict unpadded base64url, at most 128
ASCII bytes and at least 32 decoded bytes, matching gateway `src/auth.rs`.
Explicitly reject whitespace, padding, invalid trailing bits and invalid lengths.
Acceptance of a format does not establish entropy: new private keys must come from
at least 32 cryptographically random bytes, encoded as unpadded base64url.

Proposed version 1, using standard-library SHA-256 over the exact submitted text:

- Lookup: `"v1_" + lowercase_hex(SHA256("possums-intake/customer-key/lookup/v1\x00" || key_text))`.
- SDK password: `base64url_no_padding(SHA256("possums-intake/customer-key/password/v1\x00" || key_text))`.

Here `\x00` denotes one NUL byte, not the four displayed characters. The second
output is always 43 ASCII characters, within PocketBase v0.40.5's 71-character
password limit. Its stock bcrypt verifier is generated and checked by the SDK.
Lookup and password use separate domains; the stored lookup is not the SDK login
secret. There is no new pepper, custom password verifier, token minting or secret
recovery service. A fast lookup digest permits offline guesses of weak keys;
random key generation is essential, as with the gateway's existing digest scheme.

The same raw key remains suitable for Pi/API authentication. No gateway change,
credential import, account enrollment or shared accounting cutover follows.
Synthetic compatibility vectors cover 43/71/72/96/128-character valid keys,
different keys, malformed input and the full length boundary.

## 2. Necessary durable state

Create one separate PocketBase auth collection, proposed name `customer_keys`, only
through explicit schema initialization/migration. Do not use `_superusers` as a
customer collection or introduce an accounting permission tier.

| Field/state | Purpose and boundary |
|---|---|
| SDK record `id` | Internal auth identity, never substituted for the external ledger account ID. |
| `key_lookup` | Hidden required text; versioned 67-character lookup above; unique exact index. Sensitive first-party auth data, not anonymous data. |
| `account_id` | Exact external ledger identity, valid UTF-8 and 1–128 **bytes**. No normalization, new character-class restriction or account creation side effect. |
| SDK `password` | Stock hidden salted bcrypt verifier of the derived SDK password; neither raw key nor derived password is stored. |
| SDK `tokenKey` | Required hidden per-record token-signing material generated/managed by PocketBase. A secret, not lookup or diagnostic data. |
| SDK `email`, `emailVisibility`, `verified` | Required SDK schema fields retained; email optional and empty for this flow, visibility/verified default false. No email enrollment or recovery flow. |
| SDK collection/config metadata | Necessary SDK schema/auth settings and generated signing secrets. No additional per-user timestamps, last-login, origin or activity fields. |

Initial registration/list/view/create/update/delete/manage rules remain nil
(superuser-only). Enable supported password auth using `key_lookup` as its sole
identity field; use an SDK auth rule permitting the configured customer login,
without an email-verification requirement. Set initial customer SDK token duration
to one hour. Keep existing logging-off and AuthAlert-origin protections. Customer
MFA challenges are unsupported by this website and must fail closed, not turn off
MFA/OTP or alter native superuser controls.

There is no persisted website session/token, raw key, derived password, credential
copy, account history or secondary binding registry. Necessary auth rows last until
explicit admin change/deletion; there is no login-driven retention refresh or
history table. SDK schema/config and existing financial state are distinct.
SQLite/WAL/deleted pages, manually downloaded snapshots and trusted admin authority
remain persistence boundaries; deletion/rotation is not secure erasure.

## 3. Stock-native provisioning, with a mandatory SDK feasibility gate

Keep the existing stock record editor, not a new CLI/dashboard. Proposed recipe:

1. Explicitly create the zero-balance ledger account using existing native admin
   operations; preserve the external account ID. Funding remains separate.
2. Generate a key privately with a cryptographically secure local generator; paste
   it into the customer record editor's existing password/confirmation inputs and
   enter the exact ledger account ID. Do not use the generic native password
   generator unless separately shown to meet the key contract. No hand-computed
   lookup/verifier fields or email should be required.
3. A narrowly scoped supported request middleware prepares the lookup and both
   submitted password/confirmation values **before** stock form loading, so the
   SDK still performs its normal password confirmation and form/model validation.
   Empty/missing/mismatched confirmation must not become accepted. Use existing
   SDK superuser authentication to select this manual provisioning recipe, not a
   replacement authority gate; other stock administration stays stock.
4. The SDK creates/updates the auth record normally. These are separate native
   writes, not an atomic end-to-end provisioning or funding receipt. Missing,
   deleted, malformed or inconsistent binding denies customer account access.

Source inspection supports the candidate ordering: `apis/record_crud.go` obtains
record data from `RequestInfo().Body` before `forms.RecordUpsert.Load`; the stock
form then validates confirmation and saves. **This is not runtime qualification.**
Prove actual authenticated native HTTP and the installed editor, including required
hidden-field behavior, before adopting the recipe. In contrast, changing a password
in a later record-request hook can cause `syncPasswordFields` to skip stock form
password validation; do not use that shortcut or disable SDK validation.

The protocol applies to the ordinary customer record editor's create/update flow,
not as a new restriction on trusted batch/direct/schema/SQL/restore authority.
Those operations remain available and can break auth/binding invariants, just as
financial native edits can break accounting. No silent fallback, reset or repair.
If the normal editor cannot implement this recipe while preserving supported SDK
constraints, stop and ask; do not add a provisioning UI/CLI or weaken constraints.

On every protected request, revalidate the current SDK credential and resolve its
current authoritative account binding. Browser fields/query/path/session caches
cannot select an account. Binding changes must not let an old session silently
switch accounts: capture the binding privately at login and require it still to
match on subsequent validation; mismatch logs the session out and requires fresh
login. The cached value is only a consistency check, never an authority to read
its old account. Existing SDK token invalidation remains authoritative; no custom
session/revocation registry. Explicit admin reassignment can authorize the newly
bound account only after a fresh login. In-flight requests retain their captured
validated authority; no instantaneous cancellation guarantee is claimed.

## 4. One complete first-party snapshot

The authenticated Go host resolves ownership and calls existing `Service.Balance`
for that account, not raw financial SQL or loopback shared-key HTTP. Take balance,
available credit, enabled state and all current pending reservation rows from
that **one** authoritative snapshot. Show all defined reservation fields, including
expiration, timeout, idempotency key and explicit null terminal fields. No other
account lists, terminal/credit history, payments, polling or mutation controls.
`enabled=false` blocks new spending, not the owner's balance read.

Render canonical amounts exactly as integer microunits, explicitly labelled; no
float conversion, rounding or invented currency conversion. Preserve identifier
bytes, visibly quoting/escaping embedded control characters before text-buffer
presentation. HTML remains escaped. A complete list that exceeds existing text,
node or document bounds is typed unavailable, never top-N, empty or truncated.
No successful financial outcome is inferred from a later rendering failure.

Use a small copied bounded string-map view argument in the reusable ELPS page
API. Intake prepares the display strings; the core knows no financial/SDK names
and remains stdlib + pinned ELPS only. No credentials, SDK records, services,
mutable accounting objects, session state or global per-user view enters Lisp.
The default page displays the entire pending-row string, not a subset. Views and
rendered account pages are request-only, never stored in bundles or publication.
Authenticated responses remain non-cacheable. Snapshot reads may expire due holds
and advance accounting time under existing Service rules; they are not passive
SQL reads or permission to retry a failed financial operation.

## 5. Approved privacy amendment

The narrowly scoped intake-only customer-auth/account-read section in `PRIVACY.md`
implements the operator-approved substance below:

> The dedicated intake website and explicit native customer provisioning may
> receive the shared user key transiently for authentication, separate from the
> custom accounting endpoints. Persist only the versioned lookup, stock SDK
> verifier, exact ledger binding and inventoried necessary SDK fields/settings in
> this contract. Never persist the raw key or derived login secret. Return only
> the currently authenticated account's complete balance/available/pending snapshot.
> These are first-party auth/account data, never diagnostics or telemetry. This
> is a narrow exception to the intake recovery-credential receipt prohibition and
> older gateway-only customer-read boundary, not permission for gateways to send
> credentials in accounting calls. Gateway policy and shared-key accounting are
> unchanged. Initial implementation/use remains isolated synthetic local only;
> real credentials/data, integration and deployment require separate authorization.

Secret-entry input values necessarily exist transiently in the operator/customer
browser and SDK request memory. Do not emit them in server-generated markup,
Lisp, URLs, response bodies, sessions, diagnostics, logs or artifacts. Existing
page-memory-only native drafts/auth/preferences remain mandatory. Browser password
managers/DevTools, operator handling, snapshots, SDK/runtime memory, host-native
history and platform access remain separate boundaries; no immediate-erasure or
whole-browser/process-transience claim. Never provide a real key to an agent/tool.

All existing forbidden telemetry, typed stage-specific unknown fallbacks,
39-category projection and once-only ownership remain unchanged. No new counters,
logs, traces, diagnostic-triggered work or exports. Applicable policy references:
[intake accounting](../PRIVACY.md#accounting-and-processor-boundaries),
[trusted administration](../PRIVACY.md#trusted-intake-manual-admin-exception),
[typed failures](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates),
[forbidden defaults](../PRIVACY.md#forbidden-collection-and-defaults), and
[gateway boundaries](../../possums/PRIVACY.md#data-handling-boundaries).

## 6. Implementation and acceptance order after approval

1. Apply the approved scoped privacy wording; qualify key vectors, pre-form native
   processing and stock editor behavior with fake credentials only. Stop on SDK
   incompatibility rather than inventing another workflow.
2. Add explicit auth schema initialization/migration and strict startup checking;
   no credential seeding/upsert/rotation or empty-state recovery on serve/login.
   Test populated-state preservation, interrupted migration, restart and prior-code
   compatibility; no financial constraints dropped or database rollback.
3. Add the real customer adapter/binding checks and 1h absolute / 10m idle volatile
   SCS sessions capped by SDK token expiry, with no silent refresh/remember-me.
   Keep the previously accepted stock concurrent-logout limitation.
4. Add the one-snapshot Go view and app-neutral ELPS view argument. Test two-account
   isolation, forged selectors, missing/deleted/changed binding, native AuthRule
   changes, token/password invalidation, disabled spending, complete/empty/over-limit
   rows, full uint64 amounts and control characters. Race expiry/settlement/refund.
5. Qualify closed failures, actual SDK HTTP/browser presentation, privacy sentinels,
   persistent restart and session continuity across live publication. Run pinned
   race/elpscheck, vet and existing accounting/native/logging regressions.
6. Then agree ordinary runtime publication wiring and qualify the combined generated
   image. Local source/fixture evidence is not generated-image/deployed evidence.
   Seek separate approval before real-data activation or one coordinated deployment.
