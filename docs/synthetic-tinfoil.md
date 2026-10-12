# First Tinfoil test enclave — synthetic only

## Selected scope

The operator requested a **new disposable CPU-only enclave**, using the published minimum **2 CPUs / 8192 MB**, with native PocketBase operator login and direct native fake-account creation/editing. The operator explicitly declined a custom account form and approved a native account collection. No gateway cutover, real funds, payments, Honeycomb export or persistent volume. State loss is deliberately acceptable for this first test; it is not an encrypted-volume, financial restore or durability qualification.

The standalone native operator/dashboard and additive accounting compatibility changes are locally qualified and pushed to the private `ajbt200128/possums-intake` repository. Their checks are recorded in [verification](verification.md) and [gateway compatibility](gateway-compatibility.md). Synthetic remote startup and deployment are not yet accepted. Production publication/replacement workflows remain disabled.

## Authorization and remaining setup

- **Public config:** The operator approved a separate public configuration/evidence repository. `ajbt200128/possums-intake-deploy` now exists with a scope README; no deployable configuration or release has been published. The application repository remains private; do not change its visibility.
- **Cost:** The operator explicitly accepted the actual cost of the one selected disposable 2-CPU / 8192-MB instance without another price-confirmation gate. No exact rate or spending ceiling is claimed. This does not authorize additional instances or unrelated paid resources.
- **Secrets:** Create dedicated synthetic operator/accounting credentials through private input; do not reuse gateway credentials, put values in Git/config/argv, or ask for them in chat. Native operator authentication remains distinct from the shared accounting key. Registry credentials, if required for a private image, need separate narrow setup.
- **Repository access:** The Tinfoil GitHub App must be granted the selected config repository. Existing CLI login is available; that does not establish access to a new config repository or authorize unrelated organization resources.

Implementation/testing of disposable startup, the separate public config repository, and deployment/verification of the selected single synthetic instance are authorized. GitHub App access, dedicated secret setup, actual image qualification and signed release admission still need evidence before creation.

## Next executable packet

1. Preserve the locally qualified native dashboard/account editor, exact-text uint64 money, immutable identity, serialized writes, active-hold protection, key/token separation, denied unrelated surfaces and logging/privacy controls. Requalify the new synthetic startup through actual HTTP/native login tests; existing acceptance does not cover that new command. Custom accounting routes still require the shared key. No custom account form.
2. Freeze and qualify a fresh image/source identity. The old archive's acceptance does not cover added UI, the finalized first schema v1 or remote-serving behavior. No version bump/migrator is needed before first release. Preserve and reject earlier incompatible disposable layouts through strict validation; explicitly select/recreate test state rather than silently resetting it. Post-release schema changes will need tested migrations.
3. Deliberately implement and test the synthetic startup/listener profile. The existing service only binds loopback and refuses missing state; it is not currently a reachable Tinfoil deployment. A synthetic profile may explicitly initialize a new disposable temporary state directory, but must not weaken ordinary `serve`, reset an existing ledger, infer production initialization or attach a volume. Keep export off, no egress, nonroot/read-only root and bounded writable scratch.
4. After local startup/image acceptance and access/secret setup, prepare the reviewed public config with the actual immutable image digest, supported CVM version, exact HTTP path exposure and dedicated secret names. Keep debug off. Use supported signed release/provenance evidence and preserve independent matching application builds; do not activate dormant production adapters by changing a false gate.
5. Confirm the selected release, host, resource shape and no-volume/no-debug policy, then create **one** new instance under the operator's actual-cost authorization. Unknown/interrupted creation must be reconciled using passive authoritative status before any retry. Do not replace existing instances or promote a repository-wide latest release by default.
6. Test readiness and authenticated synthetic accounting. Prefer the installed verified Tinfoil CLI/proxy for operator access; a direct browser/curl connection alone does not verify enclave identity. No new gateway/client verifier implementation is included. Record observed outcomes, not inferred billing or completion.

## Public evidence

- [Configuration reference](https://docs.tinfoil.sh/containers/configuration): public config repository, resource shapes, immutable images, secret declarations and HTTP exposure.
- [First instance](https://docs.tinfoil.sh/containers/quickstart): measured/signed release, private application image option, read-only root/scratch requirements and verified CLI requests.
- [Pricing](https://tinfoil.sh/pricing): hourly CPU/memory billing; no exact rate established by the accessed content.

Installed CLI help was inspected locally. It provides `container create --repo --tag`, defaults release promotion on, and uses `--promote-release false` to decline it. Follow the installed version's supported flags rather than copying differing documentation examples. The separate public repository and scope README were created/published under explicit authorization. No image/config release publication, registry mutation, secret write, paid instance creation or deployment has been performed yet.
