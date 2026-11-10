package controller

import (
	"context"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestDynamicChannelClaimRequiresUserSession(t *testing.T) {
	s := runtime.NewScheme()
	_ = api.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	className := "telegram"
	claim := &api.ChannelClaim{ObjectMeta: metav1.ObjectMeta{Name: "canary", Namespace: "test", UID: "uid"}, Spec: api.ChannelClaimSpec{ChannelClassName: &className}}
	class := &api.ChannelClass{ObjectMeta: metav1.ObjectMeta{Name: className, Namespace: "test"}, Spec: api.ChannelClassSpec{Provisioner: "telegram"}}
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&api.ChannelClaim{}).WithObjects(claim, class).Build()
	_, err := (&ChannelClaimReconciler{Client: c}).Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(claim)})
	if err != nil {
		t.Fatal(err)
	}
	got := &api.ChannelClaim{}
	_ = c.Get(context.Background(), client.ObjectKeyFromObject(claim), got)
	if got.Status.Phase != "Pending" || got.Status.Conditions[0].Reason != "UserSessionRequired" {
		t.Fatalf("unexpected status: %+v", got.Status)
	}
	list := &api.ChannelList{}
	_ = c.List(context.Background(), list)
	if len(list.Items) != 0 {
		t.Fatal("must not provision fallback Channel")
	}
}

func TestStaticChannelClaimBindsOnlyWithCredentials(t *testing.T) {
	s := runtime.NewScheme()
	_ = api.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	empty := ""
	claim := &api.ChannelClaim{ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: "test", UID: "claim-uid"}, Spec: api.ChannelClaimSpec{ChannelClassName: &empty, ChannelName: "bot"}}
	channel := &api.Channel{ObjectMeta: metav1.ObjectMeta{Name: "bot", Namespace: "test"}, Spec: api.ChannelSpec{BotID: 1, Telegram: api.TelegramChannelSpec{ID: "example_bot", Name: "Example", TokenSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "token"}, Key: "token"}}}}
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&api.ChannelClaim{}, &api.Channel{}).WithObjects(claim, channel).Build()
	r := &ChannelClaimReconciler{Client: c}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "claim", Namespace: "test"}}
	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := &api.ChannelClaim{}
	_ = c.Get(context.Background(), req.NamespacedName, got)
	if got.Status.Phase != "Pending" {
		t.Fatalf("without credentials: %s", got.Status.Phase)
	}
	if err := c.Create(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "token", Namespace: "test"}, Data: map[string][]byte{"token": []byte("fixture")}}); err != nil {
		t.Fatal(err)
	}
	_, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Get(context.Background(), req.NamespacedName, got)
	if got.Status.Phase != "Available" || got.Status.ChannelName != "bot" {
		t.Fatalf("not available: %+v", got.Status)
	}
	bound := &api.Channel{}
	_ = c.Get(context.Background(), client.ObjectKeyFromObject(channel), bound)
	if bound.Spec.ClaimRef == nil || bound.Spec.ClaimRef.UID != claim.UID {
		t.Fatal("missing reciprocal UID binding")
	}
}
