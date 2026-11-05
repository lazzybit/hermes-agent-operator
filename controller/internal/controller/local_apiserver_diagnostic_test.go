package controller

import (
	"context"
	"fmt"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"testing"
)

func TestLocalAPIServerReconcile(t *testing.T) {
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		t.Skip("set KUBEBUILDER_ASSETS to run isolated API server integration test")
	}
	e := &envtest.Environment{CRDDirectoryPaths: []string{"../../../config/crd/bases"}, BinaryAssetsDirectory: assets}
	cfg, err := e.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := e.Stop(); err != nil {
			t.Error(err)
		}
	}()
	base, s, h, p := fixture(t)
	c, err := client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: h.Namespace}}); err != nil {
		t.Fatal(err)
	}
	// No custom resources or credentials are loaded; exercise the production apply path.
	h.UID = ""
	h.ResourceVersion = ""
	p.UID = ""
	p.ResourceVersion = ""
	for _, obj := range []client.Object{h, p} {
		if err := c.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"default-config", "profile-config"} {
		cm := &corev1.ConfigMap{}
		if err := base.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: name}, cm); err != nil {
			t.Fatal(err)
		}
		cm.ResourceVersion = ""
		if err := c.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
	}
	d := Gateway(h, []api.HermesProfile{*p})
	if err := apply(ctx, c, s, h, d); err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKeyFromObject(d)
	got := &appsv1.Deployment{}
	if err := c.Get(ctx, key, got); err != nil {
		t.Fatal(err)
	}
	generation := got.Generation
	fmt.Printf("initial generation=%d\n", generation)
	for i := 0; i < 30; i++ {
		if err := apply(ctx, c, s, h, Gateway(h, []api.HermesProfile{*p})); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, key, got); err != nil {
			t.Fatal(err)
		}
		if got.Generation != generation {
			t.Fatalf("reconcile %d changed generation %d -> %d", i, generation, got.Generation)
		}
	}
	fmt.Printf("30 real API applies: generation=%d replicas=%d unchanged\n", got.Generation, *got.Spec.Replicas)
	r := &HermesReconciler{Client: c, Scheme: s}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, key, got); err != nil {
		t.Fatal(err)
	}
	generation = got.Generation
	for i := 0; i < 30; i++ {
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, key, got); err != nil {
			t.Fatal(err)
		}
		if got.Generation != generation {
			t.Fatalf("full reconcile %d changed generation %d -> %d", i, generation, got.Generation)
		}
	}
	fmt.Printf("30 full reconciles: generation=%d replicas=%d unchanged\n", got.Generation, *got.Spec.Replicas)
	q := p.DeepCopy()
	q.Name = "beta"
	q.UID = "profile-beta"
	var baseline int64
	for i, profiles := range [][]api.HermesProfile{{*p, *q}, {*q, *p}, {*p, *q}} {
		desired := Gateway(h, profiles)
		if err := r.configurationRevision(ctx, h, profiles, desired); err != nil {
			t.Fatal(err)
		}
		if err := apply(ctx, c, s, h, desired); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, key, got); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("alternating profile order round=%d generation=%d\n", i, got.Generation)
		if i == 0 {
			baseline = got.Generation
		} else if got.Generation != baseline {
			t.Errorf("profile order caused real API rollout: %d -> %d", baseline, got.Generation)
		}
	}

}
