package sshidentity

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func fixture(t *testing.T) (client.Client, Identity, time.Time) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	ca := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "ca"}, Data: map[string][]byte{"ca_key": pem.EncodeToMemory(block)}}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(ca).Build(), Identity{Namespace: "test", Instance: "instance", Profile: "profile", ClusterDomain: "cluster.local"}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}
func adapter(t *testing.T, c client.Client) *SecretSigner {
	t.Helper()
	s, err := NewSecretSigner(c, "test", CASecretReference{Name: "ca", Key: "ca_key"}, Policy{Lifetime: 24 * time.Hour, RenewBefore: 6 * time.Hour, Backdate: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func stored(t *testing.T, c client.Client, ref types.NamespacedName) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{}
	if err := c.Get(context.Background(), ref, secret); err != nil {
		t.Fatal(err)
	}
	return secret
}
func TestIdentitySecretRenamePreservesKeys(t *testing.T) {
	c, id, now := fixture(t)
	ctx := context.Background()
	s := adapter(t, c)
	first, err := s.Ensure(ctx, id, now)
	if err != nil {
		t.Fatal(err)
	}
	old := stored(t, c, first.Secret)
	if err := c.Delete(ctx, old); err != nil {
		t.Fatal(err)
	}
	old.Name = id.Profile + "-ssh-identity"
	old.ResourceVersion = ""
	if err := c.Create(ctx, old); err != nil {
		t.Fatal(err)
	}
	migrated, err := s.Ensure(ctx, id, now)
	if err != nil {
		t.Fatal(err)
	}
	next := stored(t, c, migrated.Secret)
	if !bytes.Equal(old.Data[UserPrivateKey], next.Data[UserPrivateKey]) || !bytes.Equal(old.Data[HostPrivateKey], next.Data[HostPrivateKey]) {
		t.Fatal("rename rotated keys")
	}
	if migrated.Secret.Name != "profile-ssh-identity-"+id.Profile {
		t.Fatal(migrated.Secret)
	}
	_ = stored(t, c, client.ObjectKeyFromObject(old))
}

func TestEnsurePersistsReusesAndRenews(t *testing.T) {
	c, id, now := fixture(t)
	s := adapter(t, c)
	first, err := s.Ensure(context.Background(), id, now)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || !first.RenewAt.Equal(now.Add(18*time.Hour)) {
		t.Fatal("missing creation/renewal schedule")
	}
	before := stored(t, c, first.Secret)
	if len(before.Data) != 4 || len(before.Data[UserPrivateKey]) == 0 || len(before.Data[HostPrivateKey]) == 0 {
		t.Fatal("incomplete atomic state")
	}
	if bytes.Equal(before.Data[UserPrivateKey], before.Data[HostPrivateKey]) {
		t.Fatal("keys must differ")
	}
	again, err := adapter(t, c).Ensure(context.Background(), id, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed || stored(t, c, first.Secret).ResourceVersion != before.ResourceVersion {
		t.Fatal("restart must reuse state without writes")
	}
	renewed, err := s.Ensure(context.Background(), id, now.Add(18*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	after := stored(t, c, first.Secret)
	if !renewed.Changed || !bytes.Equal(before.Data[UserPrivateKey], after.Data[UserPrivateKey]) || !bytes.Equal(before.Data[HostPrivateKey], after.Data[HostPrivateKey]) || bytes.Equal(before.Data[UserCertificate], after.Data[UserCertificate]) || bytes.Equal(before.Data[HostCertificate], after.Data[HostCertificate]) {
		t.Fatal("renewal must change both certificates and preserve keys")
	}
	output, err := json.Marshal(renewed)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{UserPrivateKey, HostPrivateKey} {
		if bytes.Contains(output, after.Data[key]) || bytes.Contains(output, []byte("PRIVATE KEY")) {
			t.Fatal("private output")
		}
	}
}
