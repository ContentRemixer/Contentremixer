package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	appcontent "github.com/ContentRemixer/Contentremixer/app/content"
	"github.com/ContentRemixer/Contentremixer/booth/internal/content"
)

// mountContent wires the content-collaboration routes (plan §3.2 B3). All are
// behind the auth Middleware; every handler reaches the domain ONLY through
// the §6.3-gating content.Store. A nil store (a server built without
// Postgres) leaves the routes unmounted rather than panicking.
func (s *Server) mountContent(mux *http.ServeMux) {
	if s.content == nil {
		return
	}
	mux.Handle("GET /v1/content", s.protect(s.listContents))
	mux.Handle("POST /v1/content", s.protect(s.createContent))
	mux.Handle("GET /v1/content/{id}", s.protect(s.getContent))
	mux.Handle("PUT /v1/content/{id}", s.protect(s.updateContent))
	mux.Handle("DELETE /v1/content/{id}", s.protect(s.deleteContent))
	mux.Handle("GET /v1/content/{id}/comments", s.protect(s.listComments))
	mux.Handle("POST /v1/content/{id}/comments", s.protect(s.createComment))
	mux.Handle("POST /v1/content/{id}/comments/{commentId}/resolve", s.protect(s.resolveComment))
	mux.Handle("GET /v1/content/{id}/reviews", s.protect(s.listReviews))
	mux.Handle("POST /v1/content/{id}/reviews", s.protect(s.createReview))
	mux.Handle("POST /v1/content/{id}/reviews/{threadId}/resolve", s.protect(s.resolveReview))
	// Agent-request work queue (ISI-7820). The /v1/agent-requests names are the
	// generalized surface; the /v1/illustration-requests names are kept as
	// back-compat aliases to the same handlers so the existing illustration
	// flow and any in-flight clients keep working.
	for _, prefix := range []string{"/v1/agent-requests", "/v1/illustration-requests"} {
		mux.Handle("GET "+prefix, s.protect(s.listIllustrationRequests))
		mux.Handle("POST "+prefix, s.protect(s.createIllustrationRequest))
		mux.Handle("GET "+prefix+"/{id}", s.protect(s.getIllustrationRequest))
		mux.Handle("PATCH "+prefix+"/{id}", s.protect(s.updateIllustrationRequest))
	}
}

func (s *Server) listContents(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	out, err := s.content.ListContents(r.Context(), sess)
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	if out == nil {
		out = []appcontent.Content{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createContent(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req content.CreateContentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	c, err := s.content.CreateContent(r.Context(), sess, req)
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) getContent(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	c, err := s.content.GetContent(r.Context(), sess, r.PathValue("id"))
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) updateContent(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req content.UpdateContentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	c, err := s.content.UpdateContent(r.Context(), sess, r.PathValue("id"), req)
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteContent(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if err := s.content.DeleteContent(r.Context(), sess, r.PathValue("id")); err != nil {
		s.writeContentErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	out, err := s.content.ListComments(r.Context(), sess, r.PathValue("id"))
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out) // nil marshals as null; the client treats it as []
}

func (s *Server) createComment(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req content.CreateCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	c, err := s.content.CreateComment(r.Context(), sess, r.PathValue("id"), req)
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// resolveComment toggles one inline comment's resolved flag (ISI-7821 P4,
// distinct from the review-THREAD resolve). The optional body {"resolved":bool}
// defaults to resolving; an absent/empty body resolves, and {"resolved":false}
// reopens.
func (s *Server) resolveComment(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	resolved := true
	var req content.ResolveCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Resolved != nil {
		resolved = *req.Resolved
	}
	c, err := s.content.ResolveComment(r.Context(), sess, r.PathValue("id"), r.PathValue("commentId"), resolved)
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	out, err := s.content.ListReviewThreads(r.Context(), sess, r.PathValue("id"))
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createReview(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req content.CreateReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	t, err := s.content.CreateReview(r.Context(), sess, r.PathValue("id"), req)
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) resolveReview(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	t, err := s.content.ResolveReviewThread(r.Context(), sess, r.PathValue("id"), r.PathValue("threadId"))
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) listIllustrationRequests(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	// ?assignee= narrows to one agent's work queue; ?status= to one lifecycle
	// state. Both filter WITHIN the §6.3-visible set the gate computes.
	out, err := s.content.ListIllustrationRequests(r.Context(), sess, r.URL.Query().Get("assignee"), appcontent.IllustrationStatus(r.URL.Query().Get("status")))
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createIllustrationRequest(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req content.CreateIllustrationRequestPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := s.content.CreateIllustrationRequest(r.Context(), sess, req.ContentID, req)
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) getIllustrationRequest(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	out, err := s.content.GetIllustrationRequest(r.Context(), sess, r.PathValue("id"))
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) updateIllustrationRequest(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req content.UpdateIllustrationRequestPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := s.content.UpdateIllustrationRequest(r.Context(), sess, r.PathValue("id"), req)
	if err != nil {
		s.writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// writeContentErr maps the content gate's sentinel errors to HTTP codes:
// ErrForbidden → 403 (§6.3 denial), ErrNotFound → 404, ErrBadRequest → 400,
// anything else → 500 (logged).
func (s *Server) writeContentErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, content.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, content.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, content.ErrBadRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.log.Error("booth content store error", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
