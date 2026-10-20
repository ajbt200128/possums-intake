# ELPS hosting qualification — local source evidence

The operator authorized focused local implementation of [the website plan](lisp-website-plan.md), superseding its planning-only status, **not** its stop gates. The original probes below are a nested test module, not production acceptance. The subsequent root `internal/website` fixture and root dependency changes are recorded in [current implementation evidence](lisp-website-evidence.md); statements below about unchanged root dependencies describe the original probe packet only. Ordinary serving, accounting, native authentication and logging-off behavior remain unchanged. No production schema, durable sessions, website publication or customer provisioning is implemented.

## Exact candidate

Public upstream commit **`4aa96a60cafbf73ec5c399a47a80753c25dbfd91`**, resolved by Go as **`v1.79.2-0.20261019044916-4aa96a60cafb`**. Module checksum: `h1:jXp2gICzNduHMEXkgvbulQoxllfpmODbs9Kz2HNyPM4=`. The candidate is a commit pin, not a claim of a tagged release. Its go.mod requests Go 1.26.0/toolchain 1.26.9; the existing intake Nix/devenv Go **1.27.2 darwin/arm64** executes these tests with `GOTOOLCHAIN=local`.

Reviewed versioned contracts/implementation: upstream `docs/templates.md`, `docs/embed.md` context/step sections, `lisp/template.go`, `lisp/template_runtime.go`, `lisp/config.go`, relevant `lisp/env.go` initialization/entry points, `parser/parser.go`, and upstream template/context test fixtures. No assumption that an earlier ELPS release provides these APIs. Dependencies are acquired only for `qualification/elps`; root go.mod/go.sum and image vendor identity are unchanged.

## Passing synthetic probes

`qualification/elps/hosting_test.go` exercises supported embedding APIs:

- Prepare a quiescent source once with `NewTemplate`; instantiate fresh `NewVM` instances and resolve entry names **in each instance**. No retained source function executes a request.
- Sequential and concurrent map mutation and lexical mutable closure counters start at defaults. Mutating source after preparation does not alter future instances.
- Overlapping mutable cell/byte views retain local aliases while being independent of source and concurrent instances.
- Actual long-running tail recursion and empty-body `dotimes` terminate on context deadline, after at least 100 evaluation steps. Another VM returns normally during the operation and afterward. Tests receive completion, not merely a timeout notification.
- Shared per-VM step-budget exhaustion; context cancellation during initialization evaluation; malformed/deep parser input, allocation-size and evaluation-nesting refusal.
- Request callbacks installed **after** instantiation obtain fresh mutable services only from `env.Context()`. Independent counters remain independent under concurrent execution. A cooperating blocking callback observes cancellation, executes its deferred cleanup and returns before the test proceeds.
- An application callback capturing mutable Go state is refused by the fixed registration identity policy. A mutable native Go map is refused without a native sharing policy. A clean template remains usable after candidate refusal.

All test failures are locally authored content-free diagnostics; raw interpreter values/errors/stacks are not formatted. Interpreter stderr is discarded. The two-second test bounds detect broken probes; they are **not** a proposed production timeout/abandoned-goroutine substitute. The disposable Go test process has a separate 45-second bound. No request evaluator/service worker is added to intake.

## Commands and scope

From the repository root:

`cd qualification/elps && env GOWORK=off GOTOOLCHAIN=local GOPATH="$PWD/../../.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true ../../.devenv/profile/bin/go test -race -tags elpscheck -count=10 -timeout=45s ./...`

`cd qualification/elps && env GOWORK=off GOTOOLCHAIN=local GOPATH="$PWD/../../.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true ../../.devenv/profile/bin/go vet ./...`

Both passed; module verification also passed. An active LSP probe of both new Go test files reported no errors (compiler/race/vet checks remain the authoritative evidence). The executable is the existing `.devenv/profile` symlink to the pinned Nix store toolchain; this does not source private `.env` configuration. One earlier race/checked run also passed before expanding the probes. There was no project-toolchain failure.

## Original probe limits / not authorized by those results

**This is partial hosting evidence, not a passed production-hosting gate.** The fixture approves exact core registration identities captured before application code, not an approve-all predicate. That census intentionally is **not** the required production capability/callback audit. A future minimal host must review every exposed primitive and remove runtime loading/eval/debug/output capabilities rather than copying this test bootstrap into serving. No standard library/testing registry, source library, disk/network capability or request state is installed in the template here; no application callback is admitted to template sharing.

Still required before production dependency adoption: bounded eager `init.el` bundle loading (including missing/duplicate/cyclic loads), immutable source-library/cache contract, every actual host callback/native ownership audit, complete output/concurrency bounds, step/stack policy for the final host, cancellation throughout final preparation, repeated revision replacement/restart and joined service shutdown. `NewTemplate`/`NewVM` themselves have no context argument: their graph construction needs reviewed input/graph bounds and measured completion; a context on later evaluation does not by itself interrupt construction. Per-operation interpreter allocation limits are not a process memory quota. No strict source-VM garbage-collection claim is made: approved shared immutable storage can retain enclosing allocations under upstream's documented contract.

The independent [authentication qualification](lisp-auth-qualification.md) initially failed its stock SCS concurrent-invalidation prerequisite. Parent inspection of SCS `Load`/`Commit`/`Destroy` and memory-store `Commit`/`Delete`, plus 100 repeated race-tested runs of the deterministic reproducer, confirmed the behavior. **The operator subsequently selected stock SCS and explicitly accepted this overlapping-request logout/session-resurrection risk**, lifting that specific stop gate without requesting a workaround. Remaining hosting and SDK auth/CSRF requirements still need qualification; risk acceptance does not pass unrun tests. Browser/Tor, font, three-page rendering and database publication evidence are not supplied by these tests.

Privacy: [forbidden defaults](../PRIVACY.md#forbidden-collection-and-defaults), [typed failure ownership](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates), and [gateway data boundaries](../../possums/PRIVACY.md#data-handling-boundaries). Synthetic fixtures only; no telemetry construction/export, request logs, credentials, customer data or automatic diagnostic artifacts. Native host conversation/tool history and the project-local Go dependency cache remain separate development persistence boundaries.

## Bounded runtime/render packet — current local evidence

The operator subsequently authorized finishing local runtime/lifecycle/diagnostic
qualification and separate server wiring, with browser/accessibility/Tor deferred
to manual inspection. This section supersedes the original probe's outstanding
**runtime-source** audit items, not its production/publication restrictions. The
ELPS pin above is unchanged. This packet owns only `runtime.go`, `render.go`,
their two test files and this document. HTTP/session diagnostics and lifecycle,
`bundle.go`, and command/server wiring belong to separate implementation owners.
No production schema, durable publication, installation or deployment follows.

### Actual exposed primitives and sharing audit

`environment` installs only these upstream registrations, inspected on the exact
pin in `lisp/builtins.go`, `lisp/op.go` and `lisp/macro.go`:

- `set`, `set!`: mutate only the calling VM's global/lexical bindings and symbol
  documentation. They may rebind package-qualified names **within that VM**;
  package naming is not a host authority boundary. `lambda`, `let`, `defun`,
  `defmacro` construct VM-local closures and definitions. `defun`/`defmacro`
  expand through the admitted `set`/`progn`; no runtime source loader is needed.
- `progn`, `if`, `dotimes`: evaluation/control flow through the current
  environment. Empty `dotimes` turns consume steps and check context.
  `quote`, `quasiquote`: literal/composition operations, including bounded
  iterative value traversal and evaluation of unquoted expressions; neither
  imports another package or grants a host capability.
- `list`, `cons`, `first`, `rest`: local sequence construction/access/aliases.
  `rest` preserves sealed literal restrictions and clamps the view capacity;
  `cons` checks its new sequence size. `list` returns the evaluated argument
  list, not a general cumulative-memory budget.
- `sorted-map`, `get`, `assoc!`: VM-local map construction/lookup/mutation.
  Map growth uses the SDK allocation check; aliases within one VM are deliberate.
  `concat` accepts list/vector/string/bytes forms and checks result size; these
  are data constructors, not native Go service handles.
- `+`, `=`: scalar presentation arithmetic/comparison, no host effects. ELPS
  addition is **not** the ledger's checked exact-money arithmetic; no financial
  capability is exposed and Lisp values never authorize accounting.

Core registrations use their audited upstream environment/argument functions,
not request-capturing closures. Approval records the **exact LVal registration
identities before executing trusted bundle source**, not package/name/function
address alone. Later registrations, even in `lisp`, are refused. No application
native payload policy is installed: an opaque mutable Go map is rejected.
Interpreter-supported maps/closures are reconstructed by the SDK, not approved
as arbitrary shared Go objects.

All 17 project callbacks are explicitly inventoried:

- Preparation only: `site:load`, `site:get`, `site:post`, `site:group`,
  `site:custom`, `site:style`. Their package-level Go functions have no mutable
  closure captures. They obtain the candidate from the current VM context;
  request contexts lack preparation authority and refuse these operations.
- Read-only metadata/data: `site:url` reads the captured revision's named route;
  `site:option` reads only the supplied fresh preference map.
- Node constructors: `ui:document`, `ui:main`, `ui:group`, `ui:h1`, `ui:p`,
  `ui:link`, `ui:notice`, `ui:login-form`, `ui:post-form`. They produce Lisp data
  from call arguments (notice is only a slot marker); they have no backend,
  credential, file, network, clock, logging or process reference.

No standard-library/test registry, eval/apply/funcall, runtime source loading,
package-management, debug/output, OS, sleep, database, authentication or financial
primitive is registered. `NewEnv` has no source library/cache installed. The
preparation reader parses captured strings only and is cleared **before**
`NewTemplate`; request VMs have no reader/library/cache. Stderr is discarded.
Each VM is used synchronously by one goroutine; concurrent requests instantiate
separate VMs. No request function/environment/LVal is retained in the returned
Go `Node`. Public Go revision metadata is read-only after preparation by host
contract; this is not a defense against a Go caller concurrently mutating its
exported fields, nor against trusted native administration.

### Bounds, cancellation and ownership

- Capture: 1–8 text entries; at most 32 KiB total source; lowercase `.el` names
  with a 1–48-character basename. Caller map mutation after `Prepare` returns
  does not change the captured revision. Concurrent mutation **during** capture
  is outside the caller contract. Sorted, delimited bytes determine the digest.
  Every entry must be loaded eagerly; missing, cyclic, duplicate and unused
  entries fail admission. There is no disk/network fallback or lazy source load.
- Declarations: five routes total, at most 16 groups/options, bounded option help
  and choices. Limits are checked before appending. Boolean defaults require
  actual boolean symbols, not strings spelling a boolean. Routes/actions/style
  remain validated data, and registration is unavailable during requests.
- Preparation: one-second child context and shared 100,000-step budget across
  eager loads. Pages: 200-ms child context and fresh 20,000-step budget per VM.
  Already-cancelled contexts are refused before construction; cancellation is
  rechecked after template/VM construction and after page-tree copying.
- Configured SDK bounds: 32,768 logical bytes/elements per allocation operation,
  128 evaluation nesting/physical stack height, 64 macro expansions, and 1,024
  iterative value-graph depth (including template admission). Tail loops are
  bounded by steps/context, not by a claim that physical stack limits see them.
  The parser independently has a 10,000 parse-depth default; bounded source and
  later evaluation/template checks do not lower that parser default.
- Result copy/render: at most 256 nodes, depth 16, 32 KiB text/targets and a
  document/main root. Links/forms resolve only to admitted GET/auth actions.
  The Go renderer also rejects malformed/cyclic caller trees and oversized
  CSRF/notice slots. A capped writer refuses **before** growing encoded output
  past 64 KiB, including escaping expansion and repeated hidden fields; no
  partial document is returned. This fixes the previous post-allocation check.
- No evaluator goroutine, VM pool, retained request service, cleanup worker,
  timer-based abandonment, background callback, interpreter fork or supervisor
  is introduced. Child context timers are cancelled on every return. Template
  retirement means dropping ordinary Go references; no explicit Close/resource
  handles exist in this presentation-only runtime. Runtime/render alone make
  no claim about separately owned HTTP/session/SDK shutdown workers.

`NewTemplate` and `NewVM` are synchronous **not preemptible** construction calls.
The parser and individual Go builtin traversals also cannot be forcibly
interrupted mid-instruction. Context checks bound supported evaluation, not
strict wall-clock completion under scheduler/GC/resource starvation. Depth,
source, step and per-operation limits are **not** a total graph-node/allocation
quota, whole-process memory quota, capacity result, secure erasure or isolation
from arbitrary trusted-code exhaustion. No independent cumulative graph-width
budget or per-process quota is supplied by this host/SDK. These are explicit SDK
bounds, not reasons to detach work or silently promise a hard deadline.

### Tests and diagnostic handoff

The owned root tests cover repeated preparation and stable digests, retained old
revision usability, caller-source and source-environment mutation independence,
sequential/concurrent map/lexical-closure/default-preference isolation, absent
capabilities, exact callback admission, opaque-native refusal, request-time
registration refusal, missing/unused/cyclic/duplicate source, invalid/deep source,
step/recursion/macro/allocation refusal, graph-depth admission and oversized node
output. Render tests cover hostile escaping, external targets, unknown node kinds,
cyclic trees, oversized slots and exact capped-writer growth behavior.

`TestPageCancellationAndHealthyPeer` invokes the actual `Page` method with a
**test-only** exact-identity template callback: all mutable probe state comes
from its fresh context, another revision serves normally while it waits, then
cancellation runs callback cleanup before `Page` returns. This adds no blocking
production primitive. A separate post-instantiation callback probe also checks
joined cleanup. The independent `qualification/elps` tests exercise cancellation
inside tail recursion, empty-body loops and initialization evaluation on this pin;
the root tests separately cover pre-cancelled preparation/page and bounded runaway
preparation. No assertion relies on an abandoned evaluator or exact GC timing.

Runtime diagnostics return existing `ledger.Diagnostic` types without emission
or metrics ownership. Cancellation/deadline preserve the existing 39-category
projection; safe SDK step/nesting condition tags retain their known constraint.
Host source/declaration refusals use a locally authored closed condition tag;
known failures are not erased by a later context cancellation.
Unexpected page/preparation failures use stage-specific cause-unknown fallbacks;
document rendering has its own `document_render` stage. Raw error messages,
source, stacks and panic payloads are never formatted. Some SDK allocation,
physical-stack and macro errors have only a generic condition: they are not
classified by parsing hostile message text. Typed synthetic condition/payload
negative tests verify safe diagnostic serialization. The HTTP owner must preserve
these diagnostics and its independently established auth outcome rather than
replace every error with a generic HTTP render failure or recount propagation.

Actual completed checks, pinned Go 1.27.2, offline dependencies, metrics disabled:

`env GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true .devenv/profile/bin/go test -race -tags elpscheck internal/website/runtime.go internal/website/render.go internal/website/runtime_test.go internal/website/render_test.go -count=10 -timeout=90s`

`env GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true .devenv/profile/bin/go vet internal/website/runtime.go internal/website/render.go internal/website/runtime_test.go internal/website/render_test.go`

`env GOWORK=off GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true .devenv/profile/bin/go -C qualification/elps test -race -tags elpscheck -count=3 -timeout=45s ./...`

All three passed. An active LSP probe of the four owned Go files also reported
no errors. File-list execution deliberately isolates the owned runtime/
render packet while other owners edit HTTP/session/server files. A prior full
website package run passed before that expansion. A later package-wide repeated
run reported closed session/store diagnostic assertion failures and exhausted its
test time limit; another attempt could not compile transiently incomplete session
test helpers. These are **not** full-package passes or diagnoses of root cause.
The integration owner must rerun the coherent package/root suite after those
edits settle. No failure transcripts or raw output are copied into this document.
Browser/accessibility/Tor remain manual deferred checks. Persistence/publication,
real-data policy, built service/image and full joined server/session lifecycle
acceptance remain separate evidence; this source packet does not claim them.
