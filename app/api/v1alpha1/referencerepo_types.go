package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReferenceRepoSpec defines the desired state of a ReferenceRepo: the crate
// (repo of reference), its GitHub PAT (Secret ref), PR target settings, and the
// label→format routes. Admin-owned (platform-wide).
type ReferenceRepoSpec struct {
	// Repo is "owner/name" on GitHub (e.g. "ContentRemixer/Contentremixer").
	// +kubebuilder:validation:Pattern=`^[^/\s]+/[^/\s]+$`
	Repo string `json:"repo"`

	// TokenRef references the Secret key holding the GitHub PAT used to read the
	// repo, post ack comments, and open PRs. Never inlined.
	TokenRef SecretKeyRef `json:"tokenRef"`

	// WebhookSecretRef references the Secret key holding the GitHub webhook HMAC
	// secret used to verify `X-Hub-Signature-256` on deliveries.
	WebhookSecretRef SecretKeyRef `json:"webhookSecretRef"`

	// PRTarget controls how/where PRs are opened.
	// +optional
	PRTarget PRTargetSpec `json:"prTarget,omitempty"`

	// LabelRoutes maps a GitHub label to an (inputFormat, outputFormat) pair.
	// Routing is fully config-driven: the webhook resolves formats from here,
	// never by parsing the label string. See design §3.
	// +kubebuilder:validation:MinItems=1
	LabelRoutes []LabelRoute `json:"labelRoutes"`

	// Owner — Admin/platform-owned when empty (Phase-1 default).
	// +optional
	Owner OwnerRef `json:"owner,omitempty"`
}

// PRTargetSpec controls how/where recycle PRs are opened.
type PRTargetSpec struct {
	// BaseBranch is the PR base (default "main").
	// +kubebuilder:default=main
	// +optional
	BaseBranch string `json:"baseBranch,omitempty"`

	// BranchPrefix prefixes generated PR branches (default "recycle/").
	// +kubebuilder:default=recycle/
	// +optional
	BranchPrefix string `json:"branchPrefix,omitempty"`

	// Draft opens PRs as drafts when true (default false).
	// +optional
	Draft bool `json:"draft,omitempty"`
}

// LabelRoute maps an exact GitHub label to a format transformation.
type LabelRoute struct {
	// Label is the exact GitHub label, e.g. "recycle:blog".
	// +kubebuilder:validation:MinLength=1
	Label string `json:"label"`

	// InputFormat is the source document type the analyzer parses:
	// teleprompter | livestream | blog | podcast | auto.
	// "auto" => detect from the source meta.yaml (design §3).
	// Defaults to "auto" server-side when omitted; omitempty lets Go clients
	// trigger the default instead of sending an explicit empty string.
	// +kubebuilder:validation:Enum=teleprompter;livestream;blog;podcast;auto
	// +kubebuilder:default=auto
	// +optional
	InputFormat string `json:"inputFormat,omitempty"`

	// OutputFormat selects the writer style guide styles/<outputFormat>.md:
	// blog | short | tutorial | teleprompter | demo | video.
	// demo|video are media formats (fat runtime: Chromium + ffmpeg) routed to
	// the "media" actor template (ADR-1/ADR-3); the rest are lean content
	// formats routed to "content".
	// +kubebuilder:validation:Enum=blog;short;tutorial;teleprompter;demo;video
	OutputFormat string `json:"outputFormat"`

	// Skill names the skill pack the route loads (e.g. content-writer,
	// demo-recorder, video-designer). Optional: derived from OutputFormat via
	// DefaultSkill when omitted (ADR-3 — "derivable from outputFormat"). An
	// explicit value overrides the default.
	// +kubebuilder:validation:MinLength=1
	// +optional
	Skill string `json:"skill,omitempty"`

	// ActorTemplate selects the runtime footprint the WorkOrder dispatches to
	// (ADR-3): "content" (lean LLM call, inline) or "media" (fat Chromium +
	// ffmpeg, k8s-Job/substrate). Optional: derived from OutputFormat via
	// DefaultActorTemplate when omitted (demo|video => media, else content).
	// An explicit value overrides the default.
	// +kubebuilder:validation:Enum=content;media
	// +optional
	ActorTemplate string `json:"actorTemplate,omitempty"`
}

// Actor templates (ADR-3): specialization is by runtime footprint, not by
// skill identity. "content" is the lean LLM image (inline run-worker);
// "media" is the fat image (headless Chromium + ffmpeg) run as a k8s-Job
// (P0) or substrate actor (P1).
const (
	ActorTemplateContent = "content"
	ActorTemplateMedia   = "media"
)

// DefaultActorTemplate returns the runtime template an outputFormat routes to
// when a LabelRoute omits actorTemplate (ADR-3). demo|video are the fat media
// skills; every other (lean) format stays on the content template.
func DefaultActorTemplate(outputFormat string) string {
	switch outputFormat {
	case "demo", "video":
		return ActorTemplateMedia
	default:
		return ActorTemplateContent
	}
}

// DefaultSkill returns the skill pack an outputFormat loads when a LabelRoute
// omits skill (ADR-3 defaults; an explicit route.skill overrides it).
func DefaultSkill(outputFormat string) string {
	switch outputFormat {
	case "demo":
		return "demo-recorder"
	case "video":
		return "video-designer"
	case "tutorial":
		return "tutorial"
	case "teleprompter":
		return "teleprompter"
	default: // blog, short
		return "content-writer"
	}
}

// EffectiveSkill is the route's explicit skill, or the OutputFormat default.
func (r LabelRoute) EffectiveSkill() string {
	if r.Skill != "" {
		return r.Skill
	}
	return DefaultSkill(r.OutputFormat)
}

// EffectiveActorTemplate is the route's explicit template, or the OutputFormat
// default. This is the single value the Dispatcher selects a strategy by.
func (r LabelRoute) EffectiveActorTemplate() string {
	if r.ActorTemplate != "" {
		return r.ActorTemplate
	}
	return DefaultActorTemplate(r.OutputFormat)
}

// ReferenceRepoStatus defines the observed state of a ReferenceRepo.
type ReferenceRepoStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" listType:"map"`

	// RouteCount mirrors len(spec.labelRoutes) once validated.
	// +optional
	RouteCount int `json:"routeCount,omitempty"`

	// TokenResolved is true once the PAT Secret ref resolves successfully.
	// +optional
	TokenResolved bool `json:"tokenResolved,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=refrepo,path=referencerepos
// +kubebuilder:printcolumn:name="Repo",type=string,JSONPath=`.spec.repo`
// +kubebuilder:printcolumn:name="Routes",type=integer,JSONPath=`.status.routeCount`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// ReferenceRepo is the Schema for the referencerepos API.
type ReferenceRepo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReferenceRepoSpec   `json:"spec,omitempty"`
	Status ReferenceRepoStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReferenceRepoList contains a list of ReferenceRepo.
type ReferenceRepoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReferenceRepo `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ReferenceRepo{}, &ReferenceRepoList{})
}
