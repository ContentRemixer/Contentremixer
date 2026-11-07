package persona

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// CRPersonaProvider resolves a PersonaProfile from the live DevRelPersona CR.
// It is the production PersonaProvider: backed by the operator's cached
// controller-runtime reader, it fetches the persona by name within Namespace
// and enforces the §8 tenancy invariant — a run's owner MUST match the CR's
// spec.owner.subject, or conditioning is refused (never serve another owner's
// voice).
type CRPersonaProvider struct {
	// Reader is the cached controller-runtime client. Required.
	Reader client.Reader
	// Namespace is where DevRelPersona CRs live (the operator's watched
	// namespace). Required — the run context carries the persona name, not its
	// namespace (§5.2 attribution is name + owner).
	Namespace string
}

// CRPersonaProvider is a PersonaProvider.
var _ PersonaProvider = (*CRPersonaProvider)(nil)

// Lookup fetches the persona and projects it to a PersonaProfile. It errors
// when the CR is missing/unreadable or when its owner does not match the run's
// owner (cross-owner refusal); the Conditioner turns that error into a
// style-only degrade, so a bad attribution never blocks a run.
func (p *CRPersonaProvider) Lookup(ctx context.Context, ownerSubject, personaName string) (PersonaProfile, error) {
	if p == nil || p.Reader == nil {
		return PersonaProfile{}, fmt.Errorf("persona: CR provider has no reader")
	}
	if strings.TrimSpace(p.Namespace) == "" {
		return PersonaProfile{}, fmt.Errorf("persona: CR provider has no namespace")
	}

	var cr contentremixerv1alpha1.DevRelPersona
	if err := p.Reader.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: personaName}, &cr); err != nil {
		return PersonaProfile{}, fmt.Errorf("get devrelpersona %s/%s: %w", p.Namespace, personaName, err)
	}

	// Tenancy guard (§8.1): the run owner must own the persona. This mirrors the
	// VectorStore owner predicate one layer up — a mismatch is refused before
	// any retrieval, so a wrong (owner, persona) pairing can never leak voice.
	if got := strings.TrimSpace(cr.Spec.Owner.Subject); got != ownerSubject {
		return PersonaProfile{}, fmt.Errorf("persona %q owner %q does not match run owner %q (refusing cross-owner conditioning)", personaName, got, ownerSubject)
	}

	return PersonaProfile{
		SelfDescription: cr.Spec.SelfDescription,
		TopK:            cr.Spec.Retrieval.TopK,
		MinScore:        parseMinScore(cr.Spec.Retrieval.MinScore),
		Ready:           personaReady(&cr),
	}, nil
}

// parseMinScore parses the CRD's decimal-string MinScore. Empty or malformed =>
// 0 (keep all top-k): conditioning is best-effort, so a bad value degrades to
// "no floor" rather than failing the run.
func parseMinScore(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// personaReady reports whether the persona's style index is queryable: the P2-C
// Ready condition is True, or (fallback) the phase is Ready. Either signal means
// exemplars have been embedded and the owner-scoped query will not read a
// stale/absent index (§5.4).
func personaReady(cr *contentremixerv1alpha1.DevRelPersona) bool {
	if meta.IsStatusConditionTrue(cr.Status.Conditions, contentremixerv1alpha1.PersonaConditionReady) {
		return true
	}
	return cr.Status.Phase == contentremixerv1alpha1.PersonaPhaseReady
}
