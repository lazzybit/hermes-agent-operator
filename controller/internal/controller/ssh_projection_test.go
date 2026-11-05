package controller

import (
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
)

func TestSSHPrivateKeysProjectedDirectly(t *testing.T) {
	h := &api.Hermes{Spec: api.HermesSpec{HelperImage: "ignored-for-ssh", SSH: &api.SSHSpec{}}}
	p := &api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "alpha"}}
	term, _ := Terminal(p)
	sshProjection(term, "alpha", true)
	for _, d := range []*appsv1.Deployment{Gateway(h, []api.HermesProfile{*p}), term} {
		pod := d.Spec.Template.Spec
		if pod.SecurityContext.FSGroup == nil {
			t.Fatal("projected keys need fsGroup")
		}
		found := false
		for _, v := range pod.Volumes {
			if v.Name != "ssh-alpha-private" {
				continue
			}
			found = true
			s := v.Secret
			if s == nil || s.SecretName != "profile-ssh-identity-alpha" || s.DefaultMode == nil || *s.DefaultMode != 0440 || len(s.Items) != 1 || s.Items[0].Path != "key" {
				t.Fatalf("unexpected private projection: %#v", v)
			}
			key := "user_key"
			if d == term {
				key = "host_key"
			}
			if s.Items[0].Key != key {
				t.Fatalf("private key scope: %s", s.Items[0].Key)
			}
		}
		if !found {
			t.Fatal("missing private key projection")
		}
		found = false
		for _, m := range pod.Containers[0].VolumeMounts {
			if m.Name == "ssh-alpha-private" {
				found = m.ReadOnly && m.SubPath == "" && m.MountPath == "/run/hermes-ssh-private/alpha"
			}
			if strings.HasPrefix(m.MountPath, "/run/hermes-ssh") && m.SubPath != "" {
				t.Fatal("rotating key/cert material must remain live")
			}
		}
		if !found {
			t.Fatal("missing live read-only private mount")
		}
		for _, c := range pod.InitContainers {
			if c.Name == "init-ssh-alpha" {
				t.Fatal("key staging initializer remains")
			}
		}
	}
	found := false
	for _, m := range term.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.MountPath == "/run/hermes-authorized-principals" {
			found = m.ReadOnly && m.SubPath == "authorized_principals"
		}
	}
	if !found {
		t.Fatal("fixed principal file must bypass projected root for StrictModes")
	}
}
