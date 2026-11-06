package auth

import (
	"net/http"
	"strings"
)

// SessionCookieName is the cookie the Booth sets on login and reads on every
// request.
const SessionCookieName = "booth_session"

// Middleware resolves a Session from the request (cookie first, then an
// Authorization: Bearer token), verifies it, and injects it into the request
// context. Requests without a valid session are rejected with 401 before they
// reach a handler — so no Booth API code path runs unauthenticated.
type Middleware struct {
	codec *TokenCodec
}

// NewMiddleware returns a Middleware that verifies tokens with codec.
func NewMiddleware(codec *TokenCodec) *Middleware {
	return &Middleware{codec: codec}
}

// Wrap returns a handler that enforces authentication in front of next.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := tokenFromRequest(r)
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		sess, err := m.codec.Verify(token)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithSession(r.Context(), sess)))
	})
}

// tokenFromRequest pulls the raw token from the session cookie, falling back to
// an "Authorization: Bearer <token>" header (for non-browser API clients).
func tokenFromRequest(r *http.Request) (string, bool) {
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	if h := r.Header.Get("Authorization"); h != "" {
		if token, ok := strings.CutPrefix(h, "Bearer "); ok && token != "" {
			return token, true
		}
	}
	return "", false
}
