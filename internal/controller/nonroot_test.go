package controller

import (
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestWorkloadsAlwaysNonroot(t *testing.T) {
	h := &api.Hermes{Spec: api.HermesSpec{HelperImage: "helper:test", SSH: &api.SSHSpec{}}}
	p := &api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "alpha"}}
	term, _ := Terminal(p)
	for _, d := range []*appsv1.Deployment{Gateway(h, []api.HermesProfile{*p}), term} {
		pod := d.Spec.Template.Spec
		sc := pod.SecurityContext
		if sc == nil || sc.RunAsUser == nil || (*sc.RunAsUser != 65532 && *sc.RunAsUser != 10000) || sc.RunAsGroup == nil || *sc.RunAsGroup != *sc.RunAsUser || sc.FSGroup == nil || *sc.FSGroup != *sc.RunAsUser || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
			t.Fatal("all workloads/helpers must inherit nonroot UID/GID/fsGroup 65532")
		}
		for _, c := range append(pod.Containers, pod.InitContainers...) {
			if c.SecurityContext == nil || c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation || c.SecurityContext.Capabilities == nil || len(c.SecurityContext.Capabilities.Drop) != 1 || c.SecurityContext.Capabilities.Drop[0] != "ALL" {
				t.Fatal("container must drop all capabilities and prevent escalation")
			}
		}
	}
}
