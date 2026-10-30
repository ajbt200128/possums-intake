# Two public source repositories — delivery transition

## Decision and current authority

The operator selected **two independently releasable public source repositories**,
not a monorepo:

- `ajbt200128/possums`: gateway and existing clients; its own builds, signed
  releases and blue-green deployment. No gateway/client changes in this cutover.
- `ajbt200128/possums-intake`: intake source, measured configuration and signing;
  independently qualified builds and state-preserving replacement with downtime.
- `ajbt200128/possums-intake-deploy`: retain historical tags, releases and evidence;
  retire **future** publication only after the new path is qualified and accepted.

The initial packet was local-only. The operator subsequently requested main
reconciliation and execution, expressly correcting a momentary monorepo
misstatement, then selected **Full gated cutover**: small logical commits/pushes
and reviewed PRs; visibility/publication/workflow changes after source/history/output
review; one separately qualified existing-only repository transition. **Stop if
supported existing-instance/configuration/state preservation cannot be established.**
This supersedes the earlier no-commit/no-remote restriction, not qualification,
privacy, source/signature or once-only gates.

**No new license** was selected: preserve existing rights and third-party notices.
Public visibility does not grant an OSS license. No history rewrite or historical
release deletion. Use Sol for subagents. Preserve accounts, balances, holds,
credentials, website, live Stripe configuration, approved aggregate-metrics settings,
disk/key/domain/shape and tested gateway behavior. No secret-value access, gateway
change, inference probe, charge/refund, telemetry expansion or setup/init/reset/restore
follows. Source acceptance alone permits no resource mutation; stopped creation has
its own gates below. `PLAN.md` and unrelated work remain untouched. Refresh
exclusive operational writer coordination before mutations; historical pauses do
not establish current custody.

## Latest topology choice — new stopped instance, original ledger later

The operator selected **Stopped instance, move disk later**, requested enabling as
much as possible and reaffirmed **Move old ledger** instead of allocating fresh
storage. This supersedes the need for an in-place repository retarget now. Prepare
intake-owned public source/signing and the complete current live serving feature
set, then create only a separately qualified new stopped instance. No fresh ledger,
new volume, copied balances, current-service Stop/update, disk detach/attachment,
custom-domain switch, provider/payment/inference action or export is authorized
now. Future movement of the original disk/domain needs separate same-host,
single-authority admission and private secret readiness. The stopped instance
cannot serve requests, fund accounts or emit metrics.

**Create-only qualification is still required.** The pinned CLI
[`container.go:511–544`](https://github.com/tinfoilsh/tinfoil-cli/blob/b9abe8ee1f7cdda680ab271a617d9b00490481ed/container.go#L511-L544)
automatically calls Deploy after creating a volume-declaring configuration, even
with no optional volume assignment. Its
[`optional-mount regression`](https://github.com/tinfoilsh/tinfoil-cli/blob/b9abe8ee1f7cdda680ab271a617d9b00490481ed/lifecycle_create_test.go#L182-L209)
confirms this. **Do not use `tinfoil container create`; `--no-wait` does not prevent
Deploy.** The documented [Admin API create](https://docs.tinfoil.sh/admin/admin-api#create-container)
returns stopped/volume slots when mounts are declared, leaving attachment/Deploy
as subsequent operations. Mount declarations do not allocate a disk. That is a
conditional supported API path, not permission to assume CLI equivalence.

Before its one request, qualify exact signed config and private supported auth,
unique resource name, original-disk host, shape/security, missing/project-secret
validation semantics and outcome reconciliation. No replacement/domain/volume or
saved-private-value override. Afterward require a new unique identity, stopped,
exact repository/tag/host/shape, CC on/debug off, no attached disk/production/review
connection/pending deployment/update and original instance/disk/domain unchanged.
Unknown results stop for passive reconciliation, not replay. This packet has
created no new resource.

The public-only owner adapter and hard-disabled signing foundation passed scoped
independent source/offline review and merged in PR #16. The newer guarded manual
signer, CI-token readiness and read-only crypto-acceptance preparation below still
need independent review and exact-source CI/native qualification. No remote switch
was changed by this preparation; live activation is unobserved. Implementation
summaries and synthetic tests alone are not release acceptance. Secret bindings
under the new repository remain unqualified; declarations do not carry values
between projects. **Do not create or change Tinfoil secrets yet.**

## Historical in-place option — unsupported within inspected contracts

**No supported in-place repository retarget and value-free cross-project secret
migration has been established.** This is an unmet preservation gate, not proof
that the platform cannot offer one. Do not remove a repository guard, trial an
undocumented field, use replacement-create, or simulate a GitHub repository rename.
No visibility change, release publication, Stop/Deploy or secret mutation has been
attempted by this cutover packet. The existing service is not changed by local work.

Source evidence:

- Pinned public CLI **v0.19.12**, commit
  `b9abe8ee1f7cdda680ab271a617d9b00490481ed`:
  [`container.go:234–253`](https://github.com/tinfoilsh/tinfoil-cli/blob/v0.19.12/container.go#L234-L253)
  has no existing-instance `--repo` flag;
  [`buildLifecycleBody:1045–1093`](https://github.com/tinfoilsh/tinfoil-cli/blob/v0.19.12/container.go#L1045-L1093)
  has no repository field. Create's repository field is not deploy/update authority.
- [`projectSettingsCmd:124–156`](https://github.com/tinfoilsh/tinfoil-cli/blob/v0.19.12/project.go#L124-L156)
  supplies only `hold_by_default`. Project update selects tags/instances, not a
  destination config repository. These relevant files also matched public main
  when independently inspected.
- [`repo_secret.go:9–21`](https://github.com/tinfoilsh/tinfoil-cli/blob/v0.19.12/repo_secret.go#L9-L21)
  offers list/get/create/set/delete, not server-side move/re-scope. Create/set
  require values. Metadata inspection cannot migrate a secret.
- Current [Admin API](https://docs.tinfoil.sh/admin/admin-api) documents no repository
  retarget in existing deploy/update or project PATCH. `replace_container_id`
  stops/removes the old instance before replacement and does not restore it on
  later failure; that is not the authorized same-instance transition.
- [Projects](https://docs.tinfoil.sh/containers/projects) documents secret movement
  when the **same GitHub repository is renamed**, and says secrets belonging to
  different repositories are never merged. These two repositories must both be
  retained; that rename behavior is not arbitrary retargeting.
- [Secret selection](https://docs.tinfoil.sh/containers/secrets-and-env-vars) preserves
  saved manual selections when omitted for ordinary deploy/update. It does not
  establish cross-project value availability after an undocumented retarget.

An in-place switch would require supported server-side retargeting and collision/
completion semantics, preserving instance/disk/key/host/domain/configuration and
moving project-secret bindings without agent value access/resubmission. That path
is not being attempted. The later stopped-instance choice avoids retargeting, not
secret readiness or future single-disk ownership requirements. No support contact,
new credential, trial mutation or replacement-create follows from this record.
Native history/platform retention remain independent boundaries.

## Updated-main reconciliation — completed locally

- Intake fast-forwarded from `b96debd` to
  **`b8d9ce20661b70170f451be3978ecd44e51df5da`**, preserving/restoring all eight local
  foundation files cleanly. No source commit was created by reconciliation.
- A subsequent fetch exposed newer intake main
  **`6e4d5516d10e0a38279abce072ec06fc9a0ae556`**, merging the reviewed opaque
  Stripe hosted-URL fix. Fast-forwarded locally without conflicts or stashing,
  preserving every delivery edit; the six upstream payment/test files were not
  modified by this packet. The full Python suite then passed 285 tests, one
  skipped. Earlier foundation derivation/source identities below predate this
  application change and are not current-image qualification.
- Fetched gateway main **`ca922197`** and deploy main **`378d81b`**. Unrelated sibling
  working-tree changes were not checked out, reset or edited.
- Main contains merged Accounts, live Stripe Tax, the response-delivery correction
  and the explicit waiver of optional package-source association (`147a9a9`). Keep
  independent authenticated source, build/provenance and fixed public-package
  identity checks; optional association is not their substitute.
- The deploy source now selects live Stripe serving with **public egress defined
  and attached**, not the old diagnostic/payment-disabled profile. Later deployment
  records supersede old failed-boot/1-GiB assumptions; neither documents nor source
  prove today's target health, attachment size or financial durability.
- Bounded authenticated GitHub repository reads confirmed gateway/deploy public,
  intake private, all defaulting to main and none archived; no repository license
  detected. This is metadata, not source/history/Actions-output exposure audit.

## Refreshed offline foundation — implemented, not an executor

- `scripts/public-release.py` validates injected **public-only source metadata** and
  independent fixed public-package identity. The optional `package.repository`
  association is ignored; private source and wrong owner/package/type/ID refuse.
  Acquisition/authentication/build provenance are absent from this pure contract;
  the separate owner/signing adapters below remain unqualified against live evidence.
- The sole phase is **`persistent-live-stripe-serve`**. Its exact template is
  `nix/persistent-live-stripe-egress-serve-config.yml.in`, byte-identical to deploy
  main's same-named template, SHA256
  **`f135d7d6cafa2ddda365a9258a75a380861e6dc2a7b7b72a21a6051627eb56ef`**.
  This replaces the uncommitted obsolete ordinary-serving template; the old phase
  now refuses. No historical deploy template/release was changed.
- Preserve CPU2/memory8192/GPU0, CVM **0.14.14**, vendor/firmware/cmdline/four-disk
  pins, UID/GID65532, read-only root, no restart, tmpfs/healthcheck, existing unlock,
  `--operator --stripe-live`, exact tax/return configuration and current aggregate
  metrics declarations. The five secret **names only** are `LEDGER_VOLUME_KEY_B64`,
  `LEDGER_ADMIN_KEY`, `INTAKE_METRICS_HONEYCOMB_API_KEY`, `STRIPE_LIVE_API_KEY` and
  `STRIPE_LIVE_WEBHOOK_SECRET`. Rendering does not perform export or resolve values.
- Strict manifest decoding binds exact configuration and pinned structure; this is
  **not** cryptographic, authenticated-build, runtime-attestation or deploy evidence.
- CLI offers static `status` and stdout-only `render persistent-live-stripe-serve
  <digest>`. No executor, network/subprocess adapter, active root/config write or
  deployment intent is selected. No setup/init/diagnostic/repair mode or fallback.
- Closed errors retain category and known primary failure. Writes **and flushes**
  stay in the presentation boundary; failed descriptors are redirected to the null
  sink to prevent raw interpreter-shutdown errors. One best-effort closed stderr
  report, no result retry; both streams unavailable still exits unsuccessfully.
- Source snapshots require only the exact named new template, not a YAML glob;
  the Nix delivery-test sandbox includes it. Application packaging inputs, legacy
  callers, workflow activation, runtime/accounting/schema and `PLAN.md` stay unchanged.

Applicable policy: [intake authority](../PRIVACY.md#authority-and-scope),
[forbidden collection](../PRIVACY.md#forbidden-collection-and-defaults),
[typed failures](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates),
and gateway [data boundaries](../../possums/PRIVACY.md#data-handling-boundaries).
No new runtime recording or telemetry. Public-source snapshots/Nix outputs and
native agent/CLI history remain host persistence boundaries, not whole-host transience.

### Foundation-refresh verification — before authenticated adapter changes

- **PASS:** updated-main suite ran **237 tests, one skipped**, successfully with
  cached pinned Python **3.14.7**. New tests cover live intent, network attachment,
  metrics/tax/origin/secret substitutions and ignored optional source associations,
  retaining no-work and six actual CLI closed-pipe cases. An initial fixture used
  a dict subclass where exact native dicts are required; it refused correctly.
  The fixture was corrected without relaxing production shape validation.
- **PASS:** template hash/byte comparison and whitespace checks. Final active LSP
  checked all seven Python/Nix/Markdown files without diagnostics. The template
  extension has no configured LSP; exact-byte/hash tests are its evidence instead.
  Independent Sol review found no remaining source blockers and independently ran
  the entire 237-test suite successfully with one skip. It did not run Nix builds
  or independently reproduce the parent's derivation equality.
- **PASS:** latest-main versus current declared-input snapshots give the same
  x86_64-linux image derivation:
  `/nix/store/i6q1fdld1ilqc5rrpqz3q9ag1apk3d05-possums-intake-local.tar.gz.drv`.
  Declared source identity at that foundation checkpoint:
  `303bd77f189a7155510ca40136f16a9848851529f7768cdad5dd7f35b3470a1f`.
  These were uncommitted snapshots at verification time, not Git subjects or
  independent native A/B builds.
- **HISTORICAL FAILURE:** the old-base frozen Darwin Nix delivery attempt stopped
  before tests at an uncached `mailcap-2.1.54.tar.xz` fetch returning HTTP **404**.
  No pin/mirror change or blind retry followed. At that checkpoint only evaluation
  and direct Python had passed. The later exact-input unblock/current-source build
  below does not erase this failed attempt.
- Historical old-base evidence remains separate: 224 tests with one skip, initial
  independent review's buffered-output P2 corrected and independently accepted,
  and unchanged old-base image derivation. It does not qualify updated main.

Cached local reproduction; an absent tool is a blocker, not install permission:

`env PYTHONDONTWRITEBYTECODE=1 /nix/store/0kcws5blwbdyxqxjd1w8hjipz61xny3k-python3-3.14.7/bin/python3 -m unittest discover -s scripts -p 'test_*.py'`

`git diff --check`

Nix `--offline` disables substitution, **not fixed-output network acquisition**.
Do not repeat the blocked build blindly or claim image/runtime acceptance from
these checks. Resolve dependencies separately without changing verification gates.

### Current-source freeze and exact dependency unblock — local delivery passed

On reconciled main `6e4d5516d10e0a38279abce072ec06fc9a0ae556`, a fresh allowed-input
candidate snapshot has identity
`91a34bcd68d41f92ff5f290375f6fd0f6341a47c20ec36c231bed84ed2d2b6cc`.
Current-main and candidate x86_64-linux image derivations both evaluate to
`/nix/store/4pzx7cnnl6xj5iyyklivjcpfqcbisjnx-possums-intake-local.tar.gz.drv`.
The candidate Darwin delivery-test derivation is
`/nix/store/cx16jzp8k9gxivgcd1aad37izzhdmibz-intake-offline-delivery-tests.drv`.
Image equality remains evaluation evidence; this frozen source is not yet a Git
release subject. The separate actual local delivery build below has now passed.

Read-only native Nix cache metadata established that the exact missing mailcap
source/bootstrap outputs were available from the existing default signed cache.
One normal verified copy realized only those two already-declared inputs; no
source URL/hash/pin/mirror/trust change or new tool/profile/system installation.
One actual offline/no-link/no-external-builder delivery invocation then exited
**0**, joined normally and needed no kill/retry. Its previously absent output is
valid:
`/nix/store/bdqsl99i1q8b80ayi2z33nk9rjyd30b6-intake-offline-delivery-tests`.
Parent independently verified that output's exact deriver and zero-byte completion
marker, and that all 443 current declared inputs still match the frozen identity.
The child validated the frozen manifest before/after execution. A bounded log read
did not recover a strict unittest count/skip summary; **no Nix-run test count is
claimed**. The separate direct Python result remains 285 tests/one skip.

Normal Nix store/build records can persist; no additional diagnostic file was
created. Offline mode does not universally prevent fixed-output fetches. Earlier
metadata probes encountered the local Nix version-4 JSON wrapper; supported stable
queries established the dependencies without project-code changes. Native Linux
matching image builds, signing/provenance and runtime remain unqualified.

## Public native owner adapter — offline review checkpoint

The explicit `scripts/public-owner-publication.py` sibling now selects PUBLIC-only
source/package admission while reusing native saved-owner auth, exact current-main
checks/qualification/artifact downloads, archive/startup evidence and the existing
once-only digest-preserving executor. The legacy entrypoint retains PRIVATE-only
defaults. Explicit admission/presenter injection is used, not global policy patches,
optional package association or invented Actions authority. Core qualification
check/artifact reads were mechanically extracted; legacy current-main admission
still occurs before artifacts are admitted.

Independent Sol review found no material blockers in this scope and executed 20 new
public-owner, 37 unchanged owner and 65 existing first/diagnostic-publication tests.
Two additional actual closed-pipe synthetic receipt cases retained one copy and
completed outcome without raw shutdown errors. The implementation checkpoint's
full discovery ran 257 tests, one skipped, with no failures. Parent execution
also passed all 20 new tests. Active LSP checked seven parent/adapter files: six
clean, with one pre-existing structural warning at `positive()`'s `int(value)`.
That unchanged conversion follows a string/positive-decimal, 20-digit regex
guard for native CLI input; no parse-site change or new warning was introduced by
extraction.
An initial reviewer invocation from the wrong import directory failed discovery;
the corrected focused runs succeeded. These are source/offline fixture checks, **not actual native auth,
publication, native Linux builds or signing qualification**. Failure-retained private
workspaces/native host history remain separate persistence boundaries. The new
signing packet has passed scoped independent source/offline review; activation
remains off.

## Historical dormant signing foundation — source/offline review accepted

This subsection records the merged hard-disabled foundation, not the newer local
guarded-signing preparation below. Its test/build identities remain historical.

The new `scripts/public-tinfoil-release.py` admits owner-selected tag dispatch and
separately authenticates S/R checks, native qualification/artifact IDs, complete
release/signing absence, current-main/lightweight-tag R and sole-parent/config-only
S→R relation. It validates exact serving bytes and equal offline evaluated image
derivations from committed snapshots, then re-admits downloaded A/B/archive evidence
before the official action. Python never publishes, creates tags or deploys. All
activation gates remain off; package access is read-only, not publication authority.

Review confirmed an additional stdout edge case: when descriptor 1 was already
closed, opening `/dev/null` reused it, and closing the sink closed the replacement
again. The reproduced exit 120/raw shutdown warning is fixed by retaining the sink
when it equals the target descriptor. Six new subprocess cases pass; independent
review exercised eight additional signing/owner cases and confirmed exit 1 with
closed diagnostics and preserved primary categories.

Signing review found one test blocker after the workflow gained necessary
`packages: read`: a stale assertion rejected the entire permission key. The earlier
285-test green run preceded that permission change; the reviewer correctly
reproduced one failure. The assertion now requires `packages: read` and prohibits
`packages: write`, without changing authority or restoring optional association.
Parent reran **285 tests, one skipped, no failures** after this correction, with
protected runtime/accounting/plan/privacy/package paths unchanged and clean diff
whitespace. Fresh independent confirmation passed all 27 signing tests and the
full 285-test suite with one skip, plus eight additional descriptor subprocess
cases. The reported blocker is resolved; no new concrete source/offline blocker was
found. The ten reported YAML line-length warnings were subsequently addressed by
wrapping expressions/commands and retaining two immutable full action references
with narrowly scoped line-length-only annotations. An independent YAML parser
confirmed the dormant expression and all three shell-command values unchanged;
the full 285-test suite still passed with one skip and active YAML/Markdown LSP
reported zero findings. The first host-parser attempt used an unsupported Ruby
method; the compatible invocation succeeded. No authorization/validation check was
disabled; this is not an actual Nix/signing execution.
Authenticated CI-token access, actual native builds/derivation acquisition,
cryptographic signing/provenance and runtime acceptance are still unqualified.

## Guarded manual signing and read-only acceptance — local preparation

**Facts:** public [PR #16](https://github.com/ajbt200128/possums-intake/pull/16)
merged normally at `8ece81feb1db17144f52330b217ee96bc5b05911`, with exact parents
`6e4d5516d10e0a38279abce072ec06fc9a0ae556` and
`ba4d4d462532148d541af8d076f86def4a6a7722`. Its Linux PR check run
`40929665095`, attempt 1, completed successfully, including native Nix checks,
actual image/probe builds and generated-archive qualification. This is PR
merge-preview evidence, not final S/current-main A/B or signing acceptance.

The next source packet prepares, but does not remotely activate:

- The manual signer replaces its literal-false barrier with the existing AND of
  `RELEASE_AUTOMATION_ENABLED` and `PUBLIC_INTAKE_RELEASE_ENABLED`. Both workflow
  process switches use strict JSON boolean selection as well as the string check:
  GitHub equality ignores case, so `TRUE`/`True` must fail expression evaluation
  rather than normalize to enabled; other unselected values map to `false`.
  Python independently requires both process switches exactly `true` before event
  or external reads. Owner/tag/manual/first-attempt, complete
  inventories, S/R evidence, original artifact IDs and guard-before-action checks
  remain. The protected `intake-public-release` environment remains required;
  production deployment stays literal `false`, and image/deploy templates remain
  disabled. No remote switch, environment or grant was changed by this packet.
- Explicit `public-tinfoil-release.py repository-readiness` performs only the
  existing fixed-source/fixed-package authenticated GitHub GETs. `check.yml`
  invokes it with its actual temporary `GITHUB_TOKEN` and `packages: read` before
  builds. This catches access failures before final S/signing preparation. Actual
  CI success is still required; a declaration or owner-token read does not prove
  CI-token access, registry-write authority or future signer-token availability.
  Optional `package.repository` association is never consulted. `status` stays
  offline and reports no observed live activation, not a global enabled state.
- `public-release-acceptance.py accept` is an explicit read-only host command with
  validated public source/release/check/qualification/artifact/image/archive pins.
  It authenticates the saved fixed owner through the native SDK without token
  introspection; checks exact completed successful signing attempt 1, complete
  invocation inventories, current main/lightweight tag R, S/R checks and original
  qualified artifact IDs; and reuses committed S→R config/derivation gates. The
  local checkout must be R. It never accepts supplied evidence JSON or a fixture
  as command-line proof and never creates tags, publishes, retries or provisions.
- Acceptance downloads the two standard assets, `tinfoil-deployment.json` and
  `tinfoil.hash`, through bounded native SDK GETs. Actual sizes and the canonical
  SHA256-plus-newline hash file must match actual manifest bytes; exact serving
  config/vendor pins/shape must pass. It then reuses supported `gh attestation
  verify` and the existing verified-statement gate, requiring the exact tagged
  workflow certificate/source/signer R, predicate/subject and signed run/attempt.
  It rechecks selected evidence afterward. Release `target_commitish` is not tag
  authority: the pinned official action omits `--target`; actual tag and signed
  certificate binding are authoritative. Unsigned hashes alone never suffice.

Native acquisition has the existing **64 KiB per-stream** limit, stricter than the
manifest's 512 KiB structural ceiling; larger responses fail closed. Locally
authored SDK startup/deadline/reader/acquisition/decoding categories survive outer
boundaries without raw exceptions or invented HTTP statuses. Known workflow,
release, crypto and acceptance observations remain sticky through later cleanup
or output failures. Owned public-manifest workspaces are removed only after
positive child join; unconfirmed join retains them. Reused relation-snapshot
cleanup/command-join semantics were not changed or newly qualified by this packet.
Native SDK auth/cache/history, GitHub action credentials/log retention and Nix
records are separate host boundaries; project code adds no diagnostic log, raw
cache or support bundle and promises no whole-host transience. The pinned upstream
composite action can fail before project code can classify it; this packet does
not suppress or copy its native logs or promise a project code for every vendor
failure. Failed/unknown publication requires passive reconciliation, never replay.

Initial independent review passed 309 tests with one skip but found a P2: the
shared signing-inventory projection erased already-known native failure categories.
An explicit inventory-reader seam now preserves the native category without
changing default signing callers, pagination or schema admission. Parent also found
that selected check-attempt filtering could swallow those categories/statuses;
a closed-category propagation seam preserves them only for acceptance, retaining
legacy filtering defaults. New regressions traverse the actual native adapter
with synthetic command responses at signing history, qualification jobs/artifacts,
release assets and both selected check attempts, before and after crypto evidence:
96 targeted subcases cover startup/deadline/join/reader/acquisition/decode and
observed HTTP 403/404, including conservative cleanup and sticky outcomes.

Parent direct discovery now passed **312 tests, one skipped**, including 24 focused
acceptance tests. Active LSP checked 12 changed paths: 11 clean, with the already
recorded unchanged regex-guarded `positive()` numeric-parse warning at
`synthetic-publication.py:70`. No parse-site change was introduced. Real
closed-descriptor and broken-pipe subprocess cases are included, but cryptographic
successes in unit tests use internal fake ports: **no actual new signature,
final-main native A/B or runtime acceptance is claimed**. Fresh independent review
confirmed the P2 response, independently passed all 312 tests with one skip and
all 24 focused acceptance tests, and found no new concrete P1/P2 source blocker.
At that checkpoint, protected controls, actual CI-token access and native
SDK/release qualification remained outstanding.

Before the next local build, the delivery derivation was updated to copy
`check.yml` explicitly alongside its other workflow fixtures; the new readiness
assertions must not depend on an undeclared checkout file. A regression asserts
that input declaration. One actual offline **aarch64-darwin delivery-check build**
then completed, joined normally, with unchanged pins/trust/builders and no
wall-clock build wrapper. All **445 declared inputs** matched before/after; frozen
identity is `76b682e28cbd86c228ec1a4ca45d93f581fff7f3c9f65a715952342e75d5efad`.
The previously absent output
`/nix/store/qy9bjq31c826sa01mvd2g48iz37x5hvy-intake-offline-delivery-tests`
is valid with exact deriver
`/nix/store/z6ii5ryj76w596kqb90lal98icq9bgyd-intake-offline-delivery-tests.drv`
and zero-byte completion marker. No Nix-run test count is claimed. Candidate
x86_64-linux image evaluation still returns
`/nix/store/4pzx7cnnl6xj5iyyklivjcpfqcbisjnx-possums-intake-local.tar.gz.drv`,
matching the previously recorded base; no fresh base reevaluation or Linux image
build is claimed. The owned public snapshot was removed after joined completion;
normal Nix source/build records may persist. Independent final closure review
recomputed the same 445-input identity, matched stored `flake.nix`, `check.yml` and
all 29 declared Python scripts to current bytes, validated output/deriver metadata,
and independently passed 312 tests with one skip. It found no new concrete P1/P2
blocker. Passive metadata does not independently reconstruct the parent's observed
pre-build absence, process joining or snapshot removal. This is accepted local
source/check evidence, not final S qualification, actual crypto or runtime proof.

### PR #17 native CI and synthetic cancellation correction

PR #17 at `8e3cd5465ffd740fb55164e65bdc765523267f4f` passed its actual
temporary-CI-token fixed-public-package read and direct Python steps. Its first
native Linux check failed during the ledger Go tests; image/probe and generated
archive qualification were skipped. The original run remains failed, not rerun or
merged. Bounded in-memory review selected only a known, locally authored assertion
in `TestCanceledCreatePreservesPrimaryUncertaintyAndClaim`; no raw logs, exception
text or dependency-cache pointers were copied here. This pre-existing assertion
alone does not establish which predicate failed or a production root cause.

The focused correction changes only that synthetic test: its callback cancels and
withholds the response until `StartTopup` returns. An immediate deferred channel
close releases the handler before later fixture-mutex acquisition or cleanup.
All original uncertainty, reference/URL, durable claim and once-only request-count
assertions remain unchanged, as does the positive known-create/bind-failure test.
Independent source review found no P1/P2 objection to the lock/cleanup ordering;
it correctly left execution qualification conditional. No payment runtime,
receipt, cancellation or retry policy changed.

Parent execution then passed both selected methods **20 times each** at
`GOMAXPROCS=1,4`, with and without `-race` (**160 targeted executions**), followed by
both full affected payments/Stripe packages. This used the flake-selected native
Go **1.27.2**, its pinned compiler for race mode, and a 445-input owned public
snapshot with identity
`0933f878166436479d36b90b53b7d5eefdaa7503b00099c9147b1adfaef1c8fc`.
An existing public-module cache was read only through Go's file-proxy interface;
new module/build caches were temporary, no sibling-cache writes were requested,
remote fallback and telemetry were disabled, and current `go.mod`, `go.sum` and
source hashes remained unchanged. Owned snapshots/caches were removed after
joined completion. Formatting and active Go LSP checks passed; Python discovery
still passed 312 tests with one skip. Independent review found no P1/P2 source
objection and confirmed one separate `-json -race -count=2` command: exit 0 and
joined completion were retained before parsing, with two pass events per selected
test and one package-level pass. These **four independent executions** are separate
from the parent's 160; unconfirmed executions are not counted. The same 445-input
identity matched before/after. Parent subsequently admitted the unique owned
review temporary tree using its exact input manifest and filesystem ownership,
removed it without following symlinks, and confirmed removal without rerunning
any test. Corrected-head native Linux qualification remains outstanding.

An earlier local pinned-Nix-vendor realization failed before any Go test build;
its underlying cause remains unknown and it was not replayed. These later
file-proxy tests do **not** verify the complete Nix vendor hash. Normal native Nix
records may persist. Go test files are declared image inputs: the corrected
candidate x86_64-linux image derivation now evaluates to
`/nix/store/jygqk8hdv757yy9yjfdv5i7mfksyf0xa-possums-intake-local.tar.gz.drv`.
This is evaluation only. The previous `4pzx7…` image equality is historical,
not qualification of corrected source; fresh exact-source image/probe/archive
and subsequent native A/B qualification are still required. Repeated test passes
do not prove the historical CI cause. No merge, signing, release, resource
activation or secret readiness follows from this correction.

**Sequence before freezing S:** finish all activation/tests/docs/acceptance source
preparation and qualify external controls first. Then require exact final-main S
checks and reuse `qualify-synthetic-image.yml` once for independent A/B and actual-A
qualification. Preserve its original run/attempt/artifact IDs and one-day artifact
window; rehash before the one native owner copy. Create R as S's sole-parent,
regular root-config-only A/M commit, require R checks, current-main/lightweight-tag
binding and equal image derivations; do not rebuild a second A/B for config-only R.
No source or documentation commits between frozen S and R. Only the pinned
official action publishes once after approval; actual downloaded assets and
supported crypto must pass against that completed invocation. Metadata, fixture
passes, old releases and PR checks cannot substitute for these gates.

Legacy image callers still reject public source through PRIVATE defaults. The
legacy deploy-repository signer instead binds its own PUBLIC repository and
receipt; intake becoming public does **not** retire or necessarily block it.
Preserve that signer and historical evidence, retain exclusive writer custody,
and retire future legacy publication only in the separately reviewed final step.
The stopped-create/private destination/secret-readiness gates remain unmet;
**Tinfoil secret entry is not ready and the existing ledger/disk/domain is unchanged.**

## Exposure review — preliminary local history coverage only

Fetched all currently advertised origin heads/tags and pull-request head/merge refs
into local remote/audit namespaces, without remote mutation or source-history
rewriting. At this checkpoint 25 refs reached 1,415 objects and 903 unique named
blobs (16,647,916 bytes). Bounded in-memory scanning of every named blob found no
GitHub-token, complete private-key-block or AWS-key pattern candidates and no
selected private `.env`/SQLite/database-state filename candidates. Stripe-shaped
matches were 20 synthetic test literals and one explicitly non-app sandbox
credential-shape validation placeholder. Source context was reviewed with candidate
values redacted before presentation; no candidate values/lengths or private runtime
configuration were emitted or read from host settings.

This is **not a clean-secret certification or completed exposure audit**: selected
patterns cannot establish absence of every credential, private content or rights
issue. Manual privacy/rights/third-party-notice review, Actions output/artifact
exposure and fresh complete remote inventory remain gates before visibility.
Historical PR/comments and source outside those fetched refs are not certified by
the blob scan. No license is added and no historical evidence is deleted.

### Independent source/history disclosure review — historical exception approved

Independent Sol reviewed the current 496 tracked/eight nonignored untracked files
and the 25 fetched refs, reaching 120 commits, 392 trees and 903 blobs. This was
bounded screening plus contextual reading, not line-by-line certification. No new
license or concrete existing-notice conflict was found: current UI distribution's
107 manifest entries/11 component-license entries match, with two historical UI
manifest revisions and one font-manifest revision checked. PocketBase and font
notices/inventories remain unchanged. Nonvendored dependency obligations, upstream
contributor authority and trademark permissions are not newly certified.

After these findings were disclosed, the operator explicitly selected **Allow
existing historical metadata**. This is a narrow public-disclosure exception for
the reviewed existing metadata below, not transcript contents, credentials,
customer data, future private references, history rewriting or deletion. It does
not itself complete exposure/control/build qualification or change visibility:

- `PRIVATE_DEVELOPMENT_REFERENCE`: current `README.md:90` and `PLAN.md:122`
  contain private planning-thread references; PLAN also contains a local native
  pointer. They remain in 20 reachable blob versions and all 25 inspected ref
  tips. No transcript bodies were identified. Policy excludes development
  references from repository publication; a HEAD-only cleanup cannot remove this
  retained history. The operator explicitly permits these existing metadata
  references under the narrow exception; do not edit PLAN, inspect/export the
  referenced native transcripts, rewrite history or delete evidence.
- `AUTHORSHIP_IDENTITY_DISCLOSURE`: non-noreply author metadata in all 120 commits
  and non-noreply committer metadata in 105; remaining committers use GitHub service
  metadata. The operator permits the existing attribution; no identities are
  reproduced here.
- `MERCHANT_CONFIGURATION_DISCLOSURE`: `docs/stripe-live-activation.md:153–168`
  includes authenticated merchant registration/webhook status summaries, not just
  public measured declarations. The subsequent explicit exception now permits
  these existing recorded summaries, not new private merchant collection.
- `LOCAL_PATH_DISCLOSURE`: routine operator checkout/worktree/cache locations occur
  in source/history. Some external-log references are explicitly permitted by
  policy; do not mislabel all paths transcript pointers. The subsequent exception
  permits disclosure of the reviewed existing paths.

Eleven non-UTF8 blobs were nine fonts/two favicons; no committed archives,
customer-state dumps, native transcripts or screenshots were identified by the
review's screening. Arbitrary embedded/encoded content is not certified absent.
Do not reproduce the flagged private references or inspect their native transcripts.

### Retained Actions/output and control-plane review — bounded evidence

Authenticated native GitHub GETs observed intake still private/admin-accessible,
100 completed workflow runs, four unexpired artifact records, zero source-repo
releases and zero configured environments. Wiki/discussions are disabled; issues
are enabled. Actions are enabled for all actions, default workflow token permission
is read-only and workflow approval of pull requests is disabled. Repository ruleset
metadata was unavailable through the supported native read; no HTTP status or root
cause was recovered and no absence of protection is inferred. A protected signing
environment must be explicitly established/qualified before activation; merely
naming one in YAML is not evidence of protection. A bounded supported native
installed-App metadata read was also unavailable, with no recovered HTTP status;
no missing installation, permission scope or root cause is inferred. Existing App
permissions require private owner review, not new grants or credential entry.
Complete protection/environment qualification remains unfinished.

Manual bounded in-memory review fetched every retained run's CI log ZIP through
the pinned native CLI: 100/100 runs, 320 members, 6,746,404 uncompressed bytes. The
selected credential/token-bearing-query patterns had no unidentified candidates or
unavailable/capped items. This is not semantic or clean-secret certification.
No enclave/production logs were requested, raw logs displayed, extracted, cached
by the review script or saved as a new diagnostic artifact. Native credential,
host-history and GitHub retention remain separate boundaries.

All four retained artifacts were then reviewed in memory, including the image
archive's six rootfs layers/1,214 regular files. Selected patterns covered
50,291,444 content bytes. The sole webhook-shaped match was independently compared
against the source's documented non-app credential-shape validation placeholder and
located in the compiled intake binary; zero matches remain unclassified. No
candidate values, lengths or hashes were emitted, no image was run and no archive
was extracted or new raw-cache artifact created. This static review does not
establish native build, runtime privacy, provenance, financial state or exposure
acceptance. Fresh complete inventories and remaining comment/control/build gates
still apply despite the historical metadata exception. **The operator should not
create or change secrets yet.**

Subsequent complete issue/PR/comment REST inventories found 15 pull-request bodies,
zero separate issue bodies and zero issue, pull-review or commit comments. Bounded
candidate filtering and locally redacted presentation supported contextual review
of all 15 source-change/qualification/boundary summaries; no unresolved credential,
transcript or body-size candidate was identified. Contact, link, local-path and
opaque-identifier values were redacted before presentation. This is scoped source
discussion review, not a guarantee about arbitrary encoded content. No visibility,
App grant, secret, environment, branch protection, commit or push changed during
these reads. Fresh exclusive-writer coordination and private App-permission
confirmation were requested before remote integration/visibility mutations. The
operator subsequently confirmed **Paused—take custody** and **Reviewed and
intended**. This thread now owns the intake/deploy source/release/deployment writer
window; the running ledger/accounting service stays untouched. App confirmation is
operator evidence, not a recovered API permission map or new grant. Refresh custody
on handoff/contrary evidence and re-admit fresh source/control metadata before
mutation. After the frozen Nix check, four local code/refactor commits were made:
`9c6971f` (public contract/template), `b207cfa` (qualification-read extraction),
`c160611` (explicit public native owner adapter) and `8222a02` (dormant signing
workflow/guard). This authority/evidence record is a separate documentation commit.
No push, PR, visibility change, publication or resource mutation has occurred at
this checkpoint. Private review and exact-source CI precede integration; native
matching builds/signing and separately admitted resource work remain later gates.

## Operator-made public visibility — custody reconciled

A fresh read after the five local commits found `private=false` for the fixed
source repository, while main remained `6e4d551` and the intended remote branch
was absent. The PRIVATE-only push pre-admission stopped **before any push**. This
thread made no visibility-change request and did not automatically revert it.
The operator confirmed **Yes—public, custody remains yours**: the visibility
change was deliberate and other intake/deploy source/release/deployment writers
remain paused. This establishes intent/custody, not the change's exact timing or
completion of pending native/control gates.

Source integration now requires explicitly PUBLIC-only admission and a public PR,
not a private/public fallback. Record this reconciliation separately; do not rewrite
the five prior local commits. Existing historical-disclosure/no-new-license limits
remain. No push/PR has yet occurred at this reconciliation checkpoint; no secret,
release, signing, resource, current ledger/disk/domain or gateway mutation follows
from public visibility alone. Native matching builds, actual authenticated package
access, protected environment/control checks and signature acceptance remain gates.

## Revised execution order — stopped-instance preparation

1. **Exposure audit and delivery implementation.** Review tracked history/refs,
   retained Actions outputs/artifacts/releases, rights/notices, GitHub App and
   branch/environment permissions before changing visibility. No blanket clean
   audit claim or automatic artifact/history deletion. Implement only the public
   path; no accept-either-visibility switch or fallback to private publishers.
2. **Exact source/build ownership.** Finish guarded activation, tests/docs,
   acceptance glue and external controls before freezing S. Reuse
   `qualify-synthetic-image.yml`, not a competing qualification workflow; preserve
   its original IDs and one-day artifact window. Source **S** is exact successful current-main
   source, with explicit check/run/attempt/artifact bindings. Require independently
   matching native A/B images/archives and actual-image privacy/restart qualification.
   Rehash the exact accepted archive before the once-only copy and preserve known
   completed-copy evidence across later errors. Refresh quiet-writer custody.
3. **Immutable measured release.** Release **R** has sole parent S and differs only
   by its regular root measured-config A/M transformation; compare their image
   derivations for equality. No intervening source/docs edits or second config-only
   R A/B build.
   Use the pinned official action `59f7ab5dbe472b3097c888ebe64e6a4131fc0975` as sole
   publisher, `mark-as-latest: false`, least permissions. Verify downloaded standard
   assets and supported crypto against the exact repo/tag/source/workflow and actual
   completed successful signing invocation/attempt. Older three-field receipts alone
   are insufficient. Keep activation disabled until adapters/gates are qualified.
4. **One separately qualified stopped create.** Use the supported create-only API
   after the gates above, never the auto-deploying CLI. Preserve the current running
   instance/disk/domain and avoid new storage or an empty ledger. Reconcile the new
   stopped/no-attachment/no-connection/no-pending state; no retry on uncertainty.
5. **Future original-disk/domain cutover — not now.** Separately admit supported
   private secret/key availability, saved-variable configuration, same-host stopped
   original disk transfer and exclusive accounting authority before one serving boot
   and domain change. No copied spendable ledger, restore, init or simultaneous
   gateway replacement. Preserve original live Stripe behavior and unknown purchase
   outcomes, never replay them. New consumer/hardware-attestation work stays deferred;
   source signing/platform-WebPKI health are not runtime attestation or funding.
6. **Retire future old publication.** Only after accepted intake-owned operation,
   disable future deploy-repo/private-handoff publication in a separate reviewed
   packet. Preserve all old tags/releases/evidence. Share only proven small helpers
   later; no third repository or speculative deployment framework.
