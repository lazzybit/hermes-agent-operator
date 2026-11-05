package controller

import (
	"context"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"testing"
)

func TestRenameFencesOldGateway(t *testing.T) {
	c, s, h, _ := fixture(t)
	ctx := context.Background()
	old := Gateway(h, nil)
	old.Name = h.Name + "-gateway"
	old.Finalizers = []string{"fixture/hold"}
	if err := controllerutil.SetControllerReference(h, old, s); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, old); err != nil {
		t.Fatal(err)
	}
	r := &HermesReconciler{Client: c, Scheme: s}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)}
	for i := 0; i < 2; i++ {
		result, err := r.Reconcile(ctx, req)
		if err != nil || result.RequeueAfter != renameWait {
			t.Fatal(result, err)
		}
		if err := c.Get(ctx, client.ObjectKey{Name: "gateway", Namespace: h.Namespace}, &appsv1.Deployment{}); err == nil {
			t.Fatal("replacement activated before foreground deletion completed")
		}
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(old), old); err != nil {
		t.Fatal(err)
	}
	old.Finalizers = nil
	if err := c.Update(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKey{Name: "gateway", Namespace: h.Namespace}, &appsv1.Deployment{}); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentRenameNeverCreatesEmptyReplacement(t *testing.T) {
	c, s, h, _ := fixture(t)
	ctx := context.Background()
	old := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: h.Name + "-home", Namespace: h.Namespace}}
	if err := c.Create(ctx, old); err != nil {
		t.Fatal(err)
	}
	r := &HermesReconciler{Client: c, Scheme: s}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)}); err == nil {
		t.Fatal("legacy persistent claim ignored")
	}
	if err := c.Get(ctx, client.ObjectKey{Name: "gateway-data", Namespace: h.Namespace}, &corev1.PersistentVolumeClaim{}); err == nil {
		t.Fatal("created empty replacement")
	}
}

func TestRenameRejectsForeignWorkload(t *testing.T) {
	c, _, h, _ := fixture(t)
	ctx := context.Background()
	old := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: h.Name + "-gateway", Namespace: h.Namespace}}
	if err := c.Create(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := fenceRenamedDeployment(ctx, c, h, old.Name); err == nil {
		t.Fatal("deleted foreign deployment")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(old), old); err != nil {
		t.Fatal(err)
	}
}
