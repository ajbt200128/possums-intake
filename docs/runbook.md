# Synthetic local ledger runbook

This is a disposable CLI/curl check, **not a production administration tool**.
Follow [PLAN.md](../PLAN.md#concrete-local-execution-contract),
[intake privacy](../PRIVACY.md#authority-and-scope),
[forbidden collection](../PRIVACY.md#forbidden-collection-and-defaults),
[development-agent boundaries](../PRIVACY.md#development-agent-failure-records)
and the gateway's [synthetic-testing exception](../../possums/PRIVACY.md#synthetic-testing-exception).
No real accounts, payment data, inference, gateway traffic, Honeycomb export,
provisioning, publication or deployment is authorized here.

## Prerequisites and commands

Run from this checkout. Use the existing pinned devenv.sh **local** development
shell; no installation, direnv approval or production platform setup is implied.
Python 3 and curl must be available. Confirm Go **1.27.2**:

`devenv shell -- go version`

Run the offline/synthetic harness checks (including actual curl against a local
fixture, config/body escaping, missing auth, environment isolation, output caps,
deadline handling and forced-shutdown rejection):

`python3 scripts/smoke.py --self-test`

Run the Go suite with telemetry disabled:

`devenv shell -- env OTEL_SDK_DISABLED=true go test ./... -count=1`

Create a temporary binary directory, compile the actual CLI, and run smoke.
These commands use the same shell; do not replace the temporary path with a
production state path or inject an operator credential:

`smoke_build="$(mktemp -d "${TMPDIR:-/tmp}/possums-intake-build.XXXXXX")"`

`devenv shell -- go build -trimpath -o "$smoke_build/intake" ./cmd/intake`

`python3 scripts/smoke.py --binary "$smoke_build/intake"`

After the runner has returned and joined its service, remove the temporary binary:

`rm -f -- "$smoke_build/intake"; rmdir -- "$smoke_build"; unset smoke_build`

Do not run smoke if compilation fails. Concurrent source edits invalidate a
final acceptance claim: label results **pending** and let the integration owner
verify a stable source snapshot rather than racing edits or silently using an
old executable.

## What the runner does

- Accepts only a compiled binary path or `--self-test`; no operator URL, state,
  key or funding inputs. It creates fresh temporary state and explicitly calls
  `init`, then `serve` on fixed numeric loopback `127.0.0.1` and an ephemeral port.
  Initialization grants no balance. It never opens existing ledger state.
- Uses a fresh high-entropy synthetic key only in the service's runtime
  `LEDGER_ADMIN_KEY` environment. Child environments exclude inherited real
  admin/metrics credentials, OTel configuration and proxy settings; telemetry is
  forced off with `OTEL_SDK_DISABLED=true`.
- Drives readiness and every RPC with actual curl. `--disable` is the first curl
  option, `--noproxy '*'` bypasses proxies, and credentials and escaped JSON data
  share a stdin config (not competing stdin readers). The key is never placed in
  curl arguments, environment or files. Health/missing-key probes omit auth.
  No redirects, mutation retries or external targets; total curl RPC budget is
  five seconds. Nonzero curl exits are failures, not HTTP/accounting receipts.
- Checks public fixed health; absent/wrong auth on all seven routes; zero
  provision and non-resetting reprovision; keyed credit duplicate/altered
  conflict; immutable reserve retry/timeout; complete ordered scoped balance
  lists with every pending-row field, key and valid fixed UTC expiry; settlement
  duplicate/conflicting charge/refund first-outcome behavior; explicit refund;
  insufficient credit; one-second expiry with authoritative late-settlement
  evidence; retained lookup; absence of native installer/dashboard/API routes.
- Stops and joins the service, restarts the **same original synthetic state**,
  and verifies balance, active hold, immutable reserve identity and original
  credit result. It never re-initializes or copies spendable state on restart.
- Continuously drains child pipes, retaining at most 64 KiB per pipe and
  discarding excess while failing the cap gate. Raw output/bodies are never
  printed or saved. Each response is checked immediately for key disclosure,
  not accumulated as a response history. Normal service output must be empty.
- Requires joined zero-exit normal service shutdown before deleting disposable
  state. Forced termination is a failed shutdown gate with unknown in-flight
  outcome, **never billing/cancellation confirmation**. Unconfirmed joining
  preserves the temporary state. Cleanup failures also prevent a pass.

## Results and limits

Success prints `PASS smoke:all synthetic_cli_curl_restart_normal_shutdown` only
once normal final shutdown and cleanup pass. A failure prints a content-free
`SMOKE_CHECK` with stage/constraint and exits nonzero; no raw exceptions,
requests, keys or subprocess output. Inspect the named synthetic gate privately.
Do not automatically replay inference or infer a monetary outcome from a
transport/termination failure. There is no inference or provider retry here.

Historical packet observation: Go 1.27.2, the Go suite, compilation and
compiled CLI/curl smoke passed, but source was changing during that interval.
The parent subsequently reran the final runner against a stable build and
passed it independently, together with the full race suite and actual
image gates. See [current scoped evidence](verification.md). Seven harness
checks passed after runner hardening. None of these local results establishes
installed/deployed behavior or production readiness.

Smoke is a bounded subset of local acceptance, not the entire PLAN test matrix.
Concurrency/lock/commit-loss faults, clock/pruning boundaries, migrations and
prior-release compatibility, auxiliary database privacy, serialized metrics wire
checks and generated-image readiness require the integration owner's separate
suite/evidence. Missing-state refusal is covered by the Go startup tests, not by
opening a real disk here: ordinary `serve` must refuse missing/partial state and
must never silently bootstrap it. No filesystem secure-erasure guarantee is made.

Real-data storage-key unlock, attestation/verified transport, operator access,
platform logging/retention and durability remain separate gates. Local passing
checks authorize neither gateway integration nor cloud operations. No production
admin commands, financial restore, backup download or reset procedure is provided.
