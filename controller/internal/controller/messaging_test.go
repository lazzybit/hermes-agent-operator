package controller

import (
	"context"
	"strings"
	"testing"

	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestMessagingBindReleaseAndProjection(t *testing.T) {
	_, scheme, h, alpha := fixture(t)
	alpha = alpha.DeepCopy()
	alpha.ResourceVersion = ""
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&api.Hermes{}, &api.HermesProfile{}, &api.MessagingChannel{}).WithObjects(h.DeepCopy(), alpha).Build()
	beta := alpha.DeepCopy()
	beta.Name, beta.UID, beta.ResourceVersion = "beta", "profile-beta", ""
	if err := c.Create(context.Background(), beta); err != nil {
		t.Fatal(err)
	}
	token := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "messaging-channel-telegram-1", Namespace: h.Namespace}, Data: map[string][]byte{"token": []byte("placeholder-token")}}
	other := token.DeepCopy()
	other.Name = "messaging-channel-telegram-2"
	other.ResourceVersion = ""
	first := channel("telegram-1", "aaa_bot", "Example Bot", token.Name)
	second := channel("telegram-2", "bbb_bot", "Other Bot", other.Name)
	named := channel("telegram-9", "ccc_bot", "Named Bot", token.Name)
	ctx := context.Background()
	for _, obj := range []client.Object{token, other, first, second, named} {
		if err := c.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	alpha.Spec.Messaging = &api.ProfileMessagingSpec{Platform: "telegram"}
	beta.Spec.Messaging = &api.ProfileMessagingSpec{Platform: "telegram", ChannelRef: "telegram-9"}
	bound, err := syncMessaging(ctx, c, h.Namespace, []api.HermesProfile{*alpha, *beta})
	if err != nil {
		t.Fatal(err)
	}
	if len(bound) != 2 || bound[0].Name != "telegram-1" || bound[0].Status.ClaimRef.UID != alpha.UID || bound[1].Name != "telegram-9" || bound[1].Status.ClaimRef.Name != "beta" {
		t.Fatalf("bound %#v", bound)
	}
	again, err := syncMessaging(ctx, c, h.Namespace, []api.HermesProfile{*alpha, *beta})
	if err != nil || len(again) != 2 || again[0].Name != bound[0].Name {
		t.Fatalf("rebind changed claim: %v %#v", err, again)
	}
	alpha.Spec.Messaging = nil
	released, err := syncMessaging(ctx, c, h.Namespace, []api.HermesProfile{*alpha, *beta})
	if err != nil || len(released) != 1 || released[0].Name != "telegram-9" {
		t.Fatalf("release %#v %v", released, err)
	}
	got := &api.MessagingChannel{}
	if err := c.Get(ctx, types.NamespacedName{Name: "telegram-1", Namespace: h.Namespace}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != api.MessagingAvailable || got.Status.ClaimRef != nil {
		t.Fatalf("channel not released: %#v", got.Status)
	}
	d := Gateway(h, []api.HermesProfile{*beta})
	projectChannels(d, released)
	if err := projectGatewayResources(ctx, &HermesReconciler{Client: c, Scheme: scheme}, h, []api.HermesProfile{*beta}, released, d); err != nil {
		t.Fatal(err)
	}
	raw := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Name: "gateway-resources", Namespace: h.Namespace}, raw); err != nil {
		t.Fatal(err)
	}
	text := raw.Data["messaging-channel.json"]
	for _, want := range []string{`"id": "ccc_bot"`, `"name": "Named Bot"`, `"beta":`, `/run/hermes/resources/channels/telegram-9/token`} {
		if !strings.Contains(text, want) {
			t.Fatalf("document missing %s\n%s", want, text)
		}
	}
	if strings.Contains(text, "placeholder-token") || strings.Contains(text, "123456789") {
		t.Fatal("token or numeric user id leaked into messaging-channel.json")
	}
	found := false
	for _, mount := range d.Spec.Template.Spec.Containers[0].VolumeMounts {
		if mount.MountPath == "/run/hermes/resources/channels/telegram-9" && mount.ReadOnly {
			found = true
		}
	}
	if !found {
		t.Fatal("token Secret was not mounted")
	}
	beta.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
	if err := releaseProfileChannels(ctx, c, beta); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Name: "telegram-9", Namespace: h.Namespace}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != api.MessagingAvailable || got.Status.ClaimRef != nil {
		t.Fatalf("deleted profile kept the channel: %#v", got.Status)
	}
}

func channel(name, id, display, secret string) *api.MessagingChannel {
	return &api.MessagingChannel{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test"}, Spec: api.MessagingChannelSpec{Platform: "telegram", Telegram: &api.TelegramChannelSpec{ID: id, Name: display, TokenSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: "token"}}}}
}
