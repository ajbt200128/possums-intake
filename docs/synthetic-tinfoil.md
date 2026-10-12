# First Tinfoil test enclave — synthetic only

## Selected scope

The operator requested a **new disposable CPU-only enclave**, using the published minimum **2 CPUs / 8192 MB**, with native PocketBase operator login and direct native fake-account creation/editing. The operator explicitly declined a custom account form and approved a native account collection. No gateway cutover, real funds, payments, Honeycomb export or persistent volume. State loss is deliberately acceptable for this first test; it is not an encrypted-volume, financial restore or durability qualification.

The verified standalone baseline is pushed to the private `ajbt200128/possums-intake` repository. Its successful local checks are recorded in [verification](verification.md). The newly requested operator/dashboard surface is a separate local implementation/qualification packet, not yet accepted or deployed. Production publication/replacement workflows remain disabled.

## Required setup decisions

- **Public config:** Tinfoil requires a public configuration/evidence repository. The application image and source can remain private. Do not change this repository's visibility or create a public repository without the operator's choice. A separate, minimal `possums-intake-deploy` repository is the proposed option, not an existing resource.
- **Cost:** CPU/RAM usage is billed hourly, but the accessible public pricing page and inspected CLI host catalog did not establish this shape's actual rate. Obtain the console quote and operator approval (or an explicit budget against a verified rate) before a paid create call. No price or spending ceiling has been inferred.
- **Secrets:** Create dedicated synthetic operator/accounting credentials through private input; do not reuse gateway credentials, put values in Git/config/argv, or ask for them in chat. Native operator authentication remains distinct from the shared accounting key. Registry credentials, if required for a private image, need separate narrow setup.
- **Repository access:** The Tinfoil GitHub App must be granted the selected config repository. Existing CLI login is available; that does not establish access to a new config repository or authorize unrelated organization resources.

No configuration/cost selections have been recorded. These two decisions remain open; do not infer consent to make the app public or spend at an unknown rate.

## Next executable packet

1. Locally qualify the opt-in native dashboard and supported native account collection/editor: actual login, exact-text uint64 money, immutable external account identity, serialized writes, balance-edit/active-hold race protection, key/token separation, denied unrelated native CRUD/backup/log/installer surfaces, browser/server diagnostics and auxiliary-state privacy. Only direct synthetic account edits are authorized; custom accounting routes still require the shared key. No custom account form.
2. Freeze and qualify a fresh image/source identity. The old archive's acceptance does not cover added UI, the finalized first schema v1 or remote-serving behavior. No version bump/migrator is needed before first release. Preserve and reject earlier incompatible disposable layouts through strict validation; explicitly select/recreate test state rather than silently resetting it. Post-release schema changes will need tested migrations.
3. Deliberately implement and test the synthetic startup/listener profile. The existing service only binds loopback and refuses missing state; it is not currently a reachable Tinfoil deployment. A synthetic profile may explicitly initialize a new disposable temporary state directory, but must not weaken ordinary `serve`, reset an existing ledger, infer production initialization or attach a volume. Keep export off, no egress, nonroot/read-only root and bounded writable scratch.
4. After the decisions above, prepare the reviewed public config with the actual immutable image digest, supported CVM version, exact HTTP path exposure and dedicated secret names. Keep debug off. Use supported signed release/provenance evidence and preserve independent matching application builds; do not activate dormant production adapters by changing a false gate.
5. Confirm the selected release, host, resource shape, rate and no-volume/no-debug policy, then create **one** new instance. Unknown/interrupted creation must be reconciled using passive authoritative status before any retry. Do not replace existing instances or promote a repository-wide latest release by default.
6. Test readiness and authenticated synthetic accounting. Prefer the installed verified Tinfoil CLI/proxy for operator access; a direct browser/curl connection alone does not verify enclave identity. No new gateway/client verifier implementation is included. Record observed outcomes, not inferred billing or completion.

## Public evidence

- [Configuration reference](https://docs.tinfoil.sh/containers/configuration): public config repository, resource shapes, immutable images, secret declarations and HTTP exposure.
- [First instance](https://docs.tinfoil.sh/containers/quickstart): measured/signed release, private application image option, read-only root/scratch requirements and verified CLI requests.
- [Pricing](https://tinfoil.sh/pricing): hourly CPU/memory billing; no exact rate established by the accessed content.

Installed CLI help was inspected locally. It provides `container create --repo --tag`, defaults release promotion on, and uses `--promote-release false` to decline it. Follow the installed version's supported flags rather than copying differing documentation examples. No paid create, publication, registry mutation, organization secret write or deployment was performed during this preparation.
