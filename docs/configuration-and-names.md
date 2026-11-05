# Configuration and resource names

## Approved design

Each namespace contains one Hermes gateway. Administrators maintain nonsecret Hermes YAML directly as an arbitrary object in `Hermes.spec.config` and `HermesProfile.spec.config`. Unknown nested Hermes settings are preserved; this API does not duplicate the upstream configuration schema. The controller merges only the SSH settings it manages and writes one ConfigMap per home. `credentialsSecret` remains a same-namespace dotenv Secret reference. SOUL.md and other mutable Hermes state stay writable in the home volume.

The gateway and native `hermes profile create` initialization use the same `Hermes.spec.image`, from the official `nousresearch/hermes-agent` image. The published operator and terminal images remain `ghcr.io/lazzybit/hermes-agent-operator` and `ghcr.io/lazzybit/hermes-agent-terminal`. Configuration and selected credential changes automatically roll out the gateway. SSH certificate renewal, environment bridging, private-key projections, NetworkPolicies, privilege-escalation prohibition and persistent home layout remain in place.

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
| Recommended profile credential Secret | `profile-credentials-<profile>` |
| Profile SSH identity Secret | `profile-ssh-identity-<profile>` |
| Profile public SSH ConfigMap | `profile-ssh-config-<profile>` |
| Recommended external CA Secret | `ssh-ca` |

Credential and CA names remain selected references. API group and Kind names, upstream Hermes commands and identity-derived user principals do not change. SSH host certificates, known_hosts and client configuration must match the new terminal Service DNS names.

## Delivery and migration

This document precedes implementation. Implement configuration/schema, systematic names and focused regressions, then push main and publish the literal `canary` tag. Release CI builds each ARM64 candidate once, tests the loaded candidate and pushes that same image. Verify both published revisions before merging the canary-only private infrastructure PR. Argo CD performs its normal automated sync. Stable image pins and stable resource declarations remain unchanged. Shared CRDs have one infrastructure owner; optional config and deprecated legacy fields keep the independently pinned stable controller admissible.

The new controller consumes only `spec.config`. Convert the previous nonsecret source ConfigMap's `config.yaml` into that object, remove the source ConfigMap from desired infrastructure and stop mounting the previous generated ConfigMap. No runtime-config/generated-config pair remains.

Resource renames are replacement operations. Before creating the new gateway, delete the old controller-owned gateway Deployment using foreground deletion and wait for its disappearance (including dependent Pods); this prevents two Telegram writers. Apply the same fencing to old terminal Deployments. Delete obsolete controller-owned Services, NetworkPolicies and generated ConfigMaps only after replacement dependencies exist. Refuse to delete foreign resources.

Persistent migrations require an explicit maintenance window: stop the old controller and gateway/terminals, wait for all old Pods to terminate, retain old PVCs and their PV reclaim policy, copy or restore each old claim (`<instance>-home`, `<profile>-profile`, `<profile>-workspace`) into its corresponding new claim, preserving ownership, modes, dotfiles, symlinks and the native home layout. Record the same retained-for and requested-storage-class annotations required by the controller on destination claims. Verify the restored data before starting the new controller. Existing old persistent claims cause reconciliation to stop rather than silently creating empty replacements. PVCs are never deleted by rename cleanup. Automated PVC transfer/adoption is intentionally deferred; ephemeral canary homes need no transfer.

Existing SSH identity state must retain its user and host private keys. Copy an existing legacy identity into the new Secret name with normal create conflict handling, validate its identity and CA, and reissue only its host certificate for the renamed Service. Never treat foreign or invalid state as permission to generate replacement keys. Keep the old Secret available during migration; do not rotate the external CA. Infrastructure may rename an encrypted CA using an unencrypted metadata patch without modifying its SOPS payload.

Controller ServiceAccount/RBAC must be available no later than the controller Deployment's Argo sync wave. Remove the old controller before activating the renamed controller; pruning last is insufficient for singleton controller replacement.
