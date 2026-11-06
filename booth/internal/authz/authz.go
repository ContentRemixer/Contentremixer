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
// This package has no knowledge of HTTP, k8s, or any concrete CR type. It is a
// pure decision function so the invariant is auditable in one place and covered
// by the table-driven matrix in authz_test.go.
package authz

// Roles recognized by the Booth API (design §6.1).
const (
	RoleAdmin  = "Admin"
	RoleDevRel = "DevRel"
)

// Session is the authenticated caller identity, resolved by the auth middleware
// (design §6.4). The authz predicate consumes only {Subject, Role}; it is blind
// to how they were obtained (bcrypt now, OIDC later).
type Session struct {
	// Subject is the IdP subject (OIDC `sub`) of the caller, or the built-in
	// user's stable id in the bcrypt Phase-1 fallback.
	Subject string
	// Role is RoleAdmin or RoleDevRel.
	Role string
}

// IsAdmin reports whether the session holds the Admin role.
func (s Session) IsAdmin() bool { return s.Role == RoleAdmin }

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
