// Package v1alpha1 defines the controller's namespaced foundation API.
// +kubebuilder:object:generate=true
// +groupName=hermes.nousresearch.com
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var GroupVersion = schema.GroupVersion{Group: "hermes.nousresearch.com", Version: "v1alpha1"}
var SchemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &Hermes{}, &HermesList{}, &HermesProfile{}, &HermesProfileList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
})
var AddToScheme = SchemeBuilder.AddToScheme

type StorageSpec struct {
	// Ephemeral uses Pod-local emptyDir storage; no PVC is created.
	// Data survives container restarts but is discarded when the Pod is replaced.
	Ephemeral bool `json:"ephemeral,omitempty"`
	// Size is a Kubernetes storage quantity. Existing claims are never resized or replaced.
	// +kubebuilder:validation:Pattern=`^[1-9][0-9]*(Mi|Gi|Ti)$`
	Size             string  `json:"size"`
	StorageClassName *string `json:"storageClassName,omitempty"`
}

// SSHSpec contains optional connection policy overrides, not CA selection.
type SSHSpec struct {
	// +kubebuilder:default="cluster.local"
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`
	ClusterDomain string `json:"clusterDomain,omitempty"`
	// +kubebuilder:default=86400
	// LifetimeSeconds allows at least twenty minutes for issuance and propagation.
	// +kubebuilder:validation:Minimum=1200
	// +kubebuilder:validation:Maximum=31536000
	LifetimeSeconds int64 `json:"lifetimeSeconds,omitempty"`
	// +kubebuilder:default=21600
	// RenewBeforeSeconds reserves at least ten minutes for projection and restart.
	// +kubebuilder:validation:Minimum=600
	// +kubebuilder:validation:Maximum=31535999
	RenewBeforeSeconds int64 `json:"renewBeforeSeconds,omitempty"`
}

// PluginSource is one pinned tree in the projected plugins.json document.
// The gateway init fetches it. This API does not install or enable the plugin.
type PluginSource struct {
	// Name is the directory under $HERMES_HOME/plugins/<name>.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9._-]*$`
	Name string `json:"name"`
	// Repository is the HTTPS Git remote. Credentials are not accepted.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^https://[^@\s]+$`
	Repository string `json:"repository"`
	// Revision is the full 40-character commit SHA. Tags and branches are rejected.
	// +kubebuilder:validation:Pattern=`^[0-9a-fA-F]{40}$`
	Revision string `json:"revision"`
	// Path is an optional subdirectory inside the repository.
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:XValidation:rule="!self.contains('..') && !self.startsWith('/')",message="path must be a relative subdirectory without .."
	Path string `json:"path,omitempty"`
}

type HermesSpec struct {
	// SSH optionally overrides managed SSH defaults. Omission provisions an owned CA.
	// +kubebuilder:validation:XValidation:rule="self.renewBeforeSeconds < self.lifetimeSeconds",message="renewBeforeSeconds must be less than lifetimeSeconds"
	SSH *SSHSpec `json:"ssh,omitempty"`
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`
	// Deprecated: retained for independently pinned older controllers; ignored here.
	HelperImage string `json:"helperImage,omitempty"`
	// Command is explicit: no unverified Hermes image entrypoint is assumed.
	// +kubebuilder:validation:MinItems=1
	Command []string    `json:"command"`
	Storage StorageSpec `json:"storage"`
	// Deprecated: retained for older controllers; migrate settings to ConfigMap.
	ConfigSecret string `json:"configSecret,omitempty"`
	// Config is the nonsecret Hermes YAML object. Unknown fields are preserved.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type=object
	Config runtime.RawExtension `json:"config,omitempty"`
	// Deprecated: retained for older pinned controllers; ignored by this controller.
	ConfigMap string `json:"configMap,omitempty"`
	// CredentialsSecret selects the same-namespace dotenv credential file.
	CredentialsSecret *corev1.SecretKeySelector `json:"credentialsSecret,omitempty"`
	// Plugins is an optional plugins.json document. Omission or an empty list
	// installs nothing. The controller only projects the document; the gateway
	// init fetches pinned trees. Memory provider activation stays in spec.config.
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=name
	Plugins []PluginSource `json:"plugins,omitempty"`
}

// TerminalSpec selects a compatible image and workspace. SSH startup is controller-owned.
type TerminalSpec struct {
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`
	// Deprecated: startup is managed by the controller.
	Command []string    `json:"command,omitempty"`
	Storage StorageSpec `json:"storage"`
}

type HermesProfileSpec struct {
	// Deprecated: ordinary terminal Pod environment only; no SSH session bridge.
	// Use profile credentialsSecret and config.terminal.env_passthrough for SSH.
	// Retained for independently pinned older controllers.
	// +kubebuilder:validation:MaxItems=128
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:items:XValidation:rule="self.name.matches('^[A-Za-z_][A-Za-z0-9_]*$') && !self.name.startsWith('HERMES_TERMINAL_ENV_')",message="name must be a shell variable and must not use the reserved bridge prefix"
	// +kubebuilder:validation:items:XValidation:rule="!has(self.valueFrom) || (has(self.valueFrom.secretKeyRef) && !has(self.valueFrom.configMapKeyRef) && !has(self.valueFrom.fieldRef) && !has(self.valueFrom.resourceFieldRef))",message="valueFrom supports only secretKeyRef"
	// +kubebuilder:validation:items:XValidation:rule="!has(self.valueFrom) || !has(self.value) || self.value == ''",message="value and valueFrom are mutually exclusive"
	Env []corev1.EnvVar `json:"env,omitempty"`

	// HermesRef names the same-namespace parent. The binding is immutable; recreate
	// the profile to select a different parent, with an explicit data migration.
	// +kubebuilder:validation:XValidation:rule="oldSelf == self",message="hermesRef is immutable"
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	HermesRef string       `json:"hermesRef"`
	Storage   StorageSpec  `json:"storage"`
	Terminal  TerminalSpec `json:"terminal"`
	// Deprecated: retained for older controllers; migrate settings to ConfigMap.
	ConfigSecret string `json:"configSecret,omitempty"`
	// Config is the nonsecret Hermes YAML object. Unknown fields are preserved.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type=object
	Config runtime.RawExtension `json:"config,omitempty"`
	// Deprecated: retained for older pinned controllers; ignored by this controller.
	ConfigMap string `json:"configMap,omitempty"`
	// CredentialsSecret selects the same-namespace dotenv credential file.
	CredentialsSecret *corev1.SecretKeySelector `json:"credentialsSecret,omitempty"`
	// Plugins is an optional plugins.json document. Omission or an empty list
	// installs nothing. The controller only projects the document; the gateway
	// init fetches pinned trees. Memory provider activation stays in spec.config.
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=name
	Plugins []PluginSource `json:"plugins,omitempty"`
}

type ResourceStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:validation:XValidation:rule="self.metadata.name.matches('^[a-z0-9]([a-z0-9-]*[a-z0-9])?$') && size(self.metadata.name) <= 40",message="name must be a DNS label of at most 40 characters"
type Hermes struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              HermesSpec     `json:"spec"`
	Status            ResourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type HermesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Hermes `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:validation:XValidation:rule="self.metadata.name.matches('^[a-z0-9]([a-z0-9-]*[a-z0-9])?$') && size(self.metadata.name) <= 40",message="name must be a DNS label of at most 40 characters"
// +kubebuilder:validation:XValidation:rule="self.metadata.name != 'default'",message="profile name default is reserved for the instance home"
type HermesProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              HermesProfileSpec `json:"spec"`
	Status            ResourceStatus    `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type HermesProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HermesProfile `json:"items"`
}
