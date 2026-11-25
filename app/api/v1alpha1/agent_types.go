package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SkillKind selects the registered Go skill implementation an Agent runs. It is
// a CLOSED set: the one place adding a value requires new Go (ISI-7800 §7
// registry). Keep this enum in lock-step with the orchestrator SkillKindRegistry
// (ISI-7804) and the CRD manifests (app/config/crd/bases + deploy/base/crds +
// chart copy).
type SkillKind string

const (
	SkillKindArchitect     SkillKind = "architect"
	SkillKindResearch      SkillKind = "research"
	SkillKindCoding        SkillKind = "coding"
	SkillKindObservability SkillKind = "observability"
	SkillKindGraphic       SkillKind = "graphic"
	SkillKindWriter        SkillKind = "writer" // the Phase-1 linear content writer
)

// KnownSkillKinds is the closed set for AgentSpec.SkillKind. Enforced by the CRD
// OpenAPI enum (a scalar enum the apiserver rejects with a clear message) and,
// once ISI-7803/7804 land, re-checked by the Agent webhook against the live
// orchestrator SkillKindRegistry so a manifest that drifts ahead of the compiled
// registry fails closed. Same discipline as KnownOutputFormats.
var KnownSkillKinds = []SkillKind{
	SkillKindArchitect, SkillKindResearch, SkillKindCoding,
	SkillKindObservability, SkillKindGraphic, SkillKindWriter,
}

// AgentSpec defines a configured specialist: a role label, the Go skill kind that
// implements its behaviour, its model, its prompt skill-packs, and which actor
// template runs it. Admin-owned (platform-wide); Owner is defined for registry
// uniformity but is empty/"platform" in Phase 1.
type AgentSpec struct {
	// Role is the human-facing label, e.g. "System Architect". Required.
	// +kubebuilder:validation:MinLength=1
	Role string `json:"role"`

	// Description is free-text operator context. Optional.
	// +optional
	Description string `json:"description,omitempty"`

	// SkillKind selects the Go behaviour (closed set — see KnownSkillKinds).
	// +kubebuilder:validation:Enum=architect;research;coding;observability;graphic;writer
	SkillKind SkillKind `json:"skillKind"`

	// SkillPacks are named markdown prompt packs layered on top of the skill's
	// base prompt (the writer already loads a "skill pack"). Each name must
	// resolve to a pack the actor can load; unknown packs degrade (warn), they do
	// not fail admission. Optional.
	// +optional
	// +listType=atomic
	SkillPacks []string `json:"skillPacks,omitempty"`

	// ModelProfile is the litellmName of the ModelProfile this agent requests.
	// Optional — empty or "default" resolves to the system-default ModelProfile.
	// A non-empty, non-"default" value MUST name an existing ModelProfile (webhook
	// hard error otherwise — fail closed, same stance as the engine). See
	// ISI-7800 §2.2 for the global→per-agent→per-call resolution order.
	// +optional
	ModelProfile string `json:"modelProfile,omitempty"`

	// ActorTemplate selects the actor runtime: the lean "content" actor
	// (prose/code/text, the default) or the fat "media" actor (image/video,
	// ISI-7395). Reuses the ADR-3 ActorTemplateContent/Media constants shared with
	// ReferenceRepo/LabelRoute. Defaults to "content".
	// +kubebuilder:validation:Enum=content;media
	// +kubebuilder:default=content
	// +optional
	ActorTemplate string `json:"actorTemplate,omitempty"`

	// Default marks THE canonical Agent for its SkillKind — the fallback a team
	// node may reference by kind, and the row seeded to reproduce today. AT MOST
	// ONE Agent per SkillKind may set this true (webhook, ISI-7800 §4). Zero is
	// allowed.
	// +optional
	Default bool `json:"default,omitempty"`

	// Owner — platform-owned (empty) in Phase 1; present for registry uniformity.
	// +optional
	Owner OwnerRef `json:"owner,omitempty"`
}

// AgentStatus defines the observed state of an Agent.
type AgentStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" listType:"map"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=agent
// +kubebuilder:printcolumn:name="Role",type=string,JSONPath=`.spec.role`
// +kubebuilder:printcolumn:name="SkillKind",type=string,JSONPath=`.spec.skillKind`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.modelProfile`
// +kubebuilder:printcolumn:name="Default",type=boolean,JSONPath=`.spec.default`

// Agent is the Schema for the agents API: one configured specialist.
type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentSpec   `json:"spec,omitempty"`
	Status AgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentList contains a list of Agent.
type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Agent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Agent{}, &AgentList{})
}
