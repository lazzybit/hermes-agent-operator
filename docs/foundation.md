# Controller behavior and lifecycle

This reference describes the current controller implementation. Start with the
[installation guide](installation.md); live application integration remains pending.

## Build and test

Go 1.24 or newer is required. Dependencies are pinned in go.mod/go.sum.

Run from `controller/`:

```sh
go test ./...
CGO_ENABLED=0 go build -trimpath -o bin/manager .
go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.18.0 object crd paths=./api/... output:crd:artifacts:config=../config/crd/bases
go run api/v1alpha1/generate-schema.go
bin/manager --help
CGO_ENABLED=0 go run sigs.k8s.io/kustomize/kustomize/v5@v5.6.0 build ../config/default
```

The manager requires `--namespace` or `WATCH_NAMESPACE`, limits its cache to that namespace, and elects a leader using a Lease there. It will not watch the entire cluster. Kubernetes credentials are required only when actually starting the manager. The HTTP readiness probe describes manager availability, not Hermes health.

`config/default` is a Kustomize installation into a dedicated `hermes-lab` namespace. Build/load your own controller image and set the manager image before installation. CRD installation requires cluster privileges; runtime RBAC uses only a Role and RoleBinding in the target namespace. The controller can get/create/update Secrets in that namespace for SSH signing; see the sensitivity and trust boundary below. It watches namespace Secret metadata only to enqueue referencing instances; selected application credential bytes are read only for change detection. RBAC list/watch cannot enforce metadata-only responses, so this remains a trusted-controller boundary.

## API and generated resources

Both resources use `hermes.nousresearch.com/v1alpha1`. Names are DNS labels limited to 40 characters to leave room for generated suffixes. See `config/samples/foundation.yaml` for neutral, non-operational examples; replace image references with tested images. Gateway commands are explicit. Terminal startup is controller-owned; legacy terminal.command is optional and ignored.

- `Hermes.spec`: `image`, `command`, `storage`, optional `config`, `credentialsSecret`, and `plugins`, optional `ssh` (advanced overrides; managed SSH is always active).
- `HermesProfile.spec`: immutable same-namespace `hermesRef` (enforced by CRD transition validation), `storage`, `terminal.image`, `terminal.storage`, optional `config`, `credentialsSecret`, `plugins`, and `messaging`.
- `MessagingChannel.spec`: immutable `platform` and, for `telegram`, `telegram.id` (bot username without `@`), `telegram.name` (display name), and `telegram.tokenSecretRef`. Status is `Available` or `Bound` with `claimRef`.
- Each storage spec requires `size` (positive integer Mi/Gi/Ti), with optional `storageClassName`. `ephemeral: true` instead uses a Pod-local `emptyDir` and skips PVC creation; `size` remains required for API compatibility but is ignored in ephemeral mode, as is `storageClassName`. Set it independently for the instance home, profile home and terminal workspace. Data survives container restarts and is discarded on Pod replacement. Home init creates a private UID-owned child directory, mounted at the normal home path. Omitting `ephemeral` preserves retained storage. Switching modes never deletes existing claims; old retained data requires an explicit operator cleanup. Existing claims are not resized, replaced or deleted. Changes requiring migration produce a reconciliation error. An omitted class allows cluster defaulting. Each claim records the original request in `hermes.nousresearch.com/requested-storage-class`; `<omitted>` distinguishes omission from an explicit class, including an explicit empty string. Changing or removing an explicit class requires manual migration. Claims without this annotation require an operator to verify and record their original request before reuse; the controller cannot infer it from a defaulted claim.

A Hermes creates `gateway-data` and `gateway`. The gateway has one replica, Recreate updates, HERMES_HOME=/var/lib/hermes/.hermes-home, and a mount of its home claim. Referencing profiles add sorted mounts of `profile-data-<profile>` at `/run/hermes-profiles/<profile>`. Profile create/update/delete events enqueue the namespace's instances, so profile deletion removes its gateway mount. The API rejects changes to a profile reference; selecting a different parent requires recreating the profile and explicitly planning data migration. Mount changes update the Pod template.

A profile creates `profile-data-<profile>`, `terminal-data-<profile>`, and `terminal-<profile>` Deployment/ClusterIP Service on TCP 22. The terminal mounts only its workspace at `/workspace`, has no gateway/profile volumes, and receives no Kubernetes API token. It has one replica and Recreate updates. The project supplies a compatible image built from `terminal/Containerfile`; the CR still explicitly selects its image. A standalone SSH daemon configuration and command are generated automatically. A port declaration does not establish an authenticated SSH service or enforce networking.

Each instance and profile declares its nonsecret Hermes YAML object directly in `spec.config`.
Optional `credentialsSecret: {name: credentials, key: .env}` selects only its dotenv
file. The controller generates `gateway-config` / `profile-config-<profile>`, overlaying only its
native SSH connection settings for SSH-enabled profiles. ConfigMap and credential
directories mount independently from writable runtime state; configuration links
resolve to those projections. SOUL.md is neither required nor overwritten.

Gateway and init containers use the same `spec.image`, with official UID/GID
10000 and fsGroup 10000. The published gateway image is official Hermes plus
`/usr/local/bin/hermes-gateway-init`. The init container invokes that entry.
The main container still runs `spec.command`, which starts the upstream gateway.
A custom image may be selected, but if it does not implement this init entry
the operator cannot provide home, config, credential, or plugin integration.
Those steps are not generated in the controller. The init executes upstream
`hermes profile create` in a temporary home on the claim, then publishes the
newly created child home. It never creates a profile directly at a PVC
mountpoint. Existing legacy homes remain at the claim root; new homes live in
a UID-owned mode-0700 child. A stable `.hermes-home` link exposes either layout.
Profile claims mount separately and link into the instance's profile directory.
Existing nonempty parent directories that would conflict with these links
require an explicit data migration. `SOUL.md` stays writable state.
Terminal UID/GID stays 65532. Terminal Pods do not run this init and do not
mount plugin projections. All containers retain nonroot execution, dropped
capabilities, RuntimeDefault seccomp and `allowPrivilegeEscalation: false`.
Existing root-owned hostPath data still requires compatible ownership; fsGroup
alone cannot transfer ownership of old runtime files.

Optional `spec.plugins` is projected with every other home in
`/run/resources/plugins.json`. A missing entry or empty list does nothing.
Otherwise the init fetches each `repository` at the full
40-character commit `revision` into an emptyDir at `/run/hermes-plugins/<profile>`
(`default` for the instance home) and points `$HERMES_HOME/plugins` at it.
`path` selects a subdirectory and cannot contain `..`. Fetched source is not
written to the retained PVC or the ConfigMap. A pre-existing non-empty
`plugins/` directory is not deleted; init fails and leaves it in place. A
plugin `pyproject.toml` has its Python dependencies prepared into
`/run/hermes-plugin-deps/<profile>` because the official image disables lazy
installs. The gateway process puts those directories on `PYTHONPATH`. The init
writes `sitecustomize.py` there so Python appends them after the sealed
environment instead of letting them replace core modules. The init does not
run the Hermes catalog or `hermes plugins install`.
`memory.provider` stays in profile `spec.config`. The plugins document checksum
rolls the gateway the same way configuration checksums do. Plugin fetch uses
the gateway's existing public TCP 443 egress.

`spec.messaging` claims one `MessagingChannel` in the same namespace. A profile
holds at most one channel and a channel is bound to at most one profile.
`status.phase` is `Available` or `Bound`; `status.claimRef` names the profile.
Removing messaging or deleting the profile returns the channel to `Available`.
For `platform: telegram`, `spec.telegram.id` is the bot username without `@`
and `spec.telegram.name` is the display name. The init calls the Bot HTTP API
`setMyName` until that display name matches. It does not change the @username
and it does not use MTProto. The token remains in `tokenSecretRef` and is
mounted for the init; it is not copied into the CR, the resources document, or
the user's `credentialsSecret`. Profiles without `spec.messaging` are unchanged.

Stable ConfigMap/Secret names support automatic rollouts. Referenced input watches
enqueue only consuming instances. Deterministic public configuration checksums
change on settings updates, including the projected `plugins.json` document.
Selected credential bytes are compared in memory;
random tokens trigger restart on content changes without publishing credential
hashes or values. Metadata-only changes do not restart gateways. Controller
restart resets this in-memory comparison and can cause one rollout. A Pod rollout
preserves PVC data but discards emptyDir data. No manual config-revision annotation
or versioned configuration Secret name is required. Deprecated configSecret, configMap and
helperImage fields remain optional solely for independently pinned older controllers;
new-controller resources must migrate to spec.config and credentialsSecret.

From `controller/`, `go build .` builds the controller; `controller/Containerfile`
packages `/manager`. The separate terminal build contains no Go source.
No provider endpoints, tokens or personal configuration are embedded.

Generated workloads have controller owner references. Existing unowned or differently owned workloads are rejected rather than adopted. All PVCs have **no owner reference**, a retained-for annotation, and are retained unconditionally. Existing claims with any owner reference or a deletion timestamp are rejected even when the retained identity matches. Deleting/recreating an instance or profile with the same name reuses a matching retained claim; that is intentional data recovery behavior. Remove retained PVCs manually only after an explicit data decision. There is no destructive retention-policy field.

The parent instance does not own profile CRs. Deleting a Hermes garbage-collects its gateway, while profiles and their terminals remain independently declared. Terminal resources continue to reconcile, including updates and repair after deletion, when the parent is missing or deleting; no replacement gateway is created. Parent lifecycle events enqueue referencing profiles. Such profiles report `ReferenceValid=False` and `Ready=False` with reason `ReferenceInvalid`, while `ResourcesReconciled` reports independent resource reconciliation. Delete profile CRs explicitly when removing their terminal workloads. Runtime data is not written by the controller.

`ResourcesReconciled=True` means desired objects were reconciled, not that Pods are healthy. Errors set this condition false. `Ready` remains false with reason `IntegrationPending` when references are valid, even if workloads become available. Observed generations and conditions are persisted through status subresources. Periodic reconciliation repairs resources not covered by watches, including deleted PVCs. Fake-client tests exercise object behavior; they do not simulate scheduler, admission, garbage collection, storage attachment, or actual image execution. A separate test checks the generated CRD admission schema for the required immutable reference transition rule; real API-server admission enforces that rule.

## Default network policies

Gateway egress and terminal ingress/egress policies are reconciled automatically, independently of SSH provisioning. There is no networking opt-in or disable field. Gateway ingress remains administrator-managed. See [exact allowed traffic, upgrade behavior and enforcement limitations](network-policy.md).

## Managed SSH and sensitivity

SSH is always managed. The operator creates an owned `gateway-ssh-ca` Secret;
there is no external CA selection field. Optional policy overrides are:

```yaml
spec:
  ssh:
    clusterDomain: cluster.local
    lifetimeSeconds: 86400
    renewBeforeSeconds: 21600
```

Omitting `ssh` uses these internal defaults. Lifetime must be 1200–31536000 seconds; renewal threshold must be at least 600 seconds and strictly less than lifetime. The twenty-minute minimum lifetime and ten-minute minimum renewal window leave time for asynchronous kubelet projection and terminal restart. These floors are not an availability guarantee: measure cluster propagation, scheduling and startup latency and choose a larger renewal window for slower clusters or controller outage tolerance. The defaults remain one day and six hours. Domain and policy values are validated before signing as well as in the CRD. Changing CA, instance identity or cluster domain against existing identity state fails closed; explicit identity migration is required.

Profiles derive identity only from namespace, immutable parent name and profile name. The profile reconciler invokes `internal/ssh.SecretSigner.Ensure` through an uncached Secret client and creates/reuses `profile-ssh-identity-<profile>` with distinct user and host keys and certificates in one atomic Secret state. Its identity Secret is owned by the parent Hermes and garbage-collected on parent deletion along with the CA. `profile-ssh-config-<profile>` is profile-owned and contains only public client/daemon configuration, CA public key and assigned principal.

The gateway selects only `user_key` and `user_cert`; the terminal selects only `host_key` and `host_cert`. Neither receives the CA private key or whole identity Secret. Each private key is projected directly with mode 0440 and fsGroup (10000 for gateway,
65532 for terminal), beneath `/run/hermes-ssh-private/<profile>`. No key staging
helper is used. Certificates and public files remain live directory projections;
the fixed principal file uses its safe read-only subPath mount. See
[terminal images](terminal.md) and [profile environment](profile-environment.md).

The rootless daemon listens on TCP 2222; Service TCP 22 targets 2222 and NetworkPolicies use the Pod port 2222. Non-SSH terminal commands must also listen on 2222. Terminal images must provide `/usr/sbin/sshd`, an unlocked public-key-login-capable `agent` account with UID/GID **65532**, writable `/tmp` for the daemon PID file, and any image-specific OpenSSH runtime prerequisites. The daemon cannot switch users or elevate privileges. The controller supplies `[/usr/sbin/sshd, -D, -e, -f, /run/hermes-ssh/sshd_config]` regardless of legacy terminal.command values. Supply a compatible rootless image; arbitrary stock root-based sshd images are not compatible. Existing root-owned 0600 runtime files on retained PVCs require an explicit administrator migration; fsGroup alone cannot repair their ownership. See the sensitivity section below for API trust boundaries.

Reconciliation schedules renewal at earliest certificate expiry minus the threshold. Keys remain stable. A deterministic hash of the persisted public host certificate and generated public configuration updates the terminal Pod template, causing its Recreate rollout to load the new host certificate. Stable reconciliations do not restart it. Public ConfigMap events enqueue repairs; Secret contents are read directly at reconciliation and SSH identity/CA changes do not enqueue signing (manual enqueue or the next scheduled reconciliation is required). Gateway projected client certificates update through kubelet without a rollout or subPath; kubelet projection propagation is asynchronous. No certificate-expiry termination of established sessions is implemented.

**Sensitive controller privilege:** the namespace Role grants Secret `get/list/watch/create/patch/update`, including access to the CA private key and all other Secrets by name in that namespace. Kubernetes RBAC cannot restrict dynamically named Secret creation to the generated suffix. Only a metadata Secret informer is created; full Secret reads bypass caching. This reduces memory exposure but does not restrict these API permissions. Treat the controller and anyone able to modify its workloads or instance/profile resources as trusted signing administrators. Use a dedicated namespace, restrict CR and Secret writers, enable Secret encryption at rest, and protect backups. Do not log identity/CA Secret contents. Profile deletion, deleting an identity Secret  does not instantly revoke issued certificates. They remain valid until expiry wherever the CA/principal remains trusted, and expiry does not terminate established sessions. Removing trust/revoking credentials and terminating sessions require explicit operational planning. Same-namespace application Secret selection and executable images/commands are administrator powers, not tenant security boundaries; no per-author Secret allowlist or image restrictions are claimed.

Generated OpenSSH certificate settings are consumed by the pinned native Hermes SSH backend through the gateway system Include. See [packaging](packaging.md) for the upstream accept-new limitation. `Ready=False/IntegrationPending` still does not imply live workload acceptance.

## Outstanding integration and security

The following are deliberately not exposed as speculative API settings:

1. Exercise mounted-home initialization in Kubernetes with the gateway image, volume permissions and immutable Secret updates. Project tests check the init entry invocation and mounts.
2. Establish multiplex discovery and verify served-profile and messaging health against a pinned Hermes version. Init success and local identity checks do not establish gateway readiness.
3. Exercise the implemented SSH signing, projections, renewal rollouts and daemon authentication in Kubernetes. Generated strict OpenSSH configuration is not proof that the Hermes backend uses it.
4. Use the pinned native Hermes SSH backend with standard OpenSSH certificate settings; see [packaging](packaging.md). Hermes retains its own file, sync, multiplexing and environment behavior. No controller SSH plugin or additional execution restrictions are installed.
5. Validate the generated network policies with a real CNI, cross-profile rejection, credential forwarding/file-tool/sync-back boundaries, topology handling, status-driven workload health, and restart/update/recovery behavior in an independent lab. Recreate reduces overlapping gateway writers; it is not distributed fencing against force deletion, partitions, or manual Pod duplication.

Validate upgrades in an independent test instance before changing an existing deployment.
