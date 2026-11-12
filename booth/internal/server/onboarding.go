package server

import (
	"net/http"

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// onboardingStatus is the wire shape the SPA probes to decide whether to launch
// the DevRel self-service onboarding wizard (ISI-7360 D5, plan ISI-7350 §1/§4).
// It answers one question for the LOGGED-IN caller: "do I already have a
// validated persona?" — distinct from the seed-Admin bootstrap probe
// (/v1/auth/bootstrap-status), which is about a freshly provisioned install.
type onboardingStatus struct {
	// Subject is the caller's resolved owner key (echoed so the SPA can key its
	// onboarding state without a second whoami round-trip).
	Subject string `json:"subject"`
	// Role is the caller's role; only DevRel ever needs persona onboarding.
	Role string `json:"role"`
	// HasPersona is true if the caller owns at least one DevRelPersona (in any
	// phase — a persona exists but may still be ingesting/awaiting validation).
	HasPersona bool `json:"hasPersona"`
	// HasValidatedPersona is true if at least one owned persona carries the
	// Validated=True status condition (the owner reviewed+accepted its style
	// profile — the D1 contract's cheap declarative gate).
	HasValidatedPersona bool `json:"hasValidatedPersona"`
	// NeedsOnboarding drives the wizard: a DevRel with no validated persona yet.
	// Always false for an Admin (admins configure the platform, not personas).
	NeedsOnboarding bool `json:"needsOnboarding"`
}

// onboardingStatusHandler serves GET /v1/onboarding-status behind the auth
// Middleware. It reads the caller's own personas through the §6.3-gating store
// (so it never sees more than it is authorized to) and reports whether any is
// validated. An Admin is never "onboarding" even if the store returns every
// persona — the answer is scoped to the caller's OWN subject.
func (s *Server) onboardingStatusHandler(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	objs, err := s.store.List(r.Context(), sess, store.DevRelPersonaKind)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}

	var hasPersona, hasValidated bool
	for _, o := range objs {
		p, isPersona := o.(*v1alpha1.DevRelPersona)
		if !isPersona {
			continue
		}
		// Scope to the caller's OWN subject: an Admin's List returns every
		// persona (clause 1), but onboarding is a per-user question.
		if p.Spec.Owner.Subject != sess.Subject {
			continue
		}
		hasPersona = true
		if apimeta.IsStatusConditionTrue(p.Status.Conditions, v1alpha1.PersonaConditionValidated) {
			hasValidated = true
			break
		}
	}

	writeJSON(w, http.StatusOK, onboardingStatus{
		Subject:             sess.Subject,
		Role:                string(sess.Role),
		HasPersona:          hasPersona,
		HasValidatedPersona: hasValidated,
		NeedsOnboarding:     sess.Role == auth.RoleDevRel && !hasValidated,
	})
}
