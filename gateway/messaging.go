package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// telegramAPI is the Bot HTTP API origin. Tests point it at a local server.
var telegramAPI = "https://api.telegram.org"

type messagingDocument struct {
	Channels []messagingChannel `json:"channels"`
}

type messagingChannel struct {
	Platform  string             `json:"platform"`
	Name      string             `json:"name"`
	Profile   string             `json:"profile"`
	TokenPath string             `json:"tokenPath"`
	Telegram  *messagingTelegram `json:"telegram"`
}

type messagingTelegram struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// applyMessaging projects a claimed Telegram channel into /run/generated and
// reconciles the bot display name with setMyName. id is the @username without
// @ and is only checked, never changed. The token is read from tokenPath and
// is not written back to the user's credential file.
func applyMessaging(home string, opt options) error {
	if opt.messaging == "" {
		return nil
	}
	data, err := os.ReadFile(opt.messaging)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil
	}
	var doc messagingDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("messaging-channel.json: %w", err)
	}
	var channel *messagingChannel
	for i := range doc.Channels {
		if doc.Channels[i].Profile == opt.profile {
			if channel != nil {
				return fmt.Errorf("messaging-channel.json has two channels for profile %q", opt.profile)
			}
			channel = &doc.Channels[i]
		}
	}
	if channel == nil {
		return nil
	}
	if channel.Platform != "telegram" || channel.Telegram == nil {
		return fmt.Errorf("messaging channel %s is not a telegram channel", channel.Name)
	}
	if err := validateTelegramIdentity(channel.Telegram.ID, channel.Telegram.Name); err != nil {
		return err
	}
	token, err := os.ReadFile(channel.TokenPath)
	if err != nil {
		return fmt.Errorf("messaging channel %s token: %w", channel.Name, err)
	}
	token = bytes.TrimSpace(token)
	if len(token) == 0 {
		return fmt.Errorf("messaging channel %s token is empty", channel.Name)
	}
	if err := reconcileTelegramName(string(token), channel.Telegram.ID, channel.Telegram.Name); err != nil {
		return fmt.Errorf("messaging channel %s: %w", channel.Name, err)
	}
	body, err := generatedEnv(opt.credentials, string(token))
	if err != nil {
		return err
	}
	dir := opt.generated
	if dir == "" {
		dir = "/run/generated"
	}
	id := opt.profile
	if id == "" {
		id = "default"
	}
	destDir := filepath.Join(dir, id)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(destDir, ".env")
	if err := os.WriteFile(dest, body, 0o400); err != nil {
		return err
	}
	return linkFile(filepath.Join(home, ".env"), dest)
}

func validateTelegramIdentity(id, name string) error {
	if strings.HasPrefix(id, "@") || !telegramUsername(id) {
		return fmt.Errorf("telegram id must be the bot username without @")
	}
	if strings.TrimSpace(name) == "" || len(name) > 64 {
		return fmt.Errorf("telegram name must be the display name of 1 to 64 characters")
	}
	return nil
}

func telegramUsername(id string) bool {
	if len(id) < 5 || len(id) > 32 {
		return false
	}
	for i, r := range id {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case i > 0 && (r >= '0' && r <= '9' || r == '_'):
		default:
			return false
		}
	}
	return true
}

func reconcileTelegramName(token, username, displayName string) error {
	me, err := telegramGet(token, "getMe")
	if err != nil {
		return err
	}
	var user struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(me, &user); err != nil {
		return fmt.Errorf("telegram getMe: %w", err)
	}
	if !strings.EqualFold(user.Username, username) {
		return fmt.Errorf("telegram bot username does not match id %s", username)
	}
	currentRaw, err := telegramGet(token, "getMyName")
	if err != nil {
		return err
	}
	var current struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(currentRaw, &current); err != nil {
		return fmt.Errorf("telegram getMyName: %w", err)
	}
	if current.Name == displayName {
		return nil
	}
	payload, _ := json.Marshal(map[string]string{"name": displayName})
	if _, err := telegramPost(token, "setMyName", payload); err != nil {
		return err
	}
	return nil
}

func telegramGet(token, method string) (json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodGet, telegramMethod(token, method), nil)
	if err != nil {
		return nil, err
	}
	return telegramDo(req, method)
}

func telegramPost(token, method string, body []byte) (json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodPost, telegramMethod(token, method), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return telegramDo(req, method)
}

func telegramMethod(token, method string) string {
	return strings.TrimRight(telegramAPI, "/") + "/bot" + url.PathEscape(token) + "/" + method
}

func telegramDo(req *http.Request, method string) (json.RawMessage, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("telegram %s: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram %s returned %s", method, resp.Status)
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("telegram %s: %w", method, err)
	}
	if !envelope.OK {
		reason := envelope.Description
		if reason == "" {
			reason = "request failed"
		}
		return nil, fmt.Errorf("telegram %s: %s", method, reason)
	}
	return envelope.Result, nil
}

func generatedEnv(credentials, token string) ([]byte, error) {
	var lines []string
	if credentials != "" {
		data, err := os.ReadFile(credentials)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				lines = append(lines, line)
				continue
			}
			name, _, ok := strings.Cut(trimmed, "=")
			if ok && strings.TrimSpace(name) == "TELEGRAM_BOT_TOKEN" {
				continue
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, "TELEGRAM_BOT_TOKEN="+quoteEnv(token))
	body := strings.Join(lines, "\n")
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return []byte(body), nil
}

func quoteEnv(value string) string {
	if strings.ContainsAny(value, " \t#\"'\\") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
	}
	return value
}
