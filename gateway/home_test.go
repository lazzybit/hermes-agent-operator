package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitializeHomeCreatesChildAndPreservesSoul(t *testing.T) {
	root := t.TempDir()
	instance := t.TempDir()
	links := t.TempDir()
	config := t.TempDir()
	if err := os.WriteFile(filepath.Join(config, "config.yaml"), []byte("agent: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(credentials, []byte("TOKEN=one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "hermes")
	script := []byte("#!/bin/sh\n" +
		"test \"$1\" = profile && test \"$2\" = create && test \"$3\" = managed && test \"$4\" = --no-alias || exit 2\n" +
		"mkdir -p \"$HERMES_HOME/profiles/managed\"\n" +
		"echo 'soul stays' > \"$HERMES_HOME/profiles/managed/SOUL.md\"\n")
	if err := os.WriteFile(bin, script, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_INIT_PROFILE_ROOT", root)
	t.Setenv("HERMES_INIT_INSTANCE_ROOT", instance)
	t.Setenv("HERMES_INIT_PROFILE_LINK_ROOT", links)
	t.Setenv("HERMES_BIN", bin)

	home, err := initializeHome("assistant", config, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if home != filepath.Join(root, "home") {
		t.Fatalf("home = %s", home)
	}
	soul, err := os.ReadFile(filepath.Join(home, "SOUL.md"))
	if err != nil || string(soul) != "soul stays\n" {
		t.Fatalf("SOUL.md was not preserved: %q %v", soul, err)
	}
	if target, err := os.Readlink(filepath.Join(home, "config.yaml")); err != nil || target != filepath.Join(config, "config.yaml") {
		t.Fatalf("config link = %s %v", target, err)
	}
	if target, err := os.Readlink(filepath.Join(home, ".env")); err != nil || target != credentials {
		t.Fatalf("credentials link = %s %v", target, err)
	}
	if target, err := os.Readlink(filepath.Join(root, ".hermes-home")); err != nil || target != "home" {
		t.Fatalf(".hermes-home = %s %v", target, err)
	}
	info, err := os.Stat(home)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("home mode = %v %v", info, err)
	}
	linked := filepath.Join(instance, ".hermes-home", "profiles", "assistant")
	if target, err := os.Readlink(linked); err != nil || target != filepath.Join(links, "assistant", ".hermes-home") {
		t.Fatalf("profile link = %s %v", target, err)
	}
	if _, err := initializeHome("assistant", config, credentials); err != nil {
		t.Fatal(err)
	}
}

func TestInitializeHomeKeepsLegacyRoot(t *testing.T) {
	root := t.TempDir()
	config := t.TempDir()
	if err := os.WriteFile(filepath.Join(config, "config.yaml"), []byte("agent: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SOUL.md"), []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_INIT_PROFILE_ROOT", root)
	t.Setenv("HERMES_BIN", "/does/not/need/to/run")
	home, err := initializeHome("", config, "")
	if err != nil {
		t.Fatal(err)
	}
	if home != root {
		t.Fatalf("legacy home = %s", home)
	}
	soul, err := os.ReadFile(filepath.Join(root, "SOUL.md"))
	if err != nil || string(soul) != "existing\n" {
		t.Fatalf("legacy SOUL changed: %q %v", soul, err)
	}
	if _, err := os.Stat(filepath.Join(root, "home")); !os.IsNotExist(err) {
		t.Fatal("legacy layout created a child home")
	}
	if target, err := os.Readlink(filepath.Join(root, ".hermes-home")); err != nil || target != "." {
		t.Fatalf(".hermes-home = %s %v", target, err)
	}
}

func TestLinkInstanceProfileRefusesNonEmptyDirectory(t *testing.T) {
	instance := t.TempDir()
	dir := filepath.Join(instance, ".hermes-home", "profiles", "assistant")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.db"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_INIT_INSTANCE_ROOT", instance)
	if err := linkInstanceProfile("assistant"); err == nil {
		t.Fatal("non-empty profile directory was replaced")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "state.db")); err != nil || string(data) != "keep" {
		t.Fatalf("state discarded: %q %v", data, err)
	}
}
