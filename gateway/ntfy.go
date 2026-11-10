package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type messagingNtfy struct {
	ServerURL string `json:"serverURL"`
	Topic     string `json:"topic"`
}

func applyNtfyMessaging(home string, opt options, ch messagingChannel) error {
	if ch.Ntfy == nil {
		return fmt.Errorf("ntfy configuration required")
	}
	n := ch.Ntfy
	if !regexp.MustCompile(`^ha[A-Za-z0-9]{12}_[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(n.Topic) {
		return fmt.Errorf("invalid ntfy topic")
	}
	u, e := url.Parse(n.ServerURL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return fmt.Errorf("invalid ntfy server URL")
	}
	var lines []string
	if opt.credentials != "" {
		raw, e := os.ReadFile(opt.credentials)
		if e != nil {
			return e
		}
		for _, line := range strings.Split(string(raw), "\n") {
			key, _, _ := strings.Cut(strings.TrimSpace(line), "=")
			key = strings.TrimPrefix(key, "export ")
			if strings.HasPrefix(key, "TELEGRAM_") || strings.HasPrefix(key, "NTFY_") {
				continue
			}
			lines = append(lines, line)
		}
	}
	values := map[string]string{"NTFY_SERVER_URL": n.ServerURL, "NTFY_TOPIC": n.Topic, "NTFY_ALLOWED_USERS": n.Topic, "NTFY_HOME_CHANNEL": n.Topic, "NTFY_HOME_CHANNEL_NAME": ch.Name, "NTFY_MARKDOWN": "true"}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		lines = append(lines, key+"="+quoteEnv(values[key]))
	}
	if ch.TokenPath != "" {
		raw, e := os.ReadFile(ch.TokenPath)
		if e != nil {
			return e
		}
		lines = append(lines, "NTFY_TOKEN="+quoteEnv(strings.TrimSpace(string(raw))))
	}
	dir := opt.generated
	if dir == "" {
		dir = "/run/hermes/generated"
	}
	id := opt.profile
	if id == "" {
		id = "default"
	}
	dest := filepath.Join(dir, id, ".env")
	if e := os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
		return e
	}
	if e := os.WriteFile(dest, []byte(strings.Join(lines, "\n")+"\n"), 0400); e != nil {
		return e
	}
	return linkFile(filepath.Join(home, ".env"), dest)
}
