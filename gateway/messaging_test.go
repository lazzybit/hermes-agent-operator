package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTelegramNameIsReconciled(t *testing.T) {
	var setName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			writeTelegram(w, map[string]any{"id": 999, "username": "aaa_bot", "first_name": "Old"})
		case strings.HasSuffix(r.URL.Path, "/getMyName"):
			writeTelegram(w, map[string]any{"name": "Old"})
		case strings.HasSuffix(r.URL.Path, "/setMyName"):
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			setName = body.Name
			writeTelegram(w, true)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	telegramAPI = srv.URL
	t.Cleanup(func() { telegramAPI = "https://api.telegram.org" })

	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(t.TempDir(), ".env")
	original := "SERVICE_TOKEN=one\nTELEGRAM_BOT_TOKEN=old\n"
	if err := os.WriteFile(credentials, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("placeholder-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "messaging-channel.json")
	document := `{"channels":[{"platform":"telegram","name":"telegram-1","profile":"canary","tokenPath":"` + tokenPath + `","telegram":{"id":"aaa_bot","name":"Example Bot"}}]}`
	if err := os.WriteFile(manifest, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document, "placeholder-token") {
		t.Fatal("token was copied into messaging-channel.json")
	}
	generated := t.TempDir()
	err := applyMessaging(home, options{profile: "canary", credentials: credentials, messaging: manifest, generated: generated})
	if err != nil {
		t.Fatal(err)
	}
	if setName != "Example Bot" {
		t.Fatalf("setMyName received %q", setName)
	}
	linked, err := os.Readlink(filepath.Join(home, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(linked)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "SERVICE_TOKEN=one") || !strings.Contains(string(got), "TELEGRAM_BOT_TOKEN=placeholder-token") || strings.Contains(string(got), "TELEGRAM_BOT_TOKEN=old") {
		t.Fatalf("generated env = %q", got)
	}
	after, err := os.ReadFile(credentials)
	if err != nil || string(after) != original {
		t.Fatal("user credential file changed")
	}
	if _, err := os.Stat(filepath.Join(generated, "canary", ".env")); err != nil {
		t.Fatal(err)
	}
}

func TestTelegramUsernameMismatchDoesNotRename(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/setMyName") {
			called = true
		}
		writeTelegram(w, map[string]any{"id": 123456789, "username": "other_bot"})
	}))
	t.Cleanup(srv.Close)
	telegramAPI = srv.URL
	t.Cleanup(func() { telegramAPI = "https://api.telegram.org" })
	err := reconcileTelegramName("placeholder-token", "aaa_bot", "Example Bot")
	if err == nil || called {
		t.Fatalf("mismatch err=%v setMyName=%v", err, called)
	}
}

func TestTelegramNameAlreadyMatches(t *testing.T) {
	set := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			writeTelegram(w, map[string]any{"username": "Aaa_Bot"})
		case strings.HasSuffix(r.URL.Path, "/getMyName"):
			writeTelegram(w, map[string]any{"name": "Example Bot"})
		default:
			set = true
			writeTelegram(w, true)
		}
	}))
	t.Cleanup(srv.Close)
	telegramAPI = srv.URL
	t.Cleanup(func() { telegramAPI = "https://api.telegram.org" })
	if err := reconcileTelegramName("placeholder-token", "aaa_bot", "Example Bot"); err != nil {
		t.Fatal(err)
	}
	if set {
		t.Fatal("setMyName was called for an unchanged display name")
	}
}

func TestTelegramIDRejectsNumericAndAt(t *testing.T) {
	for _, id := range []string{"123456789", "@aaa_bot", "aa"} {
		if err := validateTelegramIdentity(id, "Example Bot"); err == nil {
			t.Fatalf("accepted %s", id)
		}
	}
	if err := validateTelegramIdentity("aaa_bot", "Example Bot"); err != nil {
		t.Fatal(err)
	}
}

func writeTelegram(w http.ResponseWriter, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}
