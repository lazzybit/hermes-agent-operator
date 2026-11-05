package controller

import corev1 "k8s.io/api/core/v1"

func nonrootPod() *corev1.PodSecurityContext {
	uid, yes := int64(65532), true
	return &corev1.PodSecurityContext{RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid, RunAsNonRoot: &yes, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}
}
func restrictedContainer() *corev1.SecurityContext {
	no := false
	return &corev1.SecurityContext{AllowPrivilegeEscalation: &no, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}
}
