# Isolated activation browser walkthrough

The operator authorized a separate local synthetic native-admin/publication preview
and browser acceptance pass after the source-fixture packet. This supersedes the
previous no-new-preview restriction only for this isolated test harness. It does
not enable a production command, real credentials/data, gateway integration,
installation, deployment, inference, export, commits or pushes.

## What is running

- Website: **https://[::1]:8444/**
- Stock native admin: **https://[::1]:8444/_/**
- Website public fake key: **`local-demo-key`**
- Native public fake email: **`activation-demo@example.invalid`**
- Native public fake password: **`local-activation-demo-password`**

These are intentionally public, throwaway fixture values. Never enter a real
Possums key/password, sensitive source, customer data or operator state here.
Anyone with access to this local machine and these values can administer this
synthetic fixture; it is not an authentication-hardening or production example.

The original `https://127.0.0.1:8443` preview and its executable are untouched.
Different **hosts**, not merely different ports, isolate their `__Host-` cookies.
This listener binds only IPv6 loopback. It uses a fresh in-memory self-signed
certificate: a browser warning is expected. If your browser permits it, accept
only a site-specific local exception for this exact `[::1]:8444` fixture. Do not
install a CA or disable TLS verification globally. If no exception is available,
stop and ask for a different explicit local arrangement.

The test harness stops after at most 45 minutes from its launch, or a deliberate
signal to that exact fixture process. It drains/joins through the existing server
coordinator, closes the SDK and removes scratch state on confirmed normal teardown.
Unconfirmed teardown preserves scratch state. A crash/kill can leave synthetic
files behind; deletion is not secure erasure. Starting it again creates **new**
state and sessions, not a restore of the last manual preview.

## Your eyes are useful here

The automated stock-editor pass is complete. You do not need to repeat all its
mechanical assertions. Useful human checks are:

1. Does the website still look like the accepted light text buffer—spacing,
   gutters, italic session note, hover, keyboard focus and narrow-window wrapping?
2. Is the difference between **save draft** and **publish** understandable in the
   stock record editor? Is editing the bounded JSON/source tolerable for this MVP?
3. Are refusal messages actionable, without implying a successful publish or
   requesting private diagnostics?

Please report the page/step and visible behavior. No cookies, tokens, DevTools
exports, full source dumps or real credentials are needed.

## Optional hands-on publish check

1. Sign into the website with the fake key. Keep that tab open.
2. Sign into native admin separately with the fake native credentials above.
   Native login is page-memory only: reloading that UI logs its admin session out.
3. Open collection **`website_bundles`**, then its single `websitebundle01` row.
4. In `draft`, find `Private inference, with clear boundaries.` inside the
   `files.pages.el` string and replace only that text. Keep the surrounding JSON
   quoting/escapes intact. This is ELPS, not Emacs Lisp.
5. **Click outside the JSON editor** so its stock blur handler accepts the edit.
   Leave `publish` unchecked and choose **Save changes**. Refresh the public page:
   its text should remain unchanged.
6. Reopen the same row, check `publish`, and choose **Save changes**. Refresh the
   public page: the new text should appear. The publish checkbox resets false.
   The signed-in website session should still work.

Old POST forms intentionally return a stale-revision refusal after publication;
reload the page before submitting again. That refusal is not a logout. Stock
SCS's previously accepted concurrent-logout limitation remains unchanged.

Do not edit `website_selection` for this check. Trusted admins are not technically
restricted from doing so, but direct selection changes bypass publication and are
not activation receipts. Code-only rollback means copying retained previous code
into the draft and explicitly publishing, **not restoring a database**.

## Source and local evidence

`cmd/intake/website_activation_browser_test.go` is an explicit opt-in **test
artifact**, not a new production/publication CLI. It accepts no state path and
uses only newly private synthetic scratch state. It reuses ledger initialization,
`CustomerAuthApp`/trusted `OperatorApp`, supported native router, existing auth
fixture/TLS and existing drain coordinator. No exporter is constructed. SDK
logging/AuthAlert-origin protections and stock native MFA/OTP are retained.

Separate executable: `.devenv/state/website-activation-browser/preview.test`.
The manual-only test is skipped unless `INTAKE_MANUAL_ACTIVATION_PREVIEW` equals
`synthetic-only`. Production binaries and ordinary `website-local` are unchanged.
No build or start command in this guide silently restarts a listener.

`internal/website/activation-browser-test.mjs` uses already-installed Playwright
and Chromium, without installation or gateway edits. Its browser accepts **only
this ephemeral certificate's exact SPKI**; it does not set `ignoreHTTPSErrors` or
install a CA. The public certificate is acquired from the explicitly synthetic
loopback endpoint before entering fixture credentials. This is local test pinning,
not attestation or a production trust recipe. Ordinary untrusted TLS navigation
was rejected as expected. Browser-context requests are restricted to this exact
origin. No screenshot, trace, browser exception text or tokens are retained by the
script; only closed pass/failure stages are printed.

Observed passes with pinned offline Go and telemetry disabled:

- Focused new fixture native-login/TLS/privacy/restart test under race/elpscheck,
  three repetitions. It verifies actual native login, stock UI and website HTTP,
  empty SDK logs/origins, and reopening the same isolated persistent state.
- Full `go test -race -tags elpscheck ./cmd/intake -count=1 -timeout=240s` passed,
  including existing compiled CLI and disposal tests.
- Root `go vet ./...`, JS syntax check, pinned gofmt and `git diff --check` passed.
- Actual headless Chromium stock editor: inert draft save, explicit checkbox
  publication, signed-session continuity, stale POST refusal, invalid candidate
  refusal with old code still usable, and explicit restoration of baseline through
  the stock editor. Page-memory native storage and disabled log settings were
  checked; no external page requests or page errors were observed in this pass.
- Both new paths were actively probed: no Go/type errors. Auxiliary JS advisories
  concerned correctly parenthesized awaited results and the test's fixed PASS line,
  not production logging.

Earlier browser harness attempts needed correction for stock contenteditable JSON
inputs, blur-before-save/toggle, modal reopening, and capturing the stale form only
**after** explicit synthetic baseline setup. A pending response wait also needed an
immediate rejection handler to prevent a raw browser-timeout escape. Those attempts
are not passing evidence. The successful run used actual UI actions, not an SDK
update substituted for editor clicks, and left the original website text active.

Privacy boundaries follow [the activation inventory](website-activation-evidence.md),
[intake manual administration](../PRIVACY.md#trusted-intake-manual-admin-exception),
[forbidden diagnostics](../PRIVACY.md#forbidden-collection-and-defaults), and
[gateway data boundaries](../../possums/PRIVACY.md#data-handling-boundaries).
Browser/operator history, native host history, temporary browser profiles,
SQLite/WAL/deleted pages and caches remain independent persistence boundaries.
No whole-browser erasure/transience or production acceptance is claimed.

Human visual/accessibility/Tor inspection, generated-image activation/restart,
production storage/TLS/access qualification and real-auth integration remain open.
