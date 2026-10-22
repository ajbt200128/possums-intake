# Packet 2 — bounded HTTP route surface

`NewHandler(accounting ledger.Accounting, ready func() bool, adminKey string, observer TerminalObserver) (http.Handler, error)`
builds the **custom-only** `net/http` mux. It uses no stock PocketBase router,
installer, dashboard, CRUD, auth, backup or realtime route. PocketBase core is
already initialized by the caller; this package owns no SQL, state, retries,
scheduler or financial arithmetic. A narrow final-outcome observer runs after
actual response writes, outside accounting transactions. All accounting goes through
`ledger.Accounting`, and HTTP delivery is never proof of a monetary effect.

## Runtime key

- The key is the **raw string** bytes of `adminKey` (the process reads it from
  the `LEDGER_ADMIN_KEY` environment variable in `cmd/intake`). The accepted
  wire encoding is therefore a raw string of at least `MinAdminKeyBytes = 32`
  bytes; callers must supply 32+ random bytes.
- A shorter key makes `NewHandler` return a locally authored, content-free
  error (`LEDGER_RUNTIME_KEY_INVALID`). The key is never echoed in an error,
  response, header or body, never placed in a URL, and compared with
  `crypto/subtle.ConstantTimeCompare`.
- Every accounting route — including reads, renewal and `balance` — requires
  the same `Authorization: Bearer <key>` header. `GET /healthz` never does.

## Request contract

Every accounting route is `POST`, exactly one JSON object, `Content-Type:
application/json` (one header; standard media-type parameters accepted), body ≤ `MaxBodyBytes = 4096`,
no `Content-Encoding` other than `identity`, no query parameters and no
trailing data. A single `Idempotency-Key` header is required on `/credits` and
`/reservations`. Extra fields are ignored, never bound/persisted/echoed/exported.
The decoder rejects duplicate keys, null required fields, missing required fields,
non-object JSON, non-UTF-8 bytes (including invalid byte sequences — no replacement),
unpaired UTF-16 surrogate escapes in known values, malformed escapes and
type-mismatched fields. All of these are a closed `LEDGER_HTTP_INPUT_REJECTED`
400. Valid surrogate pairs, escaped literal backslashes and literal U+FFFD remain
valid; account identity is never normalized. Duplicate Authorization, Content-Type,
Content-Encoding or Idempotency-Key fields are 400 on every custom route. Missing
or wrong credentials, including one comma-joined wrong credential, are 401.
Error text is always locally authored and never derived from the request.

Money strings use the exact ledger parser `^(0|[1-9][0-9]{0,19})$` checked
against `18446744073709551615`; `amount`/`maximum` must be positive and
`charged_amount` may be zero. `timeout_seconds` must be a JSON integer 1–1800,
not a fraction, exponent or string. These monetary/timeout semantics are checked
once by the ledger core, yielding `LEDGER_INPUT_INVALID` 400; HTTP checks JSON
shape/presence/types, not pricing, usage or duplicate financial rules. Account IDs
are the actual generated 15-character lowercase-alphanumeric `accounts.id` values;
no external label or credential-to-wallet binding remains. Native PocketBase
administration follows supported SDK validation and trusted stock authority, not
this custom accounting parser or a new project permission tier.

## Routes and success shapes

| Path | Request fields | Success body |
| --- | --- | --- |
| `/credits` | `account_id`, `amount` (+ `Idempotency-Key`) | `{"credit":credit}` |
| `/reservations` | `account_id`, `maximum`, `timeout_seconds` (+ `Idempotency-Key`) | `{"reservation":reservation}` |
| `/reservations/renew` | `reservation_id`, `expected_expires_at` | `{"reservation":reservation,"observed_at":"…"}` |
| `/reservations/settle` | `reservation_id`, `charged_amount` | `{"reservation":reservation}` |
| `/reservations/refund` | `reservation_id` | `{"reservation":reservation}` |
| `/reservations/lookup` | `reservation_id` | `{"reservation":reservation}` |
| `/balance` | `account_id` | `{"account_balance":[account],"pending_reservations":[…],"available":"…"}` |
| `GET /healthz` | — | `{"status":"ok"}` or `{"status":"not_ready"}` |

`account_balance` is a **list of at most one** account (the core returns one
object). `pending_reservations` is always a list, never null. Account fields are
exactly `account_id`, `balance`, `enabled`. Reservation fields are exactly the
table fields with fixed UTC `YYYY-MM-DDTHH:mm:ss.SSSZ` time strings and null
`charged_amount`/`terminal_at`/`retain_until` while active. Credit fields are
exactly the credit table fields. Reservations include both immutable
`initial_expires_at` and current `expires_at`. `ledger_meta` and all SDK auth fields
are never returned.

`/accounts/provision` is retired and returns a closed 404; it stays reserved from
Lisp route ownership. Create one account through the supported native Accounts
auth-record editor, not through shared-key accounting. The generated record ID is
then the account reference for credits and reservations. Customer credentials must
never enter custom accounting calls.

Renewal compares the exact canonical UTC millisecond `expected_expires_at` with the
current deadline, extending only an active enabled-account hold before expiry.
An older valid token returns the current row without extending it again; a future
token is refused. Terminal/expired states remain absorbing. Known renewal
refusals can include the committed authoritative reservation and `observed_at`;
transaction/commit uncertainty supplies no usable receipt. Renewal adds no worker,
automatic retry or hold-expiry grace. The accepted renewal route remains excluded
from the existing request/error telemetry population.

## Error status map

Closed request diagnostics (locally authored, never from ledger or parser):

| Code | Status |
| --- | --- |
| `LEDGER_HTTP_UNAUTHORIZED` | 401 |
| `LEDGER_HTTP_NOT_FOUND` | 404 |
| `LEDGER_HTTP_QUERY_REJECTED` / `_ENCODING_REJECTED` / `_CONTENT_TYPE_REJECTED` / `_HEADER_REJECTED` / `_IDEMPOTENCY_REQUIRED` / `_INPUT_REJECTED` | 400 |
| `LEDGER_HTTP_OUTCOME_UNKNOWN` | 503 |

Closed ledger diagnostics map as:

| Code | Status |
| --- | --- |
| `LEDGER_INPUT_INVALID`, `LEDGER_KEY_INVALID` | 400 |
| `LEDGER_ACCOUNT_UNKNOWN` | 404 |
| `LEDGER_CREDIT_INSUFFICIENT`, `LEDGER_ADMISSION_DISABLED`, `LEDGER_KEY_CONFLICT`, `LEDGER_CHARGE_EXCEEDS_HOLD`, `LEDGER_SETTLEMENT_EXPIRED`, `LEDGER_AMOUNT_OVERFLOW`, `LEDGER_KEY_WINDOW` | 409 |
| `LEDGER_RENEWAL_CLOSED`, `LEDGER_RENEWAL_PRECONDITION`, `LEDGER_RENEWAL_NO_PROGRESS` | 409 |
| `LEDGER_EVIDENCE_UNAVAILABLE` | 410 |
| writer/work/begin/storage/random/corrupt/commit/closed and all startup codes | 503 |

Any unexpected error or panic becomes `LEDGER_HTTP_OUTCOME_UNKNOWN` 503 with
`outcome:"unknown"`; raw exceptions, driver text, stacks and panic payloads
never cross the boundary. Known rolled-back/no-effect (`no_effect`) and unknown
outcomes stay distinct.

`/reservations/settle` on an expired hold returns **409** and preserves the
authoritative expired reservation separately from the diagnostic in
`{"code":…,"stage":…,"constraint":…,"outcome":"expired","next_step":…,"reservation":{…}}`.
A `200` never means the requested effect won: a conflicting terminal attempt
returns the first committed row.

`GET /healthz` reads only the cached `ready func() bool` and performs no
accounting work. Unknown paths, wrong methods and every native/framework route
get a closed fixed `LEDGER_HTTP_NOT_FOUND` 404.

## Tests

`devenv shell -- env OTEL_SDK_DISABLED=true go test ./internal/httpapi -count=1` runs, against an actual
isolated ledger where state matters and stubs for error/privacy boundaries:

- native-created account credit/reserve/renew/lookup/settle/refund/balance success, retired provisioning refusal, complete field
  sets, pending-row list, account scoping and expired-row receipt;
- every accounting route without/with a wrong/duplicated key, and the key never
  echoed;
- ignored additive fields, changed extras on idempotent retries,
  JSON media-type parameters and exact generated record identity;
- duplicate keys, null/missing known fields, non-object and trailing JSON,
  malformed UTF-8, oversized/compressed/query inputs, money/timeout wire rules;
- pinned status for each closed ledger code, unexpected errors and panics as
  unknown-outcome 503, closed 404 for unknown/native routes and methods;
- fixed cached health with no accounting.

Integration/metrics checks, final write error/short-write/panic/cancellation
classification and privacy boundaries are recorded in
[HTTP/metrics verification](../../docs/http-metrics-verification.md).
No real data, provider calls, live export or commits are involved.
