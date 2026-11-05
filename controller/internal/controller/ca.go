package controller

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	sshidentity "github.com/lazzybit/hermes-agent-operator/internal/ssh"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"time"
)

// effectiveSSH supplies internal defaults without modifying the user's resource.
func effectiveSSH(h *api.Hermes) *api.Hermes {
	h = h.DeepCopy()
	if h.Spec.SSH == nil {
		h.Spec.SSH = &api.SSHSpec{}
	}
	return h
}

// ensureManagedCA creates only our own CA and never replaces an invalid/foreign key.
// Its owner reference lets Kubernetes remove it when the Hermes is deleted.
func ensureManagedCA(ctx context.Context, c client.Client, s *runtime.Scheme, h *api.Hermes) error {
	key := client.ObjectKey{Namespace: h.Namespace, Name: "gateway-ssh-ca"}
	for attempt := 0; attempt < 3; attempt++ {
		secret := &corev1.Secret{}
		err := c.Get(ctx, key, secret)
		if err == nil {
			owner := metav1.GetControllerOf(secret)
			if owner == nil || owner.UID != h.UID || owner.Name != h.Name || !secret.DeletionTimestamp.IsZero() {
				return fmt.Errorf("refusing foreign or deleting managed SSH CA")
			}
			if _, err := ssh.ParsePrivateKey(secret.Data["ca_key"]); err != nil {
				return fmt.Errorf("invalid managed SSH CA; restore the original Secret")
			}
			return nil
		}
		if !apierrors.IsNotFound(err) {
			return err
		}

		// Upgrade the previous fixed-name CA without changing trust or exposing keys.
		legacy := &corev1.Secret{}
		legacyErr := c.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: "ssh-ca"}, legacy)
		if legacyErr == nil {
			if !legacy.DeletionTimestamp.IsZero() {
				return fmt.Errorf("legacy SSH CA is deleting")
			}
			domain, lifetime, renew, err := effectiveSSH(h).Spec.SSH.Settings()
			if err != nil {
				return err
			}
			authority, err := sshidentity.NewAuthority(legacy.Data["ca_key"], sshidentity.Policy{Lifetime: lifetime, RenewBefore: renew, Backdate: time.Minute})
			if err != nil {
				return fmt.Errorf("invalid legacy SSH CA")
			}
			profiles := &api.HermesProfileList{}
			if err := c.List(ctx, profiles, client.InNamespace(h.Namespace)); err != nil {
				return err
			}
			for _, p := range profiles.Items {
				if p.Spec.HermesRef != h.Name {
					continue
				}
				for _, name := range []string{"profile-ssh-identity-" + p.Name, p.Name + "-ssh-identity"} {
					state := &corev1.Secret{}
					err := c.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: name}, state)
					if apierrors.IsNotFound(err) {
						continue
					}
					if err != nil {
						return err
					}
					if owner := metav1.GetControllerOf(state); owner != nil && owner.UID != h.UID {
						return fmt.Errorf("foreign legacy SSH identity")
					}
					_, _, err = authority.Ensure(sshidentity.Identity{Namespace: h.Namespace, Instance: h.Name, Profile: p.Name, ClusterDomain: domain}, &sshidentity.State{UserPrivateKey: state.Data["user_key"], HostPrivateKey: state.Data["host_key"], UserCertificate: state.Data["user_cert"], HostCertificate: state.Data["host_cert"]}, time.Now())
					if err != nil {
						return fmt.Errorf("legacy CA does not match existing SSH identity")
					}
				}
			}
			secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"ca_key": append([]byte(nil), legacy.Data["ca_key"]...)}}
			if err := controllerutil.SetControllerReference(h, secret, s, controllerutil.WithBlockOwnerDeletion(false)); err != nil {
				return err
			}
			if err := c.Create(ctx, secret); apierrors.IsAlreadyExists(err) {
				continue
			} else {
				return err
			}
		}
		if !apierrors.IsNotFound(legacyErr) {
			return legacyErr
		}

		// Do not change the trust root underneath already-issued identity state.
		profiles := &api.HermesProfileList{}
		if err := c.List(ctx, profiles, client.InNamespace(h.Namespace)); err != nil {
			return err
		}
		for _, p := range profiles.Items {
			if p.Spec.HermesRef != h.Name {
				continue
			}
			for _, name := range []string{"profile-ssh-identity-" + p.Name, p.Name + "-ssh-identity"} {
				err := c.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: name}, &corev1.Secret{})
				if err == nil {
					return fmt.Errorf("managed SSH CA missing while identity state exists; restore CA or explicitly reset SSH identities")
				}
				if !apierrors.IsNotFound(err) {
					return err
				}
			}
		}
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		block, err := ssh.MarshalPrivateKey(private, "")
		if err != nil {
			return err
		}
		secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"ca_key": pem.EncodeToMemory(block)}}
		if err := controllerutil.SetControllerReference(h, secret, s, controllerutil.WithBlockOwnerDeletion(false)); err != nil {
			return err
		}
		if err := c.Create(ctx, secret); apierrors.IsAlreadyExists(err) {
			continue
		} else {
			return err
		}
	}
	return fmt.Errorf("managed SSH CA creation contention")
}
