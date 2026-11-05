package controller

import (
	"context"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klabels "k8s.io/apimachinery/pkg/labels"
	"net/netip"
	"os"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
	"strings"
	"testing"
)

func TestNetworkInstancePodLabelsPreserveWorkloadSelectors(t *testing.T) {
	h := &api.Hermes{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "test"}}
	p := &api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "test"}, Spec: api.HermesProfileSpec{HermesRef: "demo"}}
	g := Gateway(h, nil)
	d, s := Terminal(p)
	for _, pod := range []map[string]string{g.Spec.Template.Labels, d.Spec.Template.Labels} {
		if pod["hermes.nousresearch.com/instance"] != "demo" {
			t.Fatalf("missing same-instance Pod identity: %v", pod)
		}
	}
	if !reflect.DeepEqual(g.Spec.Selector.MatchLabels, labels("gateway", "gateway")) || !reflect.DeepEqual(d.Spec.Selector.MatchLabels, labels("terminal-alpha", "terminal")) || !reflect.DeepEqual(s.Spec.Selector, d.Spec.Selector.MatchLabels) {
		t.Fatal("immutable workload selectors changed")
	}
}

func TestNetworkPoliciesConstrainPeersAndPorts(t *testing.T) {
	h := &api.Hermes{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "test"}}
	p := &api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "test"}, Spec: api.HermesProfileSpec{HermesRef: "demo"}}
	g, term := GatewayNetworkPolicy(h), TerminalNetworkPolicy(p)
	gd := Gateway(h, nil)
	td, _ := Terminal(p)
	for _, v := range []struct {
		policy *networkingv1.NetworkPolicy
		pod    map[string]string
	}{{g, gd.Spec.Template.Labels}, {term, td.Spec.Template.Labels}} {
		sel, err := metav1.LabelSelectorAsSelector(&v.policy.Spec.PodSelector)
		if err != nil || !sel.Matches(klabels.Set(v.pod)) {
			t.Fatal("policy does not select its workload")
		}
		other := map[string]string{}
		for k, x := range v.pod {
			other[k] = x
		}
		other["hermes.nousresearch.com/resource"] = "foreign"
		if sel.Matches(klabels.Set(other)) {
			t.Fatal("policy selects unrelated workload")
		}
	}
	if !reflect.DeepEqual(g.Spec.PolicyTypes, []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}) || len(g.Spec.Ingress) != 0 {
		t.Fatal("gateway management ingress must remain unmanaged")
	}
	if !reflect.DeepEqual(term.Spec.PolicyTypes, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}) {
		t.Fatal("terminal must isolate both directions")
	}
	if len(g.Spec.Egress) != 3 || len(term.Spec.Egress) != 2 || len(term.Spec.Ingress) != 1 {
		t.Fatal("unexpected allow rules")
	}
	checkPort := func(ports []networkingv1.NetworkPolicyPort, protocol corev1.Protocol, port int32) {
		t.Helper()
		if len(ports) != 1 || ports[0].Protocol == nil || *ports[0].Protocol != protocol || ports[0].Port == nil || ports[0].Port.IntVal != port || ports[0].EndPort != nil {
			t.Fatalf("unexpected port scope: %#v", ports)
		}
	}
	checkPort(g.Spec.Egress[0].Ports, corev1.ProtocolTCP, 2222)
	checkPort(term.Spec.Ingress[0].Ports, corev1.ProtocolTCP, 2222)
	for _, item := range []struct {
		peers     []networkingv1.NetworkPolicyPeer
		component string
	}{{g.Spec.Egress[0].To, "terminal"}, {term.Spec.Ingress[0].From, "gateway"}} {
		if len(item.peers) != 1 {
			t.Fatal("SSH peers must not be ORed")
		}
		peer := item.peers[0]
		if peer.NamespaceSelector != nil || peer.PodSelector == nil || peer.IPBlock != nil {
			t.Fatal("SSH must use namespace-local pod selector only")
		}
		sel, err := metav1.LabelSelectorAsSelector(peer.PodSelector)
		if err != nil {
			t.Fatal(err)
		}
		for _, instance := range []string{"demo", "other"} {
			for _, component := range []string{"gateway", "terminal", "other"} {
				candidate := klabels.Set{"app.kubernetes.io/managed-by": "hermes-agent-controller", "hermes.nousresearch.com/instance": instance, "app.kubernetes.io/component": component}
				if sel.Matches(candidate) != (instance == "demo" && component == item.component) {
					t.Fatalf("SSH peer escaped instance/component: %v", candidate)
				}
			}
		}
	}
	for _, rules := range [][]networkingv1.NetworkPolicyEgressRule{g.Spec.Egress[1:], term.Spec.Egress} {
		dns := rules[0]
		if len(dns.To) != 1 || dns.To[0].IPBlock != nil || dns.To[0].NamespaceSelector == nil || dns.To[0].PodSelector == nil {
			t.Fatal("DNS namespace and Pod selectors must be ANDed in one peer")
		}
		if !reflect.DeepEqual(dns.To[0].NamespaceSelector.MatchLabels, map[string]string{"kubernetes.io/metadata.name": "kube-system"}) || !reflect.DeepEqual(dns.To[0].PodSelector.MatchLabels, map[string]string{"k8s-app": "kube-dns"}) {
			t.Fatal("unexpected DNS selectors")
		}
		if len(dns.Ports) != 2 {
			t.Fatal("DNS must allow UDP and TCP only")
		}
		checkPort(dns.Ports[:1], corev1.ProtocolUDP, 53)
		checkPort(dns.Ports[1:], corev1.ProtocolTCP, 53)
		https := rules[1]
		checkPort(https.Ports, corev1.ProtocolTCP, 443)
		if len(https.To) != 2 {
			t.Fatal("expected explicit dual-stack public IP blocks")
		}
		allowed := func(ip string) bool {
			addr := netip.MustParseAddr(ip)
			for _, peer := range https.To {
				if peer.IPBlock == nil || peer.PodSelector != nil || peer.NamespaceSelector != nil {
					t.Fatal("external HTTPS must use only IP blocks")
				}
				block := peer.IPBlock
				root := netip.MustParsePrefix(block.CIDR)
				denied := false
				for _, except := range block.Except {
					x := netip.MustParsePrefix(except)
					if !root.Contains(x.Addr()) || x.Bits() < root.Bits() {
						t.Fatal("invalid CIDR exclusion")
					}
					denied = denied || x.Contains(addr)
				}
				if root.Contains(addr) && !denied {
					return true
				}
			}
			return false
		}
		for _, ip := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
			if !allowed(ip) {
				t.Errorf("public HTTPS blocked: %s", ip)
			}
		}
		for _, ip := range []string{"0.0.0.0", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254", "172.16.0.1", "172.31.255.255", "192.168.0.1", "192.0.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "255.255.255.255", "::", "::1", "::ffff:10.0.0.1", "64:ff9b::a00:1", "fc00::1", "fd00::1", "fe80::1", "ff02::1", "2001:db8::1", "2002:a00:1::1"} {
			if allowed(ip) {
				t.Errorf("nonpublic HTTPS allowed: %s", ip)
			}
		}
	}
}

func TestReconcileNetworkPolicies(t *testing.T) {
	for _, kind := range []string{"gateway", "terminal"} {
		for _, foreign := range []bool{false, true} {
			t.Run(kind+fmtBool(foreign), func(t *testing.T) {
				c, s, h, p := fixture(t)
				if err := networkingv1.AddToScheme(s); err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				var owner client.Object = h
				want := GatewayNetworkPolicy(h)
				run := func() error {
					_, err := (&HermesReconciler{Client: c, Scheme: s}).Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)})
					return err
				}
				if kind == "terminal" {
					owner = p
					want = TerminalNetworkPolicy(p)
					run = func() error {
						_, err := (&ProfileReconciler{Client: c, Scheme: s}).Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
						return err
					}
				}
				if foreign {
					if err := c.Create(ctx, want); err != nil {
						t.Fatal(err)
					}
					if err := run(); err == nil {
						t.Fatal("foreign policy adopted")
					}
					got := &networkingv1.NetworkPolicy{}
					if err := c.Get(ctx, client.ObjectKeyFromObject(want), got); err != nil {
						t.Fatal(err)
					}
					if len(got.OwnerReferences) != 0 {
						t.Fatal("foreign policy modified")
					}
					return
				}
				if err := run(); err != nil {
					t.Fatal(err)
				}
				got := &networkingv1.NetworkPolicy{}
				key := client.ObjectKeyFromObject(want)
				if err := c.Get(ctx, key, got); err != nil {
					t.Fatalf("policy missing: %v", err)
				}
				ref := metav1.GetControllerOf(got)
				if ref == nil || ref.UID != owner.GetUID() || ref.BlockOwnerDeletion == nil || *ref.BlockOwnerDeletion {
					t.Fatal("unsafe or missing owner")
				}
				rv := got.ResourceVersion
				if err := run(); err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, key, got); err != nil {
					t.Fatal(err)
				}
				if got.ResourceVersion != rv {
					t.Fatal("stable reconciliation rewrote policy")
				}
				got.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{}}
				if err := c.Update(ctx, got); err != nil {
					t.Fatal(err)
				}
				if kind == "terminal" {
					if err := c.Delete(ctx, h); err != nil {
						t.Fatal(err)
					}
				}
				if err := run(); err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, key, got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.Spec, want.Spec) {
					t.Fatal("policy drift was not repaired")
				}
				if err := c.Delete(ctx, got); err != nil {
					t.Fatal(err)
				}
				if err := run(); err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, key, got); err != nil {
					t.Fatal("deleted policy not recreated")
				}
			})
		}
	}
}
func fmtBool(v bool) string {
	if v {
		return "/foreign"
	}
	return "/owned"
}

func TestNetworkPolicyNamespaceRBAC(t *testing.T) {
	data, err := os.ReadFile("../../../config/rbac/role.yaml")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, doc := range strings.Split(string(data), "---") {
		var obj struct {
			Kind string `json:"kind"`
		}
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			t.Fatal(err)
		}
		if obj.Kind == "ClusterRole" {
			t.Fatal("namespace-only controller has ClusterRole")
		}
		if obj.Kind != "Role" {
			continue
		}
		var role rbacv1.Role
		if err := yaml.Unmarshal([]byte(doc), &role); err != nil {
			t.Fatal(err)
		}
		for _, rule := range role.Rules {
			if reflect.DeepEqual(rule.APIGroups, []string{"networking.k8s.io"}) && reflect.DeepEqual(rule.Resources, []string{"networkpolicies"}) {
				found = true
				if !reflect.DeepEqual(rule.Verbs, []string{"get", "list", "watch", "create", "patch", "update", "delete"}) {
					t.Fatalf("unexpected policy permissions: %v", rule.Verbs)
				}
			}
		}
	}
	if !found {
		t.Fatal("namespace Role lacks NetworkPolicy permissions")
	}
}
