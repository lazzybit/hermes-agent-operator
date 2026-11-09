package controller

import (
	"context"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ensureWorkloadIdentity(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner client.Object, d *appsv1.Deployment) error {
	no := false
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: d.Spec.Template.Spec.ServiceAccountName, Namespace: d.Namespace}, AutomountServiceAccountToken: &no}
	return apply(ctx, c, scheme, owner, sa)
}
