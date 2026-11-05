package controller

import (
	"context"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestProfileEnvironmentIsolationAndRemoval(t *testing.T) {
	c, scheme, h, p := fixture(t)
	ctx := context.Background()
	r := &ProfileReconciler{Client: c, Scheme: scheme}
	render := func() *appsv1.Deployment {
		t.Helper()
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}); err != nil {
			t.Fatal(err)
		}
		d := &appsv1.Deployment{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: "terminal-" + p.Name}, d); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			t.Fatal(err)
		}
		return d
	}
	empty := render()
	if len(empty.Spec.Template.Spec.Containers[0].Env) != 0 {
		t.Fatal("empty profile injected environment")
	}
	p.Spec.Env = []corev1.EnvVar{{Name: "SERVICE_LABEL", Value: "coding"}, {Name: "SERVICE_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "service-credentials"}, Key: "token"}}}}
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	d := render()
	env := d.Spec.Template.Spec.Containers[0].Env
	if len(env) != 2 || env[0].Name != "SERVICE_LABEL" || env[1].ValueFrom.SecretKeyRef.Name != "service-credentials" {
		t.Fatalf("wrong environment projection: %#v", env)
	}
	// Rendering must not mutate informer data or share nested references.
	env[1].ValueFrom.SecretKeyRef.Name = "changed"
	if p.Spec.Env[1].ValueFrom.SecretKeyRef.Name != "service-credentials" {
		t.Fatal("renderer aliases API")
	}
	other := p.DeepCopy()
	other.Spec.Env = nil
	otherTerminal, _ := Terminal(other)
	if len(otherTerminal.Spec.Template.Spec.Containers[0].Env) != 0 {
		t.Fatal("other profile received environment")
	}
	gateway := Gateway(h, []api.HermesProfile{*p})
	for _, container := range append(gateway.Spec.Template.Spec.Containers, gateway.Spec.Template.Spec.InitContainers...) {
		for _, v := range container.Env {
			if v.Name == "SERVICE_TOKEN" {
				t.Fatal("gateway received environment")
			}
		}
	}
	p.Spec.Env = nil
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	removed := render()
	if !reflect.DeepEqual(empty.Spec.Template, removed.Spec.Template) {
		t.Fatal("removal did not restore empty template")
	}
}
