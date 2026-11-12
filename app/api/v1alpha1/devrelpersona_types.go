package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Reference kinds: the closed set of persona style-reference sources the
// scraper (P2-B / DevRel onboarding D2, ISI-7357) knows how to fetch. These
// constants are the single source of truth; every lock-step surface sources
// from them. Adding a kind is a LOCK-STEP change across: this list, the
// PersonaReference.Kind +kubebuilder:validation:Enum marker below, the scraper
// dispatch (app/internal/rag/scrape, which aliases these constants), app/review
// KnownSourceKinds (string-mirrored, guarded by a lock-step test), and the
// regenerated CRD manifests (app/config/crd/bases + deploy/base/crds + chart
// copy). The validating webhook needs no edit — it iterates KnownReferenceKinds.
const (
	// ReferenceKindYouTube is a YouTube video; the scraper fetches its caption
	// track and normalizes it to transcript text.
	ReferenceKindYouTube = "youtube"
	// ReferenceKindArticle is a web article or blog post; the scraper extracts
	// the readable article body. A blog reference uses this kind — there is no
	// separate "blog" kind.
	ReferenceKindArticle = "article"
	// ReferenceKindGitHubRepo is a GitHub repository of tutorial/docs content;
	// the scraper fetches its Markdown files only (board decision: markdown-only
	// for v1). Fetcher is D2 (ISI-7357).
	ReferenceKindGitHubRepo = "github-repo"
	// ReferenceKindTeleprompterScript is a DevRel talk/teleprompter script
	// document; the scraper extracts its plain text. Fetcher is D2 (ISI-7357).
	ReferenceKindTeleprompterScript = "teleprompter-script"
	// ReferenceKindTalkRecording is a recorded talk. For v1 it is a captioned
	// recording URL whose caption track is fetched; raw-audio ASR is deferred
	// (board decision). Fetcher is D2 (ISI-7357).
	ReferenceKindTalkRecording = "talk-recording"
)

// KnownReferenceKinds is the closed set of persona style-reference kinds the
// scraper knows how to fetch. The CRD OpenAPI enum and the validating webhook
// both source from this contract. See the ReferenceKind* constants for the
// lock-step surfaces that must change together when a kind is added.
var KnownReferenceKinds = []string{
	ReferenceKindYouTube,
	ReferenceKindArticle,
	ReferenceKindGitHubRepo,
	ReferenceKindTeleprompterScript,
	ReferenceKindTalkRecording,
}

// PrimaryContentKind is the dominant medium a DevRel primarily produces,
// captured during onboarding (plan O2). It conditions style synthesis (D3,
// ISI-7358) and the content test (D4, ISI-7359): a DevRel whose primary medium
// is talks is synthesized and tested differently from one who primarily blogs.
// It is distinct from a reference Kind — references are style *exemplar sources*;
// PrimaryContentKind is the *output medium* the persona targets.
type PrimaryContentKind string

const (
	PrimaryContentBlog     PrimaryContentKind = "blog"
	PrimaryContentVideo    PrimaryContentKind = "video"
	PrimaryContentTalk     PrimaryContentKind = "talk"
	PrimaryContentTutorial PrimaryContentKind = "tutorial"
)

// KnownPrimaryContentKinds is the closed set for DevRelPersonaSpec.PrimaryContentKind.
// It is enforced by the CRD OpenAPI enum (a scalar enum apiserver rejects with a
// clear message); unlike the reference-kind list it needs no webhook re-check.
var KnownPrimaryContentKinds = []PrimaryContentKind{
	PrimaryContentBlog,
	PrimaryContentVideo,
	PrimaryContentTalk,
	PrimaryContentTutorial,
}

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
	// PersonaConditionValidated reflects whether the owning DevRel has reviewed
	// the synthesized style profile and validated it (plan §10 decision 1 =
	// synthesize). It is set True by the Booth validate action (D4 content-test /
	// D7 persona-mgmt, ISI-7359/7362) once the owner accepts the profile, and is
	// the declarative gate the writer-conditioner (ISI-7147) checks to refuse
	// serving an un-validated persona's style. The style-profile *content*
	// (synthesized traits + sample) lives in Postgres as app/review.StyleProfile,
	// NOT in CRD status — see docs/design/isi-7356-devrel-model-contract.md.
	// This condition is the cheap declarative mirror of that row's Validated flag.
	PersonaConditionValidated = "Validated"
)

// DevRelPersona condition reasons for PersonaConditionValidated.
const (
	// PersonaReasonProfileSynthesized — a style profile exists and awaits the
	// owner's review (Validated=False, reason recorded by D3/D4).
	PersonaReasonProfileSynthesized = "ProfileSynthesized"
	// PersonaReasonProfileValidated — the owner accepted the profile (Validated=True).
	PersonaReasonProfileValidated = "ProfileValidated"
	// PersonaReasonAwaitingSynthesis — no style profile has been synthesized yet
	// (Validated=False); ingestion/synthesis is still pending.
	PersonaReasonAwaitingSynthesis = "AwaitingSynthesis"
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
	// the style prompt even before any references are embedded. The DevRel
	// onboarding bio / extra-context box REUSES this field — no new field is
	// added for it (ISI-7356).
	// +optional
	SelfDescription string `json:"selfDescription,omitempty"`

	// PrimaryContentKind is the dominant medium this DevRel produces, captured
	// during onboarding (plan O2): blog | video | talk | tutorial. It conditions
	// style synthesis (D3) and the content test (D4). Optional; unset means
	// "unspecified" and the synthesizer falls back to a medium-agnostic profile.
	// +kubebuilder:validation:Enum=blog;video;talk;tutorial
	// +optional
	PrimaryContentKind PrimaryContentKind `json:"primaryContentKind,omitempty"`

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

	// Kind selects how the scraper fetches the exemplar: youtube | article |
	// github-repo | teleprompter-script | talk-recording. The closed set mirrors
	// KnownReferenceKinds; the validating webhook rejects anything else with a
	// message naming the allowed kinds. Keep this enum in lock-step with the
	// ReferenceKind* constants.
	// +kubebuilder:validation:Enum=youtube;article;github-repo;teleprompter-script;talk-recording
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
// +kubebuilder:printcolumn:name="Validated",type=string,JSONPath=`.status.conditions[?(@.type=="Validated")].status`

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
