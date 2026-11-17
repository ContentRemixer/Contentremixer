package substrate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// Server implements the substrate control-plane HTTP contract (§3) over a
// durable Store and an execution Runtime.
type Server struct {
	store   Store
	runtime Runtime
	log     *slog.Logger
	now     func() time.Time
	newID   func() string
}

// NewServer wires a Server. log may be nil (a discard logger is used).
func NewServer(store Store, runtime Runtime, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	return &Server{store: store, runtime: runtime, log: log, now: time.Now, newID: newActorID}
}

// Handler returns the mux for all contract routes plus health probes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/actors", s.handleSpawn)
	mux.HandleFunc("GET /v1/actors/{id}", s.handleStatus)
	mux.HandleFunc("POST /v1/actors/{id}/suspend", s.handleSuspend)
	mux.HandleFunc("POST /v1/actors/{id}/resume", s.handleResume)
	mux.HandleFunc("DELETE /v1/actors/{id}", s.handleTerminate)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	return mux
}

func (s *Server) handleSpawn(w http.ResponseWriter, r *http.Request) {
	var req SpawnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.ActorTemplate == "" || req.Image == "" {
		writeErr(w, http.StatusBadRequest, "actorTemplate and image are required")
		return
	}
	now := s.now()
	a := &Actor{
		ID:             s.newID(),
		ActorTemplate:  req.ActorTemplate,
		Image:          req.Image,
		Skill:          req.Skill,
		WorkOrder:      req.WorkOrder,
		MemoryRef:      req.MemoryRef,
		WorkerSelector: req.WorkerSelector,
		TokenSecretRef: req.TokenSecretRef,
		State:          StateRunning,
		Phase:          PhaseSpawning,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	// Persist BEFORE spawning so a crash mid-spawn leaves a reap-able record.
	if err := s.store.Create(r.Context(), a); err != nil {
		s.log.Error("spawn: store create", slog.String("actor", a.ID), slog.Any("err", err))
		writeErr(w, http.StatusInternalServerError, "could not persist actor")
		return
	}
	jobName, err := s.runtime.Spawn(r.Context(), a)
	if err != nil {
		a.State, a.Phase, a.Error = StateFailed, PhaseDone, err.Error()
		a.UpdatedAt = s.now()
		_ = s.store.Update(r.Context(), a)
		s.log.Error("spawn: runtime", slog.String("actor", a.ID), slog.Any("err", err))
		writeErr(w, http.StatusBadGateway, "actor runtime could not spawn")
		return
	}
	a.JobName = jobName
	a.UpdatedAt = s.now()
	_ = s.store.Update(r.Context(), a)
	writeJSON(w, http.StatusCreated, SpawnResponse{ActorID: a.ID, State: a.State})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := s.load(w, r)
	if !ok {
		return
	}
	// Refresh execution state from the runtime unless already terminal/suspended.
	if a.State == StateRunning {
		st, ph, res, errStr, err := s.runtime.Observe(r.Context(), a)
		if err == nil {
			a.State, a.Phase = st, ph
			if res != nil {
				a.Result = res
			}
			if errStr != "" {
				a.Error = errStr
			}
			a.UpdatedAt = s.now()
			_ = s.store.Update(r.Context(), a)
		} else {
			s.log.Warn("status: observe", slog.String("actor", a.ID), slog.Any("err", err))
		}
	}
	writeJSON(w, http.StatusOK, StatusResponse{
		ActorID: a.ID, State: a.State, Phase: a.Phase, Result: a.Result, Error: a.Error,
	})
}

func (s *Server) handleSuspend(w http.ResponseWriter, r *http.Request) {
	a, ok := s.load(w, r)
	if !ok {
		return
	}
	// ADR-4: execution-only teardown. The actor has already flushed memory
	// (caller-enforced ordering); we tear down the Job and keep the record.
	if err := s.runtime.Teardown(r.Context(), a); err != nil {
		s.log.Error("suspend: teardown", slog.String("actor", a.ID), slog.Any("err", err))
		writeErr(w, http.StatusBadGateway, "could not suspend actor")
		return
	}
	a.State, a.Phase = StateSuspended, PhaseSuspended
	a.SnapshotID = "snap_" + a.ID
	a.UpdatedAt = s.now()
	if err := s.store.Update(r.Context(), a); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not persist suspended state")
		return
	}
	writeJSON(w, http.StatusOK, SuspendResponse{ActorID: a.ID, State: a.State, SnapshotID: a.SnapshotID})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	a, ok := s.load(w, r)
	if !ok {
		return
	}
	if a.State != StateSuspended {
		writeErr(w, http.StatusConflict, "actor is not suspended")
		return
	}
	// Optional re-ground source (contract §3.4): passed to the actor, not read.
	var req ResumeRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.MemoryRef != (MemoryRef{}) {
		a.MemoryRef = req.MemoryRef
	}
	jobName, err := s.runtime.Spawn(r.Context(), a)
	if err != nil {
		s.log.Error("resume: spawn", slog.String("actor", a.ID), slog.Any("err", err))
		writeErr(w, http.StatusBadGateway, "could not resume actor")
		return
	}
	a.JobName = jobName
	a.State, a.Phase = StateRunning, PhaseRegrounding
	a.UpdatedAt = s.now()
	if err := s.store.Update(r.Context(), a); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not persist resumed state")
		return
	}
	writeJSON(w, http.StatusOK, ResumeResponse{ActorID: a.ID, State: a.State})
}

func (s *Server) handleTerminate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, err := s.store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Idempotent terminate (contract §3.5): already gone → 204.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeErr(w, http.StatusInternalServerError, "store error")
		return
	}
	if err := s.runtime.Teardown(r.Context(), a); err != nil {
		s.log.Error("terminate: teardown", slog.String("actor", a.ID), slog.Any("err", err))
		writeErr(w, http.StatusBadGateway, "could not terminate actor")
		return
	}
	if err := s.store.Delete(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not delete actor record")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "store not ready")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// load fetches the path actor and writes 404/500 on failure.
func (s *Server) load(w http.ResponseWriter, r *http.Request) (*Actor, bool) {
	id := r.PathValue("id")
	a, err := s.store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "actor not found")
			return nil, false
		}
		writeErr(w, http.StatusInternalServerError, "store error")
		return nil, false
	}
	return a, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, errorBody{Error: msg})
}

// newActorID returns an opaque, collision-resistant id ("act_" + 16 hex bytes).
func newActorID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "act_" + hex.EncodeToString(b[:])
}

// discard is an io.Writer that drops everything (nil-logger sink).
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
