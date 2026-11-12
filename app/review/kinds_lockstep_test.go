package review

import (
	"sort"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// TestKnownSourceKindsMatchCRD mechanically enforces the lock-step between this
// package's KnownSourceKinds and the CRD contract v1alpha1.KnownReferenceKinds.
// The two lists are string-duplicated (this public package stays free of the
// k8s API types) so a reference kind added to one but not the other would let a
// kind admit against the CRD yet fail enqueue validation here (or vice versa).
// This test is that guard — a comment is not. The import of v1alpha1 is test-
// only and does not become a build dependency of the Booth module.
func TestKnownSourceKindsMatchCRD(t *testing.T) {
	got := append([]string(nil), KnownSourceKinds...)
	want := append([]string(nil), v1alpha1.KnownReferenceKinds...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("KnownSourceKinds %v != v1alpha1.KnownReferenceKinds %v (length)", KnownSourceKinds, v1alpha1.KnownReferenceKinds)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("KnownSourceKinds %v != v1alpha1.KnownReferenceKinds %v (element %d: %q vs %q)",
				KnownSourceKinds, v1alpha1.KnownReferenceKinds, i, got[i], want[i])
		}
	}
}
