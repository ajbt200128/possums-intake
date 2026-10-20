# Disposable local website walkthrough

This is the local-only, synthetic website mode of the intake binary. It uses the
same Go/PocketBase app and existing HTTP/maintenance shutdown coordinator, not a
second service or an external auth server. Ordinary `init`, `serve` and
`synthetic-serve` behavior is unchanged. No production customer collection,
provisioning contract, publication store or gateway integration is enabled.

Automated local runtime/lifecycle/diagnostics and compiled-service checks have
passed; scope and remaining production limits are in [website evidence](lisp-website-evidence.md).
The operator-led browser pass has started: the operator reported that the
original synthetic sign-in worked and requested buffer-like spacing and one-key
login. That report does not qualify the revised presentation or complete
accessibility/Tor checks. A local server is started only on explicit request.

## Starting it, when ready for the manual pass

From the repository root, build with the existing pinned development environment:

`env PATH="$PWD/.devenv/profile/bin:$PATH" GOTOOLCHAIN=local GOPATH="$PWD/.devenv/state/go" GOPROXY=off GOSUMDB=off OTEL_SDK_DISABLED=true .devenv/profile/bin/go build -o .devenv/state/website-local/intake ./cmd/intake`

`.devenv/state/website-local/intake website-local`

The foreground process listens only on `127.0.0.1:8443` by default. Open
`https://127.0.0.1:8443/` in your browser. Do not use plain HTTP. An alternative
numeric loopback address/port can be selected with `--listen`; wildcard,
non-loopback, hostname and state-directory arguments are rejected before creating
scratch state. Nothing is deployed or published by this command.

A fresh self-signed localhost certificate is generated in memory at every
startup. A browser certificate warning is expected: use only a **site-specific
local exception for this exact loopback site**, if your browser offers one.
Do not disable global TLS verification, install a system CA or use this technique
for a remote/production service. If the browser refuses an exception, stop and
we will choose an explicit local test arrangement together.

## The three pages

1. Public home: open `/`; follow **Sign in**.
2. Login: enter the **public, synthetic fixture key** `local-demo-key` in the
   single **Key** field. It is intentionally not a secret and is never created
   outside newly disposable local state. Do not enter a real Possums key. A tiny
   fixture-only adapter binds the key to a fixed synthetic SDK identity; the SDK
   still validates it as the fixture record's password. This is not a production
   single-key lookup, provisioning, recovery or storage design.
3. Signed-in home: confirm **You are signed in.**; use **Sign out**. Opening
   `/home` afterward should return to login. Logout is a POST, not another page.

The guided pass can cover wrong fake keys, first-party cookies, reload,
keyboard/focus, narrow layout/zoom, blocked fonts and JS-off/Tor behavior. No
browser/accessibility/Tor result is claimed before that pass. If something goes
wrong, share only its fixed `SITE_*` code and what step you were taking, not
cookies, tokens, headers, page source, DevTools dumps or real credentials.

## Text-buffer presentation

The Lisp bundle emits literal `ui:text` strings containing newlines, tabs and
spaces. The current pages use one newline between rows, with no blank rows;
custom literal whitespace remains preserved rather than globally collapsed. `ui:notice` takes the literal suffix to display
only when a notice exists. Template formatting adds no whitespace inside main.
The operator-approved outer body gutters are `padding-inline-start:1rem` and
`padding-block-start:1rem` (left and top in this LTR page). Inside the buffer, no paragraph/header/form margins, padding,
flex gaps or spacing-unit option create layout. Controls inherit the text font, occupy character-sized slots and
sit inline; semantic headings, labels, forms and a visible keyboard focus remain.
Soft wrapping still follows the viewport; browser/native control and font
rendering require manual inspection. `(ui:face 'tooltip "...")` is a closed,
visible italic text face, not a hidden hover popup. Only this face allows browser
style synthesis with the pinned upright font; no italic asset is added. Links
and buttons reverse the existing paper/link colors on hover and keyboard focus;
forced-color mode uses system Highlight/HighlightText. Dark mode is deferred.

## Stop and persistence boundaries

Press **Ctrl-C** in the foreground terminal. The existing coordinator cancels
request/maintenance contexts, drains HTTP and joins work before closing storage.
Confirmed normal teardown removes the disposable scratch directory. Unconfirmed
storage/drain teardown preserves it rather than deleting potentially open state.
A crash/SIGKILL can leave temporary files behind; deletion is not secure erasure.
Go build/dependency caches and browser/native host history remain independent
persistence boundaries.

Every startup initializes a new private temporary ledger/SDK database and a new
fake customer; there is no `--state` option, reset of an existing database, real
admin-key use or production provisioning. SDK logs/AuthAlert recording remain
off. This mode does not construct telemetry even if export settings are inherited.
An unpresented random accounting key keeps custom accounting unavailable to the
customer browser. Native administration is not enabled by this mode.

Sessions are volatile, session-only cookies; the current local fixture settings
are one-hour absolute and ten-minute idle expiry, capped by the SDK credential's
expiry. They are not product-policy approval. Restart loses sessions. The accepted
stock SCS overlapping-logout resurrection limitation remains unchanged; do not
assume logout revokes a copied PB token. The disposable fixture store uses no
cleanup worker; expired entries are inaccessible but retained until its host is
released. This local mode is not qualified for long-running or real-user capacity.

No automatic browser opening, Tor infrastructure, mail, payments, inference,
telemetry export, installation, deployment or production activation is performed.
