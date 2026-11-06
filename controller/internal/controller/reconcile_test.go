package controller

import (
	"context"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func fixture(t *testing.T) (client.Client, *runtime.Scheme, *api.Hermes, *api.HermesProfile) {
	t.Helper()
	s := runtime.NewScheme()
	_ = api.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = networkingv1.AddToScheme(s)
	h := &api.Hermes{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "test", UID: "instance"}, Spec: api.HermesSpec{HelperImage: "helper:test", ConfigMap: "default-config", Image: "gateway:test", Command: []string{"gateway"}, Storage: api.StorageSpec{Size: "1Gi"}}}
	p := &api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "test", UID: "profile"}, Spec: api.HermesProfileSpec{ConfigMap: "profile-config", HermesRef: "demo", Storage: api.StorageSpec{Size: "1Gi"}, Terminal: api.TerminalSpec{Image: "terminal:test", Command: []string{"/usr/sbin/sshd", "-D", "-e", "-f", "/run/hermes-ssh/sshd_config"}, Storage: api.StorageSpec{Size: "2Gi"}}}}
	return fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(h, p).WithObjects(h, p, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "default-config", Namespace: "test"}, Data: map[string]string{"config.yaml": "agent: {}\n"}}, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "profile-config", Namespace: "test"}, Data: map[string]string{"config.yaml": "agent: {}\n"}}).Build(), s, h, p
}
func TestReconcileRejectsUnsafeRetainedClaims(t *testing.T) {
	for _, kind := range []string{"owned", "deleting"} {
		t.Run(kind, func(t *testing.T) {
			c, s, h, _ := fixture(t)
			ctx := context.Background()
			r := &HermesReconciler{Client: c, Scheme: s}
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)}
			if _, err := r.Reconcile(ctx, req); err != nil {
				t.Fatal(err)
			}
			claim := &corev1.PersistentVolumeClaim{}
			key := types.NamespacedName{Name: "gateway-data", Namespace: "test"}
			if err := c.Get(ctx, key, claim); err != nil {
				t.Fatal(err)
			}
			if kind == "owned" {
				claim.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "foreign", UID: "foreign"}}
			} else {
				claim.Finalizers = []string{"test.example/retain"}
			}
			if err := c.Update(ctx, claim); err != nil {
				t.Fatal(err)
			}
			if kind == "deleting" {
				if err := c.Delete(ctx, claim); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.Reconcile(ctx, req); err == nil {
				t.Fatal("unsafe retained claim accepted")
			}
			gateway := &appsv1.Deployment{}
			if err := c.Get(ctx, types.NamespacedName{Name: "gateway", Namespace: "test"}, gateway); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestParentEventsEnqueueReferencingProfiles(t *testing.T) {
	c, s, h, _ := fixture(t)
	r := &ProfileReconciler{Client: c, Scheme: s}
	other := &api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: h.Namespace}, Spec: api.HermesProfileSpec{HermesRef: "different"}}
	if err := c.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	requests := r.parentRequests(context.Background(), h)
	if len(requests) != 1 || requests[0].Name != "alpha" || requests[0].Namespace != h.Namespace {
		t.Fatalf("unexpected parent event requests: %#v", requests)
	}
}

func TestProfileRepairsTerminalWithoutParent(t *testing.T) {
	for _, state := range []string{"missing", "deleting"} {
		t.Run(state, func(t *testing.T) {
			c, s, h, p := fixture(t)
			ctx := context.Background()
			r := &ProfileReconciler{Client: c, Scheme: s}
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}
			if _, err := r.Reconcile(ctx, req); err != nil {
				t.Fatal(err)
			}
			if state == "deleting" {
				h.Finalizers = []string{"test.example/hold"}
				if err := c.Update(ctx, h); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Delete(ctx, h); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
				t.Fatal(err)
			}
			p.Spec.Terminal.Image = "terminal:updated"
			if err := c.Update(ctx, p); err != nil {
				t.Fatal(err)
			}
			for _, remove := range []bool{false, true} {
				d := &appsv1.Deployment{}
				key := types.NamespacedName{Name: "terminal-alpha", Namespace: "test"}
				if remove {
					if err := c.Get(ctx, key, d); err != nil {
						t.Fatal(err)
					}
					if err := c.Delete(ctx, d); err != nil {
						t.Fatal(err)
					}
					service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
					if err := c.Delete(ctx, service); err != nil {
						t.Fatal(err)
					}
				}
				_, _ = r.Reconcile(ctx, req) // Missing references may return an error for retry.
				if err := c.Get(ctx, key, d); err != nil {
					t.Fatalf("terminal not repaired: %v", err)
				}
				if d.Spec.Template.Spec.Containers[0].Image != "terminal:updated" {
					t.Fatal("terminal update blocked by parent lifecycle")
				}
				if err := c.Get(ctx, key, &corev1.Service{}); err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
					t.Fatal(err)
				}
				valid, ready := false, false
				for _, condition := range p.Status.Conditions {
					valid = valid || (condition.Type == "ReferenceValid" && condition.Status == metav1.ConditionFalse && condition.Reason == "ReferenceInvalid")
					ready = ready || (condition.Type == "Ready" && condition.Status == metav1.ConditionFalse && condition.Reason == "ReferenceInvalid")
				}
				if !valid || !ready {
					t.Fatalf("missing invalid-reference status: %#v", p.Status)
				}
				deployments := &appsv1.DeploymentList{}
				if err := c.List(ctx, deployments); err != nil {
					t.Fatal(err)
				}
				if len(deployments.Items) != 1 {
					t.Fatal("profile created an extra gateway")
				}
			}
		})
	}
}

func TestReconcilePreservesRequestedStorageClass(t *testing.T) {
	for _, change := range []string{"omitted", "different"} {
		t.Run(change, func(t *testing.T) {
			c, s, h, _ := fixture(t)
			ctx := context.Background()
			original := "fast"
			h.Spec.Storage.StorageClassName = &original
			if err := c.Update(ctx, h); err != nil {
				t.Fatal(err)
			}
			r := &HermesReconciler{Client: c, Scheme: s}
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)}
			if _, err := r.Reconcile(ctx, req); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(h), h); err != nil {
				t.Fatal(err)
			}
			if change == "omitted" {
				h.Spec.Storage.StorageClassName = nil
			} else {
				different := "slow"
				h.Spec.Storage.StorageClassName = &different
			}
			if err := c.Update(ctx, h); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Reconcile(ctx, req); err == nil {
				t.Fatal("storage class change accepted")
			}
			claim := &corev1.PersistentVolumeClaim{}
			if err := c.Get(ctx, types.NamespacedName{Name: "gateway-data", Namespace: "test"}, claim); err != nil {
				t.Fatal(err)
			}
			if claim.Annotations["hermes.nousresearch.com/requested-storage-class"] != "fast" || *claim.Spec.StorageClassName != "fast" {
				t.Fatal("original requested class not preserved")
			}
		})
	}
}

func TestReconcileAllowsDefaultStorageClass(t *testing.T) {
	c, s, h, _ := fixture(t)
	ctx := context.Background()
	r := &HermesReconciler{Client: c, Scheme: s}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	claim := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Name: "gateway-data", Namespace: "test"}, claim); err != nil {
		t.Fatal(err)
	}
	if claim.Annotations["hermes.nousresearch.com/requested-storage-class"] != "<omitted>" {
		t.Fatal("omitted storage class must be recorded distinctly from an explicit empty class")
	}
	sc := "default-storage"
	claim.Spec.StorageClassName = &sc
	if err := c.Update(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("defaulted storage class rejected: %v", err)
	}
}

func TestReconcilePreservesAPIDefaults(t *testing.T) {
	c, s, h, _ := fixture(t)
	ctx := context.Background()
	r := &HermesReconciler{Client: c, Scheme: s}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	d := &appsv1.Deployment{}
	key := types.NamespacedName{Name: "gateway", Namespace: "test"}
	if err := c.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	d.Spec.Template.Spec.Containers[0].ImagePullPolicy = corev1.PullIfNotPresent
	d.Spec.Template.Spec.Containers[0].TerminationMessagePath = "/dev/termination-log"
	d.Spec.Template.Spec.Containers[0].TerminationMessagePolicy = corev1.TerminationMessageReadFile
	if err := c.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	rv := d.ResourceVersion
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	_ = c.Get(ctx, key, d)
	if d.ResourceVersion != rv {
		t.Fatal("API defaulting causes perpetual deployment updates")
	}
}

func TestReconcileRejectsForeignWorkload(t *testing.T) {
	c, s, h, _ := fixture(t)
	ctx := context.Background()
	foreign := Gateway(h, nil)
	foreign.Spec.Template.Spec.Containers[0].Image = "foreign:test"
	if err := c.Create(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	r := &HermesReconciler{Client: c, Scheme: s}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)}); err == nil {
		t.Fatal("unowned workload adopted")
	}
	got := &api.Hermes{}
	_ = c.Get(ctx, client.ObjectKeyFromObject(h), got)
	if len(got.Status.Conditions) != 2 || got.Status.Conditions[0].Status != metav1.ConditionFalse {
		t.Fatal("reconcile failure not reported")
	}
}

func TestReconcileIdempotenceAndProfileRemoval(t *testing.T) {
	c, s, h, p := fixture(t)
	ctx := context.Background()
	hr := &HermesReconciler{Client: c, Scheme: s}
	pr := &ProfileReconciler{Client: c, Scheme: s}
	req := func(o client.Object) ctrl.Request {
		return ctrl.Request{NamespacedName: types.NamespacedName{Name: o.GetName(), Namespace: o.GetNamespace()}}
	}
	for i := 0; i < 2; i++ {
		if _, err := pr.Reconcile(ctx, req(p)); err != nil {
			t.Fatal(err)
		}
		if _, err := hr.Reconcile(ctx, req(h)); err != nil {
			t.Fatal(err)
		}
	}
	ds := &appsv1.DeploymentList{}
	_ = c.List(ctx, ds)
	if len(ds.Items) != 2 {
		t.Fatalf("deployments=%d", len(ds.Items))
	}
	claims := &corev1.PersistentVolumeClaimList{}
	_ = c.List(ctx, claims)
	if len(claims.Items) != 3 {
		t.Fatalf("claims=%d", len(claims.Items))
	}
	for _, v := range claims.Items {
		if len(v.OwnerReferences) != 0 {
			t.Fatal("retained PVC owned")
		}
	}
	d := &appsv1.Deployment{}
	key := types.NamespacedName{Name: "gateway", Namespace: "test"}
	_ = c.Get(ctx, key, d)
	rv := d.ResourceVersion
	if _, err := hr.Reconcile(ctx, req(h)); err != nil {
		t.Fatal(err)
	}
	_ = c.Get(ctx, key, d)
	if rv != d.ResourceVersion {
		t.Fatal("stable reconciliation wrote deployment")
	}
	if len(d.Spec.Template.Spec.Volumes) != 13 {
		t.Fatal("profile missing")
	}
	if err := c.Delete(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := hr.Reconcile(ctx, req(h)); err != nil {
		t.Fatal(err)
	}
	_ = c.Get(ctx, key, d)
	if len(d.Spec.Template.Spec.Volumes) != 7 {
		t.Fatal("deleted profile mount retained")
	}
	claims = &corev1.PersistentVolumeClaimList{}
	_ = c.List(ctx, claims)
	if len(claims.Items) != 3 {
		t.Fatal("PVC deleted")
	}
}
