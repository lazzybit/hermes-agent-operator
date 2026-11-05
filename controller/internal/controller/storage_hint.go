package controller

import (
	"fmt"
	"strings"
)

const persistentStorageHint = "You live in a Kubernetes pod managed by Hermes Operator. Your terminal workspace at `/workspace` persists across pod replacements. Store files you need to keep there. Other terminal paths, including `/tmp` and `/home/agent`, are not guaranteed to persist. Keep long-lived credentials under `/workspace/.credentials/`, outside Git repositories, with directory permissions of `700` and file permissions of `600`. Never expose credential contents, and remove plaintext copies when they are no longer needed."

const ephemeralStorageHint = "You live in a Kubernetes pod managed by Hermes Operator. Nothing stored in your terminal filesystem is guaranteed to survive pod deletion or replacement, including files under `/workspace`. Use `/workspace` for work in progress, and deliver results or save them to an explicitly configured external persistent location before the pod is replaced. Do not rely on local files for long-term credential storage. Never expose credential contents, and remove plaintext copies when they are no longer needed."

// addStorageEnvironmentHint uses Hermes' native environment description, independently
// of the user's system prompt and personality. cfg is a decoded copy of spec.config.
func addStorageEnvironmentHint(cfg map[string]any, ephemeral bool) error {
	agent, ok := cfg["agent"].(map[string]any)
	if !ok && cfg["agent"] != nil {
		return fmt.Errorf("agent must be a mapping")
	}
	if agent == nil {
		agent = map[string]any{}
		cfg["agent"] = agent
	}
	existing, ok := agent["environment_hint"].(string)
	if !ok && agent["environment_hint"] != nil {
		return fmt.Errorf("agent.environment_hint must be a string")
	}
	hint := persistentStorageHint
	if ephemeral {
		hint = ephemeralStorageHint
	}
	if strings.TrimSpace(existing) != "" {
		hint = existing + "\n\n" + hint
	}
	agent["environment_hint"] = hint
	return nil
}
