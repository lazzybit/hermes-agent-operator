# Installation and first instance

Run the commands below from the repository root. This is an experimental setup;
resource reconciliation does not establish application readiness.

## Prerequisites

- A Kubernetes cluster with a compatible storage class and ingress/egress
  NetworkPolicy enforcement. Review [network requirements](network-policy.md),
  including the expected CoreDNS labels and public TCP 443 egress restrictions.
- Cluster privileges for CRD installation and administration of a dedicated
  namespace. Runtime controller access is namespaced, including Secret access.
- Tested, preferably digest-pinned controller, official Hermes gateway and SSH
  terminal images. Release CI currently builds project images for Linux ARM64.
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

## Enable managed SSH

Supply an externally managed OpenSSH CA private key in a namespace Secret:

```sh
kubectl -n hermes-lab create secret generic ssh-ca \
  --from-file=ca_key=/secure/path/ssh-ca
```

The operator does not create your CA. Protect and back it up separately. Its
controller can read namespace Secrets and must be treated as trusted.

Copy `config/samples/foundation.yaml` to your own manifest. Select tested image
pins, populate each `spec.config`, and add the following to the `Hermes` spec:

```yaml
ssh:
  caSecretRef:
    name: ssh-ca
    key: ca_key
```

For its `HermesProfile`, set `terminal.command` to exactly:

```yaml
command: [/usr/sbin/sshd, -D, -e, -f, /run/hermes-ssh/sshd_config]
```

The current API still requires that command explicitly and rejects incompatible
commands when parent SSH is enabled. The controller provides SSH configuration,
keys and certificates. See [terminal requirements](terminal.md) for custom images.
The unmodified sample leaves SSH provisioning disabled and does not establish an
authenticated terminal service.

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
