package controller

import (
	"encoding/json"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestTelegramTemplateRejectsInvalidInputs(t *testing.T) {
	for _, tc := range []struct{ name, template string }{
		{"canary", "ha{UNKNOWN}_{NAME}"}, {"canary", ""}, {"", "ha{RANDOMIZED}_{NAME}"},
		{"Not-kebab", "ha{RANDOMIZED}_{NAME}"}, {"canary", "ha{RANDOMIZED}_{NAME}xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
	} {
		t.Run(tc.name+tc.template, func(t *testing.T) {
			claim := &api.ChannelClaim{ObjectMeta: metav1.ObjectMeta{UID: "uid"}}
			class := &api.ChannelClass{}
			b, _ := json.Marshal(map[string]string{"name": tc.name})
			_ = json.Unmarshal(b, &claim.Spec)
			b, _ = json.Marshal(map[string]string{"usernameTemplate": tc.template})
			_ = json.Unmarshal(b, &class.Spec.Parameters)
			if got, err := generatedTelegramUsername(claim, class); err == nil {
				t.Fatalf("accepted %q", got)
			}
		})
	}
}
