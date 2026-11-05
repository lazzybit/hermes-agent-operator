# Image packaging and release

| Image | Build file | Runtime |
| --- | --- | --- |
| `ghcr.io/lazzybit/hermes-agent-operator` | `Containerfile` | `/manager`, UID 65532 |
| `ghcr.io/lazzybit/hermes-agent-terminal` | `images/terminal/Containerfile` | sshd, agent UID 65532, TCP 2222 |

Gateway and initialization both use `Hermes.spec.image`, pinned in the sample to
`nousresearch/hermes-agent:v0.22.7@sha256:e6d81ae52ebf6a43efa529701405509f20054ba818f179ddd748bed4f8af584c`.
The official runtime owns its CLI, profile creation and installation metadata.

The terminal derives from official `nousresearch/hermes-sandbox:desktop` index
`sha256:c7d8607d076f26a64e0bd6828f74e1764af43fe3281368188459bb693bfa7567`,
which includes ARM64. Its thin layer adds openssh-server and the existing login and
workspace setup. It does not rebuild the sandbox tool environment or replace sshd.

## Checks

```sh
go test ./...
HERMES_SSH_INTEGRATION=1 SSHD=/usr/sbin/sshd go test ./internal/ssh -run TestOpenSSH -v
CGO_ENABLED=0 go build -trimpath -o bin/manager .
```

The real SSH check consumes configuration and certificates produced by the Go
signer. It exercises matching logins, cross-profile principal rejection, incorrect
host identity, expiry and SendEnv/AcceptEnv value preservation. It requires an
unlocked local account, openssh-client and openssh-server. Source tests do not
claim Kubernetes projection or CNI acceptance.

CI runs on native `ubuntu-24.04-arm`. Only literal `canary` and stable `vX.Y.Z`
tags publish images. Each candidate is built once with Buildx `load: true`, then
tested and pushed without rebuilding. The controller executes `--help`; terminal
executes the colocated Go SSH test binary as the packaged agent user against its
installed sshd. Test payloads are mounted read-only, not included in runtime images.
There is no independent upstream official-image test gate or Python harness.

## Delivery

Push tested changes directly to source main. Observe the current remote annotated
`canary` tag object and replace it with an explicit force-with-lease. Verify its
peeled target, every tag workflow step and anonymous GHCR manifest/config revision
for both images before selecting digests in infrastructure.

Use a fresh private infra branch and PR for canary pins and required compatible
schema/config updates. Merge the authorized PR and read back main. Preserve stable
pins, encrypted credential payloads and the sole shared CRD owner. Do not add a
migration hook. Argo automated sync follows the infra branch; a merged PR alone
does not prove actual Pod adoption or application readiness.
