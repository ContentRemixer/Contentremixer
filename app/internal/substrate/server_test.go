package substrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeRuntime records calls and lets a test steer Observe/Spawn outcomes.
type fakeRuntime struct {
	spawnErr    error
	teardownErr error
	obsState    ActorState
	obsPhase    Phase
	obsResult   *Result
	spawns      int
	teardowns   int
}

func (f *fakeRuntime) Spawn(_ context.Context, a *Actor) (string, error) {
	f.spawns++
	if f.spawnErr != nil {
		return "", f.spawnErr
	}
	return "actor-" + a.ID, nil
}

func (f *fakeRuntime) Observe(_ context.Context, a *Actor) (ActorState, Phase, *Result, string, error) {
	st := f.obsState
	if st == "" {
		st = a.State
	}
	ph := f.obsPhase
	if ph == "" {
		ph = a.Phase
	}
	return st, ph, f.obsResult, "", nil
}

func (f *fakeRuntime) Teardown(_ context.Context, _ *Actor) error {
	f.teardowns++
	return f.teardownErr
}

func newTestServer(rt Runtime) (*Server, *MemStore) {
	st := NewMemStore()
	return NewServer(st, rt, nil), st
}

func spawnOne(t *testing.T, h http.Handler) string {
	t.Helper()
	body := `{"actorTemplate":"media","image":"ghcr.io/x/media-actor:v1","skill":"video-designer","workOrder":{"repo":"o/r","issueNumber":7,"inputs":{"outputFormat":"demo"}}}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/actors", bytes.NewBufferString(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("spawn: want 201, got %d (%s)", rec.Code, rec.Body)
	}
	var resp SpawnResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("spawn: decode: %v", err)
	}
	if resp.ActorID == "" || resp.State != StateRunning {
		t.Fatalf("spawn: bad response %+v", resp)
	}
	return resp.ActorID
}

func TestSpawnPersistsAndRunsRuntime(t *testing.T) {
	rt := &fakeRuntime{}
	srv, st := newTestServer(rt)
	h := srv.Handler()
	id := spawnOne(t, h)

	if rt.spawns != 1 {
		t.Fatalf("runtime.Spawn called %d times, want 1", rt.spawns)
	}
	a, err := st.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("actor not persisted: %v", err)
	}
	if a.JobName == "" {
		t.Fatalf("job name not recorded after spawn")
	}
}

func TestSpawnValidation(t *testing.T) {
	srv, _ := newTestServer(&fakeRuntime{})
	h := srv.Handler()
	for _, body := range []string{`{"image":"x"}`, `{"actorTemplate":"media"}`, `not json`} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/actors", bytes.NewBufferString(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: want 400, got %d", body, rec.Code)
		}
	}
}

func TestSpawnRuntimeFailureMarksActorFailed(t *testing.T) {
	rt := &fakeRuntime{spawnErr: errors.New("no nodes")}
	srv, st := newTestServer(rt)
	h := srv.Handler()
	body := `{"actorTemplate":"media","image":"img","workOrder":{"repo":"o/r","issueNumber":1}}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/actors", bytes.NewBufferString(body)))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", rec.Code)
	}
	// The record must survive as failed (reap-able), not vanish.
	var any bool
	st.mu.Lock()
	for _, a := range st.m {
		any = true
		if a.State != StateFailed {
			t.Fatalf("actor state = %q, want failed", a.State)
		}
	}
	st.mu.Unlock()
	if !any {
		t.Fatalf("failed actor was not persisted")
	}
}

func TestStatusRefreshesFromRuntime(t *testing.T) {
	rt := &fakeRuntime{obsState: StateSucceeded, obsPhase: PhaseDone, obsResult: &Result{PRURL: "http://pr/1"}}
	srv, _ := newTestServer(rt)
	h := srv.Handler()
	id := spawnOne(t, h)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/actors/"+id, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	var resp StatusResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.State != StateSucceeded || resp.Phase != PhaseDone || resp.Result == nil || resp.Result.PRURL != "http://pr/1" {
		t.Fatalf("status not refreshed: %+v", resp)
	}
}

func TestStatusNotFound(t *testing.T) {
	srv, _ := newTestServer(&fakeRuntime{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/actors/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestSuspendThenResumeLifecycle(t *testing.T) {
	rt := &fakeRuntime{}
	srv, st := newTestServer(rt)
	h := srv.Handler()
	id := spawnOne(t, h)

	// suspend
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/actors/"+id+"/suspend", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("suspend: want 200, got %d", rec.Code)
	}
	var sresp SuspendResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sresp)
	if sresp.State != StateSuspended || sresp.SnapshotID == "" {
		t.Fatalf("suspend response bad: %+v", sresp)
	}
	if rt.teardowns != 1 {
		t.Fatalf("teardown called %d times, want 1", rt.teardowns)
	}
	a, _ := st.Get(context.Background(), id)
	if a.State != StateSuspended {
		t.Fatalf("stored state = %q, want suspended", a.State)
	}

	// resume with a re-ground memoryRef
	spawnsBefore := rt.spawns
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/actors/"+id+"/resume",
		bytes.NewBufferString(`{"memoryRef":{"owner":"o","persona":"p"}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("resume: want 200, got %d (%s)", rec.Code, rec.Body)
	}
	if rt.spawns != spawnsBefore+1 {
		t.Fatalf("resume did not re-spawn")
	}
	a, _ = st.Get(context.Background(), id)
	if a.State != StateRunning || a.MemoryRef.Persona != "p" {
		t.Fatalf("resume state/regroundid bad: %+v", a)
	}
}

func TestResumeRequiresSuspended(t *testing.T) {
	srv, _ := newTestServer(&fakeRuntime{})
	h := srv.Handler()
	id := spawnOne(t, h) // state=running, not suspended
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/actors/"+id+"/resume", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", rec.Code)
	}
}

func TestTerminateIsIdempotent(t *testing.T) {
	rt := &fakeRuntime{}
	srv, _ := newTestServer(rt)
	h := srv.Handler()
	id := spawnOne(t, h)

	for i, want := range []int{http.StatusNoContent, http.StatusNoContent} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/actors/"+id, nil))
		if rec.Code != want {
			t.Fatalf("terminate #%d: want %d, got %d", i, want, rec.Code)
		}
	}
}

func TestReadyzReflectsStore(t *testing.T) {
	srv, _ := newTestServer(&fakeRuntime{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz: want 200, got %d", rec.Code)
	}
}
