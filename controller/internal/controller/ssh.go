package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	sshidentity "github.com/lazzybit/hermes-agent-operator/internal/ssh"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
)

func managedSSHDCommand() []string {
	return []string{"/usr/sbin/sshd", "-D", "-e", "-f", "/run/hermes-ssh/sshd_config"}
}

func (r *ProfileReconciler) ensureSSH(ctx context.Context, h *api.Hermes, p *api.HermesProfile, d *appsv1.Deployment) (time.Duration, error) {
	domain, lifetime, renew, err := h.Spec.SSH.Settings()
	if err != nil {
		return 0, err
	}
	if r.SecretClient == nil {
		return 0, fmt.Errorf("SSH requires an uncached Secret client")
	}
	signer, err := sshidentity.NewSecretSigner(r.SecretClient, p.Namespace, sshidentity.CASecretReference{Name: h.Spec.SSH.CASecretRef.Name, Key: h.Spec.SSH.CASecretRef.Key}, sshidentity.Policy{Lifetime: lifetime, RenewBefore: renew, Backdate: time.Minute})
	if err != nil {
		return 0, err
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	result, err := signer.Ensure(ctx, sshidentity.Identity{Namespace: p.Namespace, Instance: p.Spec.HermesRef, Profile: p.Name, ClusterDomain: domain}, now)
	if err != nil {
		return 0, err
	}
	config := result.Configuration
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "profile-ssh-config-" + p.Name, Namespace: p.Namespace}, Data: map[string]string{
		"sshd_config": config.SSHD, "client_config": config.Client, "known_hosts": config.KnownHosts, "ca.pub": config.CAPublicKey, "authorized_principals": config.AuthorizedPrincipals,
	}}
	if err := apply(ctx, r.Client, r.Scheme, p, cm); err != nil {
		return 0, err
	}
	// Hash only persisted public certificate and deterministic public config. Never
	// hash resource versions or private keys: retries must not trigger rollouts.
	public, _ := json.Marshal(struct {
		Certificate string
		Config      map[string]string
	}{result.HostCertificate, cm.Data})
	d.Spec.Template.Annotations = map[string]string{"hermes.nousresearch.com/ssh-public-hash": fmt.Sprintf("%x", sha256.Sum256(public))}
	sshProjection(d, p.Name, true)
	d.Spec.Template.Spec.Containers[0].Command = managedSSHDCommand()
	delay := result.RenewAt.Sub(now)
	if delay <= 0 {
		delay = time.Second
	}
	return delay, nil
}

func sshProjection(d *appsv1.Deployment, profile string, server bool) {
	mode := int32(0440)
	publicMode := int32(0444)
	name, path := "ssh-"+profile, "/run/hermes-ssh/"+profile
	keys := []corev1.KeyToPath{{Key: sshidentity.UserPrivateKey, Path: "id_ed25519"}, {Key: sshidentity.UserCertificate, Path: "id_ed25519-cert.pub"}}
	public := []corev1.KeyToPath{{Key: "known_hosts", Path: "known_hosts", Mode: &publicMode}, {Key: "client_config", Path: "client_config", Mode: &publicMode}}
	if server {
		path = "/run/hermes-ssh"
		keys = []corev1.KeyToPath{{Key: sshidentity.HostPrivateKey, Path: "host_key"}, {Key: sshidentity.HostCertificate, Path: "host_key-cert.pub"}}
		public = []corev1.KeyToPath{{Key: "sshd_config", Path: "sshd_config", Mode: &publicMode}, {Key: "ca.pub", Path: "ca.pub", Mode: &publicMode}, {Key: "authorized_principals", Path: "authorized_principals", Mode: &publicMode}}
	}
	pod := &d.Spec.Template.Spec
	private := name + "-private"
	pod.Volumes = append(pod.Volumes, corev1.Volume{Name: private, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
		SecretName: "profile-ssh-identity-" + profile, DefaultMode: &mode,
		Items: []corev1.KeyToPath{{Key: keys[0].Key, Path: "key"}},
	}}})
	// Root-owned keys are readable through fsGroup. Keep the directory projection
	// live so key updates do not depend on a staging init container or subPath.
	pod.Containers[0].VolumeMounts = append(pod.Containers[0].VolumeMounts, corev1.VolumeMount{Name: private, MountPath: "/run/hermes-ssh-private/" + profile, ReadOnly: true})
	pod.Volumes = append(pod.Volumes, corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{DefaultMode: &mode, Sources: []corev1.VolumeProjection{
		{Secret: &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "profile-ssh-identity-" + profile}, Items: keys[1:]}},
		{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "profile-ssh-config-" + profile}, Items: public}},
	}}}})
	pod.Containers[0].VolumeMounts = append(pod.Containers[0].VolumeMounts, corev1.VolumeMount{Name: name, MountPath: path, ReadOnly: true})
	if server {
		// The principal is fixed for this terminal. Mount the file outside the
		// writable projected root so sshd StrictModes can validate its ancestors.
		pod.Containers[0].VolumeMounts = append(pod.Containers[0].VolumeMounts, corev1.VolumeMount{Name: name, MountPath: "/run/hermes-authorized-principals", SubPath: "authorized_principals", ReadOnly: true})
	}
}
