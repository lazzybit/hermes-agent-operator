package controller

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// fenceRenamedDeployment waits for foreground deletion, including dependent Pods,
// before a replacement can start writing the same gateway or profile state.
func fenceRenamedDeployment(ctx context.Context, c client.Client, owner client.Object, oldName string) (bool, error) {
	old := &appsv1.Deployment{}
	err := c.Get(ctx, client.ObjectKey{Namespace: owner.GetNamespace(), Name: oldName}, old)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ref := metav1.GetControllerOf(old)
	if ref == nil || ref.UID != owner.GetUID() || ref.Name != owner.GetName() {
		return false, fmt.Errorf("refusing to delete foreign Deployment %s", oldName)
	}
	if old.DeletionTimestamp.IsZero() {
		if err := c.Delete(ctx, old, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	return true, nil
}

// Renamed persistent claims need an explicit data transfer; never create an empty
// replacement merely because the old name no longer matches the naming scheme.
func rejectLegacyClaim(ctx context.Context, c client.Client, owner client.Object, oldName string, ephemeral bool) error {
	if ephemeral {
		return nil
	}
	old := &corev1.PersistentVolumeClaim{}
	err := c.Get(ctx, client.ObjectKey{Namespace: owner.GetNamespace(), Name: oldName}, old)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("persistent PVC %s requires explicit migration; retain data and remove the old claim only after verified transfer", oldName)
}

func cleanupOwned(ctx context.Context, c client.Client, owner client.Object, objects ...client.Object) error {
	for _, old := range objects {
		old.SetNamespace(owner.GetNamespace())
		if err := c.Get(ctx, client.ObjectKeyFromObject(old), old); apierrors.IsNotFound(err) {
			continue
		} else if err != nil {
			return err
		}
		ref := metav1.GetControllerOf(old)
		if ref == nil || ref.UID != owner.GetUID() || ref.Name != owner.GetName() {
			return fmt.Errorf("refusing to delete foreign %T %s", old, old.GetName())
		}
		if err := c.Delete(ctx, old); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

var renameWait = 2 * time.Second
