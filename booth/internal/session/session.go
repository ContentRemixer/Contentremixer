// Package session defines the Booth auth boundary: the Authenticator interface
// that resolves {subject, role} from a request, and the request-context plumbing
// handlers use to read the authenticated Session.
//
// Per design §6.4 and ISI-7149, the session mechanism is decoupled from the
// predicate: this interface is the seam. Phase 1 ships a bcrypt built-in user
// table (board decision #8a); a future OIDC authenticator implements the same
// interface with no change to authz or the store.
package session

import (
	"context"
	"errors"
	"net/http"

	"github.com/ContentRemixer/Contentremixer/booth/internal/authz"
)

// ErrUnauthenticated is returned by an Authenticator when the request carries no
// valid credential. The server maps it to HTTP 401.
var ErrUnauthenticated = errors.New("unauthenticated")

// Session is re-exported from authz so callers depend on one Session type.
type Session = authz.Session

// Authenticator resolves an authenticated Session from an HTTP request. It is
// the ONLY coupling point to the login mechanism; implementations must not leak
// their mechanism to the rest of the Booth API.
type Authenticator interface {
	Authenticate(r *http.Request) (Session, error)
}

type contextKey struct{}

// WithSession returns a copy of ctx carrying the authenticated session.
func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, contextKey{}, s)
}

// FromContext returns the session stored by the auth middleware. ok is false if
// no session is present — which must be treated as "deny", never "allow".
func FromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(contextKey{}).(Session)
	return s, ok
}
