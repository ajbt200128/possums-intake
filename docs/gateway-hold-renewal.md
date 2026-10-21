# Renewable holds — approved local contract

## Authority and implementation status

The operator approved local intake implementation of paired 12-hour customer
native authentication and conditional renewable holds. The operator then explicitly
rejected cutting off accepted streams on intake failure and accepted the
uninterrupted-stream/operator-loss alternative. **That later decision supersedes
both the proposed renewal-failure stopping margin and the earlier V0 29-minute
accounting work cutoff.** Gateway/client source changes belong to the separate
gateway workstream; this document does not establish that they are implemented.

This worktree starts at `cb64b630e86b1a894697c988ecaa203e0bed5224` on
`feat/gateway-session-renewal`.
This is the local implementation contract; executed qualification is recorded below. No runtime configuration update, existing-state migration, live probe,
paid inference, secret binding, installation, deployment, push or merge is authorized
by local source approval. Preserve the original checkout's dirty `PLAN.md` and its
untracked notes/scripts/qualification work. There are no existing customers to
migrate; the operator's test state still must not be discarded or reset.

**Subsequent deployment approval:** the operator explicitly approved the guarded
existing-disk maintenance/serving rollout and existing-collection duration update.
See [deployment decisions and gates](gateway-renewal-deployment.md). Only manual
operator tests currently consume the deployed intake; gateway integration remains
local and separately unqualified. This approval does not establish activation.

## Gateway ownership and uninterrupted accepted work

Intake is the sole lease and monetary authority. Gateway owns the original accepted
operation, quote and trustworthy usage; it maintains no shadow spendable balance.

- Initial admission requires current authentication, a confirmed active reservation
  with usable lease remaining and the existing post-await session/submission checks.
  An uncertain initial reservation never authorizes prompt dispatch, a replacement
  hold or inference replay. Admission authority is rechecked before the accepted
  ownership handoff, which occurs before prompt-bearing preflight/tokenizer work.
- After that handoff, the uninterrupted-work policy covers preflight, inference and
  connected delivery backpressure. Neither lease age, expiry, intake outage nor
  failed renewal cancels already accepted work. It does not admit a new operation.
- Recommend an initial 1800-second lease and one renewal after 600 seconds; after
  each valid confirmation schedule the next 600 seconds later. At most one renewal
  is in flight. Use the existing bounded 5-second whole-RPC attempt. No catch-up
  bursts or automatic mutation retries.
- After **one unconfirmed renewal** (refusal, timeout, malformed/mismatched response,
  unknown commit/delivery or lost response), permanently stop scheduled renewals for
  that operation. Do not start a recovery/lookup loop or later periodic retry. If
  the last confirmed lease has already run out before dispatch, skip renewal and
  enter this same renewal-stopped state. **The original accepted work continues.**
- Track lease evidence conservatively, not as a work cutoff: translate a confirmed
  `expires_at - observed_at` interval from the attempt's monotonic pre-dispatch time.
  Network/processing time consumes that interval. Older responses never supersede
  newer confirmation; seeing the same expiry again cannot move its local deadline
  later. No late response reopens a terminal owner or restarts stopped renewals.
- Never settle/refund early while intentionally continuing generation. At normal
  completion or genuine disconnect/cancellation, the sole accepted-work owner makes
  one bounded terminal billing attempt: validated final or last trustworthy
  cumulative usage, at the original quote, capped by the original maximum. Never
  sum cumulative snapshots or estimate usage from text. Refund only when no
  trustworthy billable usage exists. Preserve an already observed actual terminal.
- A real disconnect/cancellation still closes the original upstream operation.
  Logout/reset/body loss cannot orphan terminal ownership. No lock spans network
  I/O and no network operation runs in `Drop`. No automatic inference replay.
- An uncertain terminal delivery remains **unknown**, not charged/refunded. There
  is no second debit, compensating refund, replacement reservation or lookup loop.
  Expired reservations cannot collect late settlement; the operator explicitly
  absorbs uncollected upstream cost. Continuing inference without a live lease is
  an accepted exception for this already accepted operation, not new-admission
  authority or guaranteed collection. Closing transport is not proof of immediate
  upstream compute termination. No global shutdown bound is introduced.

## Exact intake wire contract

`POST /reservations/renew` uses the existing shared accounting bearer
`LEDGER_ADMIN_KEY`, not customer or native superuser tokens. Existing 4096-byte,
one-JSON-object, JSON media type, no query/compression/trailing-data rules apply.
Duplicate headers/keys and absent/null/wrong-type required fields are refused.
Additive unknown fields are ignored, not retained/echoed/exported. Accounting must
never be sent prompts, tools, model/token/quote metadata or customer credentials.

Required JSON fields:

- `reservation_id`: existing 32-lowercase-hex reservation ID.
- `expected_expires_at`: exact last-confirmed `YYYY-MM-DDTHH:mm:ss.SSSZ` expiry.

No renewal idempotency header or new key, account selector, amount, timeout override
or caller-selected new expiry. The pair of reservation ID and expected expiry is
the conditional mutation identity.

HTTP 200 response: `{"reservation":{...},"observed_at":"..."}`.

The complete reservation object has these fields on reserve, renew, lookup,
settle, refund and pending-row responses:

- `reservation_id`, `idempotency_key`, `account_id`: unchanged strings.
- `maximum`: unchanged canonical positive uint64 decimal string.
- `timeout_seconds`: unchanged original JSON integer, 1–1800.
- `initial_expires_at`: new immutable original expiration, fixed UTC milliseconds.
- `expires_at`: current authoritative expiration, same fixed timestamp format.
- `state`: `active`, `settled`, `refunded` or `expired`.
- `charged_amount`, `terminal_at`, `retain_until`: null while active; existing
  canonical amount and fixed-time terminal invariants otherwise.

`observed_at` is effective accounting time from the same transaction, not delivery
time or a billing receipt. Known renewal refusals carrying a confirmed row use
`{code,stage,constraint,outcome,next_step,reservation,observed_at}`. Other errors
retain the existing content-free diagnostic shape without a usable row.

A gateway must validate exact reservation/account/key/maximum/timeout/initial-expiry
binding; canonical amounts/times/state/null fields; and for an active successful
renewal `expires_at > expected_expires_at`, `expires_at > observed_at` and
`expires_at <= observed_at + timeout_seconds * 1000`. It must preserve actual
terminal outcomes rather than interpret HTTP 200 as the requested financial effect.
These first-party rows are never diagnostic/support/telemetry data.

## Atomic conditional semantics

Every read, key/clock decision and effect uses the supported transaction-scoped
PocketBase app under the existing serialized writer gate. No network or exporter
inside the transaction. Existing 2-second writer/1-second work/250ms busy budgets
remain; SDK begin/commit/fsync are not hard real-time bounded.

1. Validate input syntax/range and the stored reservation. An expectation before
   `initial_expires_at` is invalid and returns 400 without a row, before expiry or
   other accounting work. Then validate the account and hold invariants and apply
   due expiry: `now >= expires_at` cannot renew even if cleanup has not run.
2. A terminal reservation is absorbing: 409 `LEDGER_RENEWAL_CLOSED`, actual terminal
   row and observed time. Expiry discovered here commits before the reply.
3. An otherwise valid active hold on a disabled account refuses renewal, including
   a duplicate, with existing
   409 `LEDGER_ADMISSION_DISABLED`. This does not release the old hold or prohibit
   settlement, refund or scoped reads.
4. Enabled active hold, expectation equal to current expiry: new expiry is exactly
   `now + original_timeout_seconds * 1000`, only when strictly later. Otherwise
   return 409 `LEDGER_RENEWAL_NO_PROGRESS` without extension.
5. Expectation earlier than current expiry (but not before initial expiry): return
   200 current active row with **no extension**. Duplicate/reordered deliveries do
   not multiply extensions. Response is current state, not a cached old reply.
6. Expectation later than current expiry: 409 `LEDGER_RENEWAL_PRECONDITION`, no
   extension. A given expected expiry can cause at most one successful extension
   because each successful change strictly increases expiry.

Known refusals retain closed stage/constraint/action and any separately confirmed
row. `no_effect` on an active refusal means no renewal effect, not proof that no
other account expiry occurred. Missing/pruned evidence is existing 410
`LEDGER_EVIDENCE_UNAVAILABLE`, never proof of refund/nonexistence. Malformed input
is 400. Existing writer/storage/unknown-completion errors remain 503 and never
fabricate a row or rollback. A lost reply can hide a committed extension or debit.

Settle/refund/expiry races remain first-terminal-wins. Late settle stays 409
`LEDGER_SETTLEMENT_EXPIRED` with expired row; conflicting later terminal requests
may return 200 with a different first terminal. No post-expiry debit API is added.

## Storage, clock, retention and upgrade

Persist one additional immutable value, `initial_expires_at`, initially equal to
`expires_at`. Original admission remains validated from initial expiry minus
original timeout; mutable expiry must not move the original key admission window.
No renewal counter, history, owner registry, fencing service or shadow balance.

Each extension is bounded by the original lease (at most 1800 seconds ahead of
effective intake time). There is no cumulative lifetime cap and no coupling to the
12-hour session lifetime. The accepted-work owner survives session removal.

Use existing `now = max(SQLite UTC milliseconds, persisted clock_high_water_ms)`.
Forward jumps may expire early; backward jumps may freeze effective time, refuse
no-progress renewal and delay real-world orphan release. Never reset the watermark.
No 30-minute wall-clock cleanup or collection guarantee is claimed.

Active rows and original reserve keys survive their entire lifetime, including
beyond the original 24-hour new-key window. Reserve replay still compares original
account/maximum/timeout and returns current state; it never renews. Terminal
retention stays `max(key-issued + 24h, terminal-time + 24h)`. Expiry's terminal time
is lease expiry, not cleanup discovery; long outages may exhaust retained evidence.
Existing bounded cleanup, settlement/read-time expiry and persisted replay
watermark protections remain. Restart grants no lease and creates no owner.

Fresh state uses schema v2. A narrow explicit `ledger-upgrade --state ABSOLUTE_PATH`
maintenance operation is for an existing **stopped** supported installation only;
qualification executes solely on disposable synthetic directories. It validates
v1 before atomically copying old expiry to initial expiry and changing exact
versioned layout. Preserve all accounts, amounts, rows, identities, constraints,
auth state and high-water mark. Validate already-v2 as a no-op. Refuse unknown,
missing/partial/corrupt state. No serve-time migration, initialization/reset,
financial restore or downgrade. Prior code must fail on v2, not reinterpret it.
No upgrade while serving is permitted. The subsequently approved stopped-instance
rollout and minimal managed-unlock addition have separate deployment gates below.

## Compatibility and privacy

This narrowly supersedes immutable-current-expiry guidance for Renew only;
original reserve inputs remain immutable. Update the gateway's actual decoder and
expiry validation before gateway integration; do not infer compatibility merely
from an additive DTO field. Intake may roll ahead only under the operator-confirmed
manual-test-only consumer inventory and the separate stopped-maintenance gates. No broad nonexistent-customer migration or compatibility
framework. Website snapshots must continue showing every defined pending field.

Follow [accounting boundaries](../PRIVACY.md#accounting-and-processor-boundaries),
[typed failures](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates)
and [forbidden collection](../PRIVACY.md#forbidden-collection-and-defaults).
No new telemetry family/category/renewal observation population, activity logs,
traces, raw exceptions or diagnostic-triggered work. Existing 39-category numeric
classification is reused where applicable, but no export activation is implied.
Disk/WAL/deleted pages, trusted unrestricted native administration, platform-managed
key custody and optional snapshots remain existing boundaries. Native writes can
invalidate these invariants; this packet does not constrain their accepted authority.

## Qualification and release gates

Local acceptance must cover actual Go/PocketBase HTTP, duplicate/reordered/lost
replies, renew/settle/refund/expiry/disable races, exact expiry, no-progress and
clock jumps, long-lived key retention, restart/pruning, upgrade preservation and
fault rollback, hostile inputs/errors and no renewal telemetry. Gateway separately
qualifies admission/accepted handoff, continued streaming under outage/backpressure,
once-only terminal ownership, trustworthy cumulative usage and unknown outcomes.

Source tests, generated images, installed clients and deployed services are distinct.
Retain PocketBase v0.40.5's pre-v1 production-critical warning. Native Linux matching
builds, artifacts, review, provenance/signing and protected activation remain separate.
No live acceptance is established by this document.

### Executed local evidence — 2026-10-21

Historical local checkpoint before the managed-upgrade addition: uncommitted
source on the baseline/branch above, with no commit, runtime update or deployment. Pinned development profile selected Go 1.27.2 on darwin/arm64,
PocketBase v0.40.5. Tests used the existing local module cache with `GOPROXY=off`,
`GOSUMDB=off`, `GOTOOLCHAIN=local`, `OTEL_SDK_DISABLED=true` and `-mod=readonly`.
No dependencies were installed or changed. The qualified Go/source-test manifest is
`cec2606902aacb773c3a1dbd15b8afd3dbc3cd3f79e0f436f7a8fd4392dde218`
(173 files): sort the union of tracked/nonignored untracked `.go` paths plus
`go.mod`, `go.sum`, `scripts/smoke.py`; SHA-256 the concatenation of each UTF-8 path,
NUL, and raw SHA-256 file digest. This excludes documentation and is not an image
or whole-repository provenance claim. LSP probes for the changed production
paths reported no errors; actual compiler/test results below are the primary gates.

Parent executed and passed:

- `go test -mod=readonly -race -tags elpscheck ./... -count=1 -timeout=240s` — all
  packages, including ledger, native auth, website, HTTP, CLI and telemetry.
- `go vet -mod=readonly -tags elpscheck ./...` and `git diff --check`.
- A compiled candidate with `scripts/smoke.py --self-test` (7 checks) and
  `scripts/smoke.py --binary` using only a disposable local binary/state. Actual
  CLI/curl paths cover initial reserve, renewal, duplicate no-reextension,
  terminal refusal, settlement/refund/expiry, snapshots, restart and shutdown.
  The harness reflects the existing public website root (GET 200, JSON POST 415)
  while preserving native-route 404 and all accounting shared-key checks.
- An actual binary built from baseline `cb64b630...` refuses a new v2 directory
  with `LEDGER_STARTUP_SCHEMA`; the current CLI subsequently validates it.
- Baseline binary explicit init creates disposable v1; current compiled
  `ledger-upgrade --state` upgrades it to v2 with unchanged native customer auth
  options and clock watermark. The baseline binary then refuses it without
  changing those values. No real installation was opened.
- Original v1 DDL is byte-for-byte unchanged; `go.mod`, `go.sum` and `PLAN.md`
  remain unchanged from baseline.

`internal/ledger/renewal_test.go` exercises concurrent duplicates and terminal/expiry
races, conditional refusals, unchanged funds, known/unknown completion, settlement
after initial expiry, >24-hour active-key retention, restart, backward/stalled
clock, orphan expiry and bounded pruning/replay rejection.
`internal/ledger/upgrade_test.go` compares populated accounts, active and all
terminal rows, credit records, native credentials and watermark across upgrade;
checks exact fresh DDL, v2 no-op, corrupt/missing/unsupported refusal, dependent
native-schema rollback, during-DDL/after-validation rollback and lost completion.
`internal/httpapi/renewal_test.go` covers complete DTOs, exact timestamp/input rules,
disabled/closed/precondition/no-progress/expiry results, additive-field privacy,
unknown dispatch and response-writer loss without retries or renewal telemetry.
The native duration/update/session tests are recorded in
[session lifetime evidence](gateway-session-lifetime.md).

Independent read-only review of production changes, tests and both contracts found
no handoff blockers. It did not itself rerun tests or claim runtime qualification;
parent test evidence is scoped above. The final documentation clarifies that invalid
pre-initial expectations are rejected before accounting work. Review is not proof
of deployed durability or absence of all defects.

**Remaining gates:** managed-upgrade source/profile qualification; generated-image
and native Linux matching builds; exact-source provenance/signing; execution of
the approved stopped-installation migration and native duration update; protected
activation and runtime verification. Gateway consumer/paired decoder and
interrupted-stream qualification remain separate integration gates. No installation,
real-data, funds, inference, telemetry export or deployment acceptance is claimed.
