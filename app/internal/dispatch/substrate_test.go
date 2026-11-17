package dispatch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// subServer is a fake substrate control plane (contract §3) for driving
// SubstrateStrategy + SubstrateClient without a cluster. It captures the spawn
// body and replays a scripted sequence of status states (last repeats).
type subServer struct {
	mu          sync.Mutex
	spawnRaw    []byte
	spawnDecode spawnRequest
	states      []actorState
	failErr     string
	resumeRaw   []byte
	statusHits  int
	suspendHits int
	terminated  bool
}

func (s *subServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/actors", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.spawnRaw, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(s.spawnRaw, &s.spawnDecode)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(spawnResponse{ActorID: "act_1", State: stateRunning})
	})
	mux.HandleFunc("GET /v1/actors/{id}", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		i := s.statusHits
		s.statusHits++
		if i >= len(s.states) {
			i = len(s.states) - 1
		}
		st := statusResponse{ActorID: "act_1", State: s.states[i], Phase: "working"}
		if s.states[i] == stateFailed {
			st.Error = s.failErr
		}
		if s.states[i] == stateSucceeded {
			st.Result = &result{PRURL: "https://x/pull/1"}
		}
		_ = json.NewEncoder(w).Encode(st)
	})
	mux.HandleFunc("POST /v1/actors/{id}/suspend", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.suspendHits++
		_ = json.NewEncoder(w).Encode(suspendResponse{ActorID: "act_1", State: stateSuspended, SnapshotID: "snap_1"})
	})
	mux.HandleFunc("POST /v1/actors/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.resumeRaw, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(resumeResponse{ActorID: "act_1", State: stateRunning})
	})
	mux.HandleFunc("DELETE /v1/actors/{id}", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.terminated = true
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func mediaWorkOrder() WorkOrder {
	return WorkOrder{
		Repo:          "ContentRemixer/Contentremixer",
		IssueNumber:   42,
		Skill:         "video-designer",
		ActorTemplate: contentremixerv1alpha1.ActorTemplateMedia,
		Inputs: Inputs{
			SourcePath:   "content/foo.md",
			Notes:        "punchy",
			InputFormat:  "auto",
			OutputFormat: "demo",
			Token:        "ghp_SUPERSECRET", // must NOT cross the wire (§3.1)
		},
		MemoryRef: MemoryRef{
			Owner:   contentremixerv1alpha1.OwnerRef{Subject: "sub-123"},
			Persona: "henrik-devrel",
		},
	}
}

func newStrategy(baseURL string) SubstrateStrategy {
	return SubstrateStrategy{
		Client:         NewSubstrateClient(baseURL),
		Image:          "ghcr.io/contentremixer/media-actor@sha256:abc",
		WorkerSelector: map[string]string{"node.kubernetes.io/instance-type": "media"},
		TokenSecretRef: &SecretRef{Name: "repo-pat", Key: "token"},
		PollInterval:   time.Millisecond,
	}
}

func TestSubstrateStrategy_Runtime(t *testing.T) {
	if got := (SubstrateStrategy{}).Runtime(); got != RuntimeSubstrate {
		t.Fatalf("Runtime() = %q, want %q", got, RuntimeSubstrate)
	}
}

func TestSubstrateStrategy_RunPollsToSuccess(t *testing.T) {
	s := &subServer{states: []actorState{stateRunning, stateRunning, stateSucceeded}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	if err := newStrategy(srv.URL).Run(context.Background(), mediaWorkOrder()); err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
	if s.statusHits < 3 {
		t.Fatalf("polled %d times, want >=3 (poll until terminal)", s.statusHits)
	}
}

func TestSubstrateStrategy_RunFailsClosedOnActorFailure(t *testing.T) {
	s := &subServer{states: []actorState{stateRunning, stateFailed}, failErr: "ffmpeg exited 1"}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	err := newStrategy(srv.URL).Run(context.Background(), mediaWorkOrder())
	if err == nil || !strings.Contains(err.Error(), "ffmpeg exited 1") {
		t.Fatalf("Run() = %v, want error carrying the actor failure detail", err)
	}
}

func TestSubstrateStrategy_RunAbortsOnContextCancel(t *testing.T) {
	s := &subServer{states: []actorState{stateRunning}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := newStrategy(srv.URL).Run(ctx, mediaWorkOrder()); err == nil {
		t.Fatal("Run() = nil, want context error when the actor never terminates")
	}
}

func TestSubstrateStrategy_FailsClosedWithoutClientOrImage(t *testing.T) {
	if err := (SubstrateStrategy{Image: "img"}).Run(context.Background(), mediaWorkOrder()); err == nil {
		t.Fatal("Run() with nil Client = nil, want error")
	}
	if err := (SubstrateStrategy{Client: NewSubstrateClient("")}).Run(context.Background(), mediaWorkOrder()); err == nil {
		t.Fatal("Run() with empty BaseURL = nil, want error")
	}
	bad := SubstrateStrategy{Client: NewSubstrateClient("http://x"), Image: ""}
	if err := bad.Run(context.Background(), mediaWorkOrder()); err == nil {
		t.Fatal("Run() with empty Image = nil, want error")
	}
}

// TestSubstrateStrategy_SpawnBodyMapping is the ADR-4 / §3.1 guard: the spawn
// body maps the WorkOrder faithfully, carries the PAT only as a tokenSecretRef
// (never a literal), and carries MemoryRef as a reference (no memory bytes).
func TestSubstrateStrategy_SpawnBodyMapping(t *testing.T) {
	s := &subServer{states: []actorState{stateSucceeded}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	if err := newStrategy(srv.URL).Run(context.Background(), mediaWorkOrder()); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	got := s.spawnDecode

	if got.ActorTemplate != contentremixerv1alpha1.ActorTemplateMedia {
		t.Errorf("ActorTemplate = %q, want media", got.ActorTemplate)
	}
	if got.Image != "ghcr.io/contentremixer/media-actor@sha256:abc" {
		t.Errorf("Image = %q", got.Image)
	}
	if got.Skill != "video-designer" {
		t.Errorf("Skill = %q, want video-designer", got.Skill)
	}
	if got.WorkOrder.Repo != "ContentRemixer/Contentremixer" || got.WorkOrder.IssueNumber != 42 {
		t.Errorf("WorkOrder repo/issue = %q/%d", got.WorkOrder.Repo, got.WorkOrder.IssueNumber)
	}
	if got.WorkOrder.Inputs.OutputFormat != "demo" || got.WorkOrder.Inputs.SourcePath != "content/foo.md" {
		t.Errorf("Inputs mapped wrong: %+v", got.WorkOrder.Inputs)
	}
	if got.MemoryRef.Owner != "sub-123" || got.MemoryRef.Persona != "henrik-devrel" {
		t.Errorf("MemoryRef = %+v, want owner=sub-123 persona=henrik-devrel", got.MemoryRef)
	}
	if got.WorkerSelector["node.kubernetes.io/instance-type"] != "media" {
		t.Errorf("WorkerSelector not forwarded: %+v", got.WorkerSelector)
	}
	if got.TokenSecretRef == nil || got.TokenSecretRef.Name != "repo-pat" || got.TokenSecretRef.Key != "token" {
		t.Errorf("TokenSecretRef = %+v, want repo-pat/token", got.TokenSecretRef)
	}
	// ADR-4 / §3.1: the PAT literal must never appear anywhere in the spawn body.
	if strings.Contains(string(s.spawnRaw), "ghp_SUPERSECRET") {
		t.Fatalf("PAT literal leaked into spawn body: %s", s.spawnRaw)
	}
}

// TestSubstrateClient_Contract exercises the client against the §3 wire shapes
// verbatim — the lock-step guard between this client and the substrate server.
func TestSubstrateClient_Contract(t *testing.T) {
	s := &subServer{states: []actorState{stateSucceeded}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	c := NewSubstrateClient(srv.URL)
	ctx := context.Background()

	sp, err := c.Spawn(ctx, spawnRequest{ActorTemplate: "media", Image: "img", Skill: "demo-recorder"})
	if err != nil || sp.ActorID != "act_1" || sp.State != stateRunning {
		t.Fatalf("Spawn = %+v, %v", sp, err)
	}
	st, err := c.Status(ctx, "act_1")
	if err != nil || st.State != stateSucceeded || st.Result == nil || st.Result.PRURL == "" {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	su, err := c.Suspend(ctx, "act_1")
	if err != nil || su.SnapshotID != "snap_1" || su.State != stateSuspended {
		t.Fatalf("Suspend = %+v, %v", su, err)
	}
	re, err := c.Resume(ctx, "act_1", resumeRequest{MemoryRef: memoryRef{Owner: "o", Persona: "p"}})
	if err != nil || re.State != stateRunning {
		t.Fatalf("Resume = %+v, %v", re, err)
	}
	var rb resumeRequest
	_ = json.Unmarshal(s.resumeRaw, &rb)
	if rb.MemoryRef.Owner != "o" || rb.MemoryRef.Persona != "p" {
		t.Fatalf("server saw wrong resume body: %+v", rb)
	}
	if err := c.Terminate(ctx, "act_1"); err != nil || !s.terminated {
		t.Fatalf("Terminate = %v, terminated=%v", err, s.terminated)
	}
}

func TestSubstrateClient_SurfacesErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "already suspended"})
	}))
	defer srv.Close()
	_, err := NewSubstrateClient(srv.URL).Suspend(context.Background(), "a")
	if err == nil || !strings.Contains(err.Error(), "already suspended") {
		t.Fatalf("err = %v, want the §3 error envelope surfaced", err)
	}
}
