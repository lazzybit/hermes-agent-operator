package controller

import (
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
)

func TestGatewayInitializesMountedIdentities(t *testing.T) {
	h := &api.Hermes{ObjectMeta: metav1.ObjectMeta{Name: "demo"}, Spec: api.HermesSpec{Image: "official@sha256:test", HelperImage: "ignored", CredentialsSecret: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "credentials"}, Key: ".env"}}}
	p := api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "assistant"}}
	pod := Gateway(h, []api.HermesProfile{p}).Spec.Template.Spec
	if *pod.SecurityContext.RunAsUser != 10000 || *pod.SecurityContext.FSGroup != 10000 {
		t.Fatal("official runtime UID/group required")
	}
	if len(pod.InitContainers) != 2 {
		t.Fatal("each home needs native initialization")
	}
	for _, c := range pod.InitContainers {
		if c.Image != h.Spec.Image || c.Command[0] != "/opt/hermes/.venv/bin/python" || !strings.Contains(c.Command[2], "'profile', 'create'") {
			t.Fatal("must use native create in the same image")
		}
		for _, m := range c.VolumeMounts {
			if m.SubPath != "" {
				t.Fatal("kubelet must not create a native target via subPath")
			}
		}
	}
	for _, m := range pod.Containers[0].VolumeMounts {
		if m.SubPath != "" {
			t.Fatal("configuration and state must use independent directories")
		}
	}
	for _, v := range pod.Volumes {
		if v.Secret != nil && v.Secret.SecretName == "credentials" && (len(v.Secret.Items) != 1 || v.Secret.Items[0].Key != ".env") {
			t.Fatal("only selected credentials may be projected")
		}
	}
	if strings.Contains(nativeInitialize, "SOUL.md').unlink") {
		t.Fatal("SOUL must remain writable state")
	}
}
