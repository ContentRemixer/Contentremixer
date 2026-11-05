package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ModelProfileSpec defines the desired state of a ModelProfile: one CR per
// LiteLLM model backend. Admin-owned (platform-wide).
type ModelProfileSpec struct {
	// Provider is the LiteLLM provider prefix (openai, anthropic, azure, vertex_ai,
	// bedrock, ollama, openai-compatible, ...). Free-form string LiteLLM understands.
	// +kubebuilder:validation:MinLength=1
	Provider string `json:"provider"`

	// Model is the provider model id (e.g. "gpt-4o", "claude-opus-4", "llama3.1:70b").
	// Rendered into LiteLLM `model` as "<provider>/<model>" unless APIBase overrides.
	// +kubebuilder:validation:MinLength=1
	Model string `json:"model"`

	// LiteLLMName is the public alias exposed by the gateway (the name agents request,
	// e.g. "default", "fast", "long-context"). Must be unique across ModelProfiles;
	// uniqueness is enforced by the webhook and re-checked by the controller.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	LiteLLMName string `json:"litellmName"`

	// APIBase overrides the endpoint for openai-compatible / self-hosted backends
	// (e.g. the homelab agentgateway/LiteLLM endpoint). Optional.
	// +optional
	APIBase string `json:"apiBase,omitempty"`

	// APIKeyRef references the Secret key holding this backend's API key.
	// Optional for keyless local backends (e.g. ollama).
	// +optional
	APIKeyRef *SecretKeyRef `json:"apiKeyRef,omitempty"`

	// Default marks the system default model. EXACTLY ONE ModelProfile in the
	// namespace may set this true. Enforced by the validating webhook (§2.1a) and
	// re-checked by the controller (defence in depth).
	// +optional
	Default bool `json:"default,omitempty"`

	// Tags are capability hints (e.g. ["long-context","vision","cheap"]) the
	// orchestrator may use to pick a non-default model. Informational in Phase 1.
	// +optional
	Tags []string `json:"tags,omitempty"`

	// Owner — see OwnerRef. Admin/platform-owned when empty (the Phase-1 default).
	// +optional
	Owner OwnerRef `json:"owner,omitempty"`
}

// ModelProfileStatus defines the observed state of a ModelProfile.
type ModelProfileStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" listType:"map"`

	// Rendered is true once this profile is present in the generated LiteLLM config.
	// +optional
	Rendered bool `json:"rendered,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mp
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.provider`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.model`
// +kubebuilder:printcolumn:name="Default",type=boolean,JSONPath=`.spec.default`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// ModelProfile is the Schema for the modelprofiles API.
type ModelProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelProfileSpec   `json:"spec,omitempty"`
	Status ModelProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ModelProfileList contains a list of ModelProfile.
type ModelProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModelProfile{}, &ModelProfileList{})
}
