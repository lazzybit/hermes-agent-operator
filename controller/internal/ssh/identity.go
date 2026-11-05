// Package sshidentity implements controller-owned SSH identities without Kubernetes
// or filesystem side effects. Callers must persist complete State values atomically.
package sshidentity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"regexp"
	"time"

	"golang.org/x/crypto/ssh"
)

// Identity is derived exclusively from trusted controller resource identity.
// No caller-selected principal or hostname is accepted.
type Identity struct{ Namespace, Instance, Profile, ClusterDomain string }

var label = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (id Identity) validate() error {
	for _, s := range []string{id.Namespace, id.Instance, id.Profile} {
		if !label.MatchString(s) {
			return fmt.Errorf("invalid resource identity %q", s)
		}
	}
	if len(id.ClusterDomain) > 253 || id.ClusterDomain == "" {
		return fmt.Errorf("invalid cluster domain")
	}
	for _, s := range bytes.Split([]byte(id.ClusterDomain), []byte(".")) {
		if !label.Match(s) {
			return fmt.Errorf("invalid cluster domain")
		}
	}
	if len("terminal-"+id.Profile) > 63 || len(id.Hostname()) > 253 {
		return fmt.Errorf("service hostname too long")
	}
	return nil
}
func (id Identity) Principal() string {
	return "hermes:" + id.Namespace + ":" + id.Instance + ":" + id.Profile
}
func (id Identity) Hostname() string {
	return "terminal-" + id.Profile + "." + id.Namespace + ".svc." + id.ClusterDomain
}

type Policy struct{ Lifetime, RenewBefore, Backdate time.Duration }

// Authority retains the supplied CA signer. It never generates a CA.
type Authority struct {
	signer ssh.Signer
	policy Policy
}

func NewAuthority(privateKey []byte, policy Policy) (*Authority, error) {
	if policy.Lifetime < time.Second || policy.RenewBefore < time.Second || policy.RenewBefore >= policy.Lifetime || policy.Backdate < 0 {
		return nil, fmt.Errorf("invalid certificate policy")
	}
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("parse CA: %w", err)
	}
	return &Authority{signer: signer, policy: policy}, nil
}

// State contains sensitive private keys. Never log it. Persist all fields in one
// CAS-protected Secret update, then publish certificates and reload sshd.
type State struct{ UserPrivateKey, HostPrivateKey, UserCertificate, HostCertificate []byte }

func newKey() ([]byte, ssh.Signer, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		return nil, nil, err
	}
	signer, err := ssh.NewSignerFromKey(key)
	return pem.EncodeToMemory(block), signer, err
}
func (a *Authority) issue(key ssh.PublicKey, kind uint32, principal string, now time.Time) ([]byte, error) {
	serial := make([]byte, 8)
	if _, err := rand.Read(serial); err != nil {
		return nil, err
	}
	cert := &ssh.Certificate{Key: key, Serial: binary.BigEndian.Uint64(serial), CertType: kind, KeyId: principal, ValidPrincipals: []string{principal}, ValidAfter: uint64(now.Add(-a.policy.Backdate).Unix()), ValidBefore: uint64(now.Add(a.policy.Lifetime).Unix())}
	if kind == ssh.UserCert {
		cert.Extensions = map[string]string{"permit-pty": ""}
	}
	if err := cert.SignCert(rand.Reader, a.signer); err != nil {
		return nil, err
	}
	return ssh.MarshalAuthorizedKey(cert), nil
}

// Ensure returns a complete new state, changed, and error. It does not mutate old.
// A nil state initializes distinct user and host keys.
func (a *Authority) Ensure(id Identity, old *State, now time.Time) (*State, bool, error) {
	if err := id.validate(); err != nil {
		return nil, false, err
	}
	if now.Add(-a.policy.Backdate).Unix() < 0 || now.Add(a.policy.Lifetime).Unix() <= now.Unix() {
		return nil, false, fmt.Errorf("invalid certificate clock")
	}
	if old != nil {
		return a.refresh(id, old, now)
	}
	user, us, err := newKey()
	if err != nil {
		return nil, false, err
	}
	host, hs, err := newKey()
	if err != nil {
		return nil, false, err
	}
	uc, err := a.issue(us.PublicKey(), ssh.UserCert, id.Principal(), now)
	if err != nil {
		return nil, false, err
	}
	hc, err := a.issue(hs.PublicKey(), ssh.HostCert, id.Hostname(), now)
	if err != nil {
		return nil, false, err
	}
	return &State{user, host, uc, hc}, true, nil
}
