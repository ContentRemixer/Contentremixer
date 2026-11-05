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
	// blog | short | tutorial | teleprompter.
	// +kubebuilder:validation:Enum=blog;short;tutorial;teleprompter
	OutputFormat string `json:"outputFormat"`
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
