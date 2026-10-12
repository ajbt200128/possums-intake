# Intake privacy and telemetry requirements

**Engineering policy, not deployment evidence.** The standalone service is implemented and synthetically verified locally as recorded in [docs/verification.md](docs/verification.md); it is not provisioned or approved for real-data use. The operator selected Go/PocketBase on a separate Tinfoil instance, accepted no scheduled backups and brief replacement downtime, and requested OpenTelemetry/Honeycomb under the gateway's privacy policy. Stripe and Monero remain future scope. No credentials, destination changes, dashboard creation, export, or production integration are authorized by this document.

## Authority and scope

Read this file before changing instrumentation, logging, tracing, SDK defaults, metrics, collectors, diagnostics, dashboards or deployment. The sibling gateway's [PRIVACY.md](../possums/PRIVACY.md) remains authoritative, particularly [data boundaries](../possums/PRIVACY.md#data-handling-boundaries), [forbidden telemetry](../possums/PRIVACY.md#telemetry-permitted-signals-and-forbidden-data), [aggregation](../possums/PRIVACY.md#aggregation-is-necessary-not-sufficient), [processor/retention controls](../possums/PRIVACY.md#processors-retention-access-and-shutdown) and [verification](../possums/PRIVACY.md#required-evidence-for-every-telemetry-change). Preserve the stricter requirement if documents conflict. The gateway's live evidence is not evidence for this Go service.

These restrictions apply to real data in every environment. A Honeycomb destination named test/dev is not evidence that traffic is synthetic. Synthetic testing requires isolated fake accounts, credentials and payment fixtures, with no real customer/provider data.

## Accounting and processor boundaries

- Persist only account totals, necessary reservations and bounded idempotency/terminal records described in [PLAN.md](PLAN.md). Never receive prompts, responses, tools, conversation history, inference model/token metadata or client recovery credentials.
- Accounting JSON parsers ignore additive unknown fields for compatibility, without binding/persisting/echoing/exporting them. A malformed or newer caller may supply extras transiently within the bounded body; ignoring them is not permission for the gateway to send content, credentials or unnecessary request metadata. Known fields alone enter accounting. Native mutation and telemetry allowlists remain closed.
- Necessary accounting is not telemetry. Account-scoped balance responses are first-party data; they are not support artifacts or observability exports.
- The encrypted disk/WAL/deleted pages and optional manually downloaded snapshots are persistence boundaries. Deleting a row is not secure erasure. No scheduled backups or external backup destination by default.
- Optional manual snapshots contain sensitive accounting and potentially PocketBase authentication/settings state. Downloading moves that state outside the enclave. No automatic uploads, support bundles or routine financial restore claims.
- Tinfoil controls infrastructure/networking and can observe metadata and disk access patterns. Customer-key-encrypted volumes are beta and do not detect rollback of old valid state. Attestation exposure does not establish verification, freshness, storage unlock, absence of retention or immunity to legal process.
- Future Stripe processing introduces provider/payment metadata outside the enclave. Monero introduces wallet/key, confirmation and chain-observation boundaries. Define and approve those retention/replay requirements separately; do not collect payment history now in anticipation.

## First telemetry scope: metrics only

Use supported Go OpenTelemetry metrics SDK/exporter libraries with explicit, closed observations and a bounded in-memory release policy. Do not copy gateway Rust code or reimplement OTLP. Export directly over authenticated, hostname/certificate-verified OTLP HTTP/protobuf TLS to Honeycomb US; no Collector, agent, external platform reader or log bridge.

Proposed minimal first signals:

- Intake-process CPU cores used and approximate RSS bytes: complete one-minute means of six ten-second numeric samples, following the gateway's cadence. Distinguish process RSS from Go heap and enclave allocation; do not fabricate container/host scope or utilization percentages.
- Custom accounting HTTP terminal counts, coarse status classes and fixed latency-bucket counts: complete non-overlapping five-minute windows. Define one matched terminal population; no per-request export, exact latency sums/min/max, exemplars or trace links. Health, attestation and operator-admin requests are excluded from this initial request family.
- Resource fields limited to fixed `service.name=possums-intake`, an honest deployment environment and an operator-assigned infrastructure slot such as `intake-01`. No automatic host/process/cloud/resource discovery, instance UUIDs or request-derived attributes. No model/payment-provider/endpoint cross-breakdowns in the first pass.

Pin exact metric names, units, bucket boundaries and complete-window/lifecycle/arithmetic checks during implementation. Incomplete, inconsistent or unavailable values are unavailable, not zero or healthy. Carry over the gateway's complete five-minute policy with no minimum-observation or linked-sparsity threshold; do not invent a new suppression framework. Sparse aggregates can describe individual activity and correlate with external timing. This is minimization, not ORAM, anonymity or differential privacy.

## Forbidden collection and defaults

No production logs, access/request records, request-level traces, automatic HTTP/SQL instrumentation, profiling, session replay or trace-to-metrics pipeline. Sampling is not sanitization.

Never put the following into telemetry, exporter diagnostics, logs or support artifacts:

- Account/customer IDs, reservation/idempotency/submission/event/transaction IDs, stable pseudonyms or hashes of identifiers.
- Balances, credit/reservation/settlement/payment amounts, money flows, token/cost events or per-account totals/counts.
- Payment payloads, Stripe customer/payment data, Monero addresses/payment identifiers, wallet or chain records.
- Credentials, superuser tokens, encryption/wallet keys, headers/cookies, URLs/query strings, bodies, client IPs or user agents.
- Exact request timestamps/durations/sizes, SQL text/parameters, raw SDK/parser/database/upstream exceptions, stack traces, panic payloads, environment values or process arguments.

PocketBase's logging/error defaults and built-in admin APIs need actual qualification; being inside an enclave does not permit creating an activity log. Native backup download URLs contain file tokens and some backup error paths echo raw exceptions: do not expose those unchanged. Keep useful closed diagnostics transient and content-free; do not turn them into telemetry labels or automatic bundles.

## Export, retention and failure behavior

Apply a final allowlist in-process before supported SDK serialization/export. Do not rely on vendor-side deletion or unrestricted inherited OTel configuration. Permit only the fixed Honeycomb US endpoint and reviewed ingestion header; credentials are runtime secrets, never metric fields. Refuse redirects. Keep batching/state and transport attempts bounded; no disk spool, unbounded backlog or automatic replay of failed metric batches.

Follow the gateway's enablement model: valid allowlisted configuration/ingestion credentials enable the reviewed exporter; missing/invalid configuration disables telemetry without failing accounting. `OTEL_SDK_DISABLED=true` is the startup off switch. No hot-toggle framework or one-second shutdown guarantee. Drop partial windows and pending batches on restart/shutdown; do not flush incomplete data.

Telemetry outages, sanitizer/SDK errors and disabled export must not change balances, reservation expiry, credits, retries, readiness or settlement outcomes. Never perform export while holding an accounting transaction. Do not detach secret/payload-bearing export work past shutdown. Standard DNS completion/whole-process exit timing remains an external runtime limitation, not a reason to invent a custom resolver.

Honeycomb is outside the enclave boundary. Apply the gateway's provider-default retention acceptance for the MVP, recording the actual destination, key permissions and effective retention when separately verified. Seven days is a future goal, not a current guarantee. Do not assume the gateway's existing key/dataset grants intake access or reuse them without authorization. Restrict operator access; no public boards, secondary archives or support exports. Platform observability and transport/audit retention remain separate unknowns; the off switch disables our exporter, not Tinfoil's collection.

## Development agent failure records

The operator authorizes a manually curated **external, local-only** development failure record at `/Users/mb5/projects/possums-intake-agent-failures.md`, outside the repository. This is the only external file this exception permits writing; no arbitrary sibling files. Preserve the content-free fields described in [docs/agent-failures.md](docs/agent-failures.md): configured model, local packet/attempt/coarse date, public source/dependency versions, locally authored expected/observed behavior, known safe diagnostic code/stage/constraint/HTTP status, source location, named synthetic tests and credential-free reproduction commands, reviewed partial-work state and disposition. Record unavailable/unknown rather than guess a cause or billing result.

Only that external development log may additionally contain development task/thread/run IDs and verified pointers to existing native T3/Pi transcripts on disk. Verify the native task-to-storage mapping and file existence; a guessed directory or filename is not evidence. If storage is database-backed, identify the verified database/native reference rather than inventing a standalone transcript. If storage cannot be located, record that limit and use the verified native task reference. Repository files must contain no development task/thread/run IDs, transcript locations or failure entries; they may reference the external log's purpose and location.

Never copy transcripts, prompts, responses, raw exceptions, full tool output, secrets, customer data, headers, URLs, provider/gateway request/account/payment/session identifiers, exact request timestamps, monetary values or telemetry payloads into the log. The development task/thread/run exception does not permit inference/customer session identifiers. A live dogfood inference is not synthetic merely because it implements synthetic ledger tests. Native T3/Pi history remains a separate host-controlled persistence boundary; pointers do not duplicate it or establish whole-host transience. This log is manually authored after inspection, not an application logger, automatic trace/bundle, telemetry destination or support export. Keep it local and outside version control; sharing or export requires separate authorization.

Recording or inspecting a failure initiates no inference, retry, telemetry export or gateway changes and changes no accounting/retry policy. Confirm old work is terminal before fallback to avoid duplicate active edits; inspect partial changes and verify any fix. An authorized subsequent attempt can incur another charge when an earlier outcome is unknown. The operator authorizes migration of existing sanitized entries into this external log, with available verified native references, only after the revised instructions are active. Creating the template itself launches no agents or inference. All production privacy restrictions and safe diagnostic requirements remain unchanged.

## Required acceptance evidence

Before real export, document the signal scope and assumptions, then inspect actual synthetic wire payloads and runtime behavior:

- Inject fake sensitive account/payment/credential/error/header/URL/SQL values; none may survive in export or diagnostics.
- Exercise complete sparse windows, duplicate/missing terminals, exact bucket counts, restart/partial-window loss and bounded state.
- Test disabled/missing/invalid config, hostile SDK/exporter errors, failed export and shutdown races; accounting remains correct and telemetry initiates no monetary work.
- Verify process numeric-source scope, generated-image/config behavior, fixed verified TLS destination, credentials/attributes separation and actual Honeycomb metric schema/retention under separate authorization.
- Record local/offline versus installed/deployed evidence in a short verification record linking the applicable policy sections. No tests or live telemetry have passed merely because this policy exists.
