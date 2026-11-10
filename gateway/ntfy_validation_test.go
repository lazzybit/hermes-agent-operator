package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNtfyRejectsMalformedTopic(t *testing.T) {
	for _, topic := range []string{"haSHORT_canary", "haAbCdEf012345_Canary", "haAbCdEf012345_canary_bad", "haAbCdEf012345_canary\nTOKEN=bad"} {
		t.Run(topic, func(t *testing.T) {
			if err := applyNtfyMessaging(t.TempDir(), options{generated: t.TempDir()}, messagingChannel{Ntfy: &messagingNtfy{ServerURL: "http://ntfy", Topic: topic}}); err == nil {
				t.Fatal("accepted invalid topic")
			}
		})
	}
}
func TestNtfyOptionalTokenRemainsPrivate(t *testing.T) {
	home := t.TempDir()
	token := filepath.Join(t.TempDir(), "token")
	os.WriteFile(token, []byte("fixture-token"), 0600)
	dir := t.TempDir()
	if err := applyNtfyMessaging(home, options{profile: "canary", generated: dir}, messagingChannel{TokenPath: token, Ntfy: &messagingNtfy{ServerURL: "http://ntfy", Topic: "haAbCdEf012345_canary"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "canary", ".env"))
	if err != nil || info.Mode().Perm() != 0400 {
		t.Fatal("generated credentials are not private")
	}
}
