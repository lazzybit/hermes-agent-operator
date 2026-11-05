package sshidentity

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestServiceRenamePreservesKeysAndPrincipal(t *testing.T) {
	a, _ := testAuthority(t)
	id := testIdentity()
	now := time.Unix(1800000000, 0)
	old, _, err := a.Ensure(id, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	hs, err := ssh.ParsePrivateKey(old.HostPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	old.HostCertificate, err = a.issue(hs.PublicKey(), ssh.HostCert, id.Profile+"-terminal."+id.Namespace+".svc."+id.ClusterDomain, now)
	if err != nil {
		t.Fatal(err)
	}
	next, changed, err := a.Ensure(id, old, now)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if !bytes.Equal(old.UserPrivateKey, next.UserPrivateKey) || !bytes.Equal(old.HostPrivateKey, next.HostPrivateKey) || !bytes.Equal(old.UserCertificate, next.UserCertificate) {
		t.Fatal("rename rotated keys or user identity")
	}
	if certificate(t, next.HostCertificate).ValidPrincipals[0] != id.Hostname() {
		t.Fatal("wrong renamed host")
	}
	if _, changed, err := a.Ensure(id, next, now); err != nil || changed {
		t.Fatal("rename not idempotent", err)
	}
}

func TestReuseAndRenewWithoutKeyRotation(t *testing.T) {
	a, ca := testAuthority(t)
	now := time.Unix(1800000000, 0)
	id := testIdentity()
	initial, _, err := a.Ensure(id, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	original := *initial
	reused, changed, err := a.Ensure(id, initial, now.Add(49*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if changed || !reflect.DeepEqual(initial, reused) {
		t.Fatal("valid state not reused")
	}
	renewed, changed, err := a.Ensure(id, initial, now.Add(50*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !changed || bytes.Equal(initial.UserCertificate, renewed.UserCertificate) || bytes.Equal(initial.HostCertificate, renewed.HostCertificate) {
		t.Fatal("renewal did not occur at threshold")
	}
	if !bytes.Equal(initial.UserPrivateKey, renewed.UserPrivateKey) || !bytes.Equal(initial.HostPrivateKey, renewed.HostPrivateKey) {
		t.Fatal("renewal rotated keys")
	}
	if !reflect.DeepEqual(original, *initial) {
		t.Fatal("Ensure mutated its input")
	}
	if err := checker(ca, now.Add(70*time.Minute)).CheckCert(id.Principal(), certificate(t, renewed.UserCertificate)); err != nil {
		t.Fatal(err)
	}
	expired, changed, err := a.Ensure(id, initial, now.Add(2*time.Hour))
	if err != nil || !changed || expired == nil {
		t.Fatalf("expired renewal: %v", err)
	}
}
func TestRejectInvalidStoredState(t *testing.T) {
	a, _ := testAuthority(t)
	now := time.Unix(1800000000, 0)
	id := testIdentity()
	good, _, err := a.Ensure(id, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	other := id
	other.Profile = "writer"
	foreign, _, err := a.Ensure(other, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	rogue, _ := testAuthority(t)
	rogueState, _, err := rogue.Ensure(id, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	badCert := *certificate(t, good.UserCertificate)
	badCert.ValidBefore++
	cases := map[string]State{
		"partial":            {UserPrivateKey: good.UserPrivateKey},
		"wrong-profile":      *foreign,
		"wrong-ca":           *rogueState,
		"swapped-cert-types": {good.UserPrivateKey, good.HostPrivateKey, good.HostCertificate, good.UserCertificate},
		"shared-key":         {good.UserPrivateKey, good.UserPrivateKey, good.UserCertificate, good.HostCertificate},
		"key-mismatch":       {foreign.UserPrivateKey, good.HostPrivateKey, good.UserCertificate, good.HostCertificate},
		"tampered":           {good.UserPrivateKey, good.HostPrivateKey, ssh.MarshalAuthorizedKey(&badCert), good.HostCertificate},
	}
	for name, state := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := a.Ensure(id, &state, now); err == nil {
				t.Fatal("invalid stored identity accepted")
			}
		})
	}
}
func TestRejectInvalidInputs(t *testing.T) {
	a, _ := testAuthority(t)
	now := time.Unix(1800000000, 0)
	for _, id := range []Identity{{}, {"lab\nPermitRootLogin yes", "demo", "coder", "cluster.local"}, {"lab", "demo", "../coder", "cluster.local"}, {"lab", "demo", "coder", "*.local"}} {
		if _, _, err := a.Ensure(id, nil, now); err == nil {
			t.Fatalf("accepted identity: %#v", id)
		}
	}
	key, _ := testCA(t)
	for _, p := range []Policy{{}, {time.Hour, time.Hour, 0}, {time.Hour, -time.Minute, 0}, {time.Hour, time.Minute, -time.Second}} {
		if _, err := NewAuthority(key, p); err == nil {
			t.Fatal("accepted invalid policy")
		}
	}
	if _, err := NewAuthority([]byte("not a key"), Policy{time.Hour, time.Minute, 0}); err == nil {
		t.Fatal("accepted bad CA")
	}
}
