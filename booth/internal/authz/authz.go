// Package authz implements the frozen design §6.3 authorization predicate that
// every Booth API read and write MUST evaluate before touching a CR.
//
// The predicate (design §6.3, frozen):
//
//	authorized == (session.role == "Admin") OR (resource.owner.subject == session.subject)
//
// with two refinements spelled out in §6.3:
//
//   - Admin-only resource classes (ModelProfile, ReferenceRepo) admit only via
//     clause 1 — a DevRel is denied regardless of ownership.
//   - Empty-owner (platform-owned) resources are Admin-only: clause 2 cannot
//     match an empty subject against a real session subject.
//
// This package is a pure decision function. It has no knowledge of HTTP, k8s,
// or the auth mechanism — the {subject, role} session is supplied by the
// booth/auth provider and passed in as plain strings — so the invariant is
// auditable in one place and covered by the table-driven matrix in authz_test.go.
package authz

// Role names recognized by the Booth API (design §6.1). These mirror the
// booth/auth role vocabulary; keeping a local copy keeps this predicate free of
// any import so it stays a pure, dependency-light decision function.
const (
	RoleAdmin  = "Admin"
	RoleDevRel = "DevRel"
)

// Authorize evaluates the frozen §6.3 predicate for one target resource.
//
//	role         — the caller's role (session.role)
//	subject      — the caller's subject (session.subject)
//	adminOnly    — true for admin-only resource classes (ModelProfile, ReferenceRepo)
//	ownerSubject — the resource's spec.owner.subject ("" == platform/Admin-owned)
//
// Returns true iff the caller is authorized for the resource.
func Authorize(role, subject string, adminOnly bool, ownerSubject string) bool {
	// Clause 1: Admin always admitted.
	if role == RoleAdmin {
		return true
	}
	// Admin-only resource classes have no second way in for non-admins.
	if adminOnly {
		return false
	}
	// Clause 2: ownership match. Both sides must be non-empty so that neither a
	// platform-owned resource (empty owner) nor an unauthenticated/empty subject
	// can match. This is the §6.3 "empty subject can't match a real sub" rule,
	// applied symmetrically.
	return subject != "" && ownerSubject != "" && subject == ownerSubject
}
