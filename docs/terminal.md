# SSH terminal images

Each profile gets a separate terminal Deployment, ClusterIP Service and workspace.
The gateway reaches it using the native Hermes SSH backend. The terminal does not
mount gateway code, profile home volumes or a Kubernetes API token.

## Supplied image

`terminal/Containerfile` builds `ghcr.io/lazzybit/hermes-agent-terminal` from the
pinned official `nousresearch/hermes-sandbox:desktop` base. It adds OpenSSH server,
an `agent` account and runtime directories. It does not build or install the
controller or depend on Go. Browser and development tooling come from the official
base; the project does not rebuild that environment.

The official base alone is not the project's SSH terminal: the thin layer supplies
the server and account required by the controller. No private keys are built into
the image.

## Ownership and custom images

`HermesProfile.spec.terminal.image` explicitly selects an image. There is currently
no controller-provided image default. Users may supply a compatible custom image,
including a derivative of the supplied terminal with extra development tools.

The image must provide:

- `/usr/sbin/sshd` with OpenSSH certificate authentication support.
- An unlocked, public-key-login-capable `agent` account with UID/GID 65532 and a
  usable shell and home. Password authentication is disabled by generated config.
- Support for running as UID/GID 65532, with all capabilities dropped,
  `allowPrivilegeEscalation: false` and RuntimeDefault seccomp.
- Writable `/tmp` for the PID file and a usable mounted `/workspace`.
- Required OpenSSH runtime directories, including `/run/sshd` where applicable.

The controller always sets the SSH Pod command explicitly, overriding image ENTRYPOINT.
Legacy `terminal.command` values are ignored.
The CR only needs to select the image and workspace:

```yaml
terminal:
  image: ghcr.io/lazzybit/hermes-agent-terminal:canary
  storage:
    size: 5Gi
```

Use a tested digest in deployed resources rather than relying on a mutable tag.
The controller does not inspect the image filesystem or execute its original
entrypoint to create users. Do not depend on image CMD arguments; compatible
images should use the supplied terminal command without additional CMD arguments.

## Configuration and certificates

The controller generates sshd configuration, principal authorization and gateway
client settings. It projects distinct user and host keys/certificates from a
managed Secret, never the CA private key. Private keys are read-only Secret
projections with mode 0440 and the workload fsGroup.

The daemon listens on Pod TCP 2222. The Service exposes TCP 22 targeting 2222;
NetworkPolicies use the Pod port. SSH authenticates only the assigned profile
principal as `agent`. Host-certificate renewal changes the terminal Pod template
and causes a Recreate rollout. Client certificates update through live projections.

The gateway is shared and can reach its profiles' terminals. These controls are
not a tenant boundary against administrators who can modify executable images,
commands or namespace Secrets. The generated strict host configuration can also
be overridden by the upstream Hermes client's command-line `accept-new` behavior.
See [SSH trust and lifecycle](foundation.md) and [network policy](network-policy.md).

## Verification

CI tests the actual built terminal candidate with the Go SSH integration test
binary mounted read-only. The binary is built from `controller/internal/ssh`;
it is not copied into the runtime image. The test covers matching authentication,
principal rejection, host identity, expiry and SSH environment transport.
These checks do not establish live Kubernetes volume projection, CNI or Hermes
application integration.
