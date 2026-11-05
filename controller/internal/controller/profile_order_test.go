package controller

import (
	"context"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"testing"
)

func TestConfigurationRevisionIndependentOfProfileOrder(t *testing.T) {
	c, s, h, p := fixture(t)
	q := p.DeepCopy()
	q.Name = "beta"
	q.UID = "profile-beta"
	for _, profile := range []*api.HermesProfile{p, q} {
		profile.Spec.CredentialsSecret = &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "test-" + profile.Name}, Key: "env"}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "test-" + profile.Name, Namespace: h.Namespace}, Data: map[string][]byte{"env": []byte("synthetic-test-value")}}
		if err := c.Create(context.Background(), secret); err != nil {
			t.Fatal(err)
		}
	}
	r := &HermesReconciler{Client: c, Scheme: s}
	var previous map[string]string
	for i, ps := range [][]api.HermesProfile{{*p, *q}, {*q, *p}, {*p, *q}} {
		d := Gateway(h, ps)
		if err := r.configurationRevision(context.Background(), h, ps, d); err != nil {
			t.Fatal(err)
		}
		revision := d.Spec.Template.Annotations
		if i > 0 && !reflect.DeepEqual(revision, previous) {
			t.Errorf("unchanged profiles reordered: rollout revision changed")
		}
		previous = revision
	}
}
