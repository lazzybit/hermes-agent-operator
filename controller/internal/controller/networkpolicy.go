package controller

import (
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func networkPort(protocol corev1.Protocol, number int32) networkingv1.NetworkPolicyPort {
	port := intstr.FromInt32(number)
	return networkingv1.NetworkPolicyPort{Protocol: &protocol, Port: &port}
}

// A PodSelector without NamespaceSelector is confined to the policy namespace.
func instancePeer(instance, component string) networkingv1.NetworkPolicyPeer {
	return networkingv1.NetworkPolicyPeer{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
		"app.kubernetes.io/managed-by":     "hermes-agent-controller",
		"app.kubernetes.io/component":      component,
		"hermes.nousresearch.com/instance": instance,
	}}}
}

func internetEgress() []networkingv1.NetworkPolicyEgressRule {
	return []networkingv1.NetworkPolicyEgressRule{
		{To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"}},
			PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}},
		}}, Ports: []networkingv1.NetworkPolicyPort{networkPort(corev1.ProtocolUDP, 53), networkPort(corev1.ProtocolTCP, 53)}},
		{To: []networkingv1.NetworkPolicyPeer{
			{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{
				"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
			}}},
			// Restrict IPv6 to global unicast; exclude special-use and transition
			// ranges so embedded private IPv4 destinations cannot bypass the rules.
			{IPBlock: &networkingv1.IPBlock{CIDR: "2000::/3", Except: []string{"2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"}}},
		}, Ports: []networkingv1.NetworkPolicyPort{networkPort(corev1.ProtocolTCP, 443)}},
	}
}

// GatewayNetworkPolicy isolates egress only: management ingress ports are not
// declared by the API and must be governed by an administrator's own policy.
func GatewayNetworkPolicy(h *api.Hermes) *networkingv1.NetworkPolicy {
	name := "gateway"
	egress := []networkingv1.NetworkPolicyEgressRule{{To: []networkingv1.NetworkPolicyPeer{instancePeer(h.Name, "terminal")}, Ports: []networkingv1.NetworkPolicyPort{networkPort(corev1.ProtocolTCP, 2222)}}}
	return &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: h.Namespace, Labels: labels(name, "gateway")}, Spec: networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: labels(name, "gateway")},
		PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
		Egress:      append(egress, internetEgress()...),
	}}
}

// TerminalNetworkPolicy remains independent of parent availability.
func TerminalNetworkPolicy(p *api.HermesProfile) *networkingv1.NetworkPolicy {
	name := "terminal-" + p.Name
	return &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: p.Namespace, Labels: labels(name, "terminal")}, Spec: networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: labels(name, "terminal")},
		PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		Ingress:     []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{instancePeer(p.Spec.HermesRef, "gateway")}, Ports: []networkingv1.NetworkPolicyPort{networkPort(corev1.ProtocolTCP, 2222)}}},
		Egress:      []networkingv1.NetworkPolicyEgressRule{{}},
	}}
}
