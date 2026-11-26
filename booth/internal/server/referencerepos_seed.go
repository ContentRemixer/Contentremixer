package server

import (
	"errors"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/ContentRemixer/Contentremixer/booth/internal/seedissues"
)

// WithReferenceRepoSeeder attaches the template-issue seeding path for the
// reference-repo onboarding flow (ISI-7896, W3 of ISI-7887) and returns the
// Server for chaining. A nil seeder leaves the route unmounted.
func (s *Server) WithReferenceRepoSeeder(sd *seedissues.Seeder) *Server {
	s.seeder = sd
	return s
}

// mountReferenceRepoSeeder wires POST /v1/referencerepos/{name}/seed-issues when
// a seeder is configured. Admin-gated in the handler — seeding is platform
// onboarding with no per-row owner, exactly like the Secret-write surface.
func (s *Server) mountReferenceRepoSeeder(mux *http.ServeMux) {
	if s.seeder == nil {
		return
	}
	mux.Handle("POST /v1/referencerepos/{name}/seed-issues", s.protect(s.seedReferenceRepoIssues))
}

// seedReferenceRepoIssues seeds the versioned template-issue pack into the named
// ReferenceRepo. Idempotent: templates already present are skipped, so re-runs
// are safe. Returns the created/skipped breakdown.
func (s *Server) seedReferenceRepoIssues(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	res, err := s.seeder.Seed(r.Context(), sess, r.PathValue("name"))
	if err != nil {
		s.writeSeedErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// writeSeedErr maps seeding failures. A missing CR is the caller's fault → 404;
// a repo with no PAT → 412 (precondition: add a token first); a transient/
// forbidden k8s read (secret) → 503; a GitHub/transport failure → 502; anything
// else → 500. Mirrors writeSecretErr's severity split.
func (s *Server) writeSeedErr(w http.ResponseWriter, err error) {
	switch {
	case seedissues.IsNotFound(err):
		writeError(w, http.StatusNotFound, "reference repo not found")
	case errors.Is(err, seedissues.ErrNoToken):
		writeError(w, http.StatusPreconditionFailed, "reference repo has no GitHub token; add one via Edit before seeding")
	case apierrors.IsForbidden(err), apierrors.IsServerTimeout(err), apierrors.IsTimeout(err),
		apierrors.IsTooManyRequests(err), apierrors.IsServiceUnavailable(err), apierrors.IsInternalError(err):
		s.log.Error("booth seed: k8s unavailable", "err", err)
		writeError(w, http.StatusServiceUnavailable, "secret store temporarily unavailable")
	default:
		// Seeding talks to GitHub after the k8s reads succeed, so the common
		// remaining failure is a GitHub-side error (bad token, repo gone, rate
		// limit). 502 — the upstream failed, not Booth's request handling.
		s.log.Error("booth seed: github error", "err", err)
		writeError(w, http.StatusBadGateway, "seeding failed: "+err.Error())
	}
}
