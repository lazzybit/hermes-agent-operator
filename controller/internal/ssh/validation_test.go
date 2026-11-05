package sshidentity

import (
	"bytes"
	"context"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestRenewalPreservesOtherSecretData(t *testing.T) {
	c, id, now := fixture(t)
	s := adapter(t, c)
	first, err := s.Ensure(context.Background(), id, now)
	if err != nil {
		t.Fatal(err)
	}
	secret := stored(t, c, first.Secret)
	secret.Data["unrelated"] = []byte("keep")
	secret.Labels = map[string]string{"test": "keep"}
	if err := c.Update(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure(context.Background(), id, now.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	renewed := stored(t, c, first.Secret)
	if !bytes.Equal(renewed.Data["unrelated"], []byte("keep")) || renewed.Labels["test"] != "keep" {
		t.Fatal("renewal discarded unrelated state")
	}
}
func TestCAFailuresReturnNoResultOrState(t *testing.T) {
	for _, scenario := range []string{"missing-secret", "missing-key", "invalid-key", "same-secret"} {
		t.Run(scenario, func(t *testing.T) {
			c, id, now := fixture(t)
			s := adapter(t, c)
			ca := stored(t, c, client.ObjectKey{Namespace: "test", Name: "ca"})
			if scenario == "missing-secret" {
				if err := c.Delete(context.Background(), ca); err != nil {
					t.Fatal(err)
				}
			} else {
				if scenario == "missing-key" {
					delete(ca.Data, "ca_key")
				}
				if scenario == "invalid-key" {
					ca.Data["ca_key"] = []byte("SENSITIVE-INVALID-KEY")
				}
				if scenario == "same-secret" {
					s.ca.Name = "profile-ssh-identity-" + id.Profile
					ca.Name = s.ca.Name
					ca.ResourceVersion = ""
					if err := c.Create(context.Background(), ca); err != nil {
						t.Fatal(err)
					}
				} else if err := c.Update(context.Background(), ca); err != nil {
					t.Fatal(err)
				}
			}
			result, err := s.Ensure(context.Background(), id, now)
			if err == nil || result.Secret.Name != "" {
				t.Fatal("invalid CA accepted")
			}
			if bytes.Contains([]byte(err.Error()), []byte("SENSITIVE")) {
				t.Fatal("CA contents in error")
			}
		})
	}
}
func TestConstructorValidation(t *testing.T) {
	c, _, _ := fixture(t)
	policy := Policy{Lifetime: 24 * time.Hour, RenewBefore: 6 * time.Hour}
	for _, tc := range []struct {
		namespace string
		reference CASecretReference
		policy    Policy
	}{
		{"", CASecretReference{Name: "ca"}, policy},
		{"test", CASecretReference{}, policy},
		{"test", CASecretReference{Name: "ca", Key: "bad/key"}, policy},
		{"test", CASecretReference{Name: "ca"}, Policy{}},
	} {
		if _, err := NewSecretSigner(c, tc.namespace, tc.reference, tc.policy); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	if _, err := NewSecretSigner(nil, "test", CASecretReference{Name: "ca"}, policy); err == nil {
		t.Fatal("nil client accepted")
	}
	s, err := NewSecretSigner(c, "test", CASecretReference{Name: "ca"}, policy)
	if err != nil || s.ca.Key != "ca_key" {
		t.Fatal("missing default CA key")
	}
}
