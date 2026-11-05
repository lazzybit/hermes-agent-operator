package sshidentity

import (
	"bytes"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

func sameKey(a, b ssh.PublicKey) bool { return bytes.Equal(a.Marshal(), b.Marshal()) }
func (a *Authority) validateCertificate(data []byte, key ssh.PublicKey, kind uint32, principal string, now time.Time) (*ssh.Certificate, error) {
	pub, _, _, rest, err := ssh.ParseAuthorizedKey(data)
	if err != nil || len(rest) != 0 {
		return nil, fmt.Errorf("invalid certificate encoding")
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil, fmt.Errorf("expected certificate")
	}
	if cert.CertType != kind || cert.KeyId != principal || len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != principal || !sameKey(cert.Key, key) || !sameKey(cert.SignatureKey, a.signer.PublicKey()) {
		return nil, fmt.Errorf("stored certificate identity mismatch")
	}
	if len(cert.CriticalOptions) != 0 || (kind == ssh.HostCert && len(cert.Extensions) != 0) {
		return nil, fmt.Errorf("unexpected certificate permissions")
	}
	if kind == ssh.UserCert {
		value, ok := cert.Extensions["permit-pty"]
		if !ok || value != "" || len(cert.Extensions) != 1 {
			return nil, fmt.Errorf("unexpected user certificate permissions")
		}
	}
	if cert.ValidBefore == ssh.CertTimeInfinity || cert.ValidBefore <= cert.ValidAfter || cert.ValidAfter > uint64(now.Unix()) {
		return nil, fmt.Errorf("invalid stored certificate validity")
	}
	// Verify the real signature at the beginning of its validity interval, so an
	// authentic expired certificate can be renewed without accepting tampering.
	check := ssh.CertChecker{Clock: func() time.Time { return time.Unix(int64(cert.ValidAfter), 0) }}
	if err := check.CheckCert(principal, cert); err != nil {
		return nil, fmt.Errorf("verify stored certificate: %w", err)
	}
	return cert, nil
}
func (a *Authority) refresh(id Identity, old *State, now time.Time) (*State, bool, error) {
	us, err := ssh.ParsePrivateKey(old.UserPrivateKey)
	if err != nil {
		return nil, false, fmt.Errorf("parse user key: %w", err)
	}
	hs, err := ssh.ParsePrivateKey(old.HostPrivateKey)
	if err != nil {
		return nil, false, fmt.Errorf("parse host key: %w", err)
	}
	if sameKey(us.PublicKey(), hs.PublicKey()) || sameKey(us.PublicKey(), a.signer.PublicKey()) || sameKey(hs.PublicKey(), a.signer.PublicKey()) {
		return nil, false, fmt.Errorf("CA, user and host keys must be distinct")
	}
	uc, err := a.validateCertificate(old.UserCertificate, us.PublicKey(), ssh.UserCert, id.Principal(), now)
	if err != nil {
		return nil, false, err
	}
	hc, err := a.validateCertificate(old.HostCertificate, hs.PublicKey(), ssh.HostCert, id.Hostname(), now)
	renamed := false
	if err != nil {
		// Accept only the exact former Service identity, signed by the same CA.
		legacy := id.Profile + "-terminal." + id.Namespace + ".svc." + id.ClusterDomain
		hc, err = a.validateCertificate(old.HostCertificate, hs.PublicKey(), ssh.HostCert, legacy, now)
		if err != nil {
			return nil, false, err
		}
		renamed = true
	}
	next := &State{bytes.Clone(old.UserPrivateKey), bytes.Clone(old.HostPrivateKey), bytes.Clone(old.UserCertificate), bytes.Clone(old.HostCertificate)}
	threshold := uint64(now.Add(a.policy.RenewBefore).Unix())
	changed := false
	if uc.ValidBefore <= threshold {
		next.UserCertificate, err = a.issue(us.PublicKey(), ssh.UserCert, id.Principal(), now)
		if err != nil {
			return nil, false, err
		}
		changed = true
	}
	if renamed || hc.ValidBefore <= threshold {
		next.HostCertificate, err = a.issue(hs.PublicKey(), ssh.HostCert, id.Hostname(), now)
		if err != nil {
			return nil, false, err
		}
		changed = true
	}
	return next, changed, nil
}
