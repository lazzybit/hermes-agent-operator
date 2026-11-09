package controller

import (
	"context"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestIdentityReconcileMigratesExistingDeployment(t *testing.T) {
	c, s, h, p := fixture(t)
	ctx := context.Background()
	r := &HermesReconciler{Client: c, Scheme: s}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	d := &appsv1.Deployment{}
	key := client.ObjectKey{Namespace: h.Namespace, Name: "gateway"}
	if err := c.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	d.Spec.Template.Spec.ServiceAccountName = "default"
	if err := c.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	if d.Spec.Template.Spec.ServiceAccountName != "gateway" {
		t.Fatal("old deployment identity not migrated")
	}
	sa := &corev1.ServiceAccount{}
	if err := c.Get(ctx, key, sa); err != nil {
		t.Fatal(err)
	}
	if sa.AutomountServiceAccountToken == nil || *sa.AutomountServiceAccountToken || metav1.GetControllerOf(sa).UID != h.UID {
		t.Fatal("unsafe gateway service account")
	}
	pr := &ProfileReconciler{Client: c, Scheme: s}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: "terminal-alpha"}, sa); err != nil {
		t.Fatal(err)
	}
	if metav1.GetControllerOf(sa).UID != p.UID {
		t.Fatal("terminal identity has wrong owner")
	}
}

func TestSeparateWorkloadIdentities(t *testing.T) {
	h := &api.Hermes{ObjectMeta: metav1.ObjectMeta{Name: "main", Namespace: "test"}}
	p := &api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "test"}}
	q := &api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "test"}}
	g := Gateway(h, nil)
	a, _ := Terminal(p)
	b, _ := Terminal(q)
	names := []string{g.Spec.Template.Spec.ServiceAccountName, a.Spec.Template.Spec.ServiceAccountName, b.Spec.Template.Spec.ServiceAccountName}
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || name == "default" || seen[name] {
			t.Fatalf("workloads share or omit identity: %v", names)
		}
		seen[name] = true
	}
}
