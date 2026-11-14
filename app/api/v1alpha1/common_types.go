package v1alpha1

// SecretKeyRef points at a key inside a Kubernetes Secret in the CR's namespace.
// Secrets are NEVER inlined (plan §Config & secrets policy).
type SecretKeyRef struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// OwnerRef identifies the subject that owns an owner-scoped CR.
// Phase 1: ModelProfile and ReferenceRepo are Admin-owned (platform-wide),
// so Owner is OPTIONAL on them and, when empty, means "platform/Admin".
// Phase 2: DevRelPersona will REQUIRE Owner. The field is defined now so the
// Booth API ownership filter (design §6) is uniform across all CRDs from day one.
type OwnerRef struct {
	// Subject is the IdP subject (OIDC `sub`) of the owning DevRel, or empty
	// for Admin/platform-owned resources.
	// +optional
	Subject string `json:"subject,omitempty"`
}

// RetrievalSpec tunes owner-scoped top-k voice-exemplar retrieval for a
// DevRelPersona at write time (design §2.0, §5). It is read by the P2-D writer
// conditioner (ISI-7147); all fields are optional with safe defaults so an
// existing persona keeps working unchanged. Conditioning is best-effort — too
// few matches degrades gracefully to style-only generation, it never fails the
// run (§5.4).
type RetrievalSpec struct {
	// TopK is the maximum number of voice exemplars injected into the writer
	// prompt. Defaults to 5 when zero.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=20
	// +kubebuilder:default=5
	// +optional
	TopK int `json:"topK,omitempty"`

	// MinScore drops exemplars below this cosine similarity (0..1), as a decimal
	// string so the CRD does not depend on float OpenAPI semantics. Empty or "0"
	// keeps all top-k; a malformed value is treated as 0 by the conditioner
	// (best-effort, never fails the run).
	// +optional
	MinScore string `json:"minScore,omitempty"`
}

// KnownInputFormats is the Phase-1 closed set of analyzer input formats.
var KnownInputFormats = []string{"teleprompter", "livestream", "blog", "podcast", "auto"}

// KnownOutputFormats is the Phase-1 closed set of writer output formats.
// Each must have a matching styles/<format>.md in the content repo.
// Enum sourcing (design §2.2a): adding a format = (1) add styles/<new>.md,
// (2) extend the enum, (3) make manifests.
var KnownOutputFormats = []string{"blog", "short", "tutorial", "teleprompter"}

// ContentFormatType is the output *deliverable* a DevRel targets a length/format
// for (DevRel onboarding D1b, ISI-7370). It is distinct from both the analyzer
// input formats above and the reference Kind (a style *exemplar source*): a
// ContentFormatType names a finished-content channel the DevRel ships to, e.g. a
// 3-minute YouTube short vs a ~25-minute long-form video. The closed set mirrors
// the KnownOutputFormats modelling (a single source-of-truth slice the CRD enum
// and validating webhook both read) but is a DIFFERENT axis — it does not require
// a styles/<format>.md. Adding a type is a lock-step change across: these
// constants, KnownContentFormatTypes, the ContentFormatTarget.ContentType
// +kubebuilder:validation:Enum marker (devrelpersona_types.go), and the
// regenerated CRD manifests (app/config/crd/bases + deploy/base/crds + chart
// copy). The validating webhook needs no edit — it iterates KnownContentFormatTypes.
type ContentFormatType string

const (
	// ContentFormatBlog is a written blog post (target by word count).
	ContentFormatBlog ContentFormatType = "blog"
	// ContentFormatYouTubeShort is a short-form vertical video (target by duration).
	ContentFormatYouTubeShort ContentFormatType = "youtube-short"
	// ContentFormatYouTubeVideo is a long-form YouTube video (target by duration).
	ContentFormatYouTubeVideo ContentFormatType = "youtube-video"
	// ContentFormatLinkedInPost is a LinkedIn text post (target by character count);
	// consumed by D9 LinkedIn promote (ISI-7369).
	ContentFormatLinkedInPost ContentFormatType = "linkedin-post"
)

// KnownContentFormatTypes is the closed set for ContentFormatTarget.ContentType.
// Like KnownPrimaryContentKinds it is enforced by the CRD OpenAPI enum (a scalar
// enum the apiserver rejects with a clear message); the validating webhook also
// re-checks it for a friendlier message. See the ContentFormat* constants for the
// lock-step surfaces that must change together when a type is added.
var KnownContentFormatTypes = []ContentFormatType{
	ContentFormatBlog,
	ContentFormatYouTubeShort,
	ContentFormatYouTubeVideo,
	ContentFormatLinkedInPost,
}
