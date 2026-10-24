# Merged-account intake deployment — current work

## Latest operator decision — fresh synthetic volume reset for newest qualified source

The operator reported that Tinfoil resolved the QEMU boot problem and changed the
attached volume, then manually deployed **v0.1.19**. A bounded passive native read
observed current tag v0.1.19/platform Running, expected repository/shape/security,
and one ledger attachment on the instance host. A fresh public process snapshot
observed the expected published-image **replacement maintenance** process exited
**1**, no OOM/restarts and no available health status. Its correlation to the
operator's latest submission is unqualified because that submission time was not
available. It is not a serving-readiness or initialization receipt. The attached
size differs from the old 1 GiB binding; the operator's displayed listing says
8 GB, but no allocation bytes or replacement identity are inferred from that
label. Historical original-target admission must not be reused.

The operator then explicitly confirmed **synthetic state only**, permanent discard
of the currently attached ledger volume and all its application state, and
**one blank replacement of the same currently configured size on the same
instance**. They confirmed the intended key is saved identically in their password
manager and the current managed `LEDGER_VOLUME_KEY_B64` secret. Use that existing
secret privately; never read/regenerate/replace it. This is the selected next
runtime plan, not another v0.1.19 replay. It narrowly supersedes prior disk
preservation for this reset only; the historical extra existing-only diagnostic
allowance below was unused by the agent and is not an additional reset/retry right.

Before deletion, qualify the newest source/image and signed fresh-bootstrap plus
existing-only serving profiles. Reconcile stopped writer and exact current
instance/host/disk/size, then one explicit detach/delete/create/attach sequence,
one fresh-disk initialization and serving only after supported successful exact
process completion. Preserve domain/CPU2/memory8192/GPU0/CC-on/debug-off, no extra
standing disk, real funds, payment/gateway activation, inference or raw logs.
Unknown mutation outcomes require passive reconciliation, not automatic replay.
No reset operation has been issued by the agent under this new authorization.
Policy/custody details are in
[the scoped fresh reset](../PRIVACY.md#subsequent-fresh-volume-reset-for-the-newest-qualified-source).

The latest user-supplied health tuple reports
**`LEDGER_VOLUME_EXCHANGE_UNAVAILABLE` / `volume_prepare` /
`completed_volume_control_exchange` / `not_ready`**. It identifies a pre-ledger
boundary but not which connect/send/receive/response branch failed. Project
`volume_linux.go` matches the documented SOCK_SEQPACKET/format2/unlock request;
immutable CVM `worker.go` maps activation errors to exact `failed`, which is not
proof of a wrong key. A blank replacement disk requires explicit initialization;
maintenance `ledger-replace` is existing-disk unlock-only and cannot initialize its
encryption. No evidence here establishes blankness or the live volume failure's
root cause.

The source-only diagnostic packet now preserves ten closed schema reasons and
six closed volume-exchange boundaries through the existing four-field bounded
startup record. It adds no raw values/errors, operation, retry, new observer
category or record field. Schema-only independent review found no material
findings; the parent independently passed the full ledger-package race suite.
Combined independent review found no material findings. Offline vet, build,
module verification and the complete Python scripts suite passed. The initial
parent full-root race wrapper timed out without a final result; a later delegated
invocation used invalid `-p1` spelling and exited before tests ran. Neither result
is application-failure evidence. The corrected invocation used separate `-p 1`
arguments, ran once and passed: **12 packages passed, three had no test files**.
Nonverbose output does not certify no individual test skipped. The direct child
joined and its process group was absent afterward; source snapshots and Git status
were unchanged. Linux-only new exchange tests still require native qualification;
an offline missing dependency is not a passing compile. Actual generated-image
qualification and exact-final-main native matching builds/provenance/signing remain
mandatory before deletion or activation.
Honeycomb remains off: its runtime is constructed after volume preparation/open
and cannot diagnose these earlier failures. Conditional operator permission to
enable approved metrics does not authorize logs/traces or bypass privacy gates.

## Previous operator decision — extra existing-only diagnostic allowance

After the failed replacement and consumed v0.1.20 diagnostic transition below,
the operator explicitly selected **One existing-only diagnostic redeploy** after
disclosure that initialization remains unknown, no source fix has been reproduced,
ordinary SDK maintenance may modify valid existing state and an identical failure
is possible. This authorizes preparation/qualification and **one additional**
existing-only diagnostic transition on the same target/disk/key, subject to normal
source/signing/original-target/stopped-writer gates. It does not renew replacement
or initialization authority or authorize reset/restore/rekey/allocation, credential
changes, raw logs or automatic replay. Unknown outcomes require passive
reconciliation. Earlier statements that no further runtime transition was admitted
record the boundary before this new decision, not an unused historical allowance.

A bounded passive preflight of the supported public process surface returned
**`PROCESS_SNAPSHOT_TRANSPORT_CAUSE_UNKNOWN` / `passive_process_preflight`**,
observed HTTP status unavailable, **no mutation**. Fresh exact-process identity
is therefore not qualified. The frozen v0.1.20 controller is specifically bound
to the preceding v0.1.19 maintenance-process baseline, not this newly authorized
transition; it must not be rerun or bypassed. The additional runtime allowance is
**unused and blocked on qualification**, not a completed or attempted deployment.
Preserve the existing disk/key and partial state.

## Previous runtime observations — replacement and first diagnostic allowance consumed

Private PR8 normally merged at **`b96debdfeac917c21ef76103ffe783eb80671c33`**.
Exact-main check **39816408475** and fresh independent native A/B qualification
**39817837988**, attempt 1, passed. Downloaded A/B identity files agreed on image
**`sha256:0e84e836678a3edabe7e4ba6253310002eed6daa45da70b397121a886a34bcb7`**
and archive **`53724a5e29c2d72d88f1260ad04ac34afe41b67d38011b9377ab0837c1219495`**.
The selected native-owner publisher completed one zero-retry digest-preserving
copy and original registry/package/private-source-link postchecks and returned
that three-field receipt. Older identical bytes were not relabeled as new-source
qualification. Browser-inside-image remains `not_run`; prior compatibility remains
`unknown` and is not what this destructive fresh-state procedure promises.

Public PR18 normally merged at **`4c2e4d02a5797468661a0009afe3b6c31a1c3163`**.
One fresh immutable **v0.1.19** protected signing invocation **39821727010**,
attempt 1, succeeded. Parent independently downloaded both assets, checked API
and local byte digests/hash file, exact replacement template/config, CVM 0.14.14,
CPU2/memory8192/GPU0/four disks, and supported GH cryptographic verification plus
exact source/tag/GitHub-hosted signer/invocation certificate fields. Config SHA256
is **`05f72d32a1d4dfb94c2c0f0097c201e77a697757561acff397c59a7207dc7525`**;
manifest SHA256 is **`3ee86f00b1e4fe8436ec443ba5594097ca58f9ce23b358ec4d986ee855013250`**.
Owned public verification workspaces were removed after tool completion, without
an erasure or whole-host-transience claim. These are release receipts, not runtime
attestation or financial durability evidence.

The installed native CLI was **0.19.10**, not the historically qualified 0.19.0.
Before mutation, its bytes matched the official Darwin ARM64 archive member and
published archive/checksum metadata; no installation occurred. SDK source review
found one application-level Stop/Deploy call each and no update-plan replan loop.
The native default HTTP redirect behavior and server internals are external
limits, not a wire-once certification. Failed return can follow accepted work
because post-acceptance metadata/rendering still occurs; it is never safe-retry
or no-effect evidence. Native auth/history/cache/platform retention are separate
boundaries; native output remains private rather than diagnostic material.

The local operator transition helper passed **15 synthetic offline tests**,
independent corrected-source review and clean active LSP. Activation requires a
separately admitted original instance/host/disk binding held only in caller
memory; the activation path cannot adopt a substitute. Its initial predecessor
baseline also required the available/recent, declared, created, healthy running
v0.1.18 process and expected old image. Strict post-submission process times,
available/recent/advancing identical-process completion, nonzero exit/HTTP-status
preservation and no intentional mutation replay are tested. No runtime IDs,
secret values, exact request timestamps, native exceptions or logs are recorded
here. `PLAN.md`, the other instance and local sandbox remain untouched.

Immediately refreshed native/domain/source/shape/security/storage/name-only
admission selected exactly one of two instances, one existing **1 GiB** disk on
its host, and the verified domain registry's sole usage matching its unique
name. The **one Stop → authoritative Stopped → one v0.1.19 replacement Deploy**
then ran. No volume/host/domain/variable/SSH mutation flags, disk formatting,
allocation, key/secret-value change, initialization fallback, payment activation,
gateway change, inference or telemetry export were requested. Existing saved
variables were decoded using the supported SDK JSONB form and contained only
`OTEL_SDK_DISABLED=true`. Only the three approved existing maintenance secret
names were selected; accounting-key delivery is for the later serving phase.

**Observed failure:** `MAINTENANCE_EXIT_NONZERO` /
`passive_exact_process_completion` / `exit_zero_required`, observed process exit
**1**. Subsequent passive reconciliation obtained **two available, recent,
advancing observations of that same exact exited process**, restart count 0,
no OOM, correct image/name/restart policy. Native metadata reports Running and
still admits the selected instance/domain/repository/shape/CC/debug/secret-name
selection and one attached same-host 1 GiB disk. Running does **not** establish
successful initialization, application readiness or financial state.

The application cause and **discard/initialization outcome remain unknown**.
No successful initialization receipt was observed; no raw logs were retrieved.
The one replacement allowance is consumed. Preserve the disk/key and partial
state: **no replacement/init/deploy replay, reset, restore or ordinary serving**.
After disclosure, the operator explicitly selected **Existing-only diagnostic
boot**: one separately reviewed/signed `persistent-diagnostic-serve` replacement
on the same disk/key despite the missing initialization exit-zero prerequisite.
It **is not read-only**: normal serving and supported SDK database maintenance
may start if existing state is valid. It never initializes or creates/upserts/
resets operator credentials, and neither certifies the failed replacement nor
permits acceptance from Running/healthy alone. Preserve source/crypto/shape/
original-target/stopped-writer gates and all disk/key/history. No replacement/
init replay, reset/restore/allocation, key access/change, inference or telemetry.

The existing bounded opt-in uses actual parsed named-volume/no-init `serve`,
exact `LEDGER_MANAGED_STARTUP_DIAGNOSTICS=true`, one fixed private replaceable
closed-enum record of at most 512 bytes and a cancellable pre-bind failure hold
of at most 120 seconds. Tinfoil may retain the closed healthcheck detail; no raw
logs, key values/lengths, identifiers/amounts, extra listener/worker/retry or new
metrics family. The new candidate keeps the published private source/image/
archive receipt unchanged and renders only the existing fixed diagnostic phase,
CVM0.14.14/four disks/CPU2/memory8192/GPU0, original mount/UID/read-only/restart/
tmpfs controls. It selects existing volume/accounting-key names, not operator
password/email or Stripe. Candidate config SHA256:
**`321911be692999952aaa39abde9ef323d51400528a062e5e56765fca88f18415`**.
Public PR19 normally merged at **`9213f9d40d9481aa4051cbb9d2c23a97925eebd2`**;
new immutable **v0.1.20** protected signing **39834439357**, attempt 1, succeeded.
Parent independently downloaded both assets and accepted their API/local byte
sizes/digests/hash file, exact diagnostic template/config/CVM/four-disk shape,
supported GH cryptographic verification and exact source/tag/GitHub-hosted
signer/invocation certificate. Manifest SHA256:
**`15f7de399e2fef2585483ae3a3f763056828b9d5e80bef4d07fd761c45822a06`**.
The owned public verification workspace was removed after joined completion,
without an erasure or whole-host-transience claim. No new application source,
registry copy, resource allocation or diagnostic boot occurred in this phase.

Public immutable CVM **`ad1a411ed483ac249237bbaf11376dd1c2e9cc30`** (0.14.14)
`containers.go` and `boot/state.go` establish that the unique failed
`containers → intake-persistent → healthy.detail` is the last Docker health
output verbatim. The parent failure summary wraps/truncates and is not a source
for classification. Supported public boot-stages metadata includes boot start;
select it only against the freshly observed post-submission exact process, never
stale preserved rows. Docker's output limit is 4096 bytes; the **512-byte cap is
the private record, not the separately formatted health Diagnostic**. A local
pure Go formatter-export test produced **67 static, locally authored complete
Diagnostic tuples** for exact whole-object comparison. It made no SDK or HTTP
requests, retained no native diagnostic data and its temporary test source was
removed. Unexpected wrappers/duplicates/extra bytes/truncation/unknown tuple
remain diagnostic-unavailable/cause-unknown; no substring or raw-error fallback.

The diagnostic transition has **41 passing mocked offline tests**. A final
review caught an otherwise-qualifying substituted failed-process PID before
Stop: the trusted caller must now supply the privately corroborated complete
failed-process identity (CID, UTC start, image) alongside the original instance/
host/disk binding. Main validates both caller inputs before auth/source work and
compares the freshly sampled baseline identity before consuming Stop. Missing/
malformed input and substituted PID/start regressions make zero mutations.
Neither identity is printed or persisted. Whole-detail projection also pins the
static local formatter catalog SHA256
**`c94a99f0a5587616936497d1c05bd28345c4ce6a1edd27c4821cc16dc44144ba`**.
A fresh read-only preflight corroborated current v0.1.19 failed-process identity
twice with available/recent/advancing observations, original-target metadata,
source/domain/shape/security/secret-name/storage gates; it made no Stop/Deploy.
This is operational metadata, not historical-identifier or runtime-attestation
proof. Final correction review resolved the process-binding finding; **41 tests
passed and all three controller/test/helper paths had zero active LSP findings**.
The exact frozen controller SHA256
**`63520cd438cb46eb97e97adefeaf3de1f2f6be9162084526d948edcbdcc36aa0`**
received independent delta signoff. An earlier hash guard refused only the three
standard-library datetime-alias typing edits **before any SDK/auth work**; it
consumed no native mutation allowance and was not a failed-deploy replay.

**The one authorized diagnostic transition is now consumed:** independent caller
corroboration and controller rechecks passed, one Stop ran, authoritative Stopped
was observed, then one same-instance **v0.1.20** Deploy ran with only the two
existing volume/accounting-key names. No init/reset/restore/rekey/allocation or
credential setup was requested. The public process snapshot remained unavailable
through the bounded passive follow-up. Result:
**`DIAGNOSTIC_FOLLOWUP_UNCONFIRMED` / `passive_diagnostic_followup`**, last constraint
**`PROCESS_SNAPSHOT_TRANSPORT_CAUSE_UNKNOWN`**, no observed HTTP status, no qualified
new-process exit or health observation. This does not establish readiness or an
application cause; no mutation was replayed.

Subsequent **passive native metadata** reports current tag **v0.1.20**, status
**Failed**, the selected domain/repository/shape/CC-on/debug-off/two-secret-name
selection and one existing attached same-host **1 GiB** disk. The uniquely selected
failed native `containers → intake-persistent → healthy.detail` exactly matched
the local formatter catalog and reports only:

- Code **`LEDGER_STARTUP_SCHEMA`**; stage **`open`**.
- Constraint **`completed_startup_checkpoint`**; outcome **`not_ready`**.
- Local next step: underlying startup cause unknown; inspect privately and do not
  reuse or reinitialize partial state.

This is a **native control-plane-reported closed health diagnostic**, not a
freshly correlated exact-process/boot-start receipt. The public process surface
was unavailable, so current application cause remains unconfirmed. In particular,
it proves neither that old split-v2 data remains intact nor that discard or
initialization completed. Never recover diagnostic text from aggregate/error
summaries, or fetch raw logs. No identifiers, key/operator-secret values, runtime
private configuration values or raw errors were recorded here.

**Both runtime allowances are consumed.** Preserve disk/key and partial state;
no further Stop/Deploy, replacement/init replay, reset/restore/allocation or
credential/key change is admitted. Ordinary serving, financial durability,
real-data/gateway/payments and fresh runtime attestation remain unqualified.
Continue only offline source/test investigation under the preparation authority;
another runtime operation needs separate admission. Historical plans/evidence
below are not a fresh replacement or general recovery allowance.

## Offline historical-producer qualification — clean shutdown only

The actual historical source **`9a8b6bac3b2407419a8637bb90b0db4f3528b3e2`**
was archived from local Git and exercised in an owned private temporary workspace
with synthetic credentials, no listener and the pinned offline development
configuration (`OTEL_SDK_DISABLED=true`, `GOPROXY=off`, `GOSUMDB=off`). Its real
`InitOperatorObserved`, customer and website initialization paths produced the
fixture; clean shutdown closed all four SDK database pools. No historical SQL or
models were reimplemented to manufacture acceptance.

Current source **`b96debdfeac917c21ef76103ffe783eb80671c33`** then admitted that
fixture through actual `openReplaceTree` and immutable `inspectReplaceV2`.
Before/after assertions preserved file bytes/hashes, inventory, identity,
permissions, sizes and modification times (access times excluded). No sidecar
normalization, checkpoint, repair, deletion or guard relaxation was used. The
parent inspected the harness, independently passed its **five offline tests**
and reran the explicit qualification, obtaining
**`OFFLINE_HISTORICAL_PRODUCER_ADMITTED`** with clean-close, immutable-admission,
unchanged-byte and owned-cleanup assertions. Owned archived source, fixture and
exclusive temporary current-package Go test were removed after children joined;
no production Go source or frozen transition helper changed.

This closes the **cleanly closed actual historical producer → current admission**
gap only. Abrupt-stop artifacts remain unqualified. It demonstrates no deployed
state, discard/initialization outcome or runtime cause, and authorizes no further
runtime operation. The platform remains last observed **Failed**; preserve its
disk/key and partial state. Applicable privacy boundaries remain those linked
below; synthetic cleanup is not secure erasure or whole-host transience.

## Offline reproduction of the exact schema/open health tuple

A separate disposable-synthetic packet exercised actual `ledger.OpenObserved`,
`startupFailureCode(phaseOpen, err)` and `startupHealthDiagnostic`, not an injected
error or fabricated diagnostic. The parent inspected the ignored harness and
independently reran it in pinned offline Go 1.27 / PocketBase 0.40.5 with telemetry,
module downloads and inherited credential/runtime authority disabled. Untouched
fresh current `ledger.Init` state opened and closed successfully. Each of these
five distinct fixtures returned actual typed `LEDGER_STARTUP_SCHEMA` and projected
the **exact complete five-field native-reported health JSON**:

- Existing split-v2 fixture produced by the replacement test helper.
- Actual pinned SDK-only bootstrap state, without ledger schema (not a claimed
  interrupted ledger initializer).
- Current fresh state with the required reservation-expiry index removed.
- Current fresh state with Accounts `ListRule` changed through supported SDK save.
- Explicitly initialized synthetic payments with a required payment index removed.

These cases map to exact DDL/object checks in `internal/ledger/storage.go`,
Accounts metadata compatibility, and optional complete-payment-schema checks in
`internal/ledger/payment_storage.go`. Later open-time connection pragma and ledger
metadata validation can also return the same code; this packet is not an exhaustive
reproduction of every branch. The health formatter in
`cmd/intake/startup_diagnostics.go` retains the allowed code but reconstructs the
generic `completed_startup_checkpoint` constraint/next step; those words are not
the identity of the underlying rejected schema check.

**Refusal is not filesystem-effect-free.** The first reproduction observed
unchanged `data.db` and `auxiliary.db` bytes, identity, permissions, size and mtime,
but changed fixture trees. Parent added closed synthetic difference classification
and reran: in all five refusals the differences were limited to newly created
`data.db-wal`, `data.db-shm` and root-directory metadata/mtime. Ordinary preflight
uses SQLite `mode=ro`, unlike replacement admission's immutable connection. No
checkpoint, normalization or sidecar removal was used to gain acceptance. This
is measured local filesystem behavior, not evidence of deployed sidecars or a
reason to delete them. Owned temporary Go source and disposable fixtures were
removed after normal child completion; no production source or frozen controller
changed. `git diff --check` passed.

Result **`OFFLINE_EQUIVALENCE_CONFIRMED`** reproduces diagnostic equivalence, **not
the deployed cause**. In particular, retained old split-v2 state and partial new
SDK state can produce the identical message, so it does not establish whether
discard occurred. Abrupt-kill artifacts and actual runtime state remain
unqualified. No further Stop/Deploy or disk/key/state operation occurred; the newly
authorized additional diagnostic transition remains unused.

## Instrumented initializer interruption — offline reported evidence

A separate local packet archived exact source
**`b96debdfeac917c21ef76103ffe783eb80671c33`** into an owned private workspace,
added archive-only synchronization points to actual initialization code, and
reported one SIGKILL plus child join per selected boundary. Parent inspected the
ignored driver/source instrumentation; these results have **not** been independently
rerun by the parent. No tracked production source or deployed state was altered.

Reported observations from one subsequent actual `ledger.Open` per fixture:

- After actual SDK bootstrap, before ledger schema: typed `LEDGER_STARTUP_SCHEMA`.
- Inside the actual still-uncommitted ledger schema transaction: same typed code.
- After ledger transaction commit with pools open: open/close succeeded.
- After ledger initialization closed, before website initialization: succeeded.
- After website initialization completed with pools open: succeeded.
- Clean ordinary and replacement-initializer controls: succeeded.

The actual recorder/health projection assertions cover schema code, open stage,
`completed_startup_checkpoint` constraint and `not_ready` outcome. The harness
labels this an exact tuple, but does not independently compare the fifth
`next_step` field; complete five-field equivalence is separately covered by the
preceding constructed-fixture packet. Both rejected interruption fixtures retained
unchanged database/WAL bytes around open but changed data SHM. Successful ordinary
open/close could change database bytes/remove sidecars. These are not effect-free
inspections, and no manual checkpoint/normalization/sidecar deletion was performed.

The packet reported **all children joined**, but final private workspace removal
returned **`REPRO_CLEANUP_UNCONFIRMED`**. Cleanup was not retried; synthetic state
and snapshot evidence remain in that private temporary workspace. Do not claim
cleanup completion, immediate erasure or whole-host transience. It is a local
cleanup issue, not an application/runtime-failure category or proof of corruption.

This demonstrates a plausible **early-initialization interruption mechanism** for
the old diagnostic without hand-building DDL. It does not demonstrate that the
live maintenance process was interrupted, explain its exit 1, or establish a
QEMU/host/disk fault, discard progress, Linux/enclave behavior or deployed cause.
No additional runtime allowance was consumed.

## Current authority and hard limits

The operator explicitly authorized deploying the updated standalone intake/backend/website to the existing Tinfoil instance, including reviewed source integration, logical commits/pushes, normal protected-main integration, native image qualification, registry handoff and protected signing. The operator subsequently selected **Replace existing database**, after disclosure that current accounts, balances, holds and operator/site state would be discarded, the encrypted disk/key preserved, and an explicitly destructive one-shot procedure qualified first.

That specific database replacement was authorized **after qualification, signing and stopped-target admission passed**, and its one invocation is now consumed with the failed outcome recorded above. The process-completion gate did not pass; broad deployment/preparation permission does not authorize its waiver or replay. The operator has separately selected the narrowly scoped diagnostic exception recorded above; that does not renew replacement authority. This is not general reset/recovery, migration, financial restore, disk formatting/recreation/allocation, key/secret changes, extra resources, DNS changes, guard bypass or uncertain-mutation replay authority.

Preserve the original disk/key, intended instance/host, **`possums.dev`**, CPU2/memory8192/GPU0, CC enabled/debug disabled and all historical releases. The operator confirmed this domain is healthy and the earlier incident resolved; the temporary incident hold was lifted without a deployed mutation. Reconcile current native identity/configuration/shape/storage immediately before activation; earlier inventory is not a fresh admission receipt. The existing local sandbox remains untouched.

Ordinary serving keeps Stripe payments **disabled**. Shipping top-up code authorizes no runtime Stripe-secret delivery, payment activation, real sales, inference, gateway/client changes or telemetry export. Gateway recognition/spending remains separate integration.

Applicable policies: [authority](../PRIVACY.md#authority-and-scope), [accounting/persistence](../PRIVACY.md#accounting-and-processor-boundaries), [trusted native administration](../PRIVACY.md#trusted-intake-manual-admin-exception), [merged accounts](../PRIVACY.md#subsequent-operator-selected-merged-account-schema), [specific replacement approval](../PRIVACY.md#explicit-same-disk-database-replacement-approval), [forbidden diagnostics](../PRIVACY.md#forbidden-collection-and-defaults), and the sibling gateway's full privacy policy. Keys never enter source, arguments, agent history, diagnostics or release artifacts. Platform-managed key custody/decryption, beta volume rollback limitations, native history and downloaded artifacts remain separate boundaries. Logical discard is not secure erasure; signing/health is not fresh runtime verification or financial durability evidence.

## Predecessor and source integration evidence — historical

Initial authenticated read-only preflight identified private predecessor main **`9a8b6bac3b2407419a8637bb90b0db4f3528b3e2`**, public main **`c03c5a36f2c308a653367198f8af4db5df6de4cc`**, and accepted ordinary existing-only **v0.1.18**. Its receipt binds image **`sha256:70c01cfb228fb9edfe5523d83d8d260689951061e84d15a1a018769eadb0484d`** and archive **`5fbe8b2638564b2daad7d58cf58a84c99aa969881e5739798f86f0ec9bdcbc1d`**. Native metadata then showed one intended running workload, correct shape, CC/debug settings, one volume slot and no pending update. No database rows, credentials/key values or lengths, raw errors or logs were read. This is predecessor metadata, not new health/financial/runtime verification or current activation admission.

That predecessor already contained conditional renewable holds, immutable initial/current expiry, once-per-account cleanup validation, twelve-hour fresh customer SDK tokens and the evaluator step-budget correction. These were integrated into the merged Accounts implementation instead of regressing accepted main. Fresh merged **schema v3** distinguishes deployed split v2 and unreleased merged v2; ordinary Open/serve reject incompatible state before mutating SDK bootstrap. Website lifetime remains one-hour absolute/ten-minute idle, and opening preserves stored native token durations. No compatibility mapping, provisioning route, v2-to-v3 migration or automatic renewal worker was introduced.

The implementation agent reported full serial root race, native-editor/customer/admin/no-JavaScript browser, vet/build/module, compiled CLI/curl/restart and language-diagnostic passes. Parent inspected critical diffs and independently passed focused schema/renewal/cleanup/auth/native-key/current-balance race tests. Independent read-only source review found no material code/test issue; its stale provisioning-doc finding was corrected. These statements are scoped to the pre-replacement source, not the later destructive command.

Parent fixed release source filtering to admit only the two public historical `split_schema_v1.sql`/`split_schema_v2.sql` fixtures. Missing/symlink inputs refuse; arbitrary SQL/database/private files remain excluded. The initial offline suite passed 174 tests with one platform-only skip.

Logical commits `e47dfec` (Stripe adapter/dependency), `f52147b` (inert Lisp presentation), `b24ac6b` (necessarily atomic sole-identity/schema/application/fixtures), documentation and normal ancestry integration through `cd4b777` formed private **PR 5**. The original head failed actual generated-image qualification with closed `DELIVERY_IMAGE_PRIVACY` / `empty_process_and_framework_logs`.

Parent reproduced a stale exact-diagnostic expectation: compiled v3 correctly refused a public synthetic split-v2 fixture with the new schema-v3 next-step text, while the image harness expected the previous text. Stdout stayed empty; database hashes/file inventory were unchanged. Commit **`98cab6a`** changed that fixed expectation and added literal current/legacy-contract coverage without relaxing exact JSON/no-log assertions. Parent passed 175 offline tests (one platform-only skip), active LSP and diff checks. The exact-head CI, including actual generated-image qualification, then passed.

Parent verified exact head, completed successful check, clean/mergeable non-draft state and no requested review changes. One normal head-matched merge, without admin/protection bypass, was confirmed **MERGED** at private main **`fb904f4a91e3eaf338615ab28945457f939e7e52`**. That image check does **not** qualify the subsequent replacement packet or substitute for exact-final-main A/B/provenance/signing gates. No native A/B dispatch, image publication, signing, stop, reset or deploy has occurred during this release preparation.

## Explicit database replacement decision

The operator was offered preserving the old database inactive with fresh state elsewhere, replacing existing database contents, or release-only preparation. The explicit selection was **Replace existing database** under the scope above. Neither original-disk preservation alone nor generic deployment approval supplied this destructive authority; the subsequent specific choice did.

The candidate maintenance command is:

`ledger-replace --state /state/ledger --tinfoil-volume ledger --operator --discard-existing-ledger`

Only the exact closed flags/fixed values are admitted. Validate required runtime operator inputs through the existing contract and supported SDK validators before volume work. Use the existing **unlock-only** volume path and actual kernel mount/device/child/no-symlink/no-nested-mount/writable checks. Never initialize the disk or select replacement from environment presence, ordinary serve/init, diagnostics or status.

Admission binds the known predecessor's **financial and customer-auth split-v2 layout/metadata** using bounded immutable read-only queries, not financial or credential rows. Both database files must be regular, nonempty and readable SQLite; this is not a complete auxiliary/SDK/application schema inventory or certification of old financial integrity. Unrelated native/operator/site tables and files **inside the selected ledger child** are within the approved whole-state discard, not preserved state. No unrelated sibling or mount-root data is authorized for removal. New merged-v3 state and unknown financial/auth layouts refuse.

The bounded opened-root tree permits at most 4,096 entries/depth 32 and refuses symlinks, hardlinked files, different devices and database WAL/SHM/journal sidecars. It does not checkpoint, repair or remove sidecars to gain admission. Recheck the inventory before removal; unlink admission-bearing `data.db` first, then remaining child entries. Preserve the ledger directory itself, mount root, `lost+found`, siblings, disk and key. Invoke normal fresh operator/website initialization once. No migration, restore, recorder, persistent reset marker, listener, healthcheck/restart loop or automatic retry.

This relies on stopped writers and the existing trusted-root model, not atomic protection against subsequent privileged mount/path changes. Discard-started failures retain `discard_outcome_unknown`; confirmed discard plus failed initialization retains `old_data_discarded_new_initialization_unconfirmed`; completed initialization with unconfirmed local closure is not ordinary serving acceptance. Retain partial state and known effects, stop and reconcile privately—never retry, silently initialize, roll back or claim discarded state was preserved. Successful command completion emits no success log; supported passive exact-process exit-zero evidence is required before separately admitted serving.

## Replacement packet qualification — local evidence only

The implementation agent delivered `cmd/intake/ledger_replace.go`, `_schema.go`, `_tree.go` and three focused test files, with only explicit dispatch added to `main.go`. It reported a final full serial root race pass, focused race, vet/build, compiled-CLI refusal/privacy and active LSP passes. Parent inspected actual production boundaries and independently passed all `TestLedgerReplace` race tests.

Independent review found unnecessary normal-initializer overrides of OTP/MFA/OAuth2/password-auth configuration. Parent verified the pinned SDK fresh defaults already disable MFA/OTP; the fix does **not** automatically enable them. All forced auth-option assignments were removed, retaining only required AuthAlert suppression. A regression proves configured native OTP/MFA/custom durations survive the initializer and the SDK operator record remains valid. Parent operator/replacement race suites passed after this correction. This remains a small separate policy-correction commit, not a new role or auth framework.

A later source review accepted the auth correction, requested complete predecessor SDK inventories and identified stale authorization wording. The wording is reconciled here. Separate independent read-only verification confirmed the limited inventory check as fact but refuted unauthorized-discard impact: unrelated native/operator/site state inside the selected application database is explicitly within the whole-state discard approval, and no complete SDK inventory requirement or concrete fresh-v3/outside-child admission scenario was established. Parent accepts that scoped verdict, documents the admission limitation above and extends the existing success fixture with an unrelated native collection/record and auxiliary table: they must disappear while mount-parent/siblings remain and fresh-v3 replay refuses. No additional registry or general validator was introduced. Reviews ran no tests; executed evidence remains separately stated.

Parent also added an actual generated-image entrypoint refusal for missing replacement confirmation/fixed target; subsequent ordinary init must still succeed on that untouched fixture. The offline harness requires the exact closed refusal, not an arbitrary failure or broad diagnostic exception. **176 offline tests passed**, one platform-only skip. Subsequently private **PR6**, exact head `b456b95e683f9b546031fc7cfac188b8dd54a1ec`, passed native CI including new actual-image checks and normally merged at `35a6ff3f7fb06900c522a53330abc27308be1200` without bypass. This is candidate-source evidence, not exact-final-main matching A/B qualification.

**Native Linux scope:** the new Linux-only actual unixpacket unlock followed by real kernel refusal of an ordinary temporary filesystem could not execute on the Darwin implementation host; optional offline cross-compilation initially stopped on an unavailable cached public dependency, without online retry. PR6's subsequent native CI passed. Neither refusal tests nor image smoke prove a successful dedicated encrypted block mount. Exact-final-main native checks and independently matching A/B builds remain mandatory. The first authorized target operation still must establish its own supported runtime mount and process-completion evidence; no test bypass is shipped.

## Public candidate profile — local evidence only

The delivered and parent-inspected `persistent-ledger-replace-config.yml.in` has SHA256 **`f01bb8a5740d244c7b4689e2f56f125bcb51f3368b3cde2152d4b0d348a10db5`** and four measured disks. It differs from ledger-init only in container name and the exact replacement argv. CPU/UID/read-only/restart/volume/tmpfs settings remain fixed; only existing volume-key/operator-email/operator-password secret **names** are declared. No healthcheck, listener, diagnostic opt-in, initialization flag, payment secret, accounting key or extra resource.

The public helper/workflow admit only this additional hash-fixed explicitly selected phase; owner/current-main/tag/attempt-one/CVM/crypto/nonreplay gates are unchanged. Older templates, active root config and accepted-image handoff remain byte-identical. Parent independently passed **96 offline tests** and actual pinned decoder qualification: v0.1.14 rejects persistent bindings, v0.1.16/v0.1.17 accept the fixtures, and malformed owner/binding/unknown-field controls reject. The qualifier reported owned temporary cache removal and no measurement/runtime verification. The six-file candidate was committed at `79b2aff6216242fc78675b89ed400aa652fcc83b`; public **PR17** normally merged at `cf023505e0e8df289ec5053e41be06842771614d`. An unrelated documentation branch was preserved rather than piggybacked. No new candidate render/sign/activation or new-image receipt is established by that merge.

## Original release sequence — historical, stopped at completion gate

The final release/publication/signing and one stopped-instance replacement were
executed as recorded above. Step 7's exit-zero prerequisite is unmet. This list
supplies no additional replacement, initialization, signing or deployment retry.

1. Replacement source/auth correction and public fixed profile are merged as recorded above. The selected native-owner helper was missing from the checkout; [restoration provenance, qualified SDK/policy bindings, offline tests and reviewed boundaries](native-owner-publication.md) are recorded separately. Parent full scripts discovery passed **203 tests, one platform-only skip**. No publication attempt was made to test setup. Keep unrelated work, private inputs and ignored helpers/caches excluded; leave `PLAN.md` unchanged.
2. Integrate the restored native-owner helper, unchanged shared CI-guard extraction, tests and factual documentation through normal PR/main checks. Existing auth/feature/documentation commits remain separate. No force/admin bypass, fabricated Actions authority or switch to the parked CI package-link investigation. Exact **final main**, not a working tree or PR merge ref, is the release source.
3. Require successful exact-final-main checks, all new native Linux tests, fresh independently matching native A/B image/archive builds and actual-image privacy/restart/startup qualification. Do not reuse or relabel older artifacts. Any unknown failure blocks progression.
4. Use the selected supported owner publication path only after source/package-link/full-inventory/baseline/unused-tag/quiet-writer admission. One digest-preserving zero-retry copy, authoritative postchecks, three-field public receipt; no grants/visibility changes or automatic replay.
5. Commit only necessary public receipt/config/profile bindings; preserve historical tags/templates and source/phase/shape/CVM guards. Require separately protected signed maintenance release and independent downloaded-byte, crypto/source/tag/invocation/config/CVM/four-disk acceptance. No guessed or overwritten tag/release.
6. Reconcile exact original instance/disk/key-name/configuration/domain/shape and quiet writers immediately before one admitted **Stop → authoritative Stopped → replacement Deploy**. No new disk/format/reset fallback. Failed/unknown outcome stops for passive reconciliation without replay.
7. Require two supported available/recent/advancing snapshots binding the same maintenance process and exit 0, then separately sign/admit ordinary existing-only serving. Running or health alone is not initialization completion. Check passive readiness/UI without unsolicited login/funding/inference probes. Do not claim successful deployment, financial durability or runtime verification without their respective evidence.
