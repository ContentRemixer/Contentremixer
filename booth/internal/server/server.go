// Package server is the Booth HTTP surface: an auth middleware that resolves the
// session (and nothing downstream runs without one) plus CRUD handlers that
// reach CRs ONLY through the authz-gating store. The route list is deliberately
// thin — the §6.3 invariant lives in the store, so adding routes cannot bypass
// it.
package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ContentRemixer/Contentremixer/booth/internal/session"
	"github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// Server wires the authenticator, the gating store, and the route table.
type Server struct {
	auth  session.Authenticator
	store *store.Store
	log   *slog.Logger
}

// New builds a Server. If log is nil, slog.Default() is used.
func New(auth session.Authenticator, st *store.Store, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{auth: auth, store: st, log: log}
}

// Handler returns the fully-wired http.Handler with auth middleware applied to
// all CRUD routes and unauthenticated health probes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Health probes — unauthenticated by design (k8s liveness/readiness).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// CRUD over the registered kinds. Every kind gets the same five routes; the
	// handlers are generic over store.ResourceKind.
	for _, kind := range []store.ResourceKind{store.ModelProfileKind, store.ReferenceRepoKind} {
		base := "/v1/" + routeName(kind.Kind)
		kind := kind // capture
		mux.Handle("GET "+base, s.authn(s.listHandler(kind)))
		mux.Handle("POST "+base, s.authn(s.createHandler(kind)))
		mux.Handle("GET "+base+"/{name}", s.authn(s.getHandler(kind)))
		mux.Handle("PUT "+base+"/{name}", s.authn(s.updateHandler(kind)))
		mux.Handle("DELETE "+base+"/{name}", s.authn(s.deleteHandler(kind)))
	}

	return mux
}

// authn is the auth middleware: it resolves the session and stores it in the
// request context, or writes 401. No handler downstream runs without a session.
func (s *Server) authn(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.auth.Authenticate(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="booth"`)
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		next.ServeHTTP(w, r.WithContext(session.WithSession(r.Context(), sess)))
	})
}

func (s *Server) listHandler(kind store.ResourceKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, _ := session.FromContext(r.Context())
		objs, err := s.store.List(r.Context(), sess, kind)
		if err != nil {
			s.writeStoreErr(w, err)
			return
		}
		// Never nil: an authorized-but-empty result is [] not null.
		if objs == nil {
			objs = []client.Object{}
		}
		writeJSON(w, http.StatusOK, objs)
	}
}

func (s *Server) getHandler(kind store.ResourceKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, _ := session.FromContext(r.Context())
		obj, err := s.store.Get(r.Context(), sess, kind, r.PathValue("name"))
		if err != nil {
			s.writeStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, obj)
	}
}

func (s *Server) createHandler(kind store.ResourceKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, _ := session.FromContext(r.Context())
		obj := kind.NewObject()
		if err := json.NewDecoder(r.Body).Decode(obj); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if err := s.store.Create(r.Context(), sess, kind, obj); err != nil {
			s.writeStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, obj)
	}
}

func (s *Server) updateHandler(kind store.ResourceKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, _ := session.FromContext(r.Context())
		obj := kind.NewObject()
		if err := json.NewDecoder(r.Body).Decode(obj); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		obj.SetName(r.PathValue("name"))
		if err := s.store.Update(r.Context(), sess, kind, obj); err != nil {
			s.writeStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, obj)
	}
}

func (s *Server) deleteHandler(kind store.ResourceKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, _ := session.FromContext(r.Context())
		if err := s.store.Delete(r.Context(), sess, kind, r.PathValue("name")); err != nil {
			s.writeStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// writeStoreErr maps store sentinel errors to HTTP status codes. ErrForbidden
// → 403 (the §6.3 denial), ErrNotFound → 404, anything else → 500.
func (s *Server) writeStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		s.log.Error("booth store error", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
