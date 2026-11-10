package controller

import (
	"context"
	"fmt"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Translate the bound Channel to the existing Gateway projection envelope.
// Pending claims project no adapter and do not block other profiles.
func consumeChannelClaim(ctx context.Context, c client.Client, profile api.HermesProfile) (*api.MessagingChannel, error) {
	claim := &api.ChannelClaim{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: profile.Namespace, Name: profile.Spec.Messaging.ClaimName}, claim); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if claim.Status.Phase != "Available" || claim.Status.ChannelName == "" || !claim.DeletionTimestamp.IsZero() {
		return nil, nil
	}
	profiles := &api.HermesProfileList{}
	if err := c.List(ctx, profiles, client.InNamespace(profile.Namespace)); err != nil {
		return nil, err
	}
	// Stable winner across reconcilers, including different Hermes instances.
	winner := profile.Name
	for _, p := range profiles.Items {
		if p.DeletionTimestamp.IsZero() && p.Spec.Messaging != nil && p.Spec.Messaging.ClaimName == claim.Name && p.Name < winner {
			winner = p.Name
		}
	}
	if winner != profile.Name {
		return nil, nil
	}
	channel := &api.Channel{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: profile.Namespace, Name: claim.Status.ChannelName}, channel); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if channel.Spec.ClaimRef == nil || channel.Spec.ClaimRef.UID != claim.UID {
		return nil, fmt.Errorf("Channel reciprocal binding does not match Claim UID")
	}
	out := &api.MessagingChannel{ObjectMeta: channel.ObjectMeta, Spec: api.MessagingChannelSpec{Platform: "telegram", Telegram: channel.Spec.Telegram.DeepCopy()}, Status: api.MessagingChannelStatus{Phase: api.MessagingBound, ClaimRef: &api.MessagingClaimRef{Kind: "HermesProfile", Name: profile.Name, UID: profile.UID}}}
	return out, nil
}
