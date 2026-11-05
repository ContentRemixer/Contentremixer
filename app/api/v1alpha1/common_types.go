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

// KnownInputFormats is the Phase-1 closed set of analyzer input formats.
var KnownInputFormats = []string{"teleprompter", "livestream", "blog", "podcast", "auto"}

// KnownOutputFormats is the Phase-1 closed set of writer output formats.
// Each must have a matching styles/<format>.md in the content repo.
// Enum sourcing (design §2.2a): adding a format = (1) add styles/<new>.md,
// (2) extend the enum, (3) make manifests.
var KnownOutputFormats = []string{"blog", "short", "tutorial", "teleprompter"}
