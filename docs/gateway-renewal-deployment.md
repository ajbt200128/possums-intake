# Renewal/auth rollout — approved maintenance, activation pending

## Operator decisions and current facts

The operator requested deployment of the reviewed intake-only 12-hour customer
authentication and conditional renewable-hold changes. This supersedes the local
packet's no-publication/no-deployment restriction, not its qualification gates.
The operator then explicitly approved:

- Preparing and qualifying the minimal managed **existing-disk** upgrade path,
  followed by **one stopped-service migration boot and one serving activation**
  on the same instance, disk and key. Preserve every operator test account,
  financial row, credential, binding, published website and clock watermark.
- Downtime that may exceed the one–two-minute target. After schema v2 migration,
  the old binary cannot open state. No automatic old-binary rollback, database
  restore, initialization, reset, disk replacement or mutation replay.
- One supported native customer collection **duration-only** update to 43200
  seconds, with verification before claiming that the existing installation
  issues 12-hour tokens. Existing tokens retain their original expiry.
- The deployed intake currently has **only operator manual-test consumers**.
  This is operator-confirmed inventory, not a traffic-log or network discovery
  claim. Manual tests must pause during maintenance.

The gateway workstream confirms no gateway replacement, delivery writer, client
installation/activation or live probe is running there. Its new adapter is local
and uncommitted; focused DTO tests do not establish production compatibility.
Renewal scheduling and paired actual-candidate acceptance remain unfinished.
There is no matching gateway rollout in this authorization. Before downtime,
reconfirm that no automated consumer or concurrent delivery writer was added.

Historical deployed acceptance is the website-enabled v0.1.16 release described
in [website image evidence](website-image.md). It is not fresh live state evidence
or qualification of this candidate. No remote mutation has been made in this
rollout at this checkpoint. The previous local source/test manifest in
[gateway holds](gateway-hold-renewal.md) predates the managed-upgrade addition;
new exact-source qualification is required.

## Minimal deployment prerequisite

The locally qualified `ledger-upgrade --state ABSOLUTE_PATH` originally required
an already accessible stopped state directory. It could not unlock the managed
disk, and the public release repository has no upgrade-only profile. Directly
replacing v1 serving with the new v2 server would refuse startup.

The implemented source addition is optional `--tinfoil-volume`, reusing the existing managed
secret/control-socket unlock and mount verification with initialization **false**,
then the existing explicit v1-to-v2 upgrade. No listener, telemetry, startup
recorder/hold, website setup, credential change or initialization options. An
unlock failure must prevent ledger work. Unknown completion preserves original
state and stops the rollout, never starts serving by inference or repeats work.

The public release phase must be fixed and separately reviewed/signed: exactly
`ledger-upgrade --state /state/ledger --tinfoil-volume ledger`, existing volume-key
secret name only, restart `no`, no healthcheck/listener, unchanged mount/ownership,
read-only root/tmpfs, CPU2/memory8192/GPU0 and CVM/four-disk requirements. Existing
profiles, historical tags and cryptographic/provenance guards remain intact.

## Required sequence

1. Qualify the managed upgrade addition and populated-state preservation with
   synthetic/offline tests and independent review. Preserve unrelated dirty work.
   Integrate small logical source commits through normal source review/CI.
2. Produce exact-source native Linux independent A/B matching builds and actual
   archive-bound qualification. Admit original unexpired artifacts and retain
   existing fresh-build, package/source-link and zero-retry publication guards.
   Historical/local cached images are not substitutes.
3. Prepare and independently verify immutable signed maintenance and serving
   releases through the existing protected workflow and supported cryptographic
   verification. Ask the operator for required protected approvals; do not bypass.
4. Reconcile current instance/tag, original attachment/host/domain/resource shape,
   existing secret-name selection, CC on/debug off and no competing writer. Never
   read the disk key, replace bindings, enable SSH or allocate another authority.
   Notify the gateway workstream before downtime.
5. One Stop, authoritative Stopped, then one upgrade-only Deploy. Require supported
   recent/advancing exact-process **exit-zero** evidence, not platform Running.
   Failure/unknown outcome stops here for passive reconciliation and a new decision.
6. After confirmed completion and refreshed release/native gates, transition once
   to the signed existing-only serving phase. Confirm authoritative state and
   bounded credential-free health/website checks. No inference or funding probes.
7. Use native SDK administration for one
   `PATCH /api/collections/customer_keys` with
   `{"authToken":{"duration":43200}}`, preserving all other settings. Use supported
   private secret entry if authentication is needed; never put credentials in
   chat/arguments/repository/diagnostics. Verify the resulting duration through
   authenticated native state, retaining only safe outcome evidence. An interrupted
   PATCH does not authorize a retry or a claim that the setting changed.
8. Report exact private/public source, image/release identities, actual schema and
   duration outcomes, untouched gateway/client state, and remaining acceptance
   limits. Notify the gateway workstream of the same scoped contract/evidence.

## Current qualification checkpoint — release gates pending

The managed CLI and public fixed phase are implemented. Private source is now
split into small logical commits; the public fixed phase is not yet committed.
The new template SHA256 is
`721c6b0549e5cd0e46bb682df233caa10a1ccc7f1cb05461d39c29d6278ff831`.
Public `accepted-image.json` and root `tinfoil-config.yml` remain unchanged;
no candidate image/config has been selected. Parent inspected the actual changes
and independently passed all **94 public offline tests**. The actual pinned
vendor decoder still needs execution for the new phase. Independent read-only
review found no blockers in the managed CLI, source/profile/tests and image-probe
addition; it did not run tests or qualify deployment.

Parent also passed **173 private offline Python tests** (one optional binary
fixture skipped), then separately built the actual current CLI/probe and passed
all **three website image-probe tests with the optional local HTTP/restart fixture
enabled**, no skips. The probe now verifies `initial_expires_at` in the complete
website snapshot; this is local binary evidence, not a generated-image pass.
The CLI implementation packet passed focused offline managed-upgrade/volume race
tests; that is child-executed evidence, not an independent parent full-suite pass.

**Current full-root race qualification is not passing.** The parent parallel run
failed `TestBoundedCleanupAndReadsIndependentOfBacklog` at the expiry cleanup
assertion with `LEDGER_WORK_CANCELLED`, and
`TestRuntimeDiagnosticClassificationAndPrivacy` with
`LISPSITE_TEST_STEP_CATEGORY`. All other reported packages passed. Both failing
tests subsequently passed three isolated serial race repetitions unchanged.
A full serial attempt repeated the same ledger cleanup failure and exceeded its
600-second parent execution budget before the remaining suite returned. No
claim of a cause, successful full suite or timeout-clean completion follows;
subsequent passive process inspection found zero Go/test/intake processes with
this worktree as their working-directory ancestor. No financial or runtime
budget, batch bound, assertion or retry rule was changed. Focused source/baseline
investigation is pending; no publication or activation proceeds on these results.
The chained vet command was not reached after the full-suite failures.

A subsequent bounded read-only investigation compared an isolated baseline Git
archive (`cb64b630...`) and the current candidate using the same pinned offline
cache. Both complete ledger/Lisp package race suites passed; both also passed
ten focused repetitions with `-cpu=1`, and three more with experimental `GOGC=10`.
No failure, candidate-specific cause or toolchain defect was reproduced. These
are child-executed comparisons, not a replacement for the failed full-root gate.
All children completed and disposable baseline copies were removed; passive
inspection found no remaining matching test processes.

Source inspection identified the pre-existing repeated active-set scan in
`Cleanup` (8,512 rows scanned while expiring 128 of 130 same-account holds) and
the Lisp fixture's race between a step budget and a one-second preparation
deadline. Neither is proven to have caused the original failures. The operator
separately approved two focused corrections, to be reviewed and committed
separately from the feature: validate each account's complete pre-expiry hold set
once per cleanup transaction, retaining every corruption check and batch/deadline
bound; and exercise real evaluator step exhaustion without the competing wall
clock in the step-specific fixture, retaining independent deadline/privacy tests.
No production Lisp limit changes. The parent implemented the test-only change and
passed ten focused race repetitions with no LSP errors; cleanup implementation
is now implemented. Its focused tests check one account/active-set read per
transaction, interleaved accounts, undued corruption, impossible sums and rollback
of earlier writes when a later account fails. Parent inspected the actual changes.
The implementation packet passed focused and full ledger race tests.

**Renewed parent full-root qualification passed after both focused corrections:**
`go test -mod=readonly -race -tags elpscheck ./... -count=1 -timeout=240s`, followed
by `go vet -mod=readonly -tags elpscheck ./...` and `git diff --check`. All root
packages passed, including ledger, CLI, native auth/operator, website, telemetry
and Lisp. This is one complete subsequent pass, not a long soak or proof of the
earlier failures' cause. The earlier failures remain historical evidence above.
A completed independent read-only review of the two corrections found no blockers
and confirmed complete pre-expiry validation, rollback/no-partial-receipt behavior,
unchanged limits and strict evaluator classification. It ran no tests; parent
execution evidence remains separate. Earlier provider-unavailable attempts
produced no review, not an acceptance decision.

The parent also executed the actual checksum-pinned vendor decoder qualifier for
all seven public profiles: v0.1.14 rejected persistent bindings with positive
controls, while v0.1.16 and runtime v0.1.17 accepted the exact profiles and passed
negative controls. Owned temporary module cache removal completed. These are
actual parser/source guards, not measurement, signing or runtime evidence.

Fresh native matching builds, archive qualification, publication/provenance,
signed release verification and actual stopped migration/auth-setting/serving
outcomes remain pending. The historical owner-publication helper also requires
recovery/review with its matching admission code. The pinned native Skopeo1.24.1
package was acquired from the existing flake's official signed dependency cache,
with no global installation or trust override. Its bundled policy has the original
required SHA256 `cddfaa8e6a7e5497b67cc0dd8e8517058d0c97de91bf46fff867528415f2d946`;
only the store path differs. This policy is not signature evidence: unchanged
independent artifact/provenance, TLS and package-link gates remain required.

The tested private source is split into cleanup correction `0be0c43`, isolated
step-limit fixture `4c387a5`, fresh native duration default `f02e2e6`, and atomic
renewal/schema/API/explicit-maintenance change `a794fd7`. The atomic API/schema
commit keeps all consumers/tests compiling together; no adjacent cleanup is mixed
in. The qualified code/test manifest is now
`3824729abcf24ad0bec4809cad452cf9182d26931ee359becab0f939f9782e42`
(174 files), using the same path/NUL/raw-file-digest algorithm described in the
original local contract. It includes the image probe and new cleanup tests,
excludes documentation and is not whole-repository/image provenance. `go.mod`,
`go.sum`, and `PLAN.md` remain unchanged.

At this checkpoint, no remote mutation, source push, runtime setting change or
service replacement has occurred. Source PR/CI and all exact-main release and
activation gates remain pending.

## Boundaries

This remains the existing synthetic/operator-test installation: no real funds,
payments, paid inference, gateway deployment, client installation, telemetry
export, new disk, restore or secret rotation. Website sessions remain one hour
absolute/ten minutes idle. Logging/AuthAlert-origin protections and the independent
accounting shared key remain unchanged. Stock native administration retains its
accepted authority risks; this rollout does not promise safe financial restore.

Applicable privacy: [managed key custody](../PRIVACY.md#selected-persistent-volume-key-custody),
[forbidden collection](../PRIVACY.md#forbidden-collection-and-defaults),
[trusted native administration](../PRIVACY.md#trusted-intake-manual-admin-exception),
and the gateway's data-handling/forbidden-telemetry requirements. Managed platform
key custody, encrypted-volume rollback limits, WAL/deleted pages, optional manual
snapshots and native host/platform history remain separate retention boundaries.
No automatic diagnostic log, support bundle, raw payload/error capture or new
metrics population is authorized. Build provenance and health are not fresh
runtime attestation, durable collection, capacity or real-data acceptance.
