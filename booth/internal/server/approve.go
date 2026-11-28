package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ContentRemixer/Contentremixer/booth/internal/approve"
)

// mountApprove wires the dual-approval route (ISI-7153, P3-5). It is behind the
// auth Middleware and reaches the domain ONLY through the §6.3-gating
// approve.Store. A nil store (Booth built without a GitHub token) leaves the
// route unmounted.
func (s *Server) mountApprove(mux *http.ServeMux) {
	if s.approve != nil {
		mux.Handle("POST /v1/approve", s.protect(s.approvePR))
	}
	// Approval→publish terminal action (ISI-7970 C3). Independent of the
	// reflect-onto-existing-PR action above: it needs the content store + a
	// publisher token, wired as the DraftGate. Unmounted when absent.
	if s.publishDraft != nil {
		mux.Handle("POST /v1/content/{id}/publish", s.protect(s.publishContentDraft))
	}
}

// publishContentDraft is the terminal approve action: approving a reviewed draft
// opens exactly one PR to its target repo, records the link on the draft and the
// origin issue, and moves the draft to published (ISI-7970 C3).
func (s *Server) publishContentDraft(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	out, err := s.publishDraft.PublishDraft(r.Context(), sess, r.PathValue("id"))
	if err != nil {
		s.writeApproveErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) approvePR(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req approve.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := s.approve.Approve(r.Context(), sess, req)
	if err != nil {
		s.writeApproveErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// writeApproveErr maps the approve gate's sentinels to HTTP codes: ErrForbidden
// → 403 (§6.3 denial), ErrNotFound → 404, ErrBadRequest → 400, ErrReflect → 502
// (GitHub-side fault after authorization passed), anything else → 500.
func (s *Server) writeApproveErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, approve.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, approve.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, approve.ErrBadRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, approve.ErrBlocked):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, approve.ErrReflect):
		s.log.Error("booth approve reflection error", slog.Any("err", err))
		writeError(w, http.StatusBadGateway, "reflecting approval to GitHub failed")
	default:
		s.log.Error("booth approve error", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
