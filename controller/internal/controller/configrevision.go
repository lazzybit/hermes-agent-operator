package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"k8s.io/apimachinery/pkg/runtime"
	"reflect"
	"strings"
	"sync"

	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func managedConfigName(owner client.Object) string {
	if _, ok := owner.(*api.Hermes); ok {
		return "gateway-config"
	}
	return "profile-config-" + owner.GetName()
}

const configRevision = "hermes.nousresearch.com/configuration"

type credentialState struct {
	value []byte
	token string
}

var credentialRollouts = struct {
	sync.Mutex
	entries map[string]credentialState
}{entries: map[string]credentialState{}}

// Credential bytes stay in process memory; only random rollout tokens leave it.
func (r *HermesReconciler) configurationRevision(ctx context.Context, h *api.Hermes, profiles []api.HermesProfile, d *appsv1.Deployment) error {
	type source struct {
		owner       client.Object
		config      runtime.RawExtension
		credentials *corev1.SecretKeySelector
		ssh         bool
	}
	sources := []source{{h, h.Spec.Config, h.Spec.CredentialsSecret, false}}
	for i := range profiles {
		p := &profiles[i]
		sources = append(sources, source{p, p.Spec.Config, p.Spec.CredentialsSecret, true})
	}
	var revisions, credentialTokens []string
	for _, src := range sources {
		var cfg map[string]any
		data := src.config.Raw
		if len(data) == 0 {
			data = []byte(`{}`)
		}
		if err := json.Unmarshal(data, &cfg); err != nil || cfg == nil {
			return fmt.Errorf("%s spec.config must be an object", src.owner.GetName())
		}
		if cfg == nil {
			cfg = map[string]any{}
		}
		if src.ssh {
			terminal, exists := cfg["terminal"].(map[string]any)
			if !exists && cfg["terminal"] != nil {
				return fmt.Errorf("terminal must be a mapping")
			}
			if terminal == nil {
				terminal = map[string]any{}
				cfg["terminal"] = terminal
			}
			name := src.owner.GetName()
			terminal["backend"], terminal["ssh_host"], terminal["ssh_user"], terminal["ssh_port"], terminal["ssh_key"] = "ssh", "terminal-"+name, "agent", 22, "/run/hermes-ssh-private/"+name+"/key"
			if _, ok := terminal["cwd"]; !ok {
				terminal["cwd"] = "/workspace"
			}
		}
		merged, err := yaml.Marshal(cfg)
		if err != nil {
			return err
		}
		generated := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: managedConfigName(src.owner), Namespace: h.Namespace}, Data: map[string]string{"config.yaml": string(merged)}}
		if src.owner == h {
			generated.Data["ssh_config"] = "Include /run/hermes-ssh/*/client_config\n"
		}
		if err := apply(ctx, r.Client, r.Scheme, src.owner, generated); err != nil {
			return err
		}
		revisions = append(revisions, fmt.Sprintf("%s:%x", generated.Name, sha256.Sum256(merged)))
		if src.credentials != nil {
			secret := &corev1.Secret{}
			if err := r.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: src.credentials.Name}, secret); err != nil {
				return err
			}
			value, ok := secret.Data[src.credentials.Key]
			if !ok {
				return fmt.Errorf("credentials Secret %s missing selected key", secret.Name)
			}
			key := string(h.UID) + "/" + h.Namespace + "/" + src.owner.GetName() + "/" + secret.Name + "/" + src.credentials.Key
			credentialRollouts.Lock()
			state, exists := credentialRollouts.entries[key]
			if !exists || !reflect.DeepEqual(state.value, value) {
				state = credentialState{append([]byte(nil), value...), string(uuid.NewUUID())}
				credentialRollouts.entries[key] = state
			}
			credentialRollouts.Unlock()
			credentialTokens = append(credentialTokens, state.token)
		}
	}
	data, _ := json.Marshal(revisions)
	d.Spec.Template.Annotations = map[string]string{configRevision: fmt.Sprintf("%x", sha256.Sum256(data)), "hermes.nousresearch.com/credentials-rollout": strings.Join(credentialTokens, ",")}
	return nil
}

// Enqueue only instances whose declarative input references the changed object.
func (r *HermesReconciler) configSecretRequests(ctx context.Context, obj client.Object) []reconcile.Request {
	instances, profiles := &api.HermesList{}, &api.HermesProfileList{}
	if r.List(ctx, instances, client.InNamespace(obj.GetNamespace())) != nil || r.List(ctx, profiles, client.InNamespace(obj.GetNamespace())) != nil {
		return nil
	}
	matches := func(credentials *corev1.SecretKeySelector) bool {
		return credentials != nil && credentials.Name == obj.GetName()
	}
	refs := map[string]bool{}
	for _, p := range profiles.Items {
		if matches(p.Spec.CredentialsSecret) {
			refs[p.Spec.HermesRef] = true
		}
	}
	var out []reconcile.Request
	for _, h := range instances.Items {
		if matches(h.Spec.CredentialsSecret) || refs[h.Name] {
			out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&h)})
		}
	}
	return out
}
