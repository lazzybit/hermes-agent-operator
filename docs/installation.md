# Installation and first instance

Run the commands below from the repository root. This is an experimental setup;
resource reconciliation does not establish application readiness.

## Prerequisites

- A Kubernetes cluster with a compatible storage class and ingress/egress
  NetworkPolicy enforcement. Review [network requirements](network-policy.md),
  including the expected CoreDNS labels and public TCP 443 egress restrictions.
- Cluster privileges for CRD installation and administration of a dedicated
  namespace. Runtime controller access is namespaced, including Secret access.
- Tested, preferably digest-pinned controller, gateway, and SSH terminal images.
  The gateway image is `ghcr.io/lazzybit/hermes-agent-gateway`, built from the
  pinned official Hermes agent image. A custom gateway image must implement
  `/usr/local/bin/hermes-gateway-init` or home, config, credential, and plugin
  integration will not work. Release CI currently builds project images for
  Linux ARM64.
- Provider and messaging credentials appropriate to your Hermes configuration.

## Install the operator

Pin the controller image in `config/manager/manager.yaml`, review the manifests,
then install:

```sh
kubectl apply -k config/default
```

The default namespace is `hermes-lab`. If you change it, update the Kustomize
namespace, Namespace resource and your declarations consistently. The controller
uses `WATCH_NAMESPACE` from its Pod namespace and cannot watch the entire cluster.

## Prepare credentials

Create separate local dotenv files for the gateway and each profile, containing
only the credentials they need. Do not commit these files.

```sh
kubectl -n hermes-lab create secret generic gateway-credentials \
  --from-file=.env=/secure/path/gateway.env
kubectl -n hermes-lab create secret generic profile-credentials-assistant \
  --from-file=.env=/secure/path/assistant.env
```

The sample references these names; references are configurable. See
[profile-scoped forwarding](profile-environment.md) when terminal commands need
selected credentials.

## Managed SSH

No SSH setup is required. Copy `config/samples/foundation.yaml`, select tested
images and populate the native Hermes settings in `spec.config`. The operator
creates an Ed25519 CA Secret named `gateway-ssh-ca`, signs profile client and host
certificates, and supplies terminal startup and connection configuration.

The generated CA and managed SSH identity Secrets are owned by the Hermes resource
and garbage-collected when it is deleted. PVC retention is unchanged. Back up the
CA if recovery is required; a missing CA with existing identities produces an error
rather than silently changing trust. Recreating a deleted Hermes requires old SSH
identity Secrets to have been garbage-collected before new identities are issued.

CA selection is not exposed in the API. Optional `spec.ssh` only overrides
cluster domain and certificate lifetime defaults; it is not an enable switch.
See [terminal compatibility](terminal.md).

## Apply and inspect

```sh
kubectl apply -f my-hermes.yaml
kubectl -n hermes-lab get hermes,hermesprofiles -o yaml
kubectl -n hermes-lab get deployments,pods,services,pvc
kubectl -n hermes-lab logs deployment/hermes-agent-controller
```

`ResourcesReconciled=True` reports object reconciliation only. `Ready` remains
false with `IntegrationPending` when references are valid. Check Pod events,
initialization, actual Hermes messaging and SSH operations independently; do not
wait for a Ready condition this version does not implement.

PVCs are retained when resources are deleted. Review [lifecycle and storage
behavior](foundation.md) before removal, reinstallation or migration.
