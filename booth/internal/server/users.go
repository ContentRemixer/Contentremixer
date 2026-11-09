package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
)

// minPasswordLen is the floor for an admin-created password. bcrypt silently
// ignores bytes past 72, so that is the hard ceiling (§8). A homelab Booth does
// not need a policy engine here — one length check keeps weak/empty passwords
// out without pretending to be more.
const (
	minPasswordLen = 8
	maxPasswordLen = 72
)

// userResponse is the wire projection of a user. It deliberately OMITS the
// bcrypt hash — a hash is never serialised over the API (§8). Every users route
// returns this shape, never auth.User.
type userResponse struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

func toUserResponse(u auth.User) userResponse {
	return userResponse{Username: u.Username, Role: string(u.Role)}
}

// mountUsers wires the Admin-only Users & Roles CRUD surface (plan §3.2 A1/Admin,
// §6.3 clause 1). Every route is behind the auth Middleware AND an explicit
// Admin-role gate — DevRel gets 403 regardless of ownership, since users are
// platform-level config with no per-row owner.
func (s *Server) mountUsers(mux *http.ServeMux) {
	mux.Handle("GET /v1/users", s.protect(s.listUsers))
	mux.Handle("POST /v1/users", s.protect(s.createUser))
	mux.Handle("PATCH /v1/users/{username}", s.protect(s.updateUserRole))
	mux.Handle("DELETE /v1/users/{username}", s.protect(s.deleteUser))
}

// requireAdmin resolves the session and enforces the Admin role. It writes the
// appropriate error (401/403) and returns ok=false when the caller is not an
// authenticated Admin, so handlers can `if !ok { return }`.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (auth.Session, bool) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return auth.Session{}, false
	}
	if sess.Role != auth.RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return auth.Session{}, false
	}
	return sess, true
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	users, err := s.auth.Store.List()
	if err != nil {
		s.writeUserErr(w, err)
		return
	}
	out := make([]userResponse, 0, len(users))
	for _, u := range users {
		out = append(out, toUserResponse(u))
	}
	writeJSON(w, http.StatusOK, out)
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var req createUserRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	if !auth.Role(req.Role).Valid() {
		writeError(w, http.StatusBadRequest, "role must be Admin or DevRel")
		return
	}
	if len(req.Password) < minPasswordLen || len(req.Password) > maxPasswordLen {
		writeError(w, http.StatusBadRequest, "password must be 8–72 characters")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.writeUserErr(w, err)
		return
	}
	if err := s.auth.Store.Create(auth.User{
		Username:   req.Username,
		BcryptHash: hash,
		Role:       auth.Role(req.Role),
	}); err != nil {
		s.writeUserErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, userResponse{Username: req.Username, Role: req.Role})
}

type updateRoleRequest struct {
	Role string `json:"role"`
}

func (s *Server) updateUserRole(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var req updateRoleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !auth.Role(req.Role).Valid() {
		writeError(w, http.StatusBadRequest, "role must be Admin or DevRel")
		return
	}
	username := r.PathValue("username")
	if err := s.auth.Store.SetRole(username, auth.Role(req.Role)); err != nil {
		s.writeUserErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userResponse{Username: username, Role: req.Role})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := s.auth.Store.Delete(r.PathValue("username")); err != nil {
		s.writeUserErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeUserErr maps auth store sentinels to HTTP codes: ErrUserNotFound → 404,
// ErrUserExists → 409, ErrLastAdmin → 409 (a conflict with the always-one-Admin
// invariant), anything else → 500.
func (s *Server) writeUserErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, auth.ErrUserExists):
		writeError(w, http.StatusConflict, "user already exists")
	case errors.Is(err, auth.ErrLastAdmin):
		writeError(w, http.StatusConflict, "cannot remove or demote the last admin")
	default:
		s.log.Error("booth user store error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
