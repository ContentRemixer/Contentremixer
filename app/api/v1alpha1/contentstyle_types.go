package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ContentStyleSpec defines the desired state of a ContentStyle: one
// output-format style guide per CR (ISI-7965 plan W1). Admin-owned and
// GLOBAL in scope - one set of guides shared across all reference repos
// (per-repo overrides are deferred).
//
// The outputFormat contract is the platform-wide KnownOutputFormats set
// declared in common_types.go (the same set the writer validFormats map
// and the DevRelPersona ContentFormatTarget enum use). The OpenAPI enum
// below is the first gate; the validating webhook re-checks against
// KnownOutputFormats. Adding a format is a LOCK-STEP change across:
// common_types.go, the Enum marker here, the writer validFormats map, and
// the regenerated CRD manifests (app/config/crd/bases + deploy/base/crds
// + chart copy).
type ContentStyleSpec struct {
	// OutputFormat is the writer output format this guide applies to.
	// Exactly one ContentStyle per outputFormat is allowed per namespace
	// (enforced by the validating webhook).
	// +kubebuilder:validation:Enum=blog;short;tutorial;teleprompter
	// +kubebuilder:validation:MinLength=1
	OutputFormat string `json:"outputFormat"`

	// Guide is the markdown style-guide body the writer renders against for
	// this output format. Non-empty after trimming (webhook-enforced).
	// +kubebuilder:validation:MinLength=1
	Guide string `json:"guide"`

	// Description is a human-readable summary shown in the Admin UI.
	// +optional
	Description string `json:"description,omitempty"`
}

// ContentStyleStatus defines the observed state of a ContentStyle.
type ContentStyleStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" listType:"map"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cstyle
// +kubebuilder:printcolumn:name=Format,type=string,JSONPath=`.spec.outputFormat`
// +kubebuilder:printcolumn:name=Description,type=string,JSONPath=`.spec.description`

// ContentStyle is the Schema for the contentstyles API.
type ContentStyle struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContentStyleSpec   `json:"spec,omitempty"`
	Status ContentStyleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ContentStyleList contains a list of ContentStyle.
type ContentStyleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContentStyle `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ContentStyle{}, &ContentStyleList{})
}
