package server

import (
	"encoding/json"
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
)

// persona builds a DevRelPersona owned by ownerSubject. When validated is true it
// carries the Validated=True status condition (the D1 contract's declarative
// gate); otherwise no Validated condition is present.
func persona(name, ownerSubject string, validated bool) *v1alpha1.DevRelPersona {
	p := &v1alpha1.DevRelPersona{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       v1alpha1.DevRelPersonaSpec{Owner: v1alpha1.OwnerRef{Subject: ownerSubject}},
	}
	if validated {
		p.Status.Conditions = []metav1.Condition{{
			Type:               v1alpha1.PersonaConditionValidated,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.PersonaReasonProfileValidated,
			LastTransitionTime: metav1.Now(),
		}}
	}
	return p
}

// TestDevRelPersonaOwnerStampedOnCreate — a DevRel POSTing a persona with no
// owner gets it bound to THEIR subject (self-service), and a supplied foreign
// owner is overridden to self (spoof-proof), so the create always succeeds owned
// by the caller rather than 403'ing on an owner mismatch.
func TestDevRelPersonaOwnerStampedOnCreate(t *testing.T) {
	h, codec := newTestServer(t)
	devTok := tokenFor(t, codec, "sub-dana", auth.RoleDevRel)

	// No owner in the body — server stamps the caller's subject.
	w := do(t, h, "POST", "/v1/devrelpersonas", devTok, `{"metadata":{"name":"p1"},"spec":{}}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}
	var got v1alpha1.DevRelPersona
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Spec.Owner.Subject != "sub-dana" {
		t.Fatalf("owner = %q, want sub-dana", got.Spec.Owner.Subject)
	}

	// An explicitly-supplied foreign owner is NOT silently re-homed — it runs the
	// §6.3 gate and is denied (the stamping only fills an omitted owner).
	w = do(t, h, "POST", "/v1/devrelpersonas", devTok,
		`{"metadata":{"name":"p2"},"spec":{"owner":{"subject":"sub-victim"}}}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("explicit foreign owner = %d, want 403 (body=%s)", w.Code, w.Body.String())
	}
}

// TestAdminPersonaOwnerHonored — an Admin may create a persona on a DevRel's
// behalf (explicit owner is honored, not overridden).
func TestAdminPersonaOwnerHonored(t *testing.T) {
	h, codec := newTestServer(t)
	adminTok := tokenFor(t, codec, "sub-admin", auth.RoleAdmin)

	w := do(t, h, "POST", "/v1/devrelpersonas", adminTok,
		`{"metadata":{"name":"p3"},"spec":{"owner":{"subject":"sub-erin"}}}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("admin create = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}
	var got v1alpha1.DevRelPersona
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Spec.Owner.Subject != "sub-erin" {
		t.Fatalf("admin-set owner = %q, want sub-erin (honored)", got.Spec.Owner.Subject)
	}
}

func getOnboarding(t *testing.T, h http.Handler, tok string) onboardingStatus {
	t.Helper()
	w := do(t, h, "GET", "/v1/onboarding-status", tok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("onboarding-status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var st onboardingStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return st
}

// TestOnboardingStatusFirstRun — a DevRel with no persona needs onboarding; once
// they own a validated persona they no longer do; an un-validated persona is not
// enough.
func TestOnboardingStatusFirstRun(t *testing.T) {
	// No personas at all.
	h, codec := newTestServer(t)
	devTok := tokenFor(t, codec, "sub-dana", auth.RoleDevRel)
	if st := getOnboarding(t, h, devTok); !st.NeedsOnboarding || st.HasPersona || st.HasValidatedPersona {
		t.Fatalf("empty: %+v, want NeedsOnboarding and no persona", st)
	}

	// A persona exists but is not yet validated → still onboarding.
	h, codec = newTestServer(t, persona("p1", "sub-dana", false))
	devTok = tokenFor(t, codec, "sub-dana", auth.RoleDevRel)
	if st := getOnboarding(t, h, devTok); !st.NeedsOnboarding || !st.HasPersona || st.HasValidatedPersona {
		t.Fatalf("unvalidated: %+v, want NeedsOnboarding with persona, not validated", st)
	}

	// A validated persona → onboarding complete.
	h, codec = newTestServer(t, persona("p1", "sub-dana", true))
	devTok = tokenFor(t, codec, "sub-dana", auth.RoleDevRel)
	if st := getOnboarding(t, h, devTok); st.NeedsOnboarding || !st.HasValidatedPersona {
		t.Fatalf("validated: %+v, want not onboarding with validated persona", st)
	}
}

// TestOnboardingStatusScopedToSubject — another DevRel's validated persona does
// not satisfy MY onboarding, and an Admin never needs onboarding.
func TestOnboardingStatusScopedToSubject(t *testing.T) {
	h, codec := newTestServer(t, persona("p1", "sub-other", true))

	// sub-dana owns nothing; sub-other's validated persona is invisible to the
	// scope and must not count.
	devTok := tokenFor(t, codec, "sub-dana", auth.RoleDevRel)
	if st := getOnboarding(t, h, devTok); !st.NeedsOnboarding || st.HasPersona {
		t.Fatalf("cross-user leak: %+v, want NeedsOnboarding with no persona", st)
	}

	// Admin never onboards, even though List returns every persona (clause 1).
	adminTok := tokenFor(t, codec, "sub-admin", auth.RoleAdmin)
	if st := getOnboarding(t, h, adminTok); st.NeedsOnboarding {
		t.Fatalf("admin: %+v, want NeedsOnboarding=false", st)
	}
}
