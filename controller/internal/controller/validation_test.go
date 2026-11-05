package controller

import (
	"context"
	"strings"
	"testing"

	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestReservedDefaultProfileRejectedBeforeResources(t *testing.T) {
	for _, reconciler := range []string{"profile", "gateway"} {
		t.Run(reconciler, func(t *testing.T) {
			c, s, h, p := fixture(t)
			ctx := context.Background()
			if err := c.Delete(ctx, p); err != nil {
				t.Fatal(err)
			}
			p.Name = "default"
			p.ResourceVersion = ""
			if err := c.Create(ctx, p); err != nil {
				t.Fatal(err)
			}
			var err error
			if reconciler == "profile" {
				_, err = (&ProfileReconciler{Client: c, Scheme: s}).Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
			} else {
				_, err = (&HermesReconciler{Client: c, Scheme: s}).Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)})
			}
			if err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Fatalf("reserved profile accepted: %v", err)
			}
			claims := &corev1.PersistentVolumeClaimList{}
			if err := c.List(ctx, claims); err != nil {
				t.Fatal(err)
			}
			for _, claim := range claims.Items {
				if strings.HasPrefix(claim.Name, "default-") {
					t.Fatalf("reserved profile claim created: %s", claim.Name)
				}
			}
			deployments := &appsv1.DeploymentList{}
			if err := c.List(ctx, deployments); err != nil {
				t.Fatal(err)
			}
			if len(deployments.Items) != 0 {
				t.Fatal("reserved profile workload created")
			}
		})
	}
}

func TestSSHCustomTerminalCommandRejectedBeforeSigning(t *testing.T) {
	c, s, h, p := fixture(t)
	ctx := context.Background()
	h.Spec.SSH = &api.SSHSpec{CASecretRef: api.SSHCASecretReference{Name: "missing-ca"}}
	if err := c.Update(ctx, h); err != nil {
		t.Fatal(err)
	}
	p.Spec.Terminal.Command = []string{"/custom-daemon", "--custom"}
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, err := (&ProfileReconciler{Client: c, Scheme: s, SecretClient: c}).Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
	if err == nil || !strings.Contains(err.Error(), "terminal.command") {
		t.Fatalf("custom command not rejected before CA access: %v", err)
	}
	claims := &corev1.PersistentVolumeClaimList{}
	if err := c.List(ctx, claims); err != nil {
		t.Fatal(err)
	}
	if len(claims.Items) != 0 {
		t.Fatal("resources created for incompatible command")
	}
}
