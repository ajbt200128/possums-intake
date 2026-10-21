# Gateway and customer session lifetimes — approved local packet

**Status: local source, disposable-test and documentation only.** The operator
approved a paired 12-hour native customer auth default together with conditional
renewable gateway holds. This document covers the intake side of that pairing plus
an offline qualification of the stock native update path. It does **not** qualify an
existing installation's runtime state, authorize that update on any real or
persistent ledger, or claim gateway/client implementation. Unrelated intake policy
stays in [PRIVACY.md](../PRIVACY.md) and the paired hold/renewal contract stays in
[gateway holds](gateway-hold-renewal.md).

**Subsequent rollout approval:** the operator explicitly approved applying the
supported duration-only update to the existing operator-test installation after
the required deployment gates. [Rollout decisions](gateway-renewal-deployment.md)
record that authority; the update has not yet been executed or verified. Source
deployment and schema migration alone do not activate the existing 12-hour setting.

## Scope of this packet

Changed intake source: `internal/siteauth/customer.go` only. The fresh explicit
customer schema now stores a **12-hour** (43200-second) `authToken.duration` through
PocketBase's supported auth-collection token setting instead of 3600. No other
customer schema, rule, field, credential, binding, MFA or binding setting moved, and
no auth framework, updater, CLI or new admin surface was added.

The website's own session policy is **independent and unchanged**: one hour absolute
with a ten-minute idle window (`cmd/intake/website.go`,
`cmd/intake/website_local.go`). A 12-hour native token therefore does not extend a
browser session; the session's absolute deadline is still capped by its own one-hour
lifetime and by whichever earlier instant the current SDK token expires.

## Why the initializer does not rewrite existing state

`InitializeCustomerAuth` is an explicit *additive* schema operation. When the
customer collection already exists and is compatible, it returns without saving the
collection. Re-running it therefore cannot silently change a stored token duration
for an existing installation; compatibility acceptance deliberately ignores the
duration value. Confirmed on disposable state for 3600, 7200 and 43200 seconds.

## Existing-installation update design (not executed on real state)

For an installation already carrying the older one-hour duration, the approved
update is one stock native collection PATCH under SDK superuser authentication:

`PATCH /api/collections/customer_keys` with body `{"authToken":{"duration":43200}}`

The stock native editor performs the same supported save. PocketBase v0.40.5 source
shape, reviewed read-only, explains why this is narrow:

- `core/record_tokens.go` (`newAuthToken`) uses
  `Collection().AuthToken.DurationTime()` when no explicit duration is supplied, so
  the setting governs newly issued SDK auth tokens.
- `core/collection_model.go` rotates `AuthToken.Secret` only when the `AuthRule`
  changes. A duration-only update keeps the signing secret, so previously issued
  tokens keep their original expiry and signature.
- `apis/collection.go` (`collectionUpdate`) binds the submitted JSON onto the
  already-loaded collection and then saves it through the SDK, so an absent field is
  preserved rather than reset.

Consequently a duration-only update is expected to leave the signing secret,
`tokenKey`, password verifier, `key_lookup`, `account_id`, auth rules, password-auth
identity fields, MFA/OTP configuration and all other token settings untouched while
new logins receive the longer lifetime. Already-issued shorter tokens keep their
original expiry and stay valid until it.

## Disposable-state qualification (what was actually run)

All of this ran against fresh `t.TempDir()` synthetic ledgers with fake credentials
and the real project native HTTP handler (`internal/operator`). Nothing touched an
existing preview, persistent volume, remote service or operator instance.

`internal/siteauth/customer_lifetime_test.go`:

- Fresh explicit initialization stores 43200 seconds and the stock defaults for
  MFA/OTP/password-reset/email-change/verification/file tokens are unchanged.
- With a modelled 3600-second installation, a token issued before the update has a
  one-hour expiry; after one `PATCH /api/collections/customer_keys`
  `{"authToken":{"duration":43200}}` that token still validates with the same
  `exp`, while newly issued tokens expire 12 hours out.
- The same PATCH leaves the signing secret, `tokenKey`, password hash, exact
  `account_id` binding, auth rules, password-auth settings, MFA/OTP configuration,
  nil CRUD rules and the other token durations equal to their pre-update values.
- Anonymous collection PATCH is refused (401/403) and a below-minimum duration is
  refused by SDK validation (400), leaving state unchanged.
- Repeating the identical supported PATCH is safe: duration and signing secret stay
  equal and a current token keeps working.
- Enabling MFA keeps the website's fail-closed refusal and creates no `_mfas`
  challenges; stock `tokenKey` rotation still revokes the 12-hour token.
- `_logs` and `_authOrigins` stay empty across the whole path.
- `InitializeCustomerAuth` preserves an existing 3600/7200/43200 duration.
- A real close/reopen of the same synthetic ledger persists the 43200 duration and
  the already-issued token, and a repeated explicit initializer still leaves the
  stored duration alone.

`internal/website/session_lifetime_test.go`:

- With the fixture collection set to a 12-hour token, the website session still has a
  one-hour absolute deadline and a committed ten-minute idle expiry, and its cookie
  stays non-persistent.

## Explicitly unqualified

- No native duration update has been applied to runtime/persistent/real state. Doing
  so was separately approved for this operator-test rollout, but execution and
  verification remain pending; these tests only qualify the supported SDK path
  on disposable fake data.
- Restart/upgrade behavior of a *deployed* ledger, gateway session handling,
  attested transport and client-side session renewal remain outside this packet.
- `_logs`/`_authOrigins` emptiness here is evidence for the exercised fixture path
  plus the existing SDK logging/AuthAlert controls, not for restored historical rows.
