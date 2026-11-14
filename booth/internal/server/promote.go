package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ContentRemixer/Contentremixer/booth/internal/promote"
)

// mountPromote wires the promotion-suggestion route (ISI-7369, plan §5 O8b/P4).
// It is behind the auth Middleware and reaches the domain ONLY through the
// §6.3-gating promote.Store. A nil store (Booth built without the review DSN or a
// LiteLLM gateway) leaves the route unmounted.
func (s *Server) mountPromote(mux *http.ServeMux) {
	if s.promote == nil {
		return
	}
	mux.Handle("POST /v1/promote", s.protect(s.suggestPromotion))
}

func (s *Server) suggestPromotion(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req promote.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := s.promote.Suggest(r.Context(), sess, req)
	if err != nil {
		s.writePromoteErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// writePromoteErr maps the promote gate's sentinels to HTTP codes: ErrForbidden
// → 403, ErrNotFound → 404, ErrNotValidated → 409, ErrRateLimited → 429,
// ErrBadRequest → 400, anything else → 500 (logged — an LLM/DB fault, not a
// caller error).
func (s *Server) writePromoteErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, promote.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, promote.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case promote.IsNotValidated(err):
		writeError(w, http.StatusConflict, err.Error())
	case promote.IsRateLimited(err):
		writeError(w, http.StatusTooManyRequests, "promote rate limit exceeded; try again shortly")
	case promote.IsBadRequest(err):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.log.Error("booth promote error", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
