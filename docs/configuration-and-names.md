# Configuration and resource names

## Current configuration model

Each namespace contains one Hermes gateway. Administrators maintain nonsecret Hermes YAML directly as an arbitrary object in `Hermes.spec.config` and `HermesProfile.spec.config`. Unknown nested Hermes settings are preserved; this API does not duplicate the upstream configuration schema. The controller merges only the SSH settings it manages and writes one ConfigMap per home. `credentialsSecret` remains a same-namespace dotenv Secret reference. SOUL.md and other mutable Hermes state stay writable in the home volume.

The gateway Deployment uses `Hermes.spec.image` for both its main container and its init container. The image to run is `ghcr.io/lazzybit/hermes-agent-gateway`, built from the pinned official `nousresearch/hermes-agent` image with `/usr/local/bin/hermes-gateway-init`. `Hermes.spec.command` still starts the upstream gateway. A custom `spec.image` is allowed, but a custom image that does not implement that init entry loses home bootstrap, config and credential projection, plugins.json handling, MessagingChannel setup, and any later init steps. The published operator and terminal images remain `ghcr.io/lazzybit/hermes-agent-operator` and `ghcr.io/lazzybit/hermes-agent-terminal`. Configuration, selected credential, `spec.plugins`, and bound channel changes automatically roll out the gateway. SSH certificate renewal, native environment passthrough, private-key projections, NetworkPolicies, privilege-escalation prohibition and persistent home layout remain in place. Optional `spec.plugins` and claimed channels are projected in `gateway-resources` at `/run/hermes/resources/plugins.json` and `/run/hermes/resources/messaging-channel.json`, each keyed by profile name. For Telegram, `spec.telegram.id` is the bot username without `@` and `spec.telegram.name` is the display name the init reconciles with `setMyName`. The token stays in its Secret.

| Resource | Name |
| --- | --- |
| Controller Deployment, ServiceAccount, Role, RoleBinding, Lease | `hermes-agent-controller` |
| Gateway Deployment and NetworkPolicy | `gateway` |
| Gateway retained data PVC | `gateway-data` |
| Gateway managed ConfigMap | `gateway-config` |
| Recommended gateway credential Secret | `gateway-credentials` |
| Profile CR | actual profile name |
| Profile terminal Deployment, Service, NetworkPolicy | `terminal-<profile>` |
| Profile retained home PVC | `profile-data-<profile>` |
| Profile retained terminal workspace PVC | `terminal-data-<profile>` |
| Profile managed ConfigMap | `profile-config-<profile>` |
| Gateway resources ConfigMap | `gateway-resources` |
| MessagingChannel | actual channel name |
| Recommended profile credential Secret | `profile-credentials-<profile>` |
| Profile SSH identity Secret | `profile-ssh-identity-<profile>` |
| Profile public SSH ConfigMap | `profile-ssh-config-<profile>` |
| Automatically managed CA Secret | `gateway-ssh-ca` |

Credential and CA names remain selected references. API group and Kind names, upstream Hermes commands and identity-derived user principals do not change. SSH host certificates, known_hosts and client configuration must match the new terminal Service DNS names.

## Legacy configuration and migration

The current controller reads nonsecret settings only from `spec.config`. Legacy
`configMap`, `configSecret` and `helperImage` fields remain accepted for older
controllers but are ignored here. Convert the previous configuration source's
`config.yaml` to the CR's `spec.config` object and use `credentialsSecret` for its
selected dotenv file. Do not put secrets in the public configuration object.

Current resource names are listed above. Legacy workloads or retained claims can
require manual migration: the controller rejects conflicting ownership and legacy
persistent claim layouts rather than adopting or copying them automatically.
See `controller/internal/controller/migration.go` for the detection rules.

For a resource rename, stop the old controller and affected workloads before
starting replacements, and wait for old Pods to terminate to avoid duplicate
gateway writers. Preserve retained PVCs and their PV reclaim policy. Copy or
restore data only during an explicitly planned maintenance window, preserving
ownership, permissions, dotfiles, symlinks and the native home layout. Destination
claims must satisfy the controller's retained identity and original storage-class
annotations. Verify restored data before restarting workloads; do not delete PVCs
as part of name cleanup.

SSH identity migrations must preserve private keys and validate the CA and identity.
Changing Service DNS names requires matching host certificates and client settings.
There is no automatic CA or identity migration. Do not replace a rejected identity
with new keys merely to bypass a reconciliation error.

Shared CRDs may also be consumed by older controllers. Review schema compatibility
separately from migrating individual resources. This repository does not prescribe
a private infrastructure repository, Argo CD workflow or automated data-transfer hook.
