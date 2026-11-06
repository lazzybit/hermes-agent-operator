package controller

import (
	"context"
	"encoding/json"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestEphemeralReconcile(t *testing.T) {
	c, s, h, p := fixture(t)
	var ephemeral api.StorageSpec
	if err := json.Unmarshal([]byte(`{"size":"1Gi","ephemeral":true}`), &ephemeral); err != nil {
		t.Fatal(err)
	}
	h.Spec.Storage = ephemeral
	p.Spec.Storage = ephemeral
	p.Spec.Terminal.Storage = ephemeral
	ctx := context.Background()
	if err := c.Update(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := (&HermesReconciler{Client: c, Scheme: s}).Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)}); err != nil {
			t.Fatal(err)
		}
		if _, err := (&ProfileReconciler{Client: c, Scheme: s}).Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}); err != nil {
			t.Fatal(err)
		}
	}
	claims := &corev1.PersistentVolumeClaimList{}
	if err := c.List(ctx, claims); err != nil {
		t.Fatal(err)
	}
	if len(claims.Items) != 0 {
		t.Fatalf("ephemeral reconciliation created %d retained PVCs", len(claims.Items))
	}
	for name, volumes := range map[string][]string{"gateway": {"home", "profile-alpha"}, "terminal-alpha": {"workspace"}} {
		d := &appsv1.Deployment{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: name}, d); err != nil {
			t.Fatal(err)
		}
		for _, name := range volumes {
			found := false
			for _, v := range d.Spec.Template.Spec.Volumes {
				if v.Name == name {
					found = true
					if v.EmptyDir == nil || v.PersistentVolumeClaim != nil {
						t.Fatalf("%s must use emptyDir", name)
					}
				}
			}
			if !found {
				t.Fatalf("missing %s", name)
			}
		}
	}
	gateway := Gateway(h, []api.HermesProfile{*p})
	for _, init := range gateway.Spec.Template.Spec.InitContainers {
		if init.Name == "init-home" || init.Name == "init-profile-alpha" {
			if len(init.Command) != 1 || init.Command[0] != gatewayInitEntrypoint || !containsArg(init.Args, "--config") {
				t.Fatalf("ephemeral home must be initialized by the gateway entry: %v", init.Args)
			}
		}
	}
}
