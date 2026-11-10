package controller

import (
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func channelTokenRef(c api.MessagingChannel) *corev1.SecretKeySelector {
	if c.Spec.Telegram != nil {
		return &c.Spec.Telegram.TokenSecretRef
	}
	if c.Spec.Ntfy != nil {
		return c.Spec.Ntfy.TokenSecretRef
	}
	return nil
}
