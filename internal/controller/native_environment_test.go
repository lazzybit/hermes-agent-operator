package controller

import (
	"context"
	"strings"
	"testing"

	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNativeEnvironmentConfigurationRemainsProfileScoped(t *testing.T) {
	c, scheme, h, first := fixture(t)
	ctx := context.Background()
	h.Spec.SSH = &api.SSHSpec{}
	second := first.DeepCopy()
	second.Name, second.ResourceVersion, second.UID = "beta", "", "beta"
	profiles := []api.HermesProfile{*first, *second}
	for i := range profiles {
		p := &profiles[i]
		p.Spec.Config = runtime.RawExtension{Raw: []byte(`{"terminal":{"env_passthrough":["SERVICE_TOKEN"]}}`)}
		p.Spec.CredentialsSecret = &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "profile-credentials-" + p.Name}, Key: ".env"}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: p.Spec.CredentialsSecret.Name, Namespace: h.Namespace}, Data: map[string][]byte{".env": []byte("SERVICE_TOKEN=private-" + p.Name)}}
		if err := c.Create(ctx, secret); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if err := c.Create(ctx, p); err != nil {
				t.Fatal(err)
			}
		}
	}
	d := Gateway(h, profiles)
	r := &HermesReconciler{Client: c, Scheme: scheme}
	if err := r.configurationRevision(ctx, h, profiles, d); err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		cm := &corev1.ConfigMap{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: "profile-config-" + p.Name}, cm); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(cm.Data["config.yaml"], "env_passthrough:") {
			t.Fatal("native allowlist lost")
		}
		for _, value := range cm.Data {
			if strings.Contains(value, "private-") {
				t.Fatal("credential leaked to public configuration")
			}
		}
		found := false
		for _, v := range d.Spec.Template.Spec.Volumes {
			if v.Name == "credentials-profile-"+p.Name {
				found = v.Secret != nil && v.Secret.SecretName == p.Spec.CredentialsSecret.Name && len(v.Secret.Items) == 1 && v.Secret.Items[0].Key == ".env"
			}
		}
		if !found {
			t.Fatal("missing profile-specific credential file projection")
		}
	}
	for _, container := range append(d.Spec.Template.Spec.Containers, d.Spec.Template.Spec.InitContainers...) {
		if len(container.EnvFrom) != 0 {
			t.Fatal("shared process environment imported credentials")
		}
		for _, e := range container.Env {
			if e.Name == "SERVICE_TOKEN" {
				t.Fatal("profile value injected into process environment")
			}
		}
	}
}
