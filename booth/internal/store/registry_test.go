package store

import (
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// TestOwnerStamping covers the owner-stamp seam (ISI-7360 D5): DevRelPersona is
// owner-stampable and round-trips a subject through SetOwnerSubject/OwnerSubject,
// while the admin-only platform-config kinds are deliberately NOT stampable (their
// empty owner is the platform-owned signal, never overwritten by the create path).
func TestOwnerStamping(t *testing.T) {
	if !DevRelPersonaKind.OwnerStampable() {
		t.Fatal("DevRelPersona should be owner-stampable")
	}
	for _, k := range []ResourceKind{ModelProfileKind, ReferenceRepoKind} {
		if k.OwnerStampable() {
			t.Fatalf("%s must not be owner-stampable (admin-only platform config)", k.Kind)
		}
	}

	obj := DevRelPersonaKind.NewObject()
	if got := DevRelPersonaKind.OwnerSubject(obj); got != "" {
		t.Fatalf("fresh persona owner = %q, want empty", got)
	}
	DevRelPersonaKind.SetOwnerSubject(obj, "booth:local:dana")
	if got := DevRelPersonaKind.OwnerSubject(obj); got != "booth:local:dana" {
		t.Fatalf("stamped owner = %q, want booth:local:dana", got)
	}
	// The stamp lands on the typed spec, not just the accessor.
	if got := obj.(*v1alpha1.DevRelPersona).Spec.Owner.Subject; got != "booth:local:dana" {
		t.Fatalf("spec.owner.subject = %q, want booth:local:dana", got)
	}
}
