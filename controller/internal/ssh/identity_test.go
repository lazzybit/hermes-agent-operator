package sshidentity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func testCA(t *testing.T) ([]byte, ssh.Signer) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "public-test-fixture")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block), signer
}
func testIdentity() Identity {
	return Identity{Namespace: "lab", Instance: "demo", Profile: "coder", ClusterDomain: "cluster.local"}
}
func testAuthority(t *testing.T) (*Authority, ssh.Signer) {
	t.Helper()
	key, signer := testCA(t)
	a, err := NewAuthority(key, Policy{Lifetime: time.Hour, RenewBefore: 10 * time.Minute, Backdate: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return a, signer
}
func certificate(t *testing.T, data []byte) *ssh.Certificate {
	t.Helper()
	pub, _, _, rest, err := ssh.ParseAuthorizedKey(data)
	if err != nil || len(rest) != 0 {
		t.Fatalf("certificate parse: %v", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		t.Fatal("not a certificate")
	}
	return cert
}
func checker(ca ssh.Signer, now time.Time) *ssh.CertChecker {
	return &ssh.CertChecker{Clock: func() time.Time { return now }, IsUserAuthority: func(k ssh.PublicKey) bool { return bytes.Equal(k.Marshal(), ca.PublicKey().Marshal()) }, IsHostAuthority: func(k ssh.PublicKey, _ string) bool { return bytes.Equal(k.Marshal(), ca.PublicKey().Marshal()) }}
}
func TestIssueBoundUserAndHostCertificates(t *testing.T) {
	a, ca := testAuthority(t)
	now := time.Unix(1800000000, 0)
	id := testIdentity()
	state, changed, err := a.Ensure(id, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("initial issue must change state")
	}
	user, host := certificate(t, state.UserCertificate), certificate(t, state.HostCertificate)
	if user.CertType != ssh.UserCert || host.CertType != ssh.HostCert {
		t.Fatal("wrong certificate types")
	}
	if user.Signature.Format != ssh.KeyAlgoED25519 || host.Signature.Format != ssh.KeyAlgoED25519 {
		t.Fatal("wrong signature algorithm")
	}
	principal := "hermes:lab:demo:coder"
	dns := "terminal-coder.lab.svc.cluster.local"
	for _, tc := range []struct {
		cert      *ssh.Certificate
		principal string
	}{{user, principal}, {host, dns}} {
		if len(tc.cert.ValidPrincipals) != 1 || tc.cert.ValidPrincipals[0] != tc.principal {
			t.Fatalf("principals: %v", tc.cert.ValidPrincipals)
		}
		if tc.cert.ValidAfter != uint64(now.Add(-time.Minute).Unix()) || tc.cert.ValidBefore != uint64(now.Add(time.Hour).Unix()) {
			t.Fatal("incorrect validity window")
		}
		if err := checker(ca, now).CheckCert(tc.principal, tc.cert); err != nil {
			t.Fatal(err)
		}
	}
	if len(user.Extensions) != 1 || user.Extensions["permit-pty"] != "" {
		t.Fatalf("user extensions: %v", user.Extensions)
	}
	if _, ok := user.Extensions["permit-pty"]; !ok {
		t.Fatal("PTY not permitted")
	}
	if len(host.Extensions) != 0 || len(user.CriticalOptions) != 0 {
		t.Fatal("unexpected certificate permissions")
	}
	uk, err := ssh.ParsePrivateKey(state.UserPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	hk, err := ssh.ParsePrivateKey(state.HostPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(uk.PublicKey().Marshal(), hk.PublicKey().Marshal()) || bytes.Equal(uk.PublicKey().Marshal(), ca.PublicKey().Marshal()) {
		t.Fatal("keys must be distinct")
	}
	if !bytes.Equal(user.Key.Marshal(), uk.PublicKey().Marshal()) || !bytes.Equal(host.Key.Marshal(), hk.PublicKey().Marshal()) {
		t.Fatal("certificate does not match key")
	}
	if err := checker(ca, now).CheckCert("hermes:lab:demo:writer", user); err == nil {
		t.Fatal("wrong profile authorized")
	}
	if err := checker(ca, now).CheckHostKey("writer-terminal.lab.svc.cluster.local:22", nil, host); err == nil {
		t.Fatal("wrong host authorized")
	}
	if err := checker(ca, now).CheckHostKey(dns+":22", nil, host); err != nil {
		t.Fatal(err)
	}
	if err := checker(ca, now.Add(time.Hour)).CheckCert(principal, user); err == nil {
		t.Fatal("expired certificate accepted")
	}
	if err := checker(ca, now.Add(-2*time.Minute)).CheckCert(principal, user); err == nil {
		t.Fatal("not-yet-valid certificate accepted")
	}
}
