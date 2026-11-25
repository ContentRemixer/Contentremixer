package server

import (
	"errors"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/ContentRemixer/Contentremixer/booth/internal/webhook"
)

// WithReferenceRepoWebhook attaches the GitHub webhook-registration path for the
// reference-repo create flow (ISI-7776 W3) and returns the Server for chaining.
// A nil registrar leaves the route unmounted — Booth then behaves as before
// (webhook registered manually, deploy/README fallback). The registrar is only
// built when a Webhooks-permission token AND the public delivery URL are both
// configured, so the route's presence doubles as the UI's capability signal.
func (s *Server) WithReferenceRepoWebhook(r *webhook.Registrar) *Server {
	s.refWebhook = r
	return s
}

// mountReferenceRepoWebhook wires POST /v1/referencerepos/{name}/webhook when a
// registrar is configured. Admin-gated in the handler (requireAdmin) — the same
// platform-config gate as the W2 secret-write path: no per-row owner.
func (s *Server) mountReferenceRepoWebhook(mux *http.ServeMux) {
	if s.refWebhook == nil {
		return
	}
	mux.Handle("POST /v1/referencerepos/{name}/webhook", s.protect(s.registerReferenceRepoWebhook))
}

// registerReferenceRepoWebhook registers (or idempotently updates) the GitHub
// `issues` webhook for the named ReferenceRepo and returns the resulting hook
// reference. The HMAC is never echoed back.
func (s *Server) registerReferenceRepoWebhook(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	res, err := s.refWebhook.Register(r.Context(), sess, r.PathValue("name"))
	if err != nil {
		s.writeWebhookErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// writeWebhookErr maps registration failures. A missing CR → 404; a missing HMAC
// Secret (W2 not run yet) → 409; a transient/forbidden apiserver failure → 503
// (mirrors the W2 secret-write mapping); a GitHub REST failure or anything else
// → 502 (the upstream the service depends on failed, not the caller).
func (s *Server) writeWebhookErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, webhook.ErrRepoNotFound):
		writeError(w, http.StatusNotFound, "reference repo not found")
	case errors.Is(err, webhook.ErrSecretMissing):
		writeError(w, http.StatusConflict, "webhook secret not written yet — save repo secrets first")
	case apierrors.IsForbidden(err), apierrors.IsServerTimeout(err), apierrors.IsTimeout(err),
		apierrors.IsTooManyRequests(err), apierrors.IsServiceUnavailable(err), apierrors.IsInternalError(err):
		s.log.Error("booth webhook registration unavailable", "err", err)
		writeError(w, http.StatusServiceUnavailable, "cluster temporarily unavailable")
	default:
		s.log.Error("booth webhook registration failed", "err", err)
		writeError(w, http.StatusBadGateway, "github webhook registration failed")
	}
}
