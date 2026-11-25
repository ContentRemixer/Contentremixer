package server

import (
	"encoding/json"
	"errors"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/ContentRemixer/Contentremixer/booth/internal/secrets"
)

// WithReferenceRepoSecrets attaches the write-only Secret-write path for the
// reference-repo create flow (ISI-7776 W2) and returns the Server for chaining.
// A setter (like WithTutorialRuns) keeps New's signature stable. A nil writer
// leaves the route unmounted — Booth then behaves as before (secrets via kubectl).
func (s *Server) WithReferenceRepoSecrets(w *secrets.Writer) *Server {
	s.refSecrets = w
	return s
}

// mountReferenceRepoSecrets wires POST /v1/referencerepos/{name}/secrets when a
// writer is configured. The route is Admin-gated in the handler (requireAdmin) —
// ReferenceRepo Secrets are platform config with no per-row owner, exactly like
// the Users surface.
func (s *Server) mountReferenceRepoSecrets(mux *http.ServeMux) {
	if s.refSecrets == nil {
		return
	}
	mux.Handle("POST /v1/referencerepos/{name}/secrets", s.protect(s.writeReferenceRepoSecrets))
}

type writeRepoSecretsRequest struct {
	// Token is the GitHub PAT (spec.tokenRef). Required.
	Token string `json:"token"`
	// WebhookSecret is the HMAC (spec.webhookSecretRef). Optional — Booth
	// auto-generates one when omitted and none is already stored.
	WebhookSecret string `json:"webhookSecret"`
}

// writeReferenceRepoSecrets writes the PAT + HMAC into the per-repo Secret and
// returns ONLY the references (name+key). Values are never echoed back (W2
// "Secret values are never echoed back to the SPA").
func (s *Server) writeReferenceRepoSecrets(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var req writeRepoSecretsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	res, err := s.refSecrets.Write(r.Context(), sess, r.PathValue("name"), req.Token, req.WebhookSecret)
	if err != nil {
		s.writeSecretErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// writeSecretErr maps secret-write failures: a missing token or an invalid repo
// name (the Secret name the apiserver rejects) are the caller's fault → 400;
// anything else is an internal/k8s error → 500.
func (s *Server) writeSecretErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, secrets.ErrTokenRequired):
		writeError(w, http.StatusBadRequest, "token is required")
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
		writeError(w, http.StatusBadRequest, "invalid reference-repo name")
	default:
		s.log.Error("booth secret write error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
