# Standalone local verification

## Current status

**Standalone synthetic source/service and generated-image acceptance gates passed.** The parent independently verified the source suite, compiled curl smoke and actual Linux/arm64 image/runtime/migration fixture. Independent read-only review found no actionable correctness, privacy or security defects in its inspected source scope. This is local acceptance, not production readiness, independent release reproduction or approval for integration. No gateway integration, real export, publication, provisioning, deployment, commits or pushes were performed or authorized.

This record describes parent-observed checks, not merely delegated-agent summaries. Historical packet records remain scoped evidence: [core](../internal/ledger/README.md), [runtime](runtime-verification.md), [HTTP/metrics integration](http-metrics-verification.md), [telemetry](telemetry-contract.md), [delivery](delivery.md), and [curl runbook](runbook.md).

## Stable source-level verification — 2026-10-11

The parent inspected the accounting, HTTP, lifecycle, telemetry and repaired smoke boundaries and independently ran the following on Go **1.27.2 / darwin/arm64**, using the pinned devenv environment:

- **PASS:** full Go race suite, including actual subprocess HTTP, persistent-state restart and the real one-minute maintenance/pruning schedule.
- **PASS:** Go vet, module checksum verification and compiled CLI build.
- **PASS:** 41 offline delivery tests.
- **PASS:** seven synthetic smoke-harness self-tests.
- **PASS:** actual compiled-service curl smoke, same-state restart and normal joined shutdown.

Repeatable commands:

`devenv shell -- env OTEL_SDK_DISABLED=true go test -race ./... -count=1 -timeout=180s`

`devenv shell -- env OTEL_SDK_DISABLED=true go vet ./...`

`devenv shell -- go mod verify`

`PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p test_delivery.py`

`python3 scripts/smoke.py --self-test`

For the compiled CLI/curl check, use the temporary-build commands in [the runbook](runbook.md#prerequisites-and-commands). The parent built with `go build -trimpath`, ran the actual runner, and removed its temporary binary after completion. The runner generated its own disposable state and synthetic credential; no operator state/key or external endpoint was accepted.

Before and after that complete verification interval, the source digest was identical:

**`efd83bcb1ce07ab153fad2832f23df7b8ed3405222a9f4ade3688dbfe6e2508d`**

This SHA256 hashes sorted `path:SHA256(file)` entries separated by newlines, with no trailing newline. Included: `.go`/`.nix` files under `cmd`, `internal`, `nix`; root `go.mod`, `go.sum`, `flake.nix`, `flake.lock`, `devenv.nix`, `devenv.yaml`, `devenv.lock`; and top-level `scripts/*.py`. It excludes documentation, workflow YAML, temporary state and native history. This is an **uncommitted source snapshot**, not a Git commit, OCI identity, signed release or exact-main CI result. Later edits require new scoped verification; the image-blocker packet is subsequent work.

Pinned accounting dependencies are PocketBase **v0.40.5**, dbx **v1.12.0**, SQLite **v1.60.1**. Metrics use supported OTel **v1.44.0** and gopsutil **v4.26.9**. The Go module toolchain directive remains 1.27.1; all parent checks above actually ran 1.27.2. Module checksums were verified, not inferred from documentation.

## Historical image-blocker packet

The parent inspected the narrowly matched bmake mirror rewrite, image probe and real-entrypoint migration-failure fixture. The pinned version/hash are unchanged. It independently passed the expanded **45 offline delivery tests** and compiled the updated Go probe with `devenv shell -- env OTEL_SDK_DISABLED=true go test ./nix` (no probe unit tests; compile-only evidence).

The earlier image snapshot was **`36f64782a7b2170840a1e54caa12ed099301d69975a803fc6cdb960423f1dec0`**, using the snapshot-manifest method documented in [delivery evidence](delivery.md#evidence-and-blockers), not the parent digest method above. Initial 300-second/600-second builds and the subsequently approved 3600-second continuation all exhausted their budgets, leaving 112 derivations and no image/probe. Those were incomplete results, not failed image acceptance. The user then approved local continuation without a wall-clock build limit.

## Final generated-image acceptance

The avoidable delay was disabling binary substitutes with `--offline`, which exposed the Darwin compiler/bootstrap dependency chain. Sol's read-only derivation/help inspection and signed official-cache prewarm are recorded in [delivery evidence](delivery.md#evidence-and-blockers). External dependency prewarm took 13.727 seconds; the actual joined image/probe build then completed in 20.789 seconds. Pins/signature verification and release fresh-A/B guards were unchanged. These are local cached build results, not independent reproduction.

Final allowlisted source manifest: **`e54575487c9efbf08a30fa748af49d6566c6ce5e0101ce759fb6c7c1145c976a`**. Only external-tool prewarm and Docker immutable-identity test/fixture files changed from the preceding image snapshot; production Go sources did not. The parent inspected the fixture correction: it resolves the installed containerd's manifest-based immutable ID and verifies its saved image config against the original archive before running it.

The parent independently reran and passed:

- **49 offline delivery tests**, no skips.
- **All ten actual generated-image gates**, using the declared archive/probe outputs: explicit empty init, real migration failure/SQL rollback/same-partial-state refusal, missing-state refusal, no raw logs, read-only nonroot runtime, usable original-state readiness, persistent same-volume restart, schema-drift refusal, telemetry disabled and unsupported-schema refusal.
- **The complete telemetry test package inside the Linux/arm64 image runtime**, with a cross-compiled test-only binary and allowlisted source mounted read-only, writable temporary memory filesystem, no external network, export off and no probe/test binary shipped in the production image. This exercised actual Linux current-process numeric collection, synthetic HTTP/accounting isolation and supported protobuf/TLS/redirect/error/shutdown fixtures. It is not a real Honeycomb export or a multi-minute live metrics release claim.

Public artifact identities observed by the parent image rerun:

- Archive SHA256: **`3b11a65b08418aff9ce8ab4823ceded91aef1942fbdda358e3600c1713dd071f`**.
- Archive-transport manifest: **`sha256:3243b017f3c23500569027ef762c6d597df6d96cde1e0b6ce85df8b4290a5dcf`**.
- Config identity: **`sha256:a42f657cbf0ff167e13ab4345da677bd553a89ae6b552034413d8deb3d68d09e`**.
- Docker immutable runtime ID: **`sha256:6eff9b53def6b01ea82ed7cab3f03ffd0c58280738a1012f5273cdaa86763f95`**.

Exact archive/probe commands are in [local delivery reproduction](delivery.md#local-reproduction). The helper is exec-only, not shipped; fixture containers/volumes were cleaned up. Public images/dependencies may remain in local Docker/Nix stores. No delegated tasks remain active.

## Privacy and diagnostic scope

Authority: intake [accounting boundaries](../PRIVACY.md#accounting-and-processor-boundaries), [forbidden defaults](../PRIVACY.md#forbidden-collection-and-defaults), [telemetry failure behavior](../PRIVACY.md#export-retention-and-failure-behavior), [required evidence](../PRIVACY.md#required-acceptance-evidence); gateway [data boundaries](../../possums/PRIVACY.md#data-handling-boundaries), [forbidden telemetry](../../possums/PRIVACY.md#telemetry-permitted-signals-and-forbidden-data), [aggregation](../../possums/PRIVACY.md#aggregation-is-necessary-not-sufficient) and [verification](../../possums/PRIVACY.md#required-evidence-for-every-telemetry-change).

Tests use isolated synthetic accounts, credentials, errors and databases. Service subprocess tests and curl smoke force export off and remove inherited credentials/configuration. Wire tests use test-only loopback capture, including actual supported protobuf serialization, TLS/redirect/error/shutdown checks; no Honeycomb traffic was authorized or used. Tests check actual HTTP, process output and framework auxiliary-state boundaries, not only error formatters. Necessary accounting/WAL state is persistence, not telemetry or secure erasure. Detailed development failures remain governed by the existing curated-log policy; native host history is a separate boundary.

Exactly-once observations cover only custom accounting routes after local final-response classification; write/flush failure, cancellation and panic remain unknown. A successful local write/flush is not a peer receipt or proof that the requested financial effect won. The first authoritative terminal reservation state remains decisive.

## Remaining gates — local acceptance is not live authorization

- **Independent release reproduction:** actual local image qualification passed; fresh independent native Linux A/B builds, signed exact-main provenance and registry digest preservation remain unrun. A cached local build is not independent A/B evidence.
- **Independent review:** completed by Luna after full-body inspection of the specified accounting/HTTP/telemetry/lifecycle/smoke and disabled delivery boundaries. It reported no actionable defects and independently passed tests, race checks, vet, checksum verification, offline delivery tests and compiled curl smoke. Its source review did not establish image/platform/live properties; subsequent image evidence above is separately parent-verified.
- **SDK internal defaults:** the OTel SDK evaluates/caches default resources despite an explicit resource. Actual outbound allowlisting passed; literal absence of all internal discovery remains unqualified. No SDK fork or global environment mutation was introduced.
- **Prior release:** none exists, so previous-release compatibility is not testable yet. Restart/v1 migration rollback and incompatible-state refusal do not prove upgrade compatibility.
- **Runtime/platform:** power-loss/fsync durability, encrypted-volume original-key unlock/rollback, attested transport/operator access, platform/processor logging and retention remain separate unqualified real-data gates.
- **Delivery:** hard-disabled local templates/fake-port algorithms are not authenticated live adapters or an activated release pipeline. No operator target, volume/key policy, registry or protected remote environment has been selected by local tests.

Local successes authorize neither a copied spendable ledger, database restore, silent initialization after storage loss nor gateway work. Passing the documented synthetic local gates permits proposing a separately authorized integration packet, not starting gateway, attestation/key-delivery, provisioning, export or deployment work.
