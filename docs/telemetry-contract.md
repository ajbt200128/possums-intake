# Packet 4: bounded metrics contract and local evidence

## Scope and authority

This implements only `internal/telemetry/*`, pinned metrics/process dependencies in `go.mod`/`go.sum`, and this contract. It does **not** install middleware or change `cmd/intake`, `internal/httpapi`, `internal/ledger`, readiness, accounting, or retries. No live Honeycomb constructor/export was invoked in qualification, no real credentials/data were used, and no resources, gateway changes, commits, or additional agents were authorized/performed by this packet.

Applicable policy: [intake first metrics scope](../PRIVACY.md#first-telemetry-scope-metrics-only), [forbidden defaults](../PRIVACY.md#forbidden-collection-and-defaults), [failure/retention behavior](../PRIVACY.md#export-retention-and-failure-behavior), [required evidence](../PRIVACY.md#required-acceptance-evidence), and gateway [data boundaries](../../possums/PRIVACY.md#data-handling-boundaries), [forbidden telemetry](../../possums/PRIVACY.md#telemetry-permitted-signals-and-forbidden-data), [aggregation](../../possums/PRIVACY.md#aggregation-is-necessary-not-sufficient), [processor/shutdown](../../possums/PRIVACY.md#processors-retention-access-and-shutdown), and [verification](../../possums/PRIVACY.md#required-evidence-for-every-telemetry-change). Repository metrics-only restrictions override the Honeycomb instrumentation skill's traces/logs/auto-instrumentation/Collector/live-canary suggestions.

Dependencies: OpenTelemetry Go API/metrics SDK and supported `otlpmetrichttp` exporter **v1.44.0**; `github.com/shirou/gopsutil/v4` **v4.26.9**; synthetic protobuf decoder `go.opentelemetry.io/proto/otlp` **v1.10.0**, protobuf **v1.36.11**. All selected checksums are in `go.sum`. Existing PocketBase **v0.40.5**, dbx **v1.12.0**, SQLite **v1.60.1**, Go **1.27**, and toolchain directive **go1.27.1** were preserved. The existing devenv actually supplies **go1.27.2**; this packet did not change its pin. Indirect trace/auto SDK modules are upstream dependencies, not an installed tracer or application auto-instrumentation.

## Integration API (parent-owned integration remains pending)

`telemetry.New(ctx context.Context) *telemetry.Runtime` reads startup configuration once, returns a disabled runtime on missing/invalid config or SDK construction failure, and owns an independent heartbeat/process sampling loop plus one sender. Construction itself sends no request. **Valid real configuration can subsequently export complete windows; do not supply it during standalone-local acceptance.** There is no endpoint override or capture constructor in the runtime API.

`(*Runtime).State() State` returns only a content-free **startup** category, not a live vendor health check and not evidence of delivery:

- `telemetry_enabled`: reviewed config accepted and SDK constructed; delivery is not established.
- `telemetry_disabled`: no config or the off switch; no SDK, sampler, or sender is created.
- `telemetry_config_invalid`: incomplete/non-allowlisted startup config; telemetry alone remains off. Review only the documented config names/constraints privately, never paste rejected values.
- `telemetry_sdk_unavailable`: construction failed; underlying cause is unknown and no export outcome is established. Check the pinned build and rerun synthetic package tests; do not report raw SDK errors or retry accounting to diagnose it.

`(*Runtime).ObserveTerminal(class StatusClass, duration time.Duration)` accepts only the numeric enum `Unknown`, `Status2xx`, `Status4xx`, `Status5xx` and a nonnegative transient elapsed duration. It converts that duration immediately into one exclusive bucket; no individual duration or terminal timestamp is retained after the hook. The clock read is used only to assign/close the aligned window. Exact timestamp state belongs to independent heartbeat/process sampling, not request records. Invalid enum/duration withholds the request window. No HTTP request, context, attributes, identifiers, raw errors, endpoint strings, or accounting callback is accepted.

**The integration must invoke this exactly once, outside transactions, only for the seven custom accounting routes, including their auth/schema failures.** Exclude health, native/admin/default routes. Do not infer success from a default status or middleware return. Interrupted/unknown terminal delivery uses `Unknown`; HTTP classes do not prove financial effects. There is intentionally no request-ID registry or auto-instrumentation.

`(*Runtime).InvalidateRequests()` withholds the current request window if integration detects a missing/duplicate terminal or other lifecycle-integrity failure. Because the narrow hook has no request identity, **the package cannot independently detect duplicate/missing middleware calls**. Parent middleware must establish exactly-once final classification (e.g. request-local once guard), and call invalidation on known lost/duplicate lifecycle evidence. It must not fabricate a zero-duration sample when latency is unavailable.

`(*Runtime).Shutdown()` cancels and **joins** owned work; it is safe concurrently/repeatedly. It drops pending batches and partial windows, never invokes exporter ForceFlush and never performs a final collection. Parent-context cancellation also closes observation hooks and discards partial/pending state. No secret-bearing sender continues after Shutdown returns. This API is deliberately blocking: export attempts have a two-second context/client timeout, but standard DNS and OS numeric methods have no hard whole-process completion guarantee. Do not abandon Shutdown in a detached goroutine and claim shutdown has completed. There is no hot toggle/flush/diagnostic-triggered work API.

## Exact release schema

All four instruments are **gauges**, not cumulative counters or an OTLP histogram:

- `possums.intake.http.requests`: signed integer gauge, unit `{request}`, solely `status_class` = `2xx`, `4xx`, `5xx`, `unknown`.
- `possums.intake.http.latency.bucket`: signed integer gauge, unit `{request}`, the same `status_class` plus `latency_bin`, exactly `[0,10)`, `[10,50)`, `[50,100)`, `[100,250)`, `[250,500)`, `[500,1000)`, `[1000,2500)`, `[2500,5000)`, `[5000,infinity)` in milliseconds. Bins are **exclusive**, not cumulative. A 10 ms duration belongs only to `[10,50)`; 5 seconds belongs only to the last bin.
- `possums.intake.process.cpu`: floating gauge, unit `{cpu}`, no point attributes; arithmetic mean of six process CPU-core rates.
- `possums.intake.process.rss`: floating gauge, unit `By`, no point attributes; arithmetic mean of six current approximate process RSS byte samples.

Request families release together: four class counts and 36 bucket counts for a **complete aligned UTC five-minute window**, including zero cells. Each class count must equal its nine-bin sum; the sum across classes must be representable. Integer cells and total are limited to **2^53−1**, preserving exact integer encoding and downstream numeric representability. Overflow, malformed cells, or mismatch withhold the whole family; counters cannot wrap.

A one-second independent heartbeat certifies quiet request windows. Heartbeat gaps exceeding two seconds, boundary closure later than one second, backwards clocks, skipped windows, and partial startup/restart windows withhold the affected window. No catch-up zeros are manufactured. A subsequent complete observed window can release again. These tolerances accommodate scheduling only; they do not permit overlapping or partial window timestamps. Samples are assigned under a short local mutex, never a financial writer gate.

Process sampling is independently scheduled at ten-second UTC boundaries. A baseline plus six numeric samples (at :10 through :60) yields the minute ending at :60; that last sample is also the next minute's baseline. Startup lacks a qualified boundary baseline, so the startup minute is withheld. All six samples must arrive between their boundary and one second afterward, with actual adjacent elapsed intervals between 9 and 11 seconds. CPU is the delta of cumulative **current-process user+system CPU seconds**, divided by actual elapsed seconds, then six core rates are averaged; it is not normalized by host CPU count. RSS is current `MemoryInfo.RSS`, not maximum RSS, heap, cgroup/enclave allocation or host usage. Only gopsutil's supported `NewProcessWithContext`, `TimesWithContext`, and `MemoryInfoWithContext` APIs are used; PID/metadata are never exported. The library's ambient HOST_* path overrides are replaced with fixed normal OS roots through its supported context EnvMap.

NaN/infinity, negative/reset CPU, rates above the representational safety limit **1024 cores**, zero/unrepresentable RSS (above 2^53−1 bytes), failed/duplicate/missed/late readings, or invalid alignment withhold the process minute. This is a value safety limit, not measured capacity or the planned enclave shape. Process failure does not suppress a separately complete request family. No raw reading, exact latency sum/min/max, last-event time, fine-grained suppression counter, or admission capacity metric is released.

Only aligned window start/end timestamps enter data points. Restart/shutdown drop partial windows and pending batches; there is no disk persistence, replay, or aggregation carried across lifetimes. Complete sparse counts are allowed and **remain correlatable**, not anonymous or k-anonymous.

## Resource, scope, and final sanitizer

Resource attributes are exactly:

- `service.name=possums-intake` (compiled constant).
- `deployment.environment.name=development|production` (closed startup choice).
- `possums.infrastructure.slot=intake-NN` (exactly two decimal digits, operator-assigned fixed infrastructure slot).

A slot must not encode a customer, dedicated customer deployment, hostname/IP, or user/session identity. Syntax checks cannot establish that semantic promise; the operator must qualify assignment. No other resource attributes or resource schema URL survive. Scope is exactly name `possums-intake/telemetry`, version `1`, empty schema URL/attributes. Metric descriptions/metadata are empty. There is no resource discovery requested by this package and no global provider registration, logger, error handler override, tracing or exemplars.

Private manual collection uses a supported OTel MeterProvider/ManualReader with fixed observable gauges, explicit resource, always-off exemplar filter, and bounded cardinality (40). Only the sender can collect. Immediately before supported exporter serialization, the final sanitizer validates complete-window source arithmetic, resources, scope, types, names/units, every point's closed attributes, distinct complete cell sets, finite/ranged numeric values and agreement with the released batch. It reconstructs clean scalar gauges with only fixed resource/scope/attributes and aggregate boundaries. Unknown metrics/attributes/types, missing/duplicate cells, or invalid scalar values reject the batch. Extra resource metadata, descriptions, exemplars and SDK observation times are discarded rather than copied. The ingestion credential is not retained in collection configuration or sanitizer state.

**Upstream SDK boundary to review:** v1.44.0 `sdk/metric/config.go:newConfig` unconditionally evaluates `resource.Default()`, and `WithResource` evaluates/merges `resource.Environment()` before applying explicit attributes. Its default resource includes SDK fields and a fallback service name derived from the executable, and is internally cached; this packet cannot disable those internal evaluations via the supported provider API. No process/host/cloud detector is requested. Runtime config rejects every ambient `OTEL_*` variable except the off switch before SDK construction; explicit fixed resources and the final sanitizer prevent unused defaults from reaching metrics/wire. Actual synthetic bytes prove the **export** allowlist, not literal absence of every SDK-internal default-resource evaluation. This is an explicit remaining strict-policy interpretation boundary, not a claim that SDK internals perform zero discovery. Do not mutate global environment/resources or fork the SDK to hide it. Runtime environment mutation between validation and SDK construction is not an authorized configuration path and needs separate qualification if a future host introduces it.

## Closed startup environment schema

Only these configuration variables are recognized:

- `OTEL_SDK_DISABLED=true`: unconditional off switch (true accepted case-insensitively). Empty or exact `false` allows validation to proceed; other nonempty values disable as invalid.
- `INTAKE_METRICS_ENDPOINT`: must be **exactly** `https://api.honeycomb.io/v1/metrics`. No alternate host, port, path, scheme, userinfo, query or fragment.
- `INTAKE_METRICS_ENVIRONMENT`: exact `development` or `production`.
- `INTAKE_METRICS_SLOT`: `^intake-[0-9]{2}$` with the semantic operator restriction above.
- `INTAKE_METRICS_HONEYCOMB_API_KEY`: runtime secret matching `^[A-Za-z0-9_-]{16,128}$`, sent only as `x-honeycomb-team`. This checks bounded header syntax, not remote validity/permissions. No dataset header or reuse of gateway credentials.

All four INTAKE_METRICS fields valid enable; all absent/empty disable; incomplete/invalid config disables telemetry alone. **Any ambient `OTEL_*` name other than `OTEL_SDK_DISABLED`, even empty, disables as invalid**, including endpoints, headers, resource/service values, exemplar config and SDK self-observability. Name inspection retains/reports no rejected values. No OTEL environment overrides are imported. Qualification uses disabled config or a numeric-loopback constructor defined only in `_test.go`, never a runtime local-endpoint flag. Keep `OTEL_SDK_DISABLED=true` in local/full-service acceptance.

## Transport, boundedness, and failure behavior

Production destination is compiled Honeycomb US; supported HTTP/protobuf serializer/exporter, no Collector or custom OTLP. Explicit exporter options fix endpoint/path, sole ingestion header, no compression, two-second timeout, 16 KiB request limit, supported aggregation/temporality selectors, and automatic retry disabled. Private standard HTTP transport has no environment proxy, verified system-root TLS with `api.honeycomb.io` hostname and TLS 1.2 minimum, one connection at most, disabled connection reuse/HTTP2, and standard net.Dialer/DNS. Redirects are refused, and the transport strips the exporter's GetBody handle to prevent net/http's transparent replay of a body-bearing POST.

There is one in-flight export and **one pending batch**. A full queue or failed attempt drops data; no retry, disk spool, unbounded channel, detached sender or failed-batch replay. A batch has at most 42 scalar points; active aggregation is four counts/36 bins plus one process minute. Supported SDK callbacks are private/synchronous and always return nil. Direct Export/Shutdown errors (including hostile response text, parser errors and partial-success messages) are discarded transiently without raw stderr/stdout, diagnostics, storage, or labels; there is no periodic reader/global error-handler route for them. No fine-grained export loss/error metric is added. Resource/callback constraints must remain closed if SDK versions change.

Standard DNS resolution, OS numeric collection, dependency internals and native host history are separate boundaries. Export contexts/client deadlines are not a hard OS resolver cancellation or whole-process exit guarantee. Honeycomb retention, key scope, processor transport/audit metadata and Tinfoil collection are **unverified** here. Provider-default retention is accepted by policy, not measured; seven days is only a future goal. The off switch affects this package, not platform observability. No generated-image/full-service readiness or Linux runtime qualification is claimed.

## Local synthetic evidence

Passed on the current devenv Go 1.27.2 host:

`devenv shell -- go test ./internal/telemetry`

`devenv shell -- go test -race -count=3 ./internal/telemetry`

`devenv shell -- go vet ./internal/telemetry`

`devenv shell -- go mod verify`

Compile-only Linux qualification (the test executable is **not run**; `/usr/bin/true` is the exec shim):

`devenv shell -- env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -exec /usr/bin/true ./internal/telemetry`

`devenv shell -- env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -exec /usr/bin/true ./internal/telemetry`

The initial Linux cross-compile with the host's default CGO failed at the platform C-toolchain boundary: the available compiler targets Darwin, not Linux. No cross-compiler was installed and no runtime source fix was inferred. Explicit pure-Go compile checks above passed; neither establishes Linux current-process behavior, DNS completion, image readiness or actual Honeycomb schema/ingestion.

Focused evidence:

- `windows_test.go`: sparse/zero full request windows, all exact exclusive bin edges/classes, startup/restart/late/missed/backwards time, no catch-up windows, checked overflow/matched populations; six-sample process means, consecutive minute baselines, missing/duplicate/late/failed/NaN/Inf/reset/range readings, fresh warmup after large jumps.
- `sdk_test.go`: independent SDK families do not retain stale gauges; final gate drops hostile resource/scope/descriptions/exemplars/times and rejects unknown names/units/point attributes/enums, missing/duplicate cells/families, wrong values/types/nonfinite numbers and invalid resources/source arithmetic.
- `runtime_test.go` and `lifecycle_test.go`: real constructor off/invalid ambient config does no work; silent hostile construction/Export/Shutdown errors; queue bounded to one pending; timeout/outage no replay; concurrent observations/invalidation/shutdown join; parent cancellation and partial shutdown loss; synthetic lost/duplicate lifecycle invalidation; independent failed-process versus valid request release; no retained individual timestamp/collection credential; current-host numeric process source ignores HOST_* overrides; source import checks exclude accounting and auto-instrumentation.
- `wire_test.go`: supported serializer emits actual synthetic HTTP/protobuf bytes through loopback TLS, decoded into OTLP protobuf and asserted for exact four gauge names/units/types, 42 points, closed dimensions, exact aggregate boundaries and no forbidden sentinels/credentials/free metadata. Captured header verifies authentication is separate from data. Supported 307 redirect, 429/503 errors, hostile partial-success protobuf and interrupted TCP each make exactly one origin attempt with no forwarding/replay or raw stdout/stderr. Untrusted certificates and hostname mismatch prevent HTTP payload delivery. Test servers suppress their own synthetic handshake diagnostics; production transport is not weakened.

Remaining parent gates: exact-once final-outcome middleware and its actual HTTP privacy tests; standalone service/accounting acceptance; generated image/Linux runtime process/DNS/logging/shutdown qualification; SDK-internal default-resource interpretation; operator slot assignment and later explicitly authorized Honeycomb credentials/schema/retention/real-data export. No successful full-service acceptance or permission to integrate/deploy follows from these package checks.
