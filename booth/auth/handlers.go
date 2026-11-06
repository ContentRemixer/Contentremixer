package auth

import (
	"encoding/json"
	"net/http"
)

// Handlers are the HTTP surfaces the Booth AuthN layer owns. They are mounted by
// the Booth API server (P3-1) rather than run as a separate binary, so the whole
// Booth is one service. Login/logout are unauthenticated; WhoAmI sits behind
// Middleware.
type Handlers struct {
	auth         Authenticator
	codec        *TokenCodec
	secureCookie bool
}

// Options tunes handler behaviour.
type Options struct {
	// SecureCookie sets the Secure flag on the session cookie. Keep true in any
	// TLS deployment (the Booth is served over TLS Ingress, §7); set false only
	// for local plaintext development.
	SecureCookie bool
}

// NewHandlers wires the login/logout/whoami handlers to auth and codec.
func NewHandlers(auth Authenticator, codec *TokenCodec, opts Options) *Handlers {
	return &Handlers{auth: auth, codec: codec, secureCookie: opts.SecureCookie}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login authenticates a credential and, on success, sets the session cookie and
// returns the resolved {subject, role}. Any failure is a generic 401.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sess, err := h.auth.Authenticate(req.Username, req.Password)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, err := h.codec.Mint(sess)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not issue session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(h.codec.TTL().Seconds()),
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, sess)
}

// Logout clears the session cookie. It is idempotent.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// WhoAmI returns the current session. It must be mounted behind Middleware.
func (h *Handlers) WhoAmI(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
