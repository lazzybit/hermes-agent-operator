package controller

import (
	"encoding/json"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
)

func TestTelegramTemplateUsesClaimName(t *testing.T) {
	claim := &api.ChannelClaim{ObjectMeta: metav1.ObjectMeta{Name: "resource-name", UID: "first-uid"}}
	class := &api.ChannelClass{}
	if err := json.Unmarshal([]byte(`{"name":"my-canary"}`), &claim.Spec); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"usernameTemplate":"ha{RANDOMIZED}_{NAME}"}`), &class.Spec.Parameters); err != nil {
		t.Fatal(err)
	}
	got, err := generatedTelegramUsername(claim, class)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "ha") || !strings.HasSuffix(got, "_my_canary") || len(got) != len("ha")+16+len("_my_canary") {
		t.Fatalf("template was not rendered: %q", got)
	}
	for _, ch := range strings.TrimSuffix(strings.TrimPrefix(got, "ha"), "_my_canary") {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", ch) {
			t.Fatal("non-alphanumeric random character")
		}
	}
	// Reuse only the persisted identity; independent generation must use fresh entropy.
	claim.Status.GeneratedUsername = got
	again, err := generatedTelegramUsername(claim, class)
	if err != nil || again != got {
		t.Fatal("persisted identity changed on retry")
	}
	claim.Status.GeneratedUsername = ""
	other, err := generatedTelegramUsername(claim, class)
	if err != nil || other == got {
		t.Fatal("independent generation reused the identity")
	}
}
