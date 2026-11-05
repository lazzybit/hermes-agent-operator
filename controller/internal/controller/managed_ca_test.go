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

func TestDefaultSSHNeedsNoUserConfiguration(t *testing.T) {
	c, s, h, p := fixture(t)
	ctx := context.Background()
	p.Spec.Terminal.Command = nil
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	r := &ProfileReconciler{Client: c, Scheme: s, SecretClient: c}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}); err != nil {
		t.Fatal(err)
	}
	ca := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: "gateway-ssh-ca"}, ca); err != nil {
		t.Fatal(err)
	}
	owner := metav1.GetControllerOf(ca)
	if owner == nil || owner.UID != h.UID {
		t.Fatal("CA must be garbage collected with Hermes")
	}
	before := string(ca.Data["ca_key"])
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(ca), ca); err != nil {
		t.Fatal(err)
	}
	if string(ca.Data["ca_key"]) != before {
		t.Fatal("CA rotated on stable reconciliation")
	}
	d := &appsv1.Deployment{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: "terminal-" + p.Name}, d); err != nil {
		t.Fatal(err)
	}
	if len(d.Spec.Template.Spec.Containers[0].Command) == 0 {
		t.Fatal("missing managed command")
	}
	gateway := Gateway(h, []api.HermesProfile{*p})
	if len(gateway.Spec.Template.Spec.Volumes) < 5 {
		t.Fatal("missing SSH projections")
	}
}

func TestManagedCARejectsForeignAndLostCA(t *testing.T) {
	for _, mode := range []string{"foreign", "lost"} {
		t.Run(mode, func(t *testing.T) {
			c, s, h, p := fixture(t)
			ctx := context.Background()
			if mode == "foreign" {
				if err := c.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "gateway-ssh-ca", Namespace: h.Namespace}, Data: map[string][]byte{"ca_key": []byte("invalid")}}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := c.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "profile-ssh-identity-" + p.Name, Namespace: h.Namespace}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := ensureManagedCA(ctx, c, s, h); err == nil {
				t.Fatal("unsafe CA creation accepted")
			}
		})
	}
}
