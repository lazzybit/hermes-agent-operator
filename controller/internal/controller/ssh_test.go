package controller

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSSHReconcileSignsReusesAndRenews(t *testing.T) {
	c, s, h, p := fixture(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := &ProfileReconciler{Client: c, Scheme: s, SecretClient: c, Now: func() time.Time { return now }}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}
	var previous *corev1.Secret
	var hash, terminalRV string
	for step := 0; step < 3; step++ {
		if step == 2 {
			now = now.Add(18 * time.Hour)
		}
		result, err := r.Reconcile(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if result.RequeueAfter != 18*time.Hour {
			t.Fatalf("renew deadline requeue: %v", result)
		}
		state := &corev1.Secret{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: "profile-ssh-identity-" + p.Name}, state); err != nil {
			t.Fatal(err)
		}
		cm := &corev1.ConfigMap{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: "profile-ssh-config-" + p.Name}, cm); err != nil {
			t.Fatal(err)
		}
		for _, value := range cm.Data {
			if strings.Contains(value, "PRIVATE KEY") {
				t.Fatal("private data in ConfigMap")
			}
		}
		if !strings.Contains(cm.Data["known_hosts"], "terminal-alpha.test.svc.cluster.local") || cm.Data["authorized_principals"] != "hermes:test:demo:alpha\n" {
			t.Fatal("wrong derived identity")
		}
		d := &appsv1.Deployment{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: "terminal-" + p.Name}, d); err != nil {
			t.Fatal(err)
		}
		currentHash := d.Spec.Template.Annotations["hermes.nousresearch.com/ssh-public-hash"]
		if currentHash == "" {
			t.Fatal("no terminal restart hash")
		}
		if step == 1 && (currentHash != hash || d.ResourceVersion != terminalRV || !reflect.DeepEqual(previous.Data, state.Data)) {
			t.Fatal("stable reconcile changed identity or terminal")
		}
		if step == 2 && (currentHash == hash || d.ResourceVersion == terminalRV || reflect.DeepEqual(previous.Data["host_cert"], state.Data["host_cert"])) {
			t.Fatal("renewal did not restart terminal")
		}
		if step > 0 && (!reflect.DeepEqual(previous.Data["user_key"], state.Data["user_key"]) || !reflect.DeepEqual(previous.Data["host_key"], state.Data["host_key"])) {
			t.Fatal("renewal replaced private keys")
		}
		assertSSHProjection(t, d, []string{"host_key:key", "host_cert:host_key-cert.pub"}, []string{"sshd_config", "ca.pub", "authorized_principals"})
		if !reflect.DeepEqual(d.Spec.Template.Spec.Containers[0].Command, []string{"/usr/sbin/sshd", "-D", "-e", "-f", "/run/hermes-ssh/sshd_config"}) {
			t.Fatal("sshd not using standalone config")
		}
		hr := &HermesReconciler{Client: c, Scheme: s}
		if _, err := hr.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h)}); err != nil {
			t.Fatal(err)
		}
		gateway := &appsv1.Deployment{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: "gateway"}, gateway); err != nil {
			t.Fatal(err)
		}
		assertSSHProjection(t, gateway, []string{"user_key:key", "user_cert:id_ed25519-cert.pub"}, []string{"known_hosts", "client_config"})
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			t.Fatal(err)
		}
		ready := meta.FindStatusCondition(p.Status.Conditions, "Ready")
		if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "IntegrationPending" {
			t.Fatal("false operational readiness")
		}
		previous = state
		hash = currentHash
		terminalRV = d.ResourceVersion
	}
}

func assertSSHProjection(t *testing.T, d *appsv1.Deployment, secretKeys, publicKeys []string) {
	t.Helper()
	var gotSecret, gotPublic []string
	for _, v := range d.Spec.Template.Spec.Volumes {
		if v.Secret != nil && strings.Contains(v.Secret.SecretName, "ssh") {
			if len(v.Secret.Items) != 1 || v.Secret.DefaultMode == nil || *v.Secret.DefaultMode != 0440 {
				t.Fatal("invalid private key selector")
			}
			for _, item := range v.Secret.Items {
				gotSecret = append(gotSecret, item.Key+":"+item.Path)
			}
		}
		if v.Projected == nil {
			continue
		}
		if v.Projected.DefaultMode == nil || *v.Projected.DefaultMode != 0440 {
			t.Fatal("private file mode")
		}
		for _, src := range v.Projected.Sources {
			if src.Secret != nil {
				if src.Secret.Name != "profile-ssh-identity-alpha" || len(src.Secret.Items) != 1 {
					t.Fatal("invalid secret selector")
				}
				for _, item := range src.Secret.Items {
					gotSecret = append(gotSecret, item.Key+":"+item.Path)
				}
			}
			if src.ConfigMap != nil {
				for _, item := range src.ConfigMap.Items {
					gotPublic = append(gotPublic, item.Key)
				}
			}
		}
		for _, m := range d.Spec.Template.Spec.Containers[0].VolumeMounts {
			if m.Name == v.Name && m.MountPath == "/run/hermes-authorized-principals" && m.SubPath == "authorized_principals" && m.ReadOnly {
				continue // Fixed principal; rotating certificates retain a live directory mount.
			}
			if m.Name == v.Name && (m.SubPath != "" || !m.ReadOnly) {
				t.Fatal("projection must update without subPath")
			}
		}
	}
	if !reflect.DeepEqual(gotSecret, secretKeys) || !reflect.DeepEqual(gotPublic, publicKeys) {
		t.Fatalf("selectors: %v %v", gotSecret, gotPublic)
	}
}
