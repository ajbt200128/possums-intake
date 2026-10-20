# Local website slice — implementation evidence and remaining gates

Status: **ready for the guided disposable local browser pass**. Automated local
runtime/session/diagnostics and server-wiring checks pass, including the revised
buffer/key source. The operator reports the original login works; the updated
preview is built, restarted and ready for renewed visual inspection. Complete
browser/accessibility/Tor inspection is not passed. This is not production
acceptance or ordinary `serve` activation, and authorizes no production schema/
persistence, real customers, deployment, gateway changes, inference smoke tests
or telemetry export. `PLAN.md` and unrelated work were left untouched.

## Implemented source

- `internal/website/runtime.go`: bounded captured ELPS bundles, eager `init.el`
  loading, selective core/project registration, one prepared template per
  revision, fresh VM and VM-local entry lookup for each page, default-only typed
  options and copied bounded presentation trees. `bundle/*.el` declares the three
  pages, five method/route declarations, composition, defaults and style values.
- `internal/website/render.go`: host-owned escaped HTML, fixed capabilities and
  native forms, digest-qualified CSS, local font paths, one text size, square
  controls, visible focus and generic monospace fallback. No JavaScript,
  arbitrary HTML/attributes, customer input evaluation or browser storage.
- `internal/siteauth`: supported SDK internal password dispatch and current-token/
  AuthRule validation. See [the auth qualification](lisp-auth-qualification.md)
  for exact final-response/MFA behavior and SDK cancellation limits.
- `internal/website/handler.go`: `NewFixture` composes stock SCS v2.9.0 and nosurf
  v1.2.0 with the auth adapter. Explicit fixture settings select absolute/idle
  lifetimes and concurrent dynamic-request admission. These are not approved
  product settings. Cookies are Secure, HttpOnly, host-only, Path `/`, SameSite
  Lax and session-only. Authentication requires actual TLS; proxy trust/TLS
  termination is not configured. Tokens remain private Go session values;
  current SDK validation precedes protected access and session expiry is capped
  by the already-validated SDK token's expiry.
- Native/accounting routes pass directly to a supplied fallback without customer
  middleware. Only declared customer routes and a closed map of embedded local
  CSS/fonts/license are handled here. Unknown asset paths are not file-server
  capabilities. Query-bearing/encoded website paths, stale forms, unapproved
  fields and oversized form bodies cannot execute backend actions.

The fixture constructor uses exported SCS configuration and stock
`memstore.NewWithCleanupInterval(0)`, just like the independent reproducer.
It introduces no custom store or revocation mechanism. An exploratory use of
stock default cleanup followed by `StopCleanup` produced a Go race report at
SDK cleanup initialization/shutdown. Zero cleanup interval avoids that worker
in disposable fixtures; it is **not** a production lifecycle fix. Expired entries
remain in fixture memory until it is released. Long-running cleanup/memory bounds
remain unqualified. The separately accepted overlapping-logout resurrection risk
is unchanged; no serialization, fencing, registry or replacement was added.

No production customer collection, provisioner, credential, website publication
store, preference row or ordinary `serve` activation was added. The subsequent
`website-local` command wires the same binary and SDK app to a disposable loopback
HTTPS fixture using the existing drain/join coordinator. It accepts no state path
and creates only a fresh synthetic customer in temporary state. `CustomerAuthApp`
exposes the existing SDK app without changing hooks; its regression test proves
the accounting identity guard remains active. Neither path calls `OperatorApp`
merely to obtain the customer app.

## Font evidence

Pinned Iosevka Term v34.9.0 upright regular/bold WOFF2 resources and verbatim OFL
are in `internal/website/assets/`. The parent independently recomputed all three
file sizes/SHA-256 values against `manifest.json`, checked both WOFF2 signatures,
TrueType flavors and declared file lengths, and read the full license. The fixed
HTTPS resource tests compare actual served bytes to those hashes. Provenance
and limitations are in `assets/NOTICE.md`; no internal font-table, glyph, hinting
or real-browser rendering claim is made. The regular/bold resources total about
3.4 MB; no Tor transfer-time or subset optimization evidence is claimed.

## Actual synthetic checks

Using the existing pinned Nix/devenv Go **1.27.2 darwin/arm64**, local dependency
cache, dependency resolution off and `OTEL_SDK_DISABLED=true`:

`env PATH="$PWD/.devenv/profile/bin:$PATH" GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true .devenv/profile/bin/go test -race -tags elpscheck ./... -count=1 -timeout=240s`

Passed all root packages, including cmd/intake, accounting, HTTP, native operator,
auth adapter, website and telemetry. Two final integrated root runs passed after
the implementation packets completed, including the compiled local command;
the last also includes the runtime-to-HTTP diagnostic handoff correction. No race
report. Earlier focused website runs passed three repetitions after fixing the
**test** form extractor to decode HTML attribute entities, as native browsers do.
Root tests do not include nested modules: the parent separately reran ELPS
qualification three times and the accepted stock SCS reproducers twenty times
under the race detector; both passed.

The actual certificate-verifying local TLS client exercises public home,
protected redirect, final SDK login, signed-in home and POST logout without
JavaScript execution. Tests cover native forms, missing/wrong origin, disabled
cookies, stale revisions, neutral credential refusal, private token retention,
reserved-route delegation, method/body/path limits, closed asset paths and exact
font/license bytes. SDK log/auth-origin rows remain empty in these fresh selected
flows. These are HTTP-client tests, **not** real-browser/Tor or timing-equivalence
evidence. No real users, mail, payment/inference operation or export is exercised.

Root `go vet ./...`, `go mod verify`, `git diff --check` and an active LSP error
probe of twelve wiring/accessor/runtime/HTTP files also passed. An initial LSP
batch left one timeout unconfirmed; the explicit twelve-file rerun confirmed all
clean. `go mod tidy` was run offline.
Root module changes select ELPS/SCS/nosurf and Go's required transitive upgrades,
including OTel core/metrics/SDK v1.47.0 while the existing OTLP metric exporter
stays v1.44.0. Existing synthetic telemetry tests passed; no production export,
image-vendor identity or runtime instrumentation equivalence is inferred.

## Local server wiring — source/compiled checks

`cmd/intake/website_local.go` adds only the explicit `website-local` mode. It
ignores inherited real admin/operator/export settings, generates a private random
accounting key and ephemeral self-signed loopback certificate, creates no telemetry
runtime, and uses the existing server's timeout/quiet-error/drain/maintenance
machinery. Ordinary command modes and native policy remain unchanged. The manual
runbook is [local walkthrough](lisp-website-local.md); no browser has been opened
or manual server left running by these tests.

Pinned race/checked tests passed three repetitions of `TestWebsiteLocal*` and
`TestLocalWebsiteCertificate*`. They build the actual intake executable and run
fresh local processes: verify the self-signed certificate is initially rejected,
pin that public certificate without disabling TLS verification, perform native
HTTPS form login/home/logout, assert empty SDK logs/auth-origins and stdout/stderr,
signal normal shutdown, and verify temporary storage disposal. Fresh process
repetition uses new state and keys; it is not persisted website publication.
Additional tests check unsafe options rejected before filesystem effects,
once-only bind failure ownership/disposal, synthetic SDK setup error/panic and
cancellation privacy/types, and preservation of the accounting validation hook.
An initial synthetic panic test exposed a nil diagnostic projection in the new
seed-error helper; it was fixed before the three passing repetitions. No raw
production data or cause is retained in diagnostics.

Final full-root race/checked tests, vet, module verification, whitespace checks
and twelve-file active LSP checks passed with the coherent source. Independent
read-only review found no local-readiness blocker; a second review checked the
final diagnostic/observer handoff, also without a blocker. No review substitutes
for the tests or for deferred browser acceptance.

The reviewed runtime has fresh request VMs, no shared request reader/native
payload, synchronous cancellation and bounded node/document copying. The HTTP
negative tests cover hostile auth/store errors and panics, actual idle/absolute/
SDK-token expiry, fixation, restart loss, credential revalidation, protected logout
under a public page declaration, and once-only failure projection. Commit failure
suppresses the original SCS response without recursive commits or cookie leakage.
Later rendering/delivery faults preserve established auth/logout effects rather
than inventing rollback. The final handoff preserves the runtime's closed
execution-limit/cancel/document stage and constraint, not just numeric type;
hostile diagnostic text is replaced with locally authored guidance. Detailed
boundaries and test names are in the two qualification documents. The fixed
39-category projection and accepted SCS concurrency behavior are unchanged.

The operator explicitly requested a built, running local preview; its health and
public page were checked over local TLS with the acquired public certificate
pinned. No credentials were sent during that readiness check. The operator then
reported original fixture login working and supplied visual feedback; this is a
manual report, not full browser/accessibility/Tor acceptance. The subsequent
one-fake-key/whitespace refinement has now passed source checks and is running
on the loopback preview; renewed operator visual checks remain open.

## Buffer/key presentation refinement

The operator selected **one public fake demo key** for the disposable preview.
`siteauth.FixtureKeyAdapter` binds it to the fixed synthetic SDK identity and
calls the unchanged final SDK login/validation bridge. No browser identity field,
real-key lookup/derivation, key provisioner or production schema is added. The
host accepts only `key`, CSRF and revision fields; the secret key and SDK token
remain outside Lisp/presentation/diagnostics. Public `local-demo-key` is not a
high-entropy credential or a suggested production format.

`ui:text` is a pure escaped scalar-text capability admitted by exact callback
identity and subject to the existing node/depth/text/document limits. Whitespace
is literal page data, preserved with `white-space:pre-wrap` and eight-column tabs.
The template trims its own source formatting; headings/paragraphs/forms display
inline without internal layout margins or padding. The operator subsequently
approved outer left and top gutters (`body` logical inline/block-start padding
of `1rem`); tests allow these exact body-only exceptions while keeping internal
box spacing zero.
Notice suffix whitespace is also
explicit bundle data and is emitted only with an actual notice. The former
`space-unit` setting is removed, rather than kept as an ignored option. Controls
use the same font with a 28-character input slot and no padded minimum height.
This models a text buffer; actual browser baseline/font geometry still needs the
operator's renewed visual check. Soft viewport wrapping is not a stored newline.

Light-palette research inspected upstream source (references, not dependencies):

- [Emacs default faces](https://github.com/emacs-mirror/emacs/blob/master/lisp/faces.el):
  the default face inherits frame settings; the light color display link face is
  `RoyalBlue3`. There is no universal platform-independent default paper color.
- [Emacs widgets](https://github.com/emacs-mirror/emacs/blob/master/lisp/wid-edit.el):
  widget buttons are bold; light fields use gray85 and a box whose negative
  horizontal thickness explicitly avoids making lines taller.
- [Doom One Light](https://github.com/doomemacs/themes/blob/master/themes/doom-one-light-theme.el):
  paper `#fafafa`, ink `#383a42`, with neutral alternate backgrounds. The preview
  uses those paper/ink colors and Emacs `RoyalBlue3` (`#3a5fcd`) links/buttons;
  `#6b6d75` control borders are locally chosen for contrast, not an exact Doom face.
  Calculated sRGB contrast against paper: ink **10.86:1**, links **5.44:1**, borders
  **4.94:1**. These calculations are not whole-page accessibility qualification.
  No dark-mode code or saved theme preference is added.

Fresh parent checks after this refinement: full root `-race -tags elpscheck`
suite passed, including the actual compiled executable's one-key login/logout,
privacy and disposal checks. Buffer-specific source/DOM whitespace and
escaping/admission tests passed three additional repetitions; the fixture-key
SDK delegation test passed. Root vet, module verification, whitespace checks,
rebuild and fourteen-file active LSP error checks passed. An independent read-only
review found no actionable source blocker. The former runtime test file-list
workflow is kept independent of HTTP helpers; none of these checks substitute
for browser pixel/focus/accessibility inspection. The isolated runtime/render/
buffer test file-list suite also passed three repetitions after the semantic
lookup helper was kept with the runtime tests.

The prior explicitly launched preview was signalled and observed exited before
its replacement was launched. The replacement's health, key-only login markup
and current digest-qualified buffer/light CSS were checked over pinned local
TLS without submitting credentials. A new synthetic state/session store and
self-signed certificate are expected at every start; the browser may require a
new site-specific exception, and old sessions/forms cannot be reused. The
operator-requested manual preview is intentionally left running, not deployed.

The gutter-only CSS follow-ups passed focused render/buffer race tests three
times, rebuild, whitespace and three-file active LSP checks. The next compact
line/face/hover refinement passed a fresh **full-root race/checked suite**, root
vet, rebuild, whitespace and four-file active LSP checks, including both gutters.
The operator-requested preview was restarted and its actual login text (one
newline per row), visible tooltip span and inverse-hover CSS were checked over
pinned local TLS without submitting credentials. This remains markup/CSS/HTTP
evidence, not a real-browser hover/italic/accessibility result.

`ui:face` admits only the symbol `tooltip` and scalar text, with the existing
bounded copying and escaped rendering. Unknown names, string names, malformed
arguments, oversized text and non-leaf caller trees are rejected. The visible
session note uses `.face-tooltip`; it is not a hidden popup or ARIA tooltip.
Since only upright regular/bold fonts are pinned, this named face alone permits
browser style synthesis for italics; all other text retains `font-synthesis:none`
and no new font request is introduced. Links/buttons invert the existing colors
on hover and keyboard focus without geometry changes; forced-color mode uses
system Highlight/HighlightText. Default bundle blank rows were replaced by
single authored newlines, not a global whitespace-collapsing transform.

Host-only CSS edits currently retain the captured bundle's asset URL, so a hard
reload can be needed; these Lisp changes do change the captured digest.
Production asset-versioning qualification is not established by this fixture.

## Gates still open

- Production publication/replacement/capacity and long-running store cleanup are
  not qualified by this fixed disposable bundle. ELPS construction is synchronous
  and non-preemptible; source/depth/step/per-operation bounds are not a cumulative
  allocation or whole-process memory quota. See [ELPS qualification](lisp-hosting-qualification.md).
  Arbitrary SDK hooks without context support cannot be forcibly cancelled.
- The local fixture's no-worker shutdown and actual service disposal are checked;
  this does not qualify a future production session cleanup worker or promise
  logout revocation beyond the explicitly accepted stock SCS semantics.
- Customer identity, provisioning, recovery, supported MFA contract, product
  lifetimes and separately approved persistence/schema proposal. Do not silently
  adopt the synthetic email/password collection or temporary policy values.
- Real browser/keyboard/responsive/font-blocked/JavaScript-disabled/Tor Safest
  inspection, generated-image/real-data integration/privacy acceptance,
  and the separately authorized publication/restart/rollback path.

Source tests are not a deployed-service receipt. SDK fixtures persist synthetic
state in temporary databases until cleanup; dependency caches and native T3/Pi
conversation/tool history remain separate development persistence boundaries.
Development-agent failure records are curated only in the external local log
`/Users/mb5/projects/possums-intake-agent-failures.md`, not copied here.
Applicable policy: [forbidden defaults](../PRIVACY.md#forbidden-collection-and-defaults),
[typed ownership](../PRIVACY.md#mandatory-interface-failure-classification-and-error-aggregates)
and [gateway data boundaries](../../possums/PRIVACY.md#data-handling-boundaries).
