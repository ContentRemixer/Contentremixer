package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KnownReferenceKinds is the Phase-2 closed set of persona style-reference
// kinds P2-B (scraper) knows how to fetch. Adding a kind = (1) teach the
// scraper, (2) extend this enum, (3) make manifests. The CRD OpenAPI enum
// and the validating webhook both source from this contract.
var KnownReferenceKinds = []string{"youtube", "article"}

// DevRelPersona ingestion phases. This is the P2-A→P2-B→P2-C hand-off
// contract: P2-A (this issue) only ever drives the persona to Pending (or
// Idle when there are no references); P2-B advances Pending→Scraping and
// P2-C advances Scraping→Embedding→Ready (or →Error). See the field docs on
// DevRelPersonaStatus.Phase.
const (
	PersonaPhaseIdle      = "Idle"      // no references to ingest
	PersonaPhasePending   = "Pending"   // references changed; scrape/embed enqueued (owned by P2-A)
	PersonaPhaseScraping  = "Scraping"  // P2-B is fetching exemplars
	PersonaPhaseEmbedding = "Embedding" // P2-C is chunking/embedding/storing
	PersonaPhaseReady     = "Ready"     // exemplars embedded and queryable
	PersonaPhaseError     = "Error"     // a downstream step failed
)

// DevRelPersona status condition types. "Accepted" and "IngestionPending" are
// owned by the P2-A reconciler; "Ready" is set True by P2-C once embedding
// completes. Keeping the names here lets every sibling reference one contract.
const (
	// PersonaConditionAccepted is set by P2-A once the spec is admitted and
	// its references are recorded. It never reflects ingestion progress.
	PersonaConditionAccepted = "Accepted"
	// PersonaConditionIngestionPending is raised True by P2-A on a
	// spec.references change (reason ReferencesChanged) and cleared False by
	// P2-C once the new exemplar set is embedded (reason Embedded).
	PersonaConditionIngestionPending = "IngestionPending"
	// PersonaConditionReady is the roll-up readiness of the persona's style
	// index. P2-C owns the True transition; P2-A only ever sets it False while
	// ingestion is pending so the Booth API never serves a stale/absent index.
	PersonaConditionReady = "Ready"
)

// DevRelPersonaSpec defines the desired state of a DevRelPersona: an
// owner-scoped style profile the writer conditions on. Unlike the Phase-1
// Admin-owned CRDs, a DevRelPersona REQUIRES an owner (design §6 ownership:
// `role==Admin OR resource.owner==subject`); the owner is enforced non-empty
// by the validating webhook.
type DevRelPersonaSpec struct {
	// Owner is the DevRel that owns this persona. REQUIRED for DevRelPersona
	// (Owner.Subject must be non-empty) — the Booth API scopes list/get/mutate
	// by it. Reuses the shared OwnerRef so the ownership filter is uniform
	// across all CRDs (see OwnerRef).
	Owner OwnerRef `json:"owner"`

	// SelfDescription is a free-text description of the persona's voice/style.
	// The agent may author or refine it (P2-D writer feedback loop); it seeds
	// the style prompt even before any references are embedded.
	// +optional
	SelfDescription string `json:"selfDescription,omitempty"`

	// References are the style exemplars to ingest (talks, posts). A change to
	// this list is what the reconciler turns into scrape→embed work.
	// +optional
	// +listType=atomic
	References []PersonaReference `json:"references,omitempty"`

	// Retrieval tunes top-k owner-scoped exemplar retrieval at write time
	// (design §5). Optional; defaults apply when unset. Read by the P2-D writer
	// conditioner (ISI-7147), never by the ingestion reconciler.
	// +optional
	Retrieval RetrievalSpec `json:"retrieval,omitempty"`
}

// PersonaReference is a single style exemplar source.
type PersonaReference struct {
	// URL of the exemplar (a YouTube video or an article page).
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// Kind selects how P2-B fetches the exemplar: youtube | article.
	// The closed set mirrors KnownReferenceKinds; the validating webhook
	// rejects anything else with a message naming the allowed kinds.
	// +kubebuilder:validation:Enum=youtube;article
	Kind string `json:"kind"`
}

// DevRelPersonaStatus defines the observed state of a DevRelPersona.
type DevRelPersonaStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" listType:"map"`

	// Phase is the ingestion phase (see the PersonaPhase* constants). P2-A
	// drives Idle/Pending; P2-B/P2-C drive Scraping/Embedding/Ready/Error.
	// +optional
	Phase string `json:"phase,omitempty"`

	// ExemplarCount is the number of exemplars successfully embedded. Owned by
	// P2-C; P2-A never writes it.
	// +optional
	ExemplarCount int `json:"exemplarCount,omitempty"`

	// LastEmbeddedAt is when the style index was last (re)built. Owned by P2-C.
	// +optional
	LastEmbeddedAt *metav1.Time `json:"lastEmbeddedAt,omitempty"`

	// ObservedReferenceHash is the fingerprint of the spec.references list the
	// reconciler last enqueued. It is the P2-A edge-trigger: a mismatch between
	// this and the live spec is what moves the persona back to Pending. Owned
	// by P2-A exclusively.
	// +optional
	ObservedReferenceHash string `json:"observedReferenceHash,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=persona;personas,path=devrelpersonas
// +kubebuilder:printcolumn:name="Owner",type=string,JSONPath=`.spec.owner.subject`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Exemplars",type=integer,JSONPath=`.status.exemplarCount`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// DevRelPersona is the Schema for the devrelpersonas API.
type DevRelPersona struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DevRelPersonaSpec   `json:"spec,omitempty"`
	Status DevRelPersonaStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DevRelPersonaList contains a list of DevRelPersona.
type DevRelPersonaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DevRelPersona `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DevRelPersona{}, &DevRelPersonaList{})
}
