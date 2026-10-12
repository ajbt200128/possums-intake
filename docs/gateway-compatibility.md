# Accounting boundary simplification and compatibility

## Scope and observed gateway

This pass changes the standalone intake only. No gateway edits, inference, integration, deployment, dependency upgrades or live export. Read gateway `AGENTS.md`, `SPEC.md`, `OVERALL_PLAN.md` and full `PRIVACY.md`; current code supersedes historical store proposals.

Current gateway source is **memory-only accounting**, not an external-ledger client:

- `../possums/src/auth.rs`: `ProvisionedAccount`, `Session.account_id` and account initialization use opaque nonempty UTF-8 IDs up to 128 bytes and `u64` demo microunits.
- `../possums/src/accounting.rs`: `Accounting` owns memory balances/submissions; `finish` calculates charges from the saved quote and validated usage.
- `../possums/src/catalog.rs`, `generation.rs`, `inference/*`, `api_stream.rs`: catalog authentication/prices, maximum quoting, context/token/stream/usage validation and upstream handling belong to the gateway. Intake implements none of them.
- Gateway `src/api.rs` typed API requests currently reject unknown fields. That is a separate public gateway boundary, not a ledger adapter, and was not changed. The referenced forwarding-plan worktree is also not a ledger adapter.

## Simplifications implemented

- Custom accounting requests **ignore extra JSON fields**, including nested/null values. Extras do not enter typed accounting inputs, persistence, response serialization or telemetry. HTTP still rejects missing/null/wrongly typed known fields, ambiguous duplicate top-level keys, malformed framing and invalid UTF-8. Known opaque strings reject unpaired surrogates; ignored extensions are not interpreted or normalized.
- Use supported `mime.ParseMediaType` instead of exact header-text comparison. `APPLICATION/JSON; charset=UTF-8` works. JSON bytes are still UTF-8; unsupported media types and malformed headers fail with closed diagnostics.
- Removed repeated HTTP monetary/timeout checks. HTTP checks shape/types; ledger core checks canonical exact money, positive reserve/credit, bounded required timeout, account identity and financial invariants before effects. Semantic invalidity now produces existing `LEDGER_INPUT_INVALID` 400 rather than a duplicated HTTP validation code. `Money.units` parses/checks uint64 once; `ParseMoney` reuses it.
- Account IDs preserve the gateway's 1–128-byte UTF-8 contract, without an additional control-character ban, normalization or trimming. NUL/newline identities round-trip exactly through supported record creation, keyed HTTP and SQL lookup; JSON escapes them. This does not change internal native record IDs or schema.
- Removed duplicate whole-body UTF-8 validation. No generic compatibility layer, aliases, version guessing or new configuration framework.

Kept: shared-key authentication, bounded transport, exact uint64 arithmetic, charge ≤ hold, mandatory immutable timeout, expiry/replay protection, serialized supported transactions, first-terminal-wins, complete snapshots, and honest unknown outcomes. These protect the ledger's own authority even when the gateway already calculated a charge. Native PocketBase mutation fields/modifiers and telemetry/export allowlists remain closed: they are different boundaries, not additive gateway DTOs.

## Compatibility limits and future adapter

Existing valid intake requests and success envelopes are unchanged; additive fields work **with the updated ledger**. Older strict ledger servers can still reject extras: upgrade the ledger before sending them. Changed units, field meaning, newly required fields or idempotency rules require an explicit contract change, not silent field ignoring.

A future separately authorized gateway adapter must:

- Encode exact `u64` microunits as canonical decimal JSON strings; use the defined ledger request/response envelopes, not forward gateway DTOs wholesale.
- Preserve session-derived account IDs; do not send prompts/history/tools, credentials, model/token/usage/catalog data or arbitrary request metadata.
- Generate and preserve timestamped `v1.<issued_ms>.<nonce>` credit/reserve keys. Gateway 32-byte in-memory submission IDs are not those keys. Settlement/refund reuse the returned ledger reservation ID.
- Require authoritative reservation before inference and inspect terminal state/charge/expiry, not equate HTTP 200 or delivery with the requested financial effect. Unknown outcomes stay unknown; no automatic inference replay or invented refund.
- Ignore optional additive response fields while validating required receipt evidence. Do not require new optional fields from older compatible servers. Gateway-local completed/in-flight counts are not silently invented by the ledger.

This pass is not end-to-end gateway integration or a promise of arbitrary future protocol compatibility.

## Local verification

Parent passed full `go test -race ./... -count=1 -timeout=180s`, vet, **53 offline delivery tests**, compiled-main curl/restart/shutdown smoke and actual Chromium native-account checks. Independent read-only review found no confirmed defect and passed focused HTTP/ledger/operator tests.

`TestAdditiveAccountingCompatibilityOnActualHTTP` exercises all seven endpoints, nested/null/large-numeric/lone-surrogate extras, MIME parameters, differently cased ignored aliases, exact control-containing IDs, changed extras on credit/reserve retries, unchanged total/holds and first terminal state. Required/malformed/duplicate/precision/timeout/authentication negatives remain. The compiled-process test also submits an additive provision request and retains restart/privacy checks.

Initial full race run exposed one outdated unknown-field rejection fixture; it now tests a malformed **known** field instead. An unrelated operator fixture reported initialization unavailable once, without enough evidence to identify its cause. Its failure output now includes safe code/stage/constraint/outcome, never raw errors. That test passed three focused race repetitions and the subsequent complete race suite. No production budget was widened, check skipped or automatic retry introduced.

A fresh local offline Linux/arm64 image/probe build and actual runtime passed all ten original gates (unchanged real-init file limit/commit-unknown/SQL rollback/same-state refusal), native operator/key separation/hold/logging checks and all 100 asset hashes. The image balance probe now includes ignored nested fields and standard JSON media parameters; its hostile-input case still requires 400 for a malformed known field, not for a harmless extension. Nonroot/read-only-root/network-none/export-off controls and immutable loaded-config verification are unchanged. Owned disposable state/source/containers/volumes were removed; public Nix/Docker artifacts may remain.

- Frozen source: `0555c454b3142e9168cc602bfc359a982057af58499cc26ad2a5db4cdee62835`.
- Archive: `/nix/store/xg5fjwyzyfvkd6k07298qsbcvhf2rc50-possums-intake-local.tar.gz`; SHA256 `c5f1981055fc74abbb9c331572c4aae63a4fce930ad0d9ad00abd13e3c12075b`.
- Archive-transport manifest: `sha256:bee5abf281965d03a39dfe4d82a4c961e36034bcc05a3eaeff4aadb462606763`.
- Original/saved config: `sha256:0b839ebf84778f5b8c44169aebdeabcbc640e1a54c7e6a73034630c60463844a`.
- Docker immutable runtime: `sha256:2a891f877a1e7ef518cffb8d11b37e35dc9c4af7b4b7ca8c4619b6e2fa3cd7b0`.
- Exec-only probe: `/nix/store/14i5s489r2pshmwipf0wibn7ya1shlap-intake-image-smoke-probe-0.1.0-local/bin/nix`; not shipped.

Use the frozen-source/image helper commands in [delivery evidence](delivery.md#final-parent-native-qualification), substituting these paths and confirming this identity before reuse. Older identities are historical. This remains cached local evidence, not fresh independent A/B, signed exact-main provenance, an in-image browser, live activation or real-data qualification.

Privacy authority: [intake accounting boundaries](../PRIVACY.md#accounting-and-processor-boundaries), [forbidden defaults](../PRIVACY.md#forbidden-collection-and-defaults), [failure behavior](../PRIVACY.md#export-retention-and-failure-behavior); gateway [data boundaries](../../possums/PRIVACY.md#data-handling-boundaries), [forbidden telemetry](../../possums/PRIVACY.md#telemetry-permitted-signals-and-forbidden-data). Extras were tested synthetically; accepting them is not permission to send sensitive content. No diagnostics/telemetry/persistence scope was expanded.
