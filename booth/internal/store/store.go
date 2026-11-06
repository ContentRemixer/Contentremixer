// Package store is the authz-gating single front door to the CRs. Every method
// takes the caller's Session and evaluates the frozen §6.3 predicate (via the
// authz package) and writes an audit record (§6.4) BEFORE and only if it
// touches the Kubernetes API. HTTP handlers reach CRs ONLY through this type —
// which is how the ISI-7149 invariant ("no code path reads or writes a CR
// without evaluating §6.3") is a structural guarantee, not a convention.
package store

import (
	"context"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/authz"
)

// Verbs recorded in audit entries.
const (
	verbGet     = "get"
	verbList    = "list"
	verbCreate  = "create"
	verbUpdate  = "update"
	verbDelete  = "delete"
	verbApprove = "approve"
)

// ErrForbidden means the §6.3 predicate denied the caller. Handlers map it to
// HTTP 403.
var ErrForbidden = errors.New("forbidden")

// ErrNotFound means the named resource does not exist. Handlers map it to 404.
// Note: Get on a resource the caller is not authorized for returns ErrForbidden,
// not ErrNotFound — we only reveal (non-)existence after the predicate admits.
var ErrNotFound = errors.New("not found")

// Store gates all CR access behind the §6.3 predicate and the audit log.
type Store struct {
	client    client.Client
	namespace string
	auditor   audit.Logger
}

// New builds a Store over a controller-runtime client scoped to one namespace.
func New(c client.Client, namespace string, auditor audit.Logger) *Store {
	return &Store{client: c, namespace: namespace, auditor: auditor}
}

func (s *Store) audit(ctx context.Context, sess auth.Session, verb, kind, name string, allowed bool) {
	decision := audit.DecisionDeny
	if allowed {
		decision = audit.DecisionAllow
	}
	s.auditor.Log(ctx, audit.Entry{
		Subject:      sess.Subject,
		Role:         string(sess.Role),
		Verb:         verb,
		ResourceKind: kind,
		ResourceName: name,
		Decision:     decision,
	})
}

// authorize applies the frozen §6.3 predicate to a session + resource owner.
func authorize(sess auth.Session, adminOnly bool, ownerSubject string) bool {
	return authz.Authorize(string(sess.Role), sess.Subject, adminOnly, ownerSubject)
}

// List returns the resources of kind the caller is authorized to see, filtered
// server-side by §6.3 (never client-side hiding). For admin-only kinds a
// non-admin receives an empty set (and a deny audit record); an Admin receives
// all rows.
func (s *Store) List(ctx context.Context, sess auth.Session, kind ResourceKind) ([]client.Object, error) {
	list := kind.NewList()
	if err := s.client.List(ctx, list, client.InNamespace(s.namespace)); err != nil {
		s.audit(ctx, sess, verbList, kind.Kind, "", false)
		return nil, err
	}

	var out []client.Object
	for _, obj := range kind.Items(list) {
		if authorize(sess, kind.AdminOnly, kind.OwnerSubject(obj)) {
			out = append(out, obj)
		}
	}
	// A list is "allowed" for audit purposes if the predicate admits the caller
	// to the class at all; an all-filtered empty result for a non-admin on an
	// admin-only class is itself a denial worth recording.
	allowed := sess.Role == auth.RoleAdmin || (!kind.AdminOnly)
	s.audit(ctx, sess, verbList, kind.Kind, "", allowed)
	return out, nil
}

// get fetches one object and evaluates §6.3. It is shared by Get/Update/Delete
// so the predicate is applied identically on the current server-side state.
func (s *Store) get(ctx context.Context, kind ResourceKind, name string) (client.Object, error) {
	obj := kind.NewObject()
	key := client.ObjectKey{Namespace: s.namespace, Name: name}
	if err := s.client.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return obj, nil
}

// Get returns one resource if the caller is authorized, else ErrForbidden.
func (s *Store) Get(ctx context.Context, sess auth.Session, kind ResourceKind, name string) (client.Object, error) {
	obj, err := s.get(ctx, kind, name)
	if err != nil {
		// Existence is not revealed before the predicate; but a NotFound here
		// carries no owner to leak, so returning it is safe and more useful.
		return nil, err
	}
	if !authorize(sess, kind.AdminOnly, kind.OwnerSubject(obj)) {
		s.audit(ctx, sess, verbGet, kind.Kind, name, false)
		return nil, ErrForbidden
	}
	s.audit(ctx, sess, verbGet, kind.Kind, name, true)
	return obj, nil
}

// Create admits a new resource only if the caller is authorized for it under
// §6.3 evaluated against the OBJECT'S OWN declared owner. A DevRel therefore
// cannot create an admin-only kind, nor a resource owned by someone else, nor a
// platform-owned (empty-owner) resource. Admins may create anything.
func (s *Store) Create(ctx context.Context, sess auth.Session, kind ResourceKind, obj client.Object) error {
	obj.SetNamespace(s.namespace)
	if !authorize(sess, kind.AdminOnly, kind.OwnerSubject(obj)) {
		s.audit(ctx, sess, verbCreate, kind.Kind, obj.GetName(), false)
		return ErrForbidden
	}
	if err := s.client.Create(ctx, obj); err != nil {
		s.audit(ctx, sess, verbCreate, kind.Kind, obj.GetName(), false)
		return err
	}
	s.audit(ctx, sess, verbCreate, kind.Kind, obj.GetName(), true)
	return nil
}

// Update admits a mutation only if the caller is authorized for BOTH the
// resource's current server-side owner and the incoming owner — so a caller may
// neither edit a resource they cannot see nor re-home it to an owner they are
// not authorized for. The incoming object's resourceVersion is preserved from
// the server copy to keep the update a safe read-modify-write.
func (s *Store) Update(ctx context.Context, sess auth.Session, kind ResourceKind, obj client.Object) error {
	name := obj.GetName()
	current, err := s.get(ctx, kind, name)
	if err != nil {
		return err
	}
	okCurrent := authorize(sess, kind.AdminOnly, kind.OwnerSubject(current))
	okIncoming := authorize(sess, kind.AdminOnly, kind.OwnerSubject(obj))
	if !okCurrent || !okIncoming {
		s.audit(ctx, sess, verbUpdate, kind.Kind, name, false)
		return ErrForbidden
	}
	obj.SetNamespace(s.namespace)
	obj.SetResourceVersion(current.GetResourceVersion())
	if err := s.client.Update(ctx, obj); err != nil {
		s.audit(ctx, sess, verbUpdate, kind.Kind, name, false)
		return err
	}
	s.audit(ctx, sess, verbUpdate, kind.Kind, name, true)
	return nil
}

// Approve gates an approve action (design §6.1: "approve only their own PRs")
// through the same §6.3 predicate as any other write, against the current
// server-side owner, and records a verbApprove audit entry. Phase 1 has no PR
// or run CR yet, so the method is generic over any registered kind; when the
// run/PR views land (Phase 2/3) they reuse this gate unchanged — the point of
// the frozen invariant is that approve is never a special-cased bypass.
func (s *Store) Approve(ctx context.Context, sess auth.Session, kind ResourceKind, name string) error {
	current, err := s.get(ctx, kind, name)
	if err != nil {
		return err
	}
	if !authorize(sess, kind.AdminOnly, kind.OwnerSubject(current)) {
		s.audit(ctx, sess, verbApprove, kind.Kind, name, false)
		return ErrForbidden
	}
	s.audit(ctx, sess, verbApprove, kind.Kind, name, true)
	return nil
}

// Delete removes one resource if the caller is authorized under §6.3 against
// the current server-side state.
func (s *Store) Delete(ctx context.Context, sess auth.Session, kind ResourceKind, name string) error {
	current, err := s.get(ctx, kind, name)
	if err != nil {
		return err
	}
	if !authorize(sess, kind.AdminOnly, kind.OwnerSubject(current)) {
		s.audit(ctx, sess, verbDelete, kind.Kind, name, false)
		return ErrForbidden
	}
	if err := s.client.Delete(ctx, current); err != nil {
		s.audit(ctx, sess, verbDelete, kind.Kind, name, false)
		return err
	}
	s.audit(ctx, sess, verbDelete, kind.Kind, name, true)
	return nil
}
