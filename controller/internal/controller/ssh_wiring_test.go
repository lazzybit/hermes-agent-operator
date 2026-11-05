package controller

import (
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
)

func TestGatewayControllerSSHProfileScopedWiring(t *testing.T) {
	h := &api.Hermes{Spec: api.HermesSpec{SSH: &api.SSHSpec{}, HelperImage: "helper:test"}}
	ps := []api.HermesProfile{{ObjectMeta: metav1.ObjectMeta{Name: "alpha"}}, {ObjectMeta: metav1.ObjectMeta{Name: "beta"}}}
	pod := Gateway(h, ps).Spec.Template.Spec
	for _, profile := range []string{"alpha", "beta"} {
		found := false
		for _, c := range pod.InitContainers {
			if c.Name == "init-profile-"+profile {
				found = len(c.Args) == 3 && c.Args[0] == profile
			}
		}
		if !found {
			t.Fatalf("missing profile overlay for %s", profile)
		}
		found = false
		for _, m := range pod.Containers[0].VolumeMounts {
			if m.Name == "ssh-"+profile+"-private" {
				found = m.ReadOnly && m.SubPath == ""
			}
		}
		if !found {
			t.Fatal("private key needs live read-only directory projection")
		}
	}
	for _, env := range pod.Containers[0].Env {
		if strings.Contains(env.Name, "SSH") || env.Name == "TERMINAL_ENV" {
			t.Fatalf("process-global routing: %v", env)
		}
	}
}
