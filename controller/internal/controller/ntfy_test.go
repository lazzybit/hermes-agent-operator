package controller

import (
	"encoding/json"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestNtfyChannelProjection(t *testing.T) {
	var c api.MessagingChannel
	if err := json.Unmarshal([]byte(`{"metadata":{"name":"canary"},"spec":{"platform":"ntfy","ntfy":{"serverURL":"http://ntfy.ntfy.svc:8080","topic":"haAbCdEf012345_canary"}},"status":{"phase":"Bound","claimRef":{"kind":"HermesProfile","name":"canary","uid":"p"}}}`), &c); err != nil {
		t.Fatal(err)
	}
	body, err := resourcesChannels([]api.MessagingChannel{c}, []api.HermesProfile{{ObjectMeta: metav1.ObjectMeta{Name: "canary", UID: "p"}}})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]any
	json.Unmarshal(body, &doc)
	if doc["canary"]["ntfy"] == nil {
		t.Fatal("ntfy channel omitted from projection")
	}
	if doc["canary"]["tokenPath"] != "" {
		t.Fatal("tokenless channel projected a token path")
	}
}
