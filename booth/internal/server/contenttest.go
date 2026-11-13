package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ContentRemixer/Contentremixer/booth/internal/contenttest"
)

// mountContentTest wires the interactive content-test + persona-validate routes
// (ISI-7359, plan §5). Both are behind the auth Middleware and reach the domain
// ONLY through the §6.3-gating contenttest.Store. A nil store (Booth built
// without the review DSN or a LiteLLM gateway) leaves the routes unmounted.
func (s *Server) mountContentTest(mux *http.ServeMux) {
	if s.contentTest == nil {
		return
	}
	mux.Handle("POST /v1/content-test", s.protect(s.runContentTest))
	mux.Handle("GET /v1/devrelpersonas/{name}/style-profile", s.protect(s.getStyleProfile))
	mux.Handle("POST /v1/devrelpersonas/{name}/validate", s.protect(s.validatePersona))
}

func (s *Server) getStyleProfile(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	sp, err := s.contentTest.GetProfile(r.Context(), sess, r.PathValue("name"))
	if err != nil {
		s.writeContentTestErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sp)
}

func (s *Server) runContentTest(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req contenttest.TestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := s.contentTest.RunTest(r.Context(), sess, req)
	if err != nil {
		s.writeContentTestErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) validatePersona(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	sp, err := s.contentTest.Validate(r.Context(), sess, r.PathValue("name"))
	if err != nil {
		s.writeContentTestErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sp)
}

// writeContentTestErr maps the content-test gate's sentinels to HTTP codes:
// ErrForbidden → 403, ErrNotFound → 404, ErrBadRequest → 400, ErrRateLimited →
// 429, anything else → 500 (logged — an LLM/DB fault, not a caller error).
func (s *Server) writeContentTestErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, contenttest.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, contenttest.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case contenttest.IsRateLimited(err):
		writeError(w, http.StatusTooManyRequests, "content-test rate limit exceeded; try again shortly")
	case contenttest.IsBadRequest(err):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.log.Error("booth content-test error", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
