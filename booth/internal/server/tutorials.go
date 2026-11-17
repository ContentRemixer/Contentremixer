package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ContentRemixer/Contentremixer/booth/internal/tutorialrun"
)

// mountTutorials wires the P3-4 multi-skill orchestration routes (ISI-7152).
// All are behind the auth Middleware; every handler reaches the orchestration
// domain ONLY through the §6.3-gating tutorialrun.Store, so a new route cannot
// bypass the gate. A nil store leaves the routes unmounted.
func (s *Server) mountTutorials(mux *http.ServeMux) {
	if s.tutorial == nil {
		return
	}
	mux.Handle("GET /v1/tutorial-runs", s.protect(s.listTutorialRuns))
	mux.Handle("POST /v1/tutorial-runs", s.protect(s.launchTutorialRun))
	mux.Handle("GET /v1/tutorial-runs/{id}", s.protect(s.getTutorialRun))
}

func (s *Server) listTutorialRuns(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	out, err := s.tutorial.ListTutorialRuns(r.Context(), sess)
	if err != nil {
		s.writeTutorialErr(w, err)
		return
	}
	if out == nil {
		out = []tutorialrun.Run{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) launchTutorialRun(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req tutorialrun.LaunchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	run, err := s.tutorial.LaunchTutorialRun(r.Context(), sess, req)
	if err != nil {
		s.writeTutorialErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) getTutorialRun(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	run, err := s.tutorial.GetTutorialRun(r.Context(), sess, r.PathValue("id"))
	if err != nil {
		s.writeTutorialErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// writeTutorialErr maps the gate's sentinels to HTTP codes: ErrForbidden → 403
// (§6.3 denial), ErrNotFound → 404, ErrBadRequest → 400, else 500 (logged).
func (s *Server) writeTutorialErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tutorialrun.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, tutorialrun.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, tutorialrun.ErrBadRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.log.Error("booth tutorial store error", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
