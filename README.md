# hermes-agent-operator

Run [Hermes Agent](https://hermes-agent.nousresearch.com/docs/) on Kubernetes with
declarative profiles, persistent workspaces and managed SSH terminals.

## What you get

- **One shared gateway, multiple profiles.** Give each profile its own configuration,
  credentials, home directory and terminal workspace.
- **A terminal built for agent work.** The terminal image extends the official Hermes
  desktop sandbox with OpenSSH; use the supplied image or build a compatible one
  with your own tools.
- **Managed SSH identities.** An externally supplied CA signs separate client and
  host certificates. The operator configures connections and renews certificates.
- **Durable or disposable storage.** Retained PVCs preserve homes and workspaces;
  opt into `emptyDir` for disposable environments.
- **Declarative updates.** Configuration and selected credential changes trigger
  gateway rollouts. NetworkPolicies constrain gateway and terminal traffic.

The controller is experimental. It reconciles resources, but application readiness
remains `Ready=False/IntegrationPending`; live Kubernetes integration is not yet
established. Profiles share a trusted gateway, not a hard tenant isolation boundary.

## Get started

You need a Kubernetes cluster with storage provisioning, a NetworkPolicy-capable
CNI and permission to install CRDs. Select tested controller and terminal image
versions before installing.

1. Clone this repository and review `config/default` for the dedicated `hermes-lab`
   namespace. Pin the controller image in `config/manager/manager.yaml`.
2. Install the operator:

   ```sh
   kubectl apply -k config/default
   ```

3. Prepare your credential Secrets, SSH CA and resource declarations using the
   [installation guide](docs/installation.md). The
   [sample](config/samples/foundation.yaml) shows a gateway and one profile;
   it is a starting point, not a working provider or SSH configuration.
4. Apply your adapted declarations and inspect the resources:

   ```sh
   kubectl apply -f my-hermes.yaml
   kubectl -n hermes-lab get hermes,hermesprofiles
   kubectl -n hermes-lab get deployments,pods,pvc
   ```

Keep nonsecret Hermes settings in `spec.config`, credentials in referenced
Secrets, and mutable agent state in its home. Add another `HermesProfile` to
provision another terminal workspace.

## Documentation

- [Install and configure your first instance](docs/installation.md)
- [Configuration, storage and lifecycle](docs/foundation.md)
- [SSH terminal images and compatibility](docs/terminal.md)
- [Profile environment and credential files](docs/profile-environment.md)
- [Resource names and migration](docs/configuration-and-names.md)
- [Network access and limitations](docs/network-policy.md)
- [Development, image builds and release](docs/packaging.md)

## Repository layout

`controller/` contains the Go module and controller image build. `terminal/`
contains the independent SSH image build, with no Go build dependency. `config/`
contains operator installation manifests and samples; `docs/` contains detailed
guides and references.
