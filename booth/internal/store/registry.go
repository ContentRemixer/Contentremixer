package store

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// ResourceKind describes one CR class the Booth API exposes, in the uniform
// terms the authz-gating store needs: how to construct the typed object and its
// list, how to read its owner subject, how to enumerate a list, and whether the
// class is admin-only (design §6.3: ModelProfile and ReferenceRepo are
// admin-only; owner-scoped classes such as the Phase-2 DevRelPersona are not).
//
// Adding DevRelPersona in Phase 2 is a single entry here — the store, authz,
// audit, and server code are generic over this table and do not change.
type ResourceKind struct {
	// Kind is the CR kind name, used in audit records and routing.
	Kind string
	// AdminOnly marks a class that only Admins may touch (clause-2 never admits).
	AdminOnly bool

	newObject func() client.Object
	newList   func() client.ObjectList
	ownerOf   func(client.Object) string
	items     func(client.ObjectList) []client.Object
}

// NewObject returns a fresh, empty typed object for this kind.
func (r ResourceKind) NewObject() client.Object { return r.newObject() }

// NewList returns a fresh, empty typed list for this kind.
func (r ResourceKind) NewList() client.ObjectList { return r.newList() }

// OwnerSubject reads spec.owner.subject from a typed object of this kind.
func (r ResourceKind) OwnerSubject(o client.Object) string { return r.ownerOf(o) }

// Items enumerates the elements of a typed list of this kind.
func (r ResourceKind) Items(l client.ObjectList) []client.Object { return r.items(l) }

// ModelProfileKind — admin-only platform config (design §2.1, §6.1).
var ModelProfileKind = ResourceKind{
	Kind:      "ModelProfile",
	AdminOnly: true,
	newObject: func() client.Object { return &v1alpha1.ModelProfile{} },
	newList:   func() client.ObjectList { return &v1alpha1.ModelProfileList{} },
	ownerOf: func(o client.Object) string {
		return o.(*v1alpha1.ModelProfile).Spec.Owner.Subject
	},
	items: func(l client.ObjectList) []client.Object {
		src := l.(*v1alpha1.ModelProfileList).Items
		out := make([]client.Object, len(src))
		for i := range src {
			out[i] = &src[i]
		}
		return out
	},
}

// DevRelPersonaKind — owner-scoped style config (Phase 2, ISI-7143). NOT
// admin-only: a DevRel may create/list/mutate their OWN personas (clause 2 of
// §6.3 admits on owner match); the validating webhook enforces a non-empty
// owner so no persona is ever platform-owned. Admin still sees all via clause 1.
var DevRelPersonaKind = ResourceKind{
	Kind:      "DevRelPersona",
	AdminOnly: false,
	newObject: func() client.Object { return &v1alpha1.DevRelPersona{} },
	newList:   func() client.ObjectList { return &v1alpha1.DevRelPersonaList{} },
	ownerOf: func(o client.Object) string {
		return o.(*v1alpha1.DevRelPersona).Spec.Owner.Subject
	},
	items: func(l client.ObjectList) []client.Object {
		src := l.(*v1alpha1.DevRelPersonaList).Items
		out := make([]client.Object, len(src))
		for i := range src {
			out[i] = &src[i]
		}
		return out
	},
}

// ReferenceRepoKind — admin-only platform config (design §2.2, §6.1).
var ReferenceRepoKind = ResourceKind{
	Kind:      "ReferenceRepo",
	AdminOnly: true,
	newObject: func() client.Object { return &v1alpha1.ReferenceRepo{} },
	newList:   func() client.ObjectList { return &v1alpha1.ReferenceRepoList{} },
	ownerOf: func(o client.Object) string {
		return o.(*v1alpha1.ReferenceRepo).Spec.Owner.Subject
	},
	items: func(l client.ObjectList) []client.Object {
		src := l.(*v1alpha1.ReferenceRepoList).Items
		out := make([]client.Object, len(src))
		for i := range src {
			out[i] = &src[i]
		}
		return out
	},
}
