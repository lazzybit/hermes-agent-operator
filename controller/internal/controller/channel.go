package controller

import (
	"context"
	"fmt"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"
)

type ChannelClaimReconciler struct{ client.Client }

func (r *ChannelClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	claim := &api.ChannelClaim{}
	if err := r.Get(ctx, req.NamespacedName, claim); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !claim.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	before := claim.DeepCopy()
	reason, message := "BindingPending", "Waiting for a Channel"
	claim.Status.Phase = "Pending"
	name := claim.Spec.ChannelName
	if claim.Status.ChannelName != "" {
		name = claim.Status.ChannelName
	}
	if name == "" && claim.Spec.ChannelClassName != nil && *claim.Spec.ChannelClassName != "" {
		class := &api.ChannelClass{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: claim.Namespace, Name: *claim.Spec.ChannelClassName}, class); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			reason, message = "ClassNotFound", "ChannelClass does not exist"
		} else {
			session := &corev1.Secret{}
			ref := class.Spec.Parameters.UserSessionSecretRef.Name
			if ref == "" {
				reason, message = "UserSessionRequired", "Automatic Telegram provisioning requires an authorized MTProto user session"
			} else if err := r.Get(ctx, types.NamespacedName{Namespace: claim.Namespace, Name: ref}, session); err != nil {
				if !apierrors.IsNotFound(err) {
					return ctrl.Result{}, err
				}
				reason, message = "UserSessionRequired", "MTProto user session Secret does not exist"
			} else {
				generated, provisionErr := r.provisionTelegram(ctx, claim, class, session)
				if provisionErr != nil {
					reason, message = "ProvisioningFailed", provisionErr.Error()
				} else {
					name = generated
				}
			}
		}
	}
	if name != "" {
		channel := &api.Channel{}
		err := r.Get(ctx, types.NamespacedName{Namespace: claim.Namespace, Name: name}, channel)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if err == nil {
			expected := ""
			if claim.Spec.ChannelClassName != nil {
				expected = *claim.Spec.ChannelClassName
			}
			if channel.Spec.ChannelClassName != expected {
				reason, message = "ClassMismatch", "Channel class differs from claim"
			} else if ref := channel.Spec.ClaimRef; ref != nil && ref.UID != claim.UID {
				reason, message = "AlreadyBound", "Channel belongs to another Claim UID"
			} else {
				secret := &corev1.Secret{}
				ref := channel.Spec.Telegram.TokenSecretRef
				err = r.Get(ctx, types.NamespacedName{Namespace: claim.Namespace, Name: ref.Name}, secret)
				if err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, err
				}
				if err != nil || len(secret.Data[ref.Key]) == 0 {
					reason, message = "CredentialsNotReady", "Channel token Secret is missing or empty"
				} else {
					err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
						current := &api.Channel{}
						if e := r.Get(ctx, client.ObjectKeyFromObject(channel), current); e != nil {
							return e
						}
						if !current.DeletionTimestamp.IsZero() {
							return fmt.Errorf("channel is deleting")
						}
						if ref := current.Spec.ClaimRef; ref != nil && ref.UID != claim.UID {
							return fmt.Errorf("channel already bound")
						}
						if current.Spec.ClaimRef != nil {
							return nil
						}
						current.Spec.ClaimRef = &api.MessagingClaimRef{Kind: "ChannelClaim", Name: claim.Name, UID: claim.UID}
						return r.Update(ctx, current)
					})
					if err != nil {
						return ctrl.Result{}, err
					}
					claim.Status.Phase = "Available"
					claim.Status.ChannelName = name
					reason, message = "Bound", "Channel and credentials are bound to this Claim"
				}
			}
		}
	}
	claim.Status.ObservedGeneration = claim.Generation
	state := metav1.ConditionFalse
	if claim.Status.Phase == "Available" {
		state = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{Type: "Available", Status: state, Reason: reason, Message: message, ObservedGeneration: claim.Generation})
	if err := r.Status().Patch(ctx, claim, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *ChannelClaimReconciler) requests(ctx context.Context, obj client.Object) []reconcile.Request {
	claims := &api.ChannelClaimList{}
	if r.List(ctx, claims, client.InNamespace(obj.GetNamespace())) != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(claims.Items))
	for _, c := range claims.Items {
		out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&c)})
	}
	return out
}
func (r *ChannelClaimReconciler) SetupWithManager(m ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(m).For(&api.ChannelClaim{}).Watches(&api.Channel{}, handler.EnqueueRequestsFromMapFunc(r.requests)).Watches(&api.ChannelClass{}, handler.EnqueueRequestsFromMapFunc(r.requests)).WatchesMetadata(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.requests)).Complete(r)
}
