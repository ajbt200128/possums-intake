# Local development environment

This document covers **only** the local Go development shell for this repository.
It is not a production or deployment platform, and it does not describe the
ledger service itself. Service behavior, storage and acceptance are defined in
`PLAN.md` and are implemented separately.

## Scope

- The pinned dependency is PocketBase `v0.40.5`, whose `go.mod` requires **Go 1.27**.
- This shell provides exactly that Go toolchain plus ordinary `git` and `curl`.
- It declares **no services**, no database, no containers and no telemetry.
- devenv.sh is used here only as a local developer-environment tool. It is **not**
  proposed as a production runtime; the ledger runs as its own Tinfoil workload.

## Files

- `devenv.yaml` — pins the exact nixpkgs revision providing `go_1_27` (1.27.2).
- `devenv.nix` — selects the Go 1.27 toolchain and sets the usual Go environment
  variables (`GOROOT`, `GOPATH`, `GOTOOLCHAIN=local`).
- `.envrc` — standard devenv/direnv integration. It is **not** auto-allowed.
- `.gitignore` — excludes build output, local Nix/devenv state and local secrets.
  Planning, source and documentation files are never ignored.

## Prerequisites (honest, not guaranteed)

- Nix with flakes enabled, and `devenv` on `PATH`.
- The pinned nixpkgs revision must be fetchable on first use. This repository has
  no cached binary substitution configured here; a cold machine may build or
  download the Go 1.27.2 toolchain.
- `direnv` is optional. The shell works through `devenv shell` without it.

## Commands

Enter the shell for this checkout:

`devenv shell`

Confirm the toolchain inside the shell:

`go version`

Confirm the pinned module environment:

`go env GOROOT GOPATH GOTOOLCHAIN`

Show the resolved inputs and packages:

`devenv info`

Validate that the configuration evaluates and the shell realizes:

`devenv shell -- true`

Run the test suite once the service exists (owned by the service packet):

`go test ./...`

Optional direnv activation (this command is what actually trusts `.envrc`):

`direnv allow`

## Reproducibility

`devenv.yaml` pins the nixpkgs input by exact revision, so the Go 1.27 toolchain
resolves to the same package across machines without a floating channel. Running
`devenv` generates `devenv.lock`, which records the resolved devenv module and
nixpkgs revisions. Retain and review that generated file with the local implementation;
committing it still requires separate authorization.

The `languages.go` devenv module is deliberately not used. In devenv 2.0.4 it
additionally builds editor tooling (`go-tools`, `gopls`, `delve`) that currently
fails to compile against Go 1.27 in this nixpkgs revision. Enabling it makes the
shell unbuildable; the toolchain package is selected directly instead.

## Local secrets and state

Do not commit credentials, the runtime accounting admin key or database files.
`.gitignore` excludes the common local cases (`.env`, `*.pem`, `*.key`, `*.db`
and SQLite WAL/SHM files). The service's chosen state path and secret-injection
method are defined by the service packet, not by this development shell.

## Pending acceptance

The parent independently confirmed `devenv shell -- go version` reports Go
1.27.2 on darwin/arm64 and the ignore rules preserve the inspected source and
planning files. This is environment evidence, not service acceptance.
The following remain unverified by this packet:

- The local service, its tests and its migrations (implemented separately).
- Whether the pinned toolchain builds the pinned PocketBase cleanly on a cold
  cache.
- Actual ledger durability, restart, cancellation and privacy behavior.
- Any deployment, image, telemetry or gateway integration.
