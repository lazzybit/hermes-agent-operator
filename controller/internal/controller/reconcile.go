package controller

import (
	"context"
	"fmt"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"
)

type HermesReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}
type ProfileReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// SecretClient must bypass the informer cache for signing CAS reads.
	SecretClient client.Client
	Now          func() time.Time
}

// apply never adopts pre-existing workloads. PVCs are retained and only reused
// when their stable resource identity matches. Existing PVC specs are immutable here.
func apply(ctx context.Context, c client.Client, s *runtime.Scheme, owner client.Object, want client.Object) error {
	pvc, isPVC := want.(*corev1.PersistentVolumeClaim)
	if isPVC {
		if pvc.Annotations == nil {
			pvc.Annotations = map[string]string{}
		}
		pvc.Annotations["hermes.nousresearch.com/retained-for"] = fmt.Sprintf("%T/%s", owner, owner.GetName())
		// The sentinel cannot be a storage class name; an explicit empty class is
		// distinct from omission, which permits API-server defaulting.
		requestedClass := "<omitted>"
		if pvc.Spec.StorageClassName != nil {
			requestedClass = *pvc.Spec.StorageClassName
		}
		pvc.Annotations["hermes.nousresearch.com/requested-storage-class"] = requestedClass
	} else if err := controllerutil.SetControllerReference(owner, want, s, controllerutil.WithBlockOwnerDeletion(false)); err != nil {
		return err
	}
	current := want.DeepCopyObject().(client.Object)
	err := c.Get(ctx, client.ObjectKeyFromObject(want), current)
	if apierrors.IsNotFound(err) {
		return c.Create(ctx, want)
	}
	if err != nil {
		return err
	}
	if isPVC {
		old := current.(*corev1.PersistentVolumeClaim)
		if len(old.OwnerReferences) != 0 || !old.DeletionTimestamp.IsZero() {
			return fmt.Errorf("refusing unsafe retained PVC %s: owner references or deletion timestamp present", want.GetName())
		}
		if old.Annotations["hermes.nousresearch.com/retained-for"] != pvc.Annotations["hermes.nousresearch.com/retained-for"] {
			return fmt.Errorf("refusing to adopt PVC %s", want.GetName())
		}
		requested, recorded := old.Annotations["hermes.nousresearch.com/requested-storage-class"]
		if !recorded || requested != pvc.Annotations["hermes.nousresearch.com/requested-storage-class"] {
			return fmt.Errorf("PVC %s requested storage class changed or is unrecorded; manual migration required", want.GetName())
		}
		a, b := old.Spec.StorageClassName, pvc.Spec.StorageClassName
		if old.Spec.Resources.Requests.Storage().Cmp(*pvc.Spec.Resources.Requests.Storage()) != 0 || (b != nil && (a == nil || *a != *b)) {
			return fmt.Errorf("PVC %s storage changes require manual migration", want.GetName())
		}
		return nil
	}
	ref := metav1.GetControllerOf(current)
	if ref == nil || ref.UID != owner.GetUID() || ref.Name != owner.GetName() {
		return fmt.Errorf("refusing to adopt %T %s", want, want.GetName())
	}
	before := current.DeepCopyObject().(client.Object)
	switch old := current.(type) {
	case *appsv1.Deployment:
		desired := want.(*appsv1.Deployment)
		old.Spec.Replicas = desired.Spec.Replicas
		old.Spec.Strategy = desired.Spec.Strategy
		old.Spec.Template.Labels = desired.Spec.Template.Labels
		old.Spec.Template.Annotations = desired.Spec.Template.Annotations
		// Preserve API-defaulted fields by patching only the fields the controller owns.
		old.Spec.Template.Spec.Containers = desired.Spec.Template.Spec.Containers
		old.Spec.Template.Spec.InitContainers = desired.Spec.Template.Spec.InitContainers
		old.Spec.Template.Spec.SecurityContext = desired.Spec.Template.Spec.SecurityContext
		old.Spec.Template.Spec.Volumes = desired.Spec.Template.Spec.Volumes
		old.Spec.Template.Spec.AutomountServiceAccountToken = desired.Spec.Template.Spec.AutomountServiceAccountToken
	case *networkingv1.NetworkPolicy:
		old.Spec = want.(*networkingv1.NetworkPolicy).Spec
	case *corev1.ConfigMap:
		old.Data = want.(*corev1.ConfigMap).Data
	case *corev1.Service:
		desired := want.(*corev1.Service)
		old.Spec.Selector = desired.Spec.Selector
		old.Spec.Ports = desired.Spec.Ports
	default:
		return fmt.Errorf("unsupported resource %T", want)
	}
	patch := client.MergeFrom(before)
	data, err := patch.Data(current)
	if err != nil {
		return err
	}
	if string(data) == "{}" {
		return nil
	}
	return c.Patch(ctx, current, patch)
}

func finish(ctx context.Context, c client.Client, obj client.Object, status *api.ResourceStatus, err error, references ...metav1.Condition) error {
	before := obj.DeepCopyObject().(client.Object)
	status.ObservedGeneration = obj.GetGeneration()
	condition := metav1.Condition{Type: "ResourcesReconciled", Status: metav1.ConditionTrue, Reason: "Reconciled", Message: "Foundation resources reconciled; workload health is not validated", ObservedGeneration: obj.GetGeneration()}
	if err != nil {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "ReconcileError"
		condition.Message = err.Error()
	}
	meta.SetStatusCondition(&status.Conditions, condition)
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "IntegrationPending", Message: "Live profile discovery, Hermes strict SSH backend integration and network enforcement remain pending", ObservedGeneration: obj.GetGeneration()})
	for _, reference := range references {
		meta.SetStatusCondition(&status.Conditions, reference)
		if reference.Status != metav1.ConditionTrue {
			meta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "ReferenceInvalid", Message: reference.Message, ObservedGeneration: obj.GetGeneration()})
		}
	}
	if patchErr := c.Status().Patch(ctx, obj, client.MergeFrom(before)); patchErr != nil {
		return fmt.Errorf("reconcile error: %v; status: %w", err, patchErr)
	}
	return err
}
func ensureClaim(ctx context.Context, c client.Client, s *runtime.Scheme, o client.Object, name string, storage api.StorageSpec) error {
	if storage.Ephemeral {
		return nil
	}
	p, err := Claim(name, o.GetNamespace(), storage)
	if err != nil {
		return err
	}
	return apply(ctx, c, s, o, p)
}
func (r *HermesReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	h := &api.Hermes{}
	if err = r.Get(ctx, req.NamespacedName, h); err != nil {
		return result, client.IgnoreNotFound(err)
	}
	if !h.DeletionTimestamp.IsZero() {
		return result, nil
	}
	defer func() { err = finish(ctx, r.Client, h, &h.Status, err) }()
	if err = rejectLegacyClaim(ctx, r.Client, h, h.Name+"-home", h.Spec.Storage.Ephemeral); err != nil {
		return
	}
	if waiting, fenceErr := fenceRenamedDeployment(ctx, r.Client, h, h.Name+"-gateway"); fenceErr != nil {
		err = fenceErr
		return
	} else if waiting {
		result.RequeueAfter = renameWait
		return
	}
	if err = ensureClaim(ctx, r.Client, r.Scheme, h, "gateway-data", h.Spec.Storage); err != nil {
		return
	}
	if err = ensureManagedCA(ctx, r.Client, r.Scheme, h); err != nil {
		return
	}
	list := &api.HermesProfileList{}
	if err = r.List(ctx, list, client.InNamespace(h.Namespace)); err != nil {
		return
	}
	profiles := []api.HermesProfile{}
	for _, p := range list.Items {
		if p.Spec.HermesRef == h.Name && p.DeletionTimestamp.IsZero() {
			if p.Name == "default" {
				err = fmt.Errorf("profile name default is reserved for the instance home")
				return
			}
			if err = rejectLegacyClaim(ctx, r.Client, &p, p.Name+"-profile", p.Spec.Storage.Ephemeral); err != nil {
				return
			}
			if err = ensureClaim(ctx, r.Client, r.Scheme, &p, "profile-data-"+p.Name, p.Spec.Storage); err != nil {
				return
			}
			profiles = append(profiles, p)
		}
	}
	if err = apply(ctx, r.Client, r.Scheme, h, GatewayNetworkPolicy(h)); err != nil {
		return
	}
	d := Gateway(h, profiles)
	if err = r.configurationRevision(ctx, h, profiles, d); err != nil {
		return
	}
	err = apply(ctx, r.Client, r.Scheme, h, d)
	if err == nil {
		err = cleanupOwned(ctx, r.Client, h, &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: h.Name + "-gateway"}}, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: h.Name + "-gateway-managed-config"}})
	}
	if err == nil {
		for i := range profiles {
			if err = cleanupOwned(ctx, r.Client, &profiles[i], &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: profiles[i].Name + "-managed-config"}}); err != nil {
				break
			}
		}
	}
	result.RequeueAfter = time.Minute
	return
}
func (r *ProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	p := &api.HermesProfile{}
	if err = r.Get(ctx, req.NamespacedName, p); err != nil {
		return result, client.IgnoreNotFound(err)
	}
	if !p.DeletionTimestamp.IsZero() {
		return result, nil
	}
	reference := metav1.Condition{Type: "ReferenceValid", Status: metav1.ConditionTrue, Reason: "ReferenceResolved", Message: "Referenced Hermes exists", ObservedGeneration: p.Generation}
	defer func() { err = finish(ctx, r.Client, p, &p.Status, err, reference) }()
	if p.Name == "default" {
		err = fmt.Errorf("profile name default is reserved for the instance home")
		return
	}
	h := &api.Hermes{}
	if err = r.Get(ctx, types.NamespacedName{Name: p.Spec.HermesRef, Namespace: p.Namespace}, h); err != nil {
		if !apierrors.IsNotFound(err) {
			reference.Status, reference.Reason, reference.Message = metav1.ConditionUnknown, "ReferenceLookupFailed", err.Error()
			return
		}
		reference.Status, reference.Reason, reference.Message = metav1.ConditionFalse, "ReferenceInvalid", "Referenced Hermes does not exist"
		err = nil
	} else if !h.DeletionTimestamp.IsZero() {
		reference.Status, reference.Reason, reference.Message = metav1.ConditionFalse, "ReferenceInvalid", "Referenced Hermes is deleting"
	}
	if err = rejectLegacyClaim(ctx, r.Client, p, p.Name+"-profile", p.Spec.Storage.Ephemeral); err != nil {
		return
	}
	if err = rejectLegacyClaim(ctx, r.Client, p, p.Name+"-workspace", p.Spec.Terminal.Storage.Ephemeral); err != nil {
		return
	}
	if waiting, fenceErr := fenceRenamedDeployment(ctx, r.Client, p, p.Name+"-terminal"); fenceErr != nil {
		err = fenceErr
		return
	} else if waiting {
		result.RequeueAfter = renameWait
		return
	}
	// Terminal resources belong to the profile and remain independently declared.
	if err = ensureClaim(ctx, r.Client, r.Scheme, p, "profile-data-"+p.Name, p.Spec.Storage); err != nil {
		return
	}
	if err = ensureClaim(ctx, r.Client, r.Scheme, p, "terminal-data-"+p.Name, p.Spec.Terminal.Storage); err != nil {
		return
	}
	if err = apply(ctx, r.Client, r.Scheme, p, TerminalNetworkPolicy(p)); err != nil {
		return
	}
	d, s := Terminal(p)
	if reference.Status == metav1.ConditionTrue {
		var delay time.Duration
		if delay, err = r.ensureSSH(ctx, h, p, d); err != nil {
			return
		}
		result.RequeueAfter = delay
	}
	if err = apply(ctx, r.Client, r.Scheme, p, d); err != nil {
		return
	}
	err = apply(ctx, r.Client, r.Scheme, p, s)
	if err == nil {
		err = cleanupOwned(ctx, r.Client, p, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-terminal"}}, &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-terminal"}}, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-ssh-public"}})
	}
	if result.RequeueAfter == 0 {
		result.RequeueAfter = time.Minute
	}
	return
}

// Profile lifecycle events enqueue namespace instances to refresh gateway mounts.
// Parent bindings are immutable through CRD admission.
func (r *HermesReconciler) profileRequests(ctx context.Context, obj client.Object) []reconcile.Request {
	list := &api.HermesList{}
	if err := r.List(ctx, list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for _, h := range list.Items {
		out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&h)})
	}
	return out
}
func (r *HermesReconciler) SetupWithManager(m ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(m).For(&api.Hermes{}).Owns(&networkingv1.NetworkPolicy{}).Owns(&appsv1.Deployment{}).Owns(&corev1.ConfigMap{}).Watches(&api.HermesProfile{}, handler.EnqueueRequestsFromMapFunc(r.profileRequests)).WatchesMetadata(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.configSecretRequests)).Complete(r)
}
func (r *ProfileReconciler) parentRequests(ctx context.Context, obj client.Object) []reconcile.Request {
	list := &api.HermesProfileList{}
	if err := r.List(ctx, list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var out []reconcile.Request
	for _, p := range list.Items {
		if p.Spec.HermesRef == obj.GetName() {
			out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&p)})
		}
	}
	return out
}
func (r *ProfileReconciler) SetupWithManager(m ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(m).For(&api.HermesProfile{}).Owns(&networkingv1.NetworkPolicy{}).Owns(&appsv1.Deployment{}).Owns(&corev1.Service{}).Owns(&corev1.ConfigMap{}).Watches(&api.Hermes{}, handler.EnqueueRequestsFromMapFunc(r.parentRequests)).Complete(r)
}
