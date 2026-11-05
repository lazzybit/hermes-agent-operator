# Development, images and release

## Component boundaries

- `controller/`: one Go module, entrypoint `main.go`, API types under `api/v1alpha1`,
  reconciliation under `internal/controller`, SSH signing and tests under
  `internal/ssh`, and its own `Containerfile`.
- `terminal/`: an independent `Containerfile` extending the official sandbox with
  OpenSSH and the runtime account. It has no Go module or build dependency on the
  controller.
- `config/`: operator CRDs, namespaced RBAC, controller Deployment, Kustomize
  installation and examples.

## Build and test the controller

Go 1.24 or newer is required. Dependencies are pinned in the controller module.
Run from `controller/`:

```sh
go test ./...
CGO_ENABLED=0 go build -trimpath -o bin/manager .
bin/manager --help
```

Regenerate API objects and CRDs from that same directory:

```sh
go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.18.0 object crd paths=./api/... output:crd:artifacts:config=../config/crd/bases
go run api/v1alpha1/generate-schema.go
```

Render installation manifests from the repository root:

```sh
CGO_ENABLED=0 go run sigs.k8s.io/kustomize/kustomize/v5@v5.6.0 build config/default
```

That rendering command uses Go's version-qualified tool invocation; it does not
require a Go module at the repository root. Alternatively use `kubectl kustomize`.

## Build images independently

From the repository root:

```sh
docker build -f controller/Containerfile -t hermes-agent-operator:dev controller
docker build -f terminal/Containerfile -t hermes-agent-terminal:dev terminal
```

The published names are `ghcr.io/lazzybit/hermes-agent-operator` and
`ghcr.io/lazzybit/hermes-agent-terminal`. Gateway and home initialization use the
same official image selected by `Hermes.spec.image`; this project does not build
the upstream Hermes runtime. See [terminal compatibility](terminal.md).

## SSH integration checks

From `controller/`, with openssh-client, openssh-server and an unlocked local
account available:

```sh
HERMES_SSH_INTEGRATION=1 SSHD=/usr/sbin/sshd go test ./internal/ssh -run TestOpenSSH -v
```

The check consumes generated configuration and certificates. It covers successful
logins, cross-profile principal rejection, incorrect host identity, certificate
expiry and SendEnv/AcceptEnv preservation. It does not prove Kubernetes volume
projection, scheduling, network enforcement or upstream Hermes integration.

## CI and publication

`.github/workflows/ci.yml` runs on native `ubuntu-24.04-arm`, using Go 1.24.13.
Main pushes, pull requests and manual dispatch run Go and real OpenSSH tests.
Tag pushes also run release validation: literal `canary` and `vMAJOR.MINOR.PATCH`
without leading zeroes are accepted. Stable image tags omit the leading `v`.

The publication matrix uses separate component build contexts. Each Linux ARM64
candidate is built once, loaded locally and tested before that same image is
pushed. The controller candidate runs `--help`; the terminal candidate runs the
SSH integration test binary as its packaged agent user. That test binary is built
in the controller job context and mounted read-only, never included in the image.
The matrix is not an atomic publication transaction: one image can publish even
if the other fails.

Before deploying a release, check both image publications and their source revision,
select immutable digests and validate in a test namespace. CI publication and
successful reconciliation do not establish application readiness. Infrastructure
promotion is deployment-specific and is not automated by this repository.
