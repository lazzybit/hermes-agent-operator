package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNtfyReplacesTelegram(t *testing.T) {
	home := t.TempDir()
	credentials := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(credentials, []byte("SERVICE_TOKEN=keep\nTELEGRAM_BOT_TOKEN=old\nNTFY_TOPIC=stale\n"), 0600)
	manifest := filepath.Join(t.TempDir(), "messaging.json")
	os.WriteFile(manifest, []byte(`{"canary":{"platform":"ntfy","name":"canary","ntfy":{"serverURL":"http://ntfy.ntfy.svc:8080","topic":"haAbCdEf012345_canary"}}}`), 0600)
	err := applyMessaging(home, options{profile: "canary", credentials: credentials, messaging: manifest, generated: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(home, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SERVICE_TOKEN=keep", "NTFY_TOPIC=haAbCdEf012345_canary", "NTFY_ALLOWED_USERS=haAbCdEf012345_canary", "NTFY_SERVER_URL=http://ntfy.ntfy.svc:8080"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(string(body), "TELEGRAM_BOT_TOKEN") || strings.Contains(string(body), "NTFY_TOPIC=stale") {
		t.Fatal("retired channel credentials retained")
	}
}
