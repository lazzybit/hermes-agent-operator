package controller

import (
	"strings"
	"testing"
)

func TestStorageEnvironmentHint(t *testing.T) {
	for _, ephemeral := range []bool{false, true} {
		cfg := map[string]any{"agent": map[string]any{"environment_hint": "Existing context.", "system_prompt": "User instructions.", "max_turns": 17}, "display": map[string]any{"personality": "concise"}}
		if err := addStorageEnvironmentHint(cfg, ephemeral); err != nil {
			t.Fatal(err)
		}
		agent := cfg["agent"].(map[string]any)
		hint := agent["environment_hint"].(string)
		if !strings.HasPrefix(hint, "Existing context.\n\nYou live in a Kubernetes pod managed by Hermes Operator.") {
			t.Fatal("existing context not preserved")
		}
		if strings.Contains(hint, "Nothing stored") != ephemeral {
			t.Fatal("wrong storage policy")
		}
		if agent["system_prompt"] != "User instructions." || agent["max_turns"] != 17 || cfg["display"].(map[string]any)["personality"] != "concise" {
			t.Fatal("user settings changed")
		}
	}
	if err := addStorageEnvironmentHint(map[string]any{"agent": "invalid"}, false); err == nil {
		t.Fatal("invalid agent mapping accepted")
	}
}
