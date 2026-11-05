# hermes-agent-operator

Manage [Hermes Agent](https://hermes-agent.nousresearch.com/docs/) gateways and
profiles declaratively on Kubernetes. One namespaced controller reconciles a shared
gateway, separate profile homes and terminal workspaces, SSH certificates and
network policies. Configuration comes from custom resources; credentials stay in
Kubernetes Secrets and runtime state remains writable.

This is an experimental controller. Resource reconciliation is tested; status keeps
application readiness pending until live integration is established.

## Quick start

Install Go 1.24 or newer and select published controller/terminal image digests in
`config/manager/manager.yaml` and your profile declarations.

```sh
go test ./...
CGO_ENABLED=0 go build -trimpath -o bin/manager .
bin/manager --help
go run sigs.k8s.io/kustomize/kustomize/v5@v5.6.0 build config/default
```

Install the rendered resources in a dedicated namespace, supply an SSH CA Secret,
and adapt [the generic sample](config/samples/foundation.yaml). Gateway and native
profile initialization use the same pinned official Hermes image. Each profile
selects its own configuration, credential Secret and terminal image.

## Configuration and boundaries

- `Hermes` declares the gateway image, configuration, storage and optional SSH CA.
- `HermesProfile` declares a parent, configuration, credential Secret and terminal.
- `spec.config` is rendered into `gateway-config` or `profile-config-<profile>`;
  managed SSH connection fields are overlaid automatically.
- Profiles use native dotenv loading, environment passthrough and credential-file
  synchronization. See [profile environment](docs/profile-environment.md).
- Homes and workspaces use retained PVCs, or opt into disposable `emptyDir` storage.
- A single CA signs distinct profile client and terminal host certificates. Each
  terminal accepts its assigned principal. Keys are projected directly from Secrets.

Terminal Pods do not mount gateway code, profile homes or Kubernetes API tokens.
The shared gateway remains a common trust boundary. Hermes owns native SSH commands,
file synchronization and environment selection. Its command-line `accept-new` option
overrides stricter generated host-checking defaults. NetworkPolicy enforcement
requires a compatible CNI.

## Development and delivery

The project has one root `main.go` and `Containerfile`, API types under
`api/v1alpha1`, reconciliation under `internal/controller`, SSH signing and transport
tests under `internal/ssh`, and installation manifests under `config`. The thin
terminal image lives at `images/terminal/Containerfile`. Go tests live beside code.

CI tests project-produced resources, configuration and SSH behavior. Release tags
build each operator/terminal candidate once, test it and push that same artifact to
`ghcr.io/lazzybit/hermes-agent-operator` and
`ghcr.io/lazzybit/hermes-agent-terminal`. Gateway uses official
`nousresearch/hermes-agent`; this repository does not retest upstream runtime internals.

Source refactors go directly to main. Literal `canary` publication is followed by
verified image digests and a canary-only infrastructure PR; stable pins remain unchanged.

- [Configuration, ownership and initialization](docs/foundation.md)
- [Resource names](docs/configuration-and-names.md)
- [Images, checks and release](docs/packaging.md)
- [Network policy](docs/network-policy.md)
- [Simplification contract](docs/simplification.md)
