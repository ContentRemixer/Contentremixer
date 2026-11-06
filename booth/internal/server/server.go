// Package server is the Booth HTTP surface. It mounts the booth/auth provider's
// login/logout/whoami routes, wraps every CR route with the auth Middleware (so
// nothing runs without a verified session), and routes CR access ONLY through
// the authz-gating store. The route list is deliberately thin — the §6.3
// invariant lives in the store, so adding routes cannot bypass it.
package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// Server wires the auth provider, the gating store, and the route table.
type Server struct {
	auth  *auth.Provider
	store *store.Store
	log   *slog.Logger
}

// New builds a Server. If log is nil, slog.Default() is used.
func New(prov *auth.Provider, st *store.Store, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{auth: prov, store: st, log: log}
}

// Handler returns the fully-wired http.Handler: unauthenticated health probes
// and login/logout, plus auth-wrapped whoami and CRUD routes.
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

	// AuthN routes (booth/auth, P3-2). Login/logout are public; whoami is behind
	// the session Middleware.
	mux.HandleFunc("POST /v1/auth/login", s.auth.Handlers.Login)
	mux.HandleFunc("POST /v1/auth/logout", s.auth.Handlers.Logout)
	mux.Handle("GET /v1/auth/whoami", s.auth.Middleware.Wrap(http.HandlerFunc(s.auth.Handlers.WhoAmI)))

	// CRUD over the registered kinds, each wrapped by the auth Middleware so the
	// session is resolved (or the request 401'd) before the handler runs.
	for _, kind := range []store.ResourceKind{store.ModelProfileKind, store.ReferenceRepoKind} {
		base := "/v1/" + routeName(kind.Kind)
		kind := kind // capture
		mux.Handle("GET "+base, s.protect(s.listHandler(kind)))
		mux.Handle("POST "+base, s.protect(s.createHandler(kind)))
		mux.Handle("GET "+base+"/{name}", s.protect(s.getHandler(kind)))
		mux.Handle("PUT "+base+"/{name}", s.protect(s.updateHandler(kind)))
		mux.Handle("DELETE "+base+"/{name}", s.protect(s.deleteHandler(kind)))
	}

	return mux
}

// protect wraps a handler with the auth session Middleware.
func (s *Server) protect(h http.HandlerFunc) http.Handler {
	return s.auth.Middleware.Wrap(h)
}

// session pulls the verified session the Middleware injected. ok is false only
// if a route were mounted without the Middleware — in which case we deny.
func session(r *http.Request) (auth.Session, bool) {
	return auth.SessionFromContext(r.Context())
}

func (s *Server) listHandler(kind store.ResourceKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := session(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		objs, err := s.store.List(r.Context(), sess, kind)
		if err != nil {
			s.writeStoreErr(w, err)
			return
		}
		if objs == nil {
			objs = []client.Object{}
		}
		writeJSON(w, http.StatusOK, objs)
	}
}

func (s *Server) getHandler(kind store.ResourceKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := session(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
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
		sess, ok := session(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
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
		sess, ok := session(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
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
		sess, ok := session(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
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
