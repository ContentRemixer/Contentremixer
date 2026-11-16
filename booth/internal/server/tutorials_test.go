package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/orchestrator"
	"github.com/ContentRemixer/Contentremixer/booth/internal/tutorialrun"
)

// newTutorialServer builds a minimal server with ONLY the P3-4 tutorial store
// wired, so the orchestration routes mount without needing Postgres/k8s.
func newTutorialServer(t *testing.T) (http.Handler, *auth.TokenCodec) {
	t.Helper()
	adminHash, err := auth.HashPassword("admin-pw")
	if err != nil {
		t.Fatal(err)
	}
	prov, err := auth.Bootstrap(auth.Config{
		SessionKey:    testKey,
		SessionTTL:    time.Hour,
		AdminUsername: "admin",
		AdminHash:     adminHash,
		SecureCookie:  false,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := tutorialrun.New(tutorialrun.NewMemoryRepo(), orchestrator.DefaultEngine(), audit.NewSlogLogger(nil))
	srv := New(prov, nil, nil, nil, nil, nil, nil).WithTutorialRuns(ts)
	return srv.Handler(), prov.Codec
}

func TestTutorialRoutesRequireAuth(t *testing.T) {
	h, _ := newTutorialServer(t)
	if w := do(t, h, "GET", "/v1/tutorial-runs", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list = %d, want 401", w.Code)
	}
}

func TestTutorialLaunchAndReadBackAsOwner(t *testing.T) {
	h, codec := newTutorialServer(t)
	alice := tokenFor(t, codec, "alice", auth.RoleDevRel)

	w := do(t, h, "POST", "/v1/tutorial-runs", alice, `{"topic":"trace a Go service","audience":"SREs"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("launch = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	var run tutorialrun.Run
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.OwnerSubject != "alice" || run.Status != tutorialrun.StatusSucceeded || run.Mix == nil {
		t.Fatalf("unexpected run: %+v", run)
	}

	// Owner reads it back.
	if g := do(t, h, "GET", "/v1/tutorial-runs/"+run.ID, alice, ""); g.Code != http.StatusOK {
		t.Fatalf("owner GET = %d, want 200", g.Code)
	}
}

func TestTutorialCrossOwnerIsForbidden(t *testing.T) {
	h, codec := newTutorialServer(t)
	alice := tokenFor(t, codec, "alice", auth.RoleDevRel)
	bob := tokenFor(t, codec, "bob", auth.RoleDevRel)

	w := do(t, h, "POST", "/v1/tutorial-runs", alice, `{"topic":"alice only"}`)
	var run tutorialrun.Run
	_ = json.Unmarshal(w.Body.Bytes(), &run)

	if g := do(t, h, "GET", "/v1/tutorial-runs/"+run.ID, bob, ""); g.Code != http.StatusForbidden {
		t.Fatalf("bob GET alice run = %d, want 403", g.Code)
	}
	// Bob's list is empty (server-side §6.3 filter).
	lw := do(t, h, "GET", "/v1/tutorial-runs", bob, "")
	var bobRuns []tutorialrun.Run
	_ = json.Unmarshal(lw.Body.Bytes(), &bobRuns)
	if len(bobRuns) != 0 {
		t.Fatalf("bob list = %d runs, want 0", len(bobRuns))
	}
}

func TestTutorialEmptyTopicIs400(t *testing.T) {
	h, codec := newTutorialServer(t)
	alice := tokenFor(t, codec, "alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/tutorial-runs", alice, `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty topic = %d, want 400", w.Code)
	}
}
