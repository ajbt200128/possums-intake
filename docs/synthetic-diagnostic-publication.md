# Existing-public-package diagnostic image publication

**Offline implementation/fixtures only; no publication or deployment evidence.**
This narrowly separate manual path publishes one source-SHA tag in the already
public `ghcr.io/ajbt200128/possums-intake-synthetic` package, owned by
`ajbt200128` and linked to the private `ajbt200128/possums-intake` source.
The operator explicitly approved that package remaining public permanently.
A public executable and embedded assets can be downloaded and reverse-engineered;
a private source repository is not binary confidentiality. This path neither
changes visibility/access grants nor publishes source, releases or production
configuration. The original first/private publisher still refuses every visible
existing package and cannot perform this update.

## Fixed gates and trust boundary

`.github/workflows/publish-synthetic-diagnostic-image.yml` invokes
`scripts/synthetic-diagnostic-publication.py`. Dispatch is manual, `publish` and
`writers_quiet_confirmed` default false, and authorization binds `expected_source`,
`quiet_confirmation_source` and the fixed known baseline:
`sha256:e37d729571bd8ab82eb715a0e5ff97ab3e5a0d8c76b98c1baa1dfa27fb2d55ca`.
Only the owner on current main, on attempt 1, may proceed. The authenticated full
private repository, exact in-progress owner/triggering-owner workflow identity,
successful exact-source `check.yml`, exact completed native qualification attempt,
independent fresh A/B jobs/runners and bound artifact identities are checked with
the existing admission gates. Artifact IDs are selected by admission, not dispatch.
Only the copy job has package-write permission; validation has read permissions.
Action pins and the concurrency group match the first publisher.

Before copying, authenticated package metadata must establish the fixed name,
owner, container type, public visibility and private source link with matching
repository ID. Active package versions are read to the end of pagination, at
100 versions/page with at most 100 pages. Missing/malformed/inaccessible evidence,
duplicate version IDs or reaching the cap with a full final page refuses the
operation. The baseline digest must occur in that complete evidence and the exact
new source-SHA tag must not occur anywhere. A 404 establishes neither absence nor
authority. Native API inventory is not printed or saved.

All original archive hash, raw manifest/config digest, native Linux/amd64 A image,
ordinary runtime/native-operator and six synthetic-case gates remain mandatory.
Additionally, the actual A image's `synthetic-qualification.json` must contain
`startup_diagnostics` as **exactly** these five fields, each JSON boolean `true`:
`healthy_healthcheck`, `missing_key_capture`, `invalid_operator_capture`,
`signal_cancels_hold`, `closed_failure_output`. Missing/extra fields, integers,
strings and null refuse acceptance. Source-only diagnostic tests do not substitute
for that generated-image evidence.

Dispatch confirmation, admission/current main/CI/artifacts, archive bytes and
complete package/tag evidence are re-read immediately before the single
`--preserve-digests --retry-times 0` copy of the approved archive to that source-SHA
tag in the same package. No rebuild, overwrite, delete, latest-tag write,
other-package write, grant/visibility change, automatic trigger or retry exists.
After copy, the same package/repository IDs, public/private-link metadata and raw
registry manifest digest at the exact new tag must agree with the archive receipt.

**Observed tag absence is not atomic immutable-tag creation.** Pagination is not
a transactional snapshot; even the final read and copy have a race. The owner
must freshly confirm every other registry writer is stopped and keep them stopped
through copy and postcheck. Shared Actions concurrency covers these two workflows,
not another workflow, operator or external registry writer. Platform/API correctness
and owner control of that quiet window remain trust boundaries.

## Failure and privacy behavior

The executor marks the session used and outcome `unknown` before starting copy.
Successful copy is `completed` even if later package/digest acceptance, auth cleanup
or receipt presentation fails. Do not rerun, roll back or infer registry absence
from an uncertain result; inspect authoritative state privately. The consumed
in-memory session refuses another attempt, retaining its observed outcome; across
new dispatches there is no durable attempt journal, so operator non-replay discipline
also matters. Nothing here verifies deployment, readiness, attestation or CVM
compatibility. Parent independent review, actual-image/native CI acceptance and the
separate selected CVM 0.14.14/release/deployment gates remain necessary before the
authorized one copy/deployment.

Inherited bounded commands suppress raw subprocess streams and join the tool
before cleanup. Credentials enter only a private owned `/tmp` authfile (0700
directory, 0600 file), never argv, stdout or artifacts; inherited `TMPDIR` cannot
redirect it. Removal is attempted on every exit, and cleanup failure supplements
rather than replaces a primary failure. Closed locally authored codes include
`DELIVERY_DIAGNOSTIC_PACKAGE`, `DELIVERY_DIAGNOSTIC_COPY` and
`DELIVERY_DIAGNOSTIC_PUBLIC`, with known safe HTTP status where observed and a
stage-specific cause-unknown fallback. No error-text classification or raw
exception/configuration output is permitted. The accepted receipt remains only
`publicSourceSHA`, `imageDigest`, `archiveSHA`, retained in the private source
repository for one day. No raw/private evidence or credentials are artifacts.

Applicable policy: [intake forbidden collection/defaults](../PRIVACY.md#forbidden-collection-and-defaults),
[intake temporary startup exception](../PRIVACY.md#temporary-synthetic-startup-diagnostic-exception),
[gateway forbidden telemetry](../../possums/PRIVACY.md#telemetry-permitted-signals-and-forbidden-data)
and [processor boundaries](../../possums/PRIVACY.md#processors-retention-access-and-shutdown).
Native Actions/host event/history retention and GitHub/registry/platform metadata
are independent persistence boundaries; authfile cleanup and short receipt
retention are not whole-platform transience or secure-erasure claims.

## Offline commands

Passive status/report commands perform no API, archive inspection or copy:

`PYTHONDONTWRITEBYTECODE=1 python3 scripts/synthetic-diagnostic-publication.py status`

`PYTHONDONTWRITEBYTECODE=1 python3 scripts/synthetic-diagnostic-publication.py report`

Injected fixture and workflow checks, without Docker/GitHub/registry access:

`PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_synthetic*publication.py'`

`PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_synthetic_workflow.py'`

`git diff --check`

`admit` performs authenticated read-only gate checks and selects artifact IDs;
`publish` is a side-effecting workflow-only boundary, not a status/recovery command.
Do not dispatch it merely to test or diagnose. This packet does not run either.
