package controller

import (
	"context"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
)

func TestClaimConsumerProjectsOnlyAvailableReciprocalBinding(t *testing.T) {
	s := runtime.NewScheme()
	_ = api.AddToScheme(s)
	profile := api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "canary", Namespace: "test", UID: "profile-uid"}, Spec: api.HermesProfileSpec{Messaging: &api.ProfileMessagingSpec{ClaimName: "request"}}}
	claim := &api.ChannelClaim{ObjectMeta: metav1.ObjectMeta{Name: "request", Namespace: "test", UID: "claim-uid"}, Status: api.ChannelClaimStatus{Phase: "Pending", ChannelName: "child"}}
	channel := &api.Channel{ObjectMeta: metav1.ObjectMeta{Name: "child", Namespace: "test"}, Spec: api.ChannelSpec{ClaimRef: &api.MessagingClaimRef{Kind: "ChannelClaim", Name: "request", UID: claim.UID}, Telegram: api.TelegramChannelSpec{ID: "example_bot", Name: "Example"}}}
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&api.ChannelClaim{}).WithObjects(&profile, claim, channel).Build()
	ctx := context.Background()
	got, err := consumeChannelClaim(ctx, c, profile)
	if err != nil || got != nil {
		t.Fatalf("Pending projected: %v %v", got, err)
	}
	claim.Status.Phase = "Available"
	if err = c.Status().Update(ctx, claim); err != nil {
		t.Fatal(err)
	}
	got, err = consumeChannelClaim(ctx, c, profile)
	if err != nil || got == nil {
		t.Fatalf("Available not projected: %v %v", got, err)
	}
	raw, err := resourcesChannels([]api.MessagingChannel{*got}, []api.HermesProfile{profile})
	if err != nil || !strings.Contains(string(raw), "example_bot") {
		t.Fatalf("missing bot in Gateway projection: %s %v", raw, err)
	}
	channel.Spec.ClaimRef.UID = "replaced-claim"
	if err = c.Update(ctx, channel); err != nil {
		t.Fatal(err)
	}
	if _, err = consumeChannelClaim(ctx, c, profile); err == nil {
		t.Fatal("accepted mismatched Claim UID")
	}
}
