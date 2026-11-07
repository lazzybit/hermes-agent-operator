# Default NetworkPolicies

Every Hermes and HermesProfile receives a namespaced `networking.k8s.io/v1` NetworkPolicy. No new API fields or opt-in are required: the foundation API has no network configuration, and these boundaries are default behavior regardless of `spec.ssh`. This is an intentional change for existing instances: reconciliation installs policies and adds instance identity to Pod templates, causing a rollout. Deployment and Service selectors remain unchanged. Terminal egress is unrestricted to support development tools and arbitrary protocols. Gateway egress remains restricted; private providers, HTTP endpoints and non-443 messaging require administrator-managed gateway policies.

| Workload / direction | Allowed new connections |
| --- | --- |
| Gateway egress | TCP 2222 to terminal Pods of the same instance and namespace; DNS below; public TCP 443 below |
| Terminal ingress | TCP 2222 from gateway Pods of the same instance and namespace only |
| Terminal egress | All destinations, ports and protocols, including private networks |
| Gateway ingress | Unmanaged by this controller |

All other new gateway egress and terminal ingress connections are denied by these policies. Established connection replies are implicitly permitted by Kubernetes NetworkPolicy semantics. Terminal policies explicitly contain `egress: [{}]`, allowing all outbound traffic even when other additive egress policies select the same Pod. Destination ingress policies, authentication and authorization still apply. Terminal tools can initiate SSH, HTTP, UDP and private-network connections; no terminal destination or port allow list is imposed. The shared gateway may reach all its instance's terminals; NetworkPolicy cannot distinguish profiles inside one gateway process. SSH principal authorization remains necessary.

## Selectors and DNS

Policies select exact generated workloads using the existing managed-by, component and resource labels. SSH peers require managed-by, component and `hermes.nousresearch.com/instance` labels together. A peer containing only a Pod selector is namespace-local; no namespace wildcard is added. Instance labels are placed on Pod templates, without modifying immutable Deployment selectors.

Gateway DNS permits UDP 53 and TCP 53 only to Pods labeled `k8s-app=kube-dns` in the namespace labeled `kubernetes.io/metadata.name=kube-system`. Both selectors are in **one peer** (AND), rather than separate peers (OR). This matches common CoreDNS installations. Custom DNS labels, external DNS, NodeLocal DNSCache and host-network DNS are not automatically supported. Verify Service destination translation and selector behavior with the actual CNI; adapt infrastructure DNS or separately reviewed policies if needed. No unrestricted DNS IP exception is installed.

## Public HTTPS address boundaries

Gateway external egress permits **TCP 443 only**, with these IP blocks:

- IPv4 `0.0.0.0/0`, excluding `0.0.0.0/8`, `10.0.0.0/8`, `100.64.0.0/10`, `127.0.0.0/8`, `169.254.0.0/16`, `172.16.0.0/12`, `192.0.0.0/24`, `192.0.2.0/24`, `192.168.0.0/16`, `198.18.0.0/15`, `198.51.100.0/24`, `203.0.113.0/24`, `224.0.0.0/4`, and `240.0.0.0/4`.
- IPv6 global unicast `2000::/3`, excluding special-use `2001::/23`, documentation `2001:db8::/32` and `3fff::/20`, and 6to4 `2002::/16`. This does not allow ULA `fc00::/7`, link-local `fe80::/10`, multicast, loopback, IPv4-mapped addresses or NAT64 ranges outside that block. Some special-use globally routable services are deliberately excluded.

These rules filter addresses and ports, not TLS, hostnames or application identity. Public proxies, tunnels and DNS exfiltration remain possible. Public-addressed cluster Pods, Services, nodes or API servers can match public TCP 443; address-only portable NetworkPolicy cannot distinguish those from public providers. This implementation assumes internal workload/API addresses use excluded private ranges. Clusters using public internal CIDRs require additional CNI-specific deny controls before claiming equivalent isolation. Special-use address allocations can change; the static exclusions are not a live IANA registry.

## Reconciliation and operational limits

Upgrading from the previous restrictive terminal policy replaces its DNS/public-443 rules with one unrestricted egress rule during reconciliation; this policy-only change does not require a workload restart. Gateway egress and terminal ingress remain unchanged.

Policies are named `gateway` and `terminal-<profile>`, owned by their respective CR with `blockOwnerDeletion=false`. Reconciliation installs the policy before creating/updating its workload, repairs policy spec drift and recreation after deletion, and rejects unowned or differently owned name collisions. Both controllers watch their owned policies. Profile policies continue reconciling when the parent is absent or deleting. The namespace Role grants policy get/list/watch/create/patch/update/delete, including cleanup of obsolete controller-owned policies after a rename. No cluster policy privileges are added.

A policy is not an enforcement attestation. The CNI must implement both ingress and egress NetworkPolicy, including IPv6 where used. Kubernetes permits traffic to/from a Pod's own node irrespective of these policies; host-network traffic and node-originated traffic handling also vary by CNI. Node-local metadata/services and node compromise need host firewall or CNI host-policy controls. Node exceptions cannot be removed with these portable rules. NAT and Service destination rewriting order varies; verify real cluster paths.

NetworkPolicy allows are additive: another policy selecting the same Pod can broaden access, and these policies cannot override it with an explicit deny. Restrict workload/label/policy writers in the namespace; labels are selectors, not cryptographic identity. Policy application and Pod scheduling are not atomic, and CNI programming is asynchronous, especially when upgrading existing workloads. Gate rollout operationally if a transient window is unacceptable.

Gateway ingress is intentionally not selected for isolation: the API declares no management/listener ports, so a guessed ingress allow list could break existing gateways. There is no controller-generated gateway ingress allow-all policy either. Supply an administrator-managed ingress NetworkPolicy with verified management source selectors and ports. Existing namespace ingress policies continue to apply; generating egress-only policy does not open ingress they deny.

Unit and fake-client tests verify selectors, ports, CIDR examples, ownership, foreign-policy rejection, stable reconciliation, drift repair, deletion recovery and namespace RBAC. They do not send packets through a real CNI, verify node exceptions, exercise garbage collection or establish that a live deployment is secure. `ResourcesReconciled=True` means objects were reconciled; readiness remains pending integration. No live Kubernetes resources are changed by these tests.
