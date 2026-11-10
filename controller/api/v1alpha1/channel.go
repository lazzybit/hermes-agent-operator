package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &Channel{}, &ChannelList{}, &ChannelClaim{}, &ChannelClaimList{}, &ChannelClass{}, &ChannelClassList{})
		return nil
	})
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type Channel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ChannelSpec   `json:"spec"`
	Status            ChannelStatus `json:"status,omitempty"`
}
type ChannelSpec struct {
	ChannelClassName string              `json:"channelClassName,omitempty"`
	DisplayName      string              `json:"displayName,omitempty"`
	Telegram         TelegramChannelSpec `json:"telegram"`
	BotID            int64               `json:"botID"`
	OwnerUserID      int64               `json:"ownerUserID,omitempty"`
	ClaimRef         *MessagingClaimRef  `json:"claimRef,omitempty"`
}
type ChannelStatus struct {
	ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
type ChannelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Channel `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type ChannelClaim struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ChannelClaimSpec   `json:"spec"`
	Status            ChannelClaimStatus `json:"status,omitempty"`
}
type ChannelClaimSpec struct {
	// Name is the logical channel name, independent of the Kubernetes resource name.
	// +kubebuilder:validation:Pattern="^[a-z0-9]+(-[a-z0-9]+)*$"
	Name string `json:"name,omitempty"`
	// Explicit empty disables dynamic provisioning. No default class is selected.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="channelClassName is immutable"
	ChannelClassName *string `json:"channelClassName,omitempty"`
	ChannelName      string  `json:"channelName,omitempty"`
	DisplayName      string  `json:"displayName,omitempty"`
}
type ChannelClaimStatus struct {
	ResourceStatus `json:",inline"`
	// +kubebuilder:validation:Enum=Pending;Available
	Phase       string `json:"phase,omitempty"`
	ChannelName string `json:"channelName,omitempty"`
	// Persist generated identity before external side effects.
	GeneratedUsername string             `json:"generatedUsername,omitempty"`
	ExternalBotID     int64              `json:"externalBotID,omitempty"`
	ConsumerRef       *MessagingClaimRef `json:"consumerRef,omitempty"`
}

// +kubebuilder:object:root=true
type ChannelClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ChannelClaim `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type ChannelClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ChannelClassSpec `json:"spec"`
	Status            ResourceStatus   `json:"status,omitempty"`
}
type ChannelClassSpec struct {
	// +kubebuilder:validation:Enum=telegram
	Provisioner string                        `json:"provisioner"`
	Parameters  TelegramProvisionerParameters `json:"parameters"`
}
type TelegramProvisionerParameters struct {
	ManagerTokenSecretRef corev1.SecretKeySelector `json:"managerTokenSecretRef"`
	// Session Secret contains api_id, api_hash and session (Telethon StringSession).
	UserSessionSecretRef corev1.LocalObjectReference `json:"userSessionSecretRef"`
	// UsernameTemplate supports only {RANDOMIZED} and {NAME}.
	UsernameTemplate string `json:"usernameTemplate"`
}

// +kubebuilder:object:root=true
type ChannelClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ChannelClass `json:"items"`
}
