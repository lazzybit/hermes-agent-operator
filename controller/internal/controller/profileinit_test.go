package controller

import (
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
)

func TestGatewayInitializesMountedIdentities(t *testing.T) {
	h := &api.Hermes{ObjectMeta: metav1.ObjectMeta{Name: "demo"}, Spec: api.HermesSpec{Image: "official@sha256:test", Command: []string{"/opt/hermes/.venv/bin/hermes", "gateway", "run"}, HelperImage: "ignored", CredentialsSecret: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "credentials"}, Key: ".env"}}}
	p := api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "assistant"}}
	d := Gateway(h, []api.HermesProfile{p})
	pod := d.Spec.Template.Spec
	if *pod.SecurityContext.RunAsUser != 10000 || *pod.SecurityContext.FSGroup != 10000 {
		t.Fatal("official runtime UID/group required")
	}
	if len(pod.InitContainers) != 2 {
		t.Fatal("each home needs native initialization")
	}
	if pod.Containers[0].Image != h.Spec.Image || strings.Join(pod.Containers[0].Command, "\x00") != strings.Join(h.Spec.Command, "\x00") {
		t.Fatal("gateway container must keep the declared Hermes command")
	}
	want := map[string][]string{
		"init-home":              {"--profile", "", "--plugins", "/run/hermes-config/home/plugins.json", "--plugins-dir", "/run/hermes-plugins/default", "--credentials", "/run/hermes-credentials/home/.env"},
		"init-profile-assistant": {"--profile", "assistant", "--plugins", "/run/hermes-config/profile-assistant/plugins.json", "--plugins-dir", "/run/hermes-plugins/assistant"},
	}
	for _, c := range pod.InitContainers {
		if c.Image != h.Spec.Image || len(c.Command) != 1 || c.Command[0] != gatewayInitEntrypoint {
			t.Fatalf("init must use the gateway image entrypoint, got %#v", c.Command)
		}
		joined := strings.Join(c.Command, " ") + " " + strings.Join(c.Args, " ")
		if strings.Contains(joined, "python") || strings.Contains(joined, "profile', 'create'") || strings.Contains(joined, "SOUL.md") {
			t.Fatal("controller must not embed home initialization")
		}
		for _, token := range want[c.Name] {
			if !containsArg(c.Args, token) && token != "" {
				t.Fatalf("%s missing %q in %#v", c.Name, token, c.Args)
			}
		}
		if c.Name == "init-home" && !argValue(c.Args, "--profile", "") {
			t.Fatal("instance init profile must be empty")
		}
		for _, m := range c.VolumeMounts {
			if m.SubPath != "" {
				t.Fatal("kubelet must not create a native target via subPath")
			}
			if m.MountPath == "/run/hermes-plugins/default" || m.MountPath == "/run/hermes-plugins/assistant" {
				if m.ReadOnly {
					t.Fatal("init must be able to write the plugin projection")
				}
			}
		}
	}
	for _, m := range pod.Containers[0].VolumeMounts {
		if m.SubPath != "" {
			t.Fatal("configuration and state must use independent directories")
		}
		if m.MountPath == "/run/hermes-plugins/assistant" && !m.ReadOnly {
			t.Fatal("plugin projection must be read-only for the gateway process")
		}
	}
	for _, v := range pod.Volumes {
		if v.Secret != nil && v.Secret.SecretName == "credentials" && (len(v.Secret.Items) != 1 || v.Secret.Items[0].Key != ".env") {
			t.Fatal("only selected credentials may be projected")
		}
		if v.Name == "plugins-home" && v.EmptyDir == nil {
			t.Fatal("plugin bytes must use an emptyDir")
		}
	}
	var pythonpath string
	for _, e := range pod.Containers[0].Env {
		if e.Name == "PYTHONPATH" {
			pythonpath = e.Value
		}
	}
	if pythonpath != "/run/hermes-plugin-deps/default:/run/hermes-plugin-deps/assistant" {
		t.Fatalf("python path = %s", pythonpath)
	}
	term, _ := Terminal(&p)
	if len(term.Spec.Template.Spec.InitContainers) != 0 || term.Spec.Template.Spec.Containers[0].Command[0] != "/usr/sbin/sshd" {
		t.Fatal("terminal path changed")
	}
}

func containsArg(args []string, token string) bool {
	for _, arg := range args {
		if arg == token {
			return true
		}
	}
	return false
}

func argValue(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1] == value
		}
	}
	return false
}
