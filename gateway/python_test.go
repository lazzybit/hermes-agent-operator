package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallUsesHermesUVNotVenvPip(t *testing.T) {
	store := t.TempDir()
	entry := filepath.Join(store, "uv-entry")
	if err := os.Mkdir(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + logPath + "\n"
	if err := os.WriteFile(filepath.Join(entry, "uv"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	facts := `{"schema":1,"packages":{"uv":{"entry":"uv-entry","version":"0.9.0"}}}`
	if err := os.WriteFile(filepath.Join(store, "facts.json"), []byte(facts), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := t.TempDir()
	marker := filepath.Join(t.TempDir(), "pip-called")
	python := "#!/bin/sh\nprintf pip > " + marker + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(sentinel, "python3"), []byte(python), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", sentinel)
	t.Setenv("HERMES_RUNTIME_DIR", store)
	t.Setenv("HERMES_PYTHON", "/opt/hermes/.venv/bin/python")
	t.Setenv("HERMES_PLUGIN_INSTALLER", "")
	site := t.TempDir()
	if err := installRequirementsDefault(site, []string{"requests>=2,<3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("sealed venv python -m pip was invoked")
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(logged)
	for _, want := range []string{"pip", "install", "--python", "/opt/hermes/.venv/bin/python", "--target", site, "--link-mode", "copy", "requests>=2,<3"} {
		if !strings.Contains(text, want) {
			t.Fatalf("uv args missing %q:\n%s", want, text)
		}
	}
}

func TestMissingUVDoesNotInvokePip(t *testing.T) {
	store := t.TempDir()
	sentinel := t.TempDir()
	marker := filepath.Join(t.TempDir(), "pip-called")
	python := "#!/bin/sh\nprintf pip > " + marker + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(sentinel, "python3"), []byte(python), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sentinel, "uv"), []byte("#!/bin/sh\nexit 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", sentinel)
	t.Setenv("HERMES_RUNTIME_DIR", store)
	t.Setenv("HERMES_PLUGIN_INSTALLER", "")
	err := installRequirementsDefault(t.TempDir(), []string{"requests"})
	if err == nil || !strings.Contains(err.Error(), "uv") {
		t.Fatalf("expected a uv error, got %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatal("python -m pip was invoked")
	}
}

func TestUVLauncherSnippet(t *testing.T) {
	uv := filepath.Join(t.TempDir(), "uv")
	if err := os.WriteFile(uv, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(t.TempDir(), "python3")
	script := "#!/bin/sh\nprintf '%s\\n' " + uv + "\n"
	if err := os.WriteFile(python, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := uvFromPython(python)
	if err != nil || got != uv {
		t.Fatalf("launcher = %q %v", got, err)
	}
}
