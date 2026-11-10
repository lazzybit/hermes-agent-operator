package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const messagingFinalizer = "hermes.nousresearch.com/messaging"

const resourcesConfigMap = "gateway-resources"

// syncMessaging binds each profile that asks for a channel to one Available
// MessagingChannel and releases claims that no longer match. A conflict is
// returned so the caller retries, the same way a PV bind retries.
func syncMessaging(ctx context.Context, c client.Client, namespace string, profiles []api.HermesProfile) ([]api.MessagingChannel, error) {
	listed := &api.MessagingChannelList{}
	if err := c.List(ctx, listed, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	channels := append([]api.MessagingChannel(nil), listed.Items...)
	sort.Slice(channels, func(i, j int) bool { return channels[i].Name < channels[j].Name })
	live, err := liveProfileUIDs(ctx, c, namespace)
	if err != nil {
		return nil, err
	}
	ours := map[types.UID]api.HermesProfile{}
	for _, profile := range profiles {
		ours[profile.UID] = profile
	}
	claimed := map[types.UID]string{}
	for i := range channels {
		channel := &channels[i]
		ref := channel.Status.ClaimRef
		if channel.Status.Phase != api.MessagingBound || ref == nil || ref.Kind != "HermesProfile" || ref.UID == "" {
			continue
		}
		profile, known := ours[ref.UID]
		if _, exists := live[ref.UID]; !exists || (known && !channelWanted(channel, profile)) {
			if err := updateChannelStatus(ctx, c, channel, api.MessagingAvailable, nil); err != nil {
				return nil, err
			}
			channel.Status.Phase = api.MessagingAvailable
			channel.Status.ClaimRef = nil
			continue
		}
		if known && channelWanted(channel, profile) {
			if _, taken := claimed[ref.UID]; taken {
				if err := updateChannelStatus(ctx, c, channel, api.MessagingAvailable, nil); err != nil {
					return nil, err
				}
				channel.Status.Phase = api.MessagingAvailable
				channel.Status.ClaimRef = nil
				continue
			}
			claimed[ref.UID] = channel.Name
		}
	}
	var bound []api.MessagingChannel
	order := append([]api.HermesProfile(nil), profiles...)
	sort.Slice(order, func(i, j int) bool { return order[i].Name < order[j].Name })
	for _, profile := range order {
		if profile.Spec.Messaging == nil {
			continue
		}
		if profile.Spec.Messaging.ClaimName != "" {
			channel, claimErr := consumeChannelClaim(ctx, c, profile)
			if claimErr != nil {
				return nil, claimErr
			}
			if channel != nil {
				bound = append(bound, *channel)
			}
			continue
		}
		if name, ok := claimed[profile.UID]; ok {
			channel := findChannel(channels, name)
			bound = append(bound, *channel)
			continue
		}
		channel, err := selectChannel(channels, profile)
		if err != nil {
			return nil, err
		}
		ref := &api.MessagingClaimRef{Kind: "HermesProfile", Name: profile.Name, UID: profile.UID}
		if err := updateChannelStatus(ctx, c, channel, api.MessagingBound, ref); err != nil {
			return nil, err
		}
		channel.Status.Phase = api.MessagingBound
		channel.Status.ClaimRef = ref
		claimed[profile.UID] = channel.Name
		bound = append(bound, *channel)
	}
	sort.Slice(bound, func(i, j int) bool { return bound[i].Name < bound[j].Name })
	return bound, nil
}

func liveProfileUIDs(ctx context.Context, c client.Client, namespace string) (map[types.UID]bool, error) {
	list := &api.HermesProfileList{}
	if err := c.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	out := map[types.UID]bool{}
	for _, profile := range list.Items {
		if profile.DeletionTimestamp.IsZero() {
			out[profile.UID] = true
		}
	}
	return out, nil
}

func channelWanted(channel *api.MessagingChannel, profile api.HermesProfile) bool {
	want := profile.Spec.Messaging
	if want == nil || channel.Spec.Platform != want.Platform {
		return false
	}
	if want.ChannelRef != "" && want.ChannelRef != channel.Name {
		return false
	}
	return true
}

func findChannel(channels []api.MessagingChannel, name string) *api.MessagingChannel {
	for i := range channels {
		if channels[i].Name == name {
			return &channels[i]
		}
	}
	return nil
}

func selectChannel(channels []api.MessagingChannel, profile api.HermesProfile) (*api.MessagingChannel, error) {
	want := profile.Spec.Messaging
	if want == nil || want.Platform == "" {
		return nil, fmt.Errorf("profile %s messaging platform is required", profile.Name)
	}
	if want.ChannelRef != "" {
		channel := findChannel(channels, want.ChannelRef)
		if channel == nil {
			return nil, fmt.Errorf("messaging channel %s not found", want.ChannelRef)
		}
		if channel.Spec.Platform != want.Platform {
			return nil, fmt.Errorf("messaging channel %s platform is %s", channel.Name, channel.Spec.Platform)
		}
		if ref := channel.Status.ClaimRef; channel.Status.Phase == api.MessagingBound && ref != nil && ref.UID != profile.UID {
			return nil, fmt.Errorf("messaging channel %s is bound to %s", channel.Name, ref.Name)
		}
		return channel, nil
	}
	for i := range channels {
		channel := &channels[i]
		if channel.Spec.Platform != want.Platform || !channelAvailable(channel) {
			continue
		}
		return channel, nil
	}
	return nil, fmt.Errorf("no Available %s MessagingChannel for %s", want.Platform, profile.Name)
}

func channelAvailable(channel *api.MessagingChannel) bool {
	if channel.Status.Phase == api.MessagingBound {
		return false
	}
	return channel.Status.ClaimRef == nil
}

func updateChannelStatus(ctx context.Context, c client.Client, channel *api.MessagingChannel, phase string, ref *api.MessagingClaimRef) error {
	key := client.ObjectKeyFromObject(channel)
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		current := &api.MessagingChannel{}
		if err := c.Get(ctx, key, current); err != nil {
			return err
		}
		current.Status.Phase = phase
		current.Status.ClaimRef = ref
		last = c.Status().Update(ctx, current)
		if !apierrors.IsConflict(last) {
			return last
		}
	}
	if last == nil {
		last = fmt.Errorf("messaging channel %s status conflict", channel.Name)
	}
	return last
}

func releaseProfileChannels(ctx context.Context, c client.Client, profile *api.HermesProfile) error {
	list := &api.MessagingChannelList{}
	if err := c.List(ctx, list, client.InNamespace(profile.Namespace)); err != nil {
		return err
	}
	for i := range list.Items {
		channel := &list.Items[i]
		ref := channel.Status.ClaimRef
		if ref == nil || ref.Kind != "HermesProfile" || ref.UID != profile.UID {
			continue
		}
		if err := updateChannelStatus(ctx, c, channel, api.MessagingAvailable, nil); err != nil {
			return err
		}
	}
	return nil
}

func ensureMessagingFinalizer(ctx context.Context, c client.Client, profile *api.HermesProfile) error {
	want := profile.Spec.Messaging != nil && profile.DeletionTimestamp.IsZero()
	has := controllerutil.ContainsFinalizer(profile, messagingFinalizer)
	if want == has {
		return nil
	}
	base := profile.DeepCopy()
	if want {
		controllerutil.AddFinalizer(profile, messagingFinalizer)
	} else {
		controllerutil.RemoveFinalizer(profile, messagingFinalizer)
	}
	return c.Patch(ctx, profile, client.MergeFrom(base))
}

type resourceChannel struct {
	Platform  string               `json:"platform"`
	Name      string               `json:"name"`
	TokenPath string               `json:"tokenPath"`
	Telegram  *resourceTelegram    `json:"telegram,omitempty"`
	Ntfy      *api.NtfyChannelSpec `json:"ntfy,omitempty"`
}

type resourceTelegram struct {
	// ID is the bot username without @. Name is the display name setMyName reconciles.
	ID   string `json:"id"`
	Name string `json:"name"`
}

func projectGatewayResources(ctx context.Context, r *HermesReconciler, h *api.Hermes, profiles []api.HermesProfile, channels []api.MessagingChannel, d *appsv1.Deployment) error {
	plugins, err := resourcesPlugins(h, profiles)
	if err != nil {
		return err
	}
	messaging, err := resourcesChannels(channels, profiles)
	if err != nil {
		return err
	}
	generated := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: resourcesConfigMap, Namespace: h.Namespace}, Data: map[string]string{
		"plugins.json":           string(plugins),
		"messaging-channel.json": string(messaging),
	}}
	if err := apply(ctx, r.Client, r.Scheme, h, generated); err != nil {
		return err
	}
	if d.Spec.Template.Annotations == nil {
		d.Spec.Template.Annotations = map[string]string{}
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(append(append([]byte{}, plugins...), messaging...)))
	d.Spec.Template.Annotations["hermes.nousresearch.com/resources"] = sum
	tokens, err := messagingRollout(ctx, r, h, channels)
	if err != nil {
		return err
	}
	d.Spec.Template.Annotations["hermes.nousresearch.com/messaging-rollout"] = tokens
	return nil
}

func resourcesPlugins(h *api.Hermes, profiles []api.HermesProfile) ([]byte, error) {
	doc := map[string][]api.PluginSource{}
	if len(h.Spec.Plugins) > 0 {
		if _, err := pluginsDocument(h.Spec.Plugins); err != nil {
			return nil, fmt.Errorf("%s spec.plugins: %w", h.Name, err)
		}
		doc[""] = h.Spec.Plugins
	}
	ordered := append([]api.HermesProfile(nil), profiles...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	for _, profile := range ordered {
		if len(profile.Spec.Plugins) == 0 {
			continue
		}
		if _, err := pluginsDocument(profile.Spec.Plugins); err != nil {
			return nil, fmt.Errorf("%s spec.plugins: %w", profile.Name, err)
		}
		doc[profile.Name] = profile.Spec.Plugins
	}
	return marshalSortedJSON(doc)
}

func resourcesChannels(channels []api.MessagingChannel, profiles []api.HermesProfile) ([]byte, error) {
	byUID := map[types.UID]string{}
	for _, profile := range profiles {
		byUID[profile.UID] = profile.Name
	}
	doc := map[string]resourceChannel{}
	ordered := append([]api.MessagingChannel(nil), channels...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	for _, channel := range ordered {
		if channel.Status.Phase != api.MessagingBound || channel.Status.ClaimRef == nil {
			continue
		}
		profile, ok := byUID[channel.Status.ClaimRef.UID]
		if !ok {
			continue
		}
		if channel.Spec.Ntfy != nil {
			n := *channel.Spec.Ntfy
			n.TokenSecretRef = nil
			entry := resourceChannel{Platform: channel.Spec.Platform, Name: channel.Name, Ntfy: &n}
			if channel.Spec.Ntfy.TokenSecretRef != nil {
				entry.TokenPath = "/run/hermes/resources/channels/" + channel.Name + "/token"
			}
			doc[profile] = entry
			continue
		}
		if channel.Spec.Telegram == nil {
			continue
		}
		doc[profile] = resourceChannel{
			Platform:  channel.Spec.Platform,
			Name:      channel.Name,
			TokenPath: "/run/hermes/resources/channels/" + channel.Name + "/token",
			Telegram:  &resourceTelegram{ID: channel.Spec.Telegram.ID, Name: channel.Spec.Telegram.Name},
		}
	}
	return marshalSortedJSON(doc)
}

func marshalSortedJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, key := range keys {
		encoded, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, obj[key], "  ", "  "); err != nil {
			return nil, err
		}
		buf.WriteString("  ")
		buf.Write(encoded)
		buf.WriteString(": ")
		buf.Write(pretty.Bytes())
		if i+1 < len(keys) {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}

func messagingRollout(ctx context.Context, r *HermesReconciler, h *api.Hermes, channels []api.MessagingChannel) (string, error) {
	var tokens []string
	ordered := append([]api.MessagingChannel(nil), channels...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	for _, channel := range ordered {
		refPtr := channelTokenRef(channel)
		if refPtr == nil {
			continue
		}
		ref := *refPtr
		secret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: h.Namespace, Name: ref.Name}, secret); err != nil {
			return "", err
		}
		value, ok := secret.Data[ref.Key]
		if !ok {
			return "", fmt.Errorf("messaging channel %s token Secret %s missing selected key", channel.Name, secret.Name)
		}
		key := string(h.UID) + "/" + h.Namespace + "/channel/" + channel.Name + "/" + secret.Name + "/" + ref.Key
		credentialRollouts.Lock()
		state, exists := credentialRollouts.entries[key]
		if !exists || !reflect.DeepEqual(state.value, value) {
			state = credentialState{append([]byte(nil), value...), string(uuid.NewUUID())}
			credentialRollouts.entries[key] = state
		}
		credentialRollouts.Unlock()
		tokens = append(tokens, state.token)
	}
	return strings.Join(tokens, ","), nil
}

func projectChannels(d *appsv1.Deployment, channels []api.MessagingChannel) {
	pod := &d.Spec.Template.Spec
	mode := int32(0440)
	ordered := append([]api.MessagingChannel(nil), channels...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	for _, channel := range ordered {
		refPtr := channelTokenRef(channel)
		if refPtr == nil {
			continue
		}
		ref := *refPtr
		volume := "channel-" + channel.Name
		pod.Volumes = append(pod.Volumes, corev1.Volume{Name: volume, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName: ref.Name, DefaultMode: &mode, Optional: ref.Optional,
			Items: []corev1.KeyToPath{{Key: ref.Key, Path: "token"}},
		}}})
		mount := corev1.VolumeMount{Name: volume, MountPath: "/run/hermes/resources/channels/" + channel.Name, ReadOnly: true}
		pod.Containers[0].VolumeMounts = append(pod.Containers[0].VolumeMounts, mount)
		for i := range pod.InitContainers {
			pod.InitContainers[i].VolumeMounts = append(pod.InitContainers[i].VolumeMounts, mount)
		}
	}
}
