# Development, images and release

## Component boundaries

- `controller/`: one Go module, entrypoint `main.go`, API types under `api/v1alpha1`,
  reconciliation under `internal/controller`, SSH signing and tests under
  `internal/ssh`, and its own `Containerfile`.
- `gateway/`: an independent Go module and `Containerfile`. It builds
  `ghcr.io/lazzybit/hermes-agent-gateway` from the pinned official Hermes agent
  image and installs one init entry. It has no build dependency on the controller.
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
docker build -f gateway/Containerfile -t hermes-agent-gateway:dev gateway
docker build -f terminal/Containerfile -t hermes-agent-terminal:dev terminal
```

The published names are `ghcr.io/lazzybit/hermes-agent-operator`,
`ghcr.io/lazzybit/hermes-agent-gateway`, and
`ghcr.io/lazzybit/hermes-agent-terminal`. The gateway image starts from the
pinned official `nousresearch/hermes-agent:v0.22.7` image
(`sha256:e6d81ae52ebf6a43efa529701405509f20054ba818f179ddd748bed4f8af584c`).
This project does not rebuild the upstream Hermes runtime. See
[terminal compatibility](terminal.md) and the gateway contract below.

## Gateway image and init contract

`gateway/Containerfile` copies one program to `/usr/local/bin/hermes-gateway-init`
and leaves the upstream entrypoint in place. `Hermes.spec.image` selects the
image for both the gateway container and its init container. `Hermes.spec.command`
still starts the upstream Hermes gateway. The init container runs only
`/usr/local/bin/hermes-gateway-init`.

Use the published gateway image unless you are replacing that integration.
A custom image is allowed: set `Hermes.spec.image` to it. A custom image that
does not implement this same init entry and behavior loses the operator's
integration. Home bootstrap, config and credential projection, plugins.json
fetch, MessagingChannel setup, and any later CR-driven init steps will not
run. The operator does not fall back to an embedded script.

The init entry accepts:

```text
--profile       profile name; empty for the instance home
--config        /run/hermes-config/<volume>, containing config.yaml
--credentials   /run/hermes-credentials/<volume>/.env, or empty
--plugins       /run/hermes/resources/plugins.json
--plugins-dir   /run/hermes/resources/plugins/<profile> emptyDir
--plugin-deps   /run/hermes-plugin-deps/<profile> emptyDir
--messaging     /run/hermes/resources/messaging-channel.json
--generated     /run/hermes/generated
```

For the instance home, `<profile>` is `default`. The program creates the home
with upstream `hermes profile create` when the claim is new, keeps a legacy
home at the claim root, links `config.yaml` and `.env` into `$HERMES_HOME`,
and publishes `.hermes-home`. It does not delete `SOUL.md` or other mutable
state. Profile homes are linked from the instance profile directory.

`spec.plugins` is optional. Omission or an empty list installs nothing. When
set, the controller projects the declaration in `gateway-resources` at
`/run/hermes/resources/plugins.json` as an object keyed by profile name
(the `--profile` value; the instance home uses `""`), and includes its
checksum in the gateway rollout annotation. The init fetches each pinned
tree into the emptyDir at `/run/hermes/resources/plugins/<profile>`
(`default` for the instance home) and points `$HERMES_HOME/plugins` at that directory.
Plugin source is not copied onto the retained PVC and is not stored in the
ConfigMap. A non-empty real `plugins/` directory is left in place and the init
fails, because replacing it would delete existing data.

Each entry requires `name`, an `https` `repository` without credentials, and a
full 40-character commit `revision`. Tags and branches are rejected. Optional
`path` is a subdirectory and must not contain `..`. If a fetched plugin has
`pyproject.toml` dependencies, the init installs them into the dependency
emptyDir with the `uv` binary shipped in the Hermes image. The sealed
runtime venv has no `pip` module, so the init does not run `python -m pip`,
`hermes plugins`, or the catalog. Official images disable lazy installs, so
mounting the files alone does not make those imports available. The gateway
process sets `PYTHONPATH` to each dependency directory. The official Hermes
launcher unsets that variable and loads extra paths only through
`site.addsitedir(HERMES_SITE)`, so the image ships a static `.pth` that
points at `/run/hermes/generated/plugin-deps`. The container command runs
`/usr/local/bin/hermes-plugin-path` first to materialize those directories
there, then execs the official `hermes`.
A `sitecustomize.py`
written there moves those directories behind the sealed environment on
`sys.path`, so an already installed core package stays ahead of a plugin
dependency of the same name. Setting `memory.provider` remains
`HermesProfile.spec.config`; the init does not enable plugins or select a
memory provider.

A profile may set `spec.messaging` to claim one namespaced `MessagingChannel`.
`platform` is immutable and, for `telegram`, requires `spec.telegram` only.
`spec.telegram.id` is the bot username without `@` (`aaa_bot` for `@aaa_bot`),
not the numeric user id. `spec.telegram.name` is the display name. The init
reads `/run/hermes/resources/messaging-channel.json`, an object keyed by
profile name whose value is the claimed channel (`platform`, `name`,
`tokenPath`, and `telegram`). It checks `getMe` username against
`id`, and calls Bot HTTP API `setMyName` when the live display name differs.
It does not call MTProto and does not try to change the @username. The bot
token stays in the referenced Secret, mounted at
`/run/hermes/resources/channels/<channel>/token`. The init writes
`TELEGRAM_BOT_TOKEN` into `/run/hermes/generated/<profile>/.env` and links the profile
`.env` there, leaving the user's `credentialsSecret` unchanged. Profiles
without `spec.messaging` keep today's credential projection and receive no
channel. A custom image that does not implement this init behavior loses
channel integration.

Later CR-driven setup is added to this init program. The controller's job is
to mount the declared files and invoke the entry.

Run the init tests from `gateway/`:

```sh
go test ./...
```

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
pushed. The controller candidate runs `--help`; the gateway candidate runs
`/usr/local/bin/hermes-gateway-init --help`; the terminal candidate runs the
SSH integration test binary as its packaged agent user. That test binary is built
in the controller job context and mounted read-only, never included in the image.
The matrix is not an atomic publication transaction: one image can publish even
if another fails.

Before deploying a release, check both image publications and their source revision,
select immutable digests and validate in a test namespace. CI publication and
successful reconciliation do not establish application readiness. Infrastructure
promotion is deployment-specific and is not automated by this repository.
