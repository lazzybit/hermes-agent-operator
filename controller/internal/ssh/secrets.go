// Package signing persists SSH identities in Kubernetes Secrets. Only this
// adapter needs access to the supplied CA private key; results contain public
// configuration and a state reference, never private key material.
package sshidentity

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	UserPrivateKey  = "user_key"
	HostPrivateKey  = "host_key"
	UserCertificate = "user_cert"
	HostCertificate = "host_cert"
)

// CASecretReference selects an existing Secret in the signer's namespace.
// Key defaults to ca_key. The CA is never generated or copied into state.
type CASecretReference struct{ Name, Key string }

// Result is safe for public configuration consumers. Secret must only be mounted
// in process-specific subsets described by sshidentity's integration contract.
type Result struct {
	// HostCertificate is public persisted certificate data for server reload hashes.
	HostCertificate string
	Secret          types.NamespacedName
	Configuration   Configuration
	RenewAt         time.Time
	Changed         bool
}

// SecretSigner is namespace scoped and uses one supplied CA for all profiles.
// Use an uncached client/API reader for reliable conflict retries and CA reads.
type SecretSigner struct {
	client    client.Client
	namespace string
	ca        CASecretReference
	policy    Policy
}

func NewSecretSigner(c client.Client, namespace string, ca CASecretReference, policy Policy) (*SecretSigner, error) {
	if c == nil || len(validation.IsDNS1123Label(namespace)) != 0 || len(validation.IsDNS1123Subdomain(ca.Name)) != 0 {
		return nil, fmt.Errorf("invalid signer namespace, client or CA reference")
	}
	if ca.Key == "" {
		ca.Key = "ca_key"
	}
	if len(validation.IsConfigMapKey(ca.Key)) != 0 {
		return nil, fmt.Errorf("invalid CA data key")
	}
	if policy.Lifetime < time.Second || policy.RenewBefore < time.Second || policy.RenewBefore >= policy.Lifetime || policy.Backdate < 0 {
		return nil, fmt.Errorf("invalid certificate policy")
	}
	return &SecretSigner{client: c, namespace: namespace, ca: ca, policy: policy}, nil
}

// Ensure derives fixed principals from resource identity and atomically creates
// or renews the complete state. No caller-selected principal is accepted.
// State names are fixed per profile; instance/CA changes fail closed until an
// explicit migration is performed. It never returns an unpersisted candidate.
func (s *SecretSigner) Ensure(ctx context.Context, id Identity, now time.Time) (Result, error) {
	if id.Namespace != s.namespace {
		return Result{}, fmt.Errorf("identity outside signer namespace")
	}
	ca := &corev1.Secret{}
	if err := s.client.Get(ctx, types.NamespacedName{Namespace: s.namespace, Name: s.ca.Name}, ca); err != nil {
		return Result{}, fmt.Errorf("read CA Secret: %w", err)
	}
	authority, err := NewAuthority(ca.Data[s.ca.Key], s.policy)
	if err != nil {
		return Result{}, fmt.Errorf("invalid CA Secret key")
	}
	configuration, err := authority.Configuration(id)
	if err != nil {
		return Result{}, err
	}
	ref := types.NamespacedName{Namespace: s.namespace, Name: "profile-ssh-identity-" + id.Profile}
	if ref.Name == s.ca.Name {
		return Result{}, fmt.Errorf("CA and state Secret must differ")
	}
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		result, err := s.ensureOnce(ctx, authority, id, now, configuration, ref)
		if err == nil {
			return result, nil
		}
		if !apierrors.IsConflict(err) && !apierrors.IsAlreadyExists(err) {
			return Result{}, err
		}
		last = err
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
	}
	return Result{}, fmt.Errorf("SSH state contention: %w", last)
}

func (s *SecretSigner) ensureOnce(ctx context.Context, authority *Authority, id Identity, now time.Time, configuration Configuration, ref types.NamespacedName) (Result, error) {
	secret := &corev1.Secret{}
	err := s.client.Get(ctx, ref, secret)
	absent := apierrors.IsNotFound(err)
	if absent {
		legacy := &corev1.Secret{}
		legacyErr := s.client.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: id.Profile + "-ssh-identity"}, legacy)
		if legacyErr == nil {
			if len(legacy.OwnerReferences) != 0 || !legacy.DeletionTimestamp.IsZero() {
				return Result{}, fmt.Errorf("unsafe legacy SSH identity state")
			}
			// Validate both certificates, keys, CA and identity before copying state.
			previous := &State{UserPrivateKey: legacy.Data[UserPrivateKey], HostPrivateKey: legacy.Data[HostPrivateKey], UserCertificate: legacy.Data[UserCertificate], HostCertificate: legacy.Data[HostCertificate]}
			next, _, validationErr := authority.Ensure(id, previous, now)
			if validationErr != nil {
				return Result{}, fmt.Errorf("invalid legacy SSH identity state")
			}
			candidate := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: ref.Namespace}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{UserPrivateKey: next.UserPrivateKey, HostPrivateKey: next.HostPrivateKey, UserCertificate: next.UserCertificate, HostCertificate: next.HostCertificate}}
			if createErr := s.client.Create(ctx, candidate); createErr != nil {
				return Result{}, createErr
			}
			secret, absent, err = candidate, false, nil
		} else if !apierrors.IsNotFound(legacyErr) {
			return Result{}, legacyErr
		}
	}
	if err != nil && !absent {
		return Result{}, fmt.Errorf("read SSH state: %w", err)
	}
	var previous *State
	if !absent {
		previous = &State{UserPrivateKey: secret.Data[UserPrivateKey], HostPrivateKey: secret.Data[HostPrivateKey], UserCertificate: secret.Data[UserCertificate], HostCertificate: secret.Data[HostCertificate]}
	}
	next, changed, err := authority.Ensure(id, previous, now)
	if err != nil {
		return Result{}, fmt.Errorf("invalid SSH identity state")
	}
	if changed {
		if absent {
			secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ref.Namespace, Name: ref.Name}, Type: corev1.SecretTypeOpaque}
		}
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		secret.Data[UserPrivateKey] = next.UserPrivateKey
		secret.Data[HostPrivateKey] = next.HostPrivateKey
		secret.Data[UserCertificate] = next.UserCertificate
		secret.Data[HostCertificate] = next.HostCertificate
		if absent {
			err = s.client.Create(ctx, secret)
		} else {
			err = s.client.Update(ctx, secret)
		}
		if err != nil {
			return Result{}, err
		}
	}
	renewAt := time.Time{}
	for _, data := range [][]byte{next.UserCertificate, next.HostCertificate} {
		key, _, _, _, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			return Result{}, fmt.Errorf("invalid persisted certificate")
		}
		cert, ok := key.(*ssh.Certificate)
		if !ok {
			return Result{}, fmt.Errorf("invalid persisted certificate type")
		}
		deadline := time.Unix(int64(cert.ValidBefore), 0).Add(-s.policy.RenewBefore)
		if renewAt.IsZero() || deadline.Before(renewAt) {
			renewAt = deadline
		}
	}
	return Result{Secret: ref, Configuration: configuration, HostCertificate: string(next.HostCertificate), RenewAt: renewAt, Changed: changed}, nil
}
