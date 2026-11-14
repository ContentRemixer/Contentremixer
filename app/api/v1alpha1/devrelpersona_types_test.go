package v1alpha1

import (
	"reflect"
	"testing"
)

// TestDevRelPersonaSpecDeepCopyContentKinds guards that the regenerated
// deepcopy performs a true copy of the ContentKinds slice (ISI-7424): a deep
// copy must be equal in value yet backed by a distinct array, so mutating the
// copy never writes through to the original.
func TestDevRelPersonaSpecDeepCopyContentKinds(t *testing.T) {
	orig := DevRelPersonaSpec{
		Owner:              OwnerRef{Subject: "alice"},
		PrimaryContentKind: PrimaryContentBlog,
		ContentKinds:       []PrimaryContentKind{PrimaryContentBlog, PrimaryContentVideo},
	}

	cp := orig.DeepCopy()
	if !reflect.DeepEqual(orig, *cp) {
		t.Fatalf("deepcopy differs from original:\n orig=%+v\n copy=%+v", orig, *cp)
	}

	// Mutating the copy must not affect the original — proves the slice was
	// copied, not aliased.
	cp.ContentKinds[0] = PrimaryContentTalk
	if orig.ContentKinds[0] != PrimaryContentBlog {
		t.Fatalf("mutating copy wrote through to original: %v", orig.ContentKinds)
	}
}

// TestDevRelPersonaSpecDeepCopyNilContentKinds confirms a nil ContentKinds
// round-trips to nil (backward compatibility: personas predating multi-select).
func TestDevRelPersonaSpecDeepCopyNilContentKinds(t *testing.T) {
	orig := DevRelPersonaSpec{Owner: OwnerRef{Subject: "bob"}}
	cp := orig.DeepCopy()
	if cp.ContentKinds != nil {
		t.Fatalf("nil ContentKinds should stay nil, got %v", cp.ContentKinds)
	}
}
