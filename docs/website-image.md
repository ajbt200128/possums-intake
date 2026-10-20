# Combined customer website — local generated-image qualification

## Scope and authority

The operator authorized website image-packaging fixes and qualification, scoped
application/release commits and pushes, and one update of the existing
synthetic Tinfoil instance after the release gates pass. This grants no additional
instance, volume, secret change, gateway change, real credentials/data, payments,
telemetry export or production activation. Unrelated checkout work stays out.

**Subsequent integration boundary:** authoritative main contains newer managed
storage/startup changes. The operator approved source integration and renewed
qualification, not replacing the current persistent profile with disposable
`synthetic-serve`. Preserve the existing disk/key and all explicit setup gates.
The artifact results below describe the pre-integration website source, not the
merged candidate or acceptance of live initialization/state. The operator later
explicitly authorized one additive managed website setup boot, followed only
after observed setup process exit zero by one serving activation on the same
disk/key. Other delivery writers were confirmed paused. This supersedes the
original single-update scope, not storage, qualification or nonreplay guards.

## Public inputs and dependency closure

`nix/packages.nix` now includes `pkg` Go sources, the six individually named
website Lisp documents, and the two pinned Iosevka fonts and OFL license. Both
Nix and the frozen source inventory include the six named reusable-library Lisp
test fixtures; the Nix ledger check now tests `./pkg/...` as well as CLI/internal
packages. Arbitrary neighboring Lisp/assets, environment files and private state
are not imported. The snapshot additionally retains the public font manifest and
notice, checks asset byte sizes/digests, and rejects missing or symlinked inputs.
Existing native dashboard assets/license checks remain intact.

Offline `go mod vendor` and an independent actual Nix fixed-output dependency
build agreed on `sha256-3HwXGLiB+gstzqW8iLoE2VstcHjkl5yfxsoMURd+/UU=`.
The toolchain remains pinned Go 1.27.2; this is not a dependency-upgrade request.

## Historical pre-integration archive and runtime evidence

Frozen declared-source identity:
`55dca24d8064ef6fa4da79ebf609f131026eec5c90f5ada54f46400c9ad459c0`.
This local snapshot includes uncommitted public source and is **not exact-main or
native AMD64 release evidence**.

- Linux/arm64 image archive SHA-256:
  `627981ed302f416a87afe40f4116d180273d50a6cabff90e50c88e0a8abc0268`.
- Raw image manifest: `sha256:012b0c43f0f2b7cece0bfb68c3ec7add137ba9acedf6a165dd384df32c96c6a9`.
- Archive config: `sha256:e8d55399bff89d6ca16c99d3a6e92ce3aaf00203c67c9c331395cf114f776a64`.
- Local Docker immutable runtime identity:
  `sha256:e5cc5ab339de0fbef39cdd5d7fa3c2de49f18d014e929d761b7ba35e67a888c1`.
  The existing containerd compatibility gate saved this immutable image and
  verified that its archive config matched; unlike classic Docker, these two
  identity formats need not be identical.

Actual Nix image/probe builds, the Nix ledger test derivation, and the offline
Nix delivery check passed. The frozen ordinary archive fixture passed all ten
accounting/startup cases and the existing native administrator/privacy/asset
checks. Its native-operator sequence now mandates website checks before success:
stock long-key customer provisioning, default pages, compiled CSS/font/license
hashes, independent customer sessions, complete exact balance/hold text compared
with the accounting API, guest and bearer-authority separation, inert draft/live
publication, session continuity, logout/stale-cookie rejection, and same-volume
restart with persisted code/auth/accounting. Both stopped processes passed empty
stdio and database logging/origin checks. The probe is not shipped in the image.

The separate actual synthetic-startup archive fixture passed all six synthetic
and five startup-diagnostic cases. A parent presentation wrapper initially used
an incorrect receipt-key allowlist after that script exited successfully; a fresh
disposable fixture run with the correct display schema also passed. No service
retry policy, accounting budget or deployment was changed to obtain this result.

The serial source race/elpscheck suite, combined ordinary-host Chromium test,
root vet and module verification passed. All 179 offline Python tests passed
(one optional local-binary fixture skipped); the actual archive exercised its
website HTTP path independently. Independent read-only packaging review found no
issues. Browser execution **inside the image** remains `not_run`.

## Newer managed-startup integration and initialization correction

The website source was merged with newer application main
`ef6a663cb7d08b39497bb2395ada83bf3fed504b`, retaining explicit managed-volume
unlock, mount verification, selected diagnostics, accounting budgets and strict
setup boundaries. Independent integration review found no actionable issue.
Full merged race runs exposed initialization failures before intended later fault
stages; a bounded twenty-repeat reproduction obtained the closed
`LEDGER_WORK_CANCELLED` code at `initialize`. Original generic failures without
retained codes remain cause-unknown; later evidence does not retrospectively
classify them.

Source inspection placed synchronous stock SDK password hashing inside the
one-second initialization transaction. The correction prepares the new native
record through the SDK before that transaction, with root-context checks before
and after preparation. The transaction app still performs fresh collection
settings decisions, stock credential validation and all atomic schema/credential
writes. No password strength, accounting budget, empty-only policy or retry
behavior changes. Synchronous bcrypt cannot be interrupted mid-call; no worker is
detached and no hash/credential is retained in diagnostics.

The corrected merged source passed the parent's full serial race/elpscheck suite,
vet, module verification and all 185 offline Python tests (one optional fixture
skipped). The implementation packet also passed focused ledger race repetitions
and twenty relevant CLI/fault/operator race repetitions. Independent source/API
review found no actionable issue; its default-cache offline test attempt was
unavailable, so it is not independent execution evidence. The operator separately
authorized an additive managed website setup boot followed by existing serving,
with all other delivery writers paused. No live action occurred during this
correction; the final managed-website candidate still requires fresh image and
native release qualification.

## Fresh committed-source local qualification

After the additive managed setup review found no actionable issue, private source
`b93ac99a970d5a35576ccb216061fe8ffdbd27d9` was exported through Git archive and
the existing declared-input freezer. This excludes unrelated untracked scripts,
state, environment files and caches rather than snapshotting the dirty checkout.
Frozen input identity:
`b303a7e924a2f662a60516ddce41384bb08511086b9cea071e06faa323bef111`.
Subsequent documentation-only edits do not change these declared build inputs.

Fresh pinned Nix image/probe, ledger-test and delivery-test derivations passed.
All **173 committed-source offline Python tests** passed (one optional fixture
skipped). The earlier 185-test working-tree count also included unrelated
untracked test files; those are not release inputs or publication evidence.

- Local Linux/arm64 archive SHA256:
  `5a5e710eb23008671aca18200a61b503794369de91a997ef609f4b970fb82bd4`.
- Raw manifest: `sha256:f0be40f65e71845d14cdaadd4673a7c9e8ae54723c44a42a4c046b75a7f4d59f`.
- Archive config: `sha256:cdcf30a089945c7323dd814cf757d80e23b4293eb6b6225250ba94d8541dcb39`.
- Docker runtime identity: `sha256:f9ac4a3a860da82e6094bdeedc01250db641c4a6c7549cbe352bc76f112a9c46`.

The actual archive passed all ten ordinary accounting/startup cases, native
operator/privacy/frozen-asset checks and mandatory website HTTP and same-volume
restart probes. The separate synthetic fixture passed all six synthetic and five
startup diagnostic checks against the same archive/config/runtime identities.
Receipt schemas remain unchanged; browser inside the image is still `not_run`.
These local Docker volumes are disposable fixtures, not the existing Tinfoil disk.
No control-socket unlock or managed website setup completion is established by
these tests. Native AMD64 exact-main independent A/B, publication, signed-phase
verification and actual setup/serving remain separate gates.

## Release and deployment boundaries

Local ARM results do not replace exact-current-main checks, independent fresh
native AMD64 A/B builds, archive-bound qualification, immutable publication,
signed release verification or authoritative post-update state. No historical
image receipt qualifies these website edits. Keep the existing receipt schema
and disabled production templates unchanged; a website probe failure aborts the
ordinary qualification before its success receipt.

The HTTPS-offload/private-backend/original-host contract is still the explicit
operator assumption in [website runtime](website-runtime.md), not measured ingress
or runtime-attestation evidence. Sessions remain process-memory only and restart
loses them; the stock cleanup worker has process lifetime, not joined teardown.
Real-data use, capacity, encrypted persistence, financial restore safety and gateway
integration remain unqualified. Applicable boundaries: [customer auth and reads](../PRIVACY.md#customer-key-authentication-and-scoped-website-account-reads),
[forbidden collection](../PRIVACY.md#forbidden-collection-and-defaults), and
[trusted manual administration](../PRIVACY.md#trusted-intake-manual-admin-exception).
