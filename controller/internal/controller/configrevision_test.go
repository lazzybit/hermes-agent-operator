package controller

import (
	"context"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
)

func TestConfigurationAutomaticRollouts(t *testing.T) {
	c, s, h, p := fixture(t)
	ctx := context.Background()
	h.Spec.SSH = &api.SSHSpec{}
	p.Spec.CredentialsSecret = &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "credentials"}, Key: ".env"}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: h.Namespace}, Data: map[string][]byte{".env": []byte("FIXTURE_TOKEN=one")}}
	if err := c.Create(ctx, secret); err != nil {
		t.Fatal(err)
	}
	r := &HermesReconciler{Client: c, Scheme: s}
	render := func() *appsv1.Deployment {
		t.Helper()
		d := Gateway(h, []api.HermesProfile{*p})
		if err := r.configurationRevision(ctx, h, []api.HermesProfile{*p}, d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	first := render().Spec.Template.Annotations[configRevision]
	firstCredential := render().Spec.Template.Annotations["hermes.nousresearch.com/credentials-rollout"]
	if render().Spec.Template.Annotations[configRevision] != first {
		t.Fatal("idempotent reconciliation changed rollout")
	}
	secret.Labels = map[string]string{"irrelevant": "metadata"}
	_ = c.Update(ctx, secret)
	if render().Spec.Template.Annotations[configRevision] != first {
		t.Fatal("metadata changed rollout")
	}
	secret.Data[".env"] = []byte("FIXTURE_TOKEN=two")
	_ = c.Update(ctx, secret)
	next := render().Spec.Template.Annotations["hermes.nousresearch.com/credentials-rollout"]
	if next == "" || next == firstCredential {
		t.Fatal("credential change must restart gateway")
	}
	p.Spec.Config = runtime.RawExtension{Raw: []byte(`{"terminal":{"cwd":"/custom","env_passthrough":["SERVICE_TOKEN"],"credential_files":["service.json"]},"agent":{"max_turns":17},"unknown":{"nested":[1,true,"value"]}}`)}
	if render().Spec.Template.Annotations[configRevision] == first {
		t.Fatal("config change must restart gateway")
	}
	generated := &corev1.ConfigMap{}
	_ = c.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: "profile-config-" + p.Name}, generated)
	data := generated.Data["config.yaml"]
	for _, want := range []string{"backend: ssh", "cwd: /custom", "env_passthrough:", "SERVICE_TOKEN", "credential_files:", "service.json", "max_turns: 17", "unknown:", "nested:", "ssh_host: terminal-alpha", "ssh_key: /run/hermes-ssh-private/alpha/key"} {
		if !strings.Contains(data, want) {
			t.Fatal(data)
		}
	}
	if !strings.Contains(data, "Your terminal workspace") {
		t.Fatal("persistent environment hint missing")
	}
	before := render().Spec.Template.Annotations[configRevision]
	p.Spec.Terminal.Storage.Ephemeral = true
	if render().Spec.Template.Annotations[configRevision] == before {
		t.Fatal("storage policy must change rollout revision")
	}
	if err := c.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: "profile-config-" + p.Name}, generated); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(generated.Data["config.yaml"], "Nothing stored") {
		t.Fatal("ephemeral environment hint missing")
	}
	// Re-rendering must not append a second hint or mutate the source resource.
	if render().Spec.Template.Annotations[configRevision] != render().Spec.Template.Annotations[configRevision] {
		t.Fatal("unstable generated hint")
	}
	if strings.Contains(string(p.Spec.Config.Raw), "environment_hint") {
		t.Fatal("source configuration mutated")
	}
	if strings.Contains(data, "FIXTURE_TOKEN") {
		t.Fatal("credentials leaked into public config")
	}
	sha := strings.Repeat("ab", 20)
	beforePlugins := render().Spec.Template.Annotations[configRevision]
	p.Spec.Plugins = []api.PluginSource{{Name: "hindsight", Repository: "https://github.com/vectorize-io/hindsight", Revision: sha, Path: "hindsight-integrations/hermes"}}
	withPlugins := render()
	if withPlugins.Spec.Template.Annotations[configRevision] == beforePlugins {
		t.Fatal("plugins.json must roll the gateway")
	}
	if err := projectGatewayResources(ctx, r, h, []api.HermesProfile{*p}, nil, withPlugins); err != nil {
		t.Fatal(err)
	}
	resources := &corev1.ConfigMap{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: "gateway-resources"}, resources); err != nil {
		t.Fatal(err)
	}
	document := resources.Data["plugins.json"]
	if !strings.Contains(document, sha) || strings.Contains(document, "plugin.py") || strings.Contains(resources.Data["messaging-channel.json"], "TELEGRAM_BOT_TOKEN") {
		t.Fatal("plugins.json must be the declaration, not fetched plugin bytes or a token")
	}
	if err := c.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: "profile-config-" + p.Name}, generated); err != nil {
		t.Fatal(err)
	}
	if _, ok := generated.Data["plugins.json"]; ok {
		t.Fatal("plugins.json must be projected at /run/resources, not the profile config")
	}
	if render().Spec.Template.Annotations[configRevision] != withPlugins.Spec.Template.Annotations[configRevision] {
		t.Fatal("unchanged plugins.json changed rollout")
	}
	p.Spec.Plugins[0].Revision = strings.Repeat("cd", 20)
	if render().Spec.Template.Annotations[configRevision] == withPlugins.Spec.Template.Annotations[configRevision] {
		t.Fatal("plugin revision must roll the gateway")
	}
	p.Spec.Plugins = append(p.Spec.Plugins, p.Spec.Plugins[0])
	if err := r.configurationRevision(ctx, h, []api.HermesProfile{*p}, Gateway(h, []api.HermesProfile{*p})); err == nil {
		t.Fatal("duplicate plugin name accepted")
	}
}

func TestInstanceAndProfileSameNameConfigIsolation(t *testing.T) {
	c, s, h, p := fixture(t)
	p.Name = h.Name
	p.ResourceVersion = ""
	if err := c.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	r := &HermesReconciler{Client: c, Scheme: s}
	if err := r.configurationRevision(context.Background(), h, []api.HermesProfile{*p}, Gateway(h, []api.HermesProfile{*p})); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gateway-config", "profile-config-" + p.Name} {
		if err := c.Get(context.Background(), client.ObjectKey{Namespace: h.Namespace, Name: name}, &corev1.ConfigMap{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigurationRevisionTargetsReferences(t *testing.T) {
	c, s, h, p := fixture(t)
	ctx := context.Background()
	p.Spec.CredentialsSecret = &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "credentials"}, Key: ".env"}
	_ = c.Update(ctx, p)
	r := &HermesReconciler{Client: c, Scheme: s}
	for _, obj := range []client.Object{&metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: h.Namespace}}} {
		if len(r.configSecretRequests(ctx, obj)) != 1 {
			t.Fatal("referencing gateway not enqueued")
		}
	}
	if len(r.configSecretRequests(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: h.Namespace}})) != 0 {
		t.Fatal("unrelated config enqueued")
	}
	h.Spec.Config = runtime.RawExtension{Raw: []byte(`[]`)}
	if r.configurationRevision(ctx, h, nil, Gateway(h, nil)) == nil {
		t.Fatal("non-object config accepted")
	}
	h.Spec.Config = runtime.RawExtension{}
	if err := r.configurationRevision(ctx, h, nil, Gateway(h, nil)); err != nil {
		t.Fatal("optional config rejected", err)
	}
}
