# Controller simplification

## Approved implementation target

Keep one executable at `main.go`, one root `Containerfile`, `api/v1alpha1`,
`internal/controller`, `internal/ssh`, installation resources under `config`, and
one thin terminal image at `images/images/terminal/Containerfile`. Go tests live beside
code. Use direct Go commands; remove component layers, Makefile aliases, the
standalone Python harness and tests of the upstream official runtime.

The controller continues to own reconciliation, configuration overlays, storage,
network resources, certificate signing and renewal. Gateway and initialization
use the same official `Hermes.spec.image`. Private keys remain direct Secret
projections with mode 0440 and fsGroup. Configuration remains a CR object rendered
into one ConfigMap per home, credentials stay in Secrets, and SOUL.md remains
writable runtime state. Preserve automatic configuration/credential rollouts.

## Native environment and files

Replace the terminal Pod marker/environment bridge and sshd binary wrapper with
Hermes profile credential loading and native `terminal.env_passthrough`. Values
come from that profile's `credentialsSecret` dotenv file; names come from its
`spec.config.terminal.env_passthrough`. The generated server accepts SSH environment
requests. Do not inject profile secrets into the shared gateway process environment.
Hermes applies its profile secret scope when selecting forwarded values.

This deliberately changes the credential location from terminal-only Pod env to
profile-scoped gateway credentials. Deprecated `spec.env` remains accepted for
older pinned stable controllers. This controller keeps its ordinary terminal Pod
env injection for compatibility, but no longer turns those values into SSH session
environment; migrate SSH consumers explicitly. Never silently copy or merge Secret
values, or expose them in a ConfigMap. Native `terminal.credential_files` delivers
explicit credential files; delivering a dotenv file does not automatically export it.

The terminal image derives from immutable official `nousresearch/hermes-sandbox:desktop`
index `sha256:c7d8607d076f26a64e0bd6828f74e1764af43fe3281368188459bb693bfa7567`
with an ARM64 variant. Add only OpenSSH server, the existing login account and
workspace setup. Keep `allowPrivilegeEscalation: false` and the existing terminal UID.

## Delivery sequence

Update these documents first, implement the single-project layout and native
passthrough, then execute colocated Go resource/configuration/certificate and real
SSH tests. Candidate packaging checks cover project payload, not upstream runtime
behavior. Build each candidate once on native ARM CI, test it and push the same image.

Push tested commits directly to source main. Update the literal annotated `canary`
tag with an observed-object force-with-lease. Verify its peeled target, every tag
CI job and anonymous GHCR manifest/config revision. Update the two canary digest
pins in a fresh private infra branch, deliver and merge its authorized PR. Preserve
stable/recovery resources, encrypted payloads and the sole shared CRD owner; do not
add migration hooks. Infrastructure merge and actual cluster adoption are distinct.
