package auth

import "context"

// Role is the platform RBAC role (design §6.1). The vocabulary is shared with
// the Booth API; the authz predicate (§6.3) lives in P3-1, not here.
type Role string

const (
	// RoleAdmin has full platform configuration access (§6.1). The seed Admin
	// bootstrapped from the k8s Secret (§8) is the sole initial Admin
	// (board decision #8b).
	RoleAdmin Role = "Admin"
	// RoleDevRel is scoped to self (§6.1); created in Phase 2+ once personas exist.
	RoleDevRel Role = "DevRel"
)

// Valid reports whether r is a role the platform recognises.
func (r Role) Valid() bool {
	return r == RoleAdmin || r == RoleDevRel
}

// Session is the frozen contract P3-1 consumes. It carries the authenticated
// subject (the user's stable owner key — booth:local:<username> until OIDC, see
// auth.User.Subject) and role; the Booth API evaluates §6.3 against it.
type Session struct {
	Subject string `json:"sub"`
	Role    Role   `json:"role"`
}

type contextKey struct{}

var sessionKey contextKey

// WithSession returns a copy of ctx carrying s.
func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, sessionKey, s)
}

// SessionFromContext returns the Session injected by Middleware, if any. Booth
// API handlers call this, then apply the §6.3 predicate before touching the
// k8s API.
func SessionFromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(sessionKey).(Session)
	return s, ok
}
