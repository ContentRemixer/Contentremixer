package tutorialrun

import (
	"context"
	"errors"
	"testing"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/orchestrator"
)

func newStore() *Store {
	return New(NewMemoryRepo(), orchestrator.DefaultEngine(), audit.NewSlogLogger(nil))
}

func devrel(sub string) auth.Session { return auth.Session{Subject: sub, Role: auth.RoleDevRel} }
func admin(sub string) auth.Session  { return auth.Session{Subject: sub, Role: auth.RoleAdmin} }

func TestLaunchStampsOwnerFromSession(t *testing.T) {
	s := newStore()
	run, err := s.LaunchTutorialRun(context.Background(), devrel("alice"), LaunchRequest{Topic: "trace a service"})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if run.OwnerSubject != "alice" {
		t.Errorf("owner = %q, want alice (stamped from session)", run.OwnerSubject)
	}
	if run.Status != StatusSucceeded || run.Mix == nil {
		t.Fatalf("expected succeeded run with a Mix, got status=%q mix=%v", run.Status, run.Mix)
	}
	if run.Mix.Meta["owner"] != "alice" {
		t.Errorf("RunContext.Owner did not flow to Mix.Meta: %v", run.Mix.Meta["owner"])
	}
}

func TestEmptyTopicIsBadRequest(t *testing.T) {
	s := newStore()
	_, err := s.LaunchTutorialRun(context.Background(), devrel("alice"), LaunchRequest{})
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("want ErrBadRequest, got %v", err)
	}
}

// The core §6.3 acceptance: a second owner can neither read nor list another
// owner's run, and this uses the SAME authz.Authorize predicate P3-1 shipped —
// P3-4 adds no new authz path.
func TestCrossOwnerIsolation(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	aliceRun, _ := s.LaunchTutorialRun(ctx, devrel("alice"), LaunchRequest{Topic: "alice topic"})

	// Bob (a different DevRel) cannot GET Alice's run.
	if _, err := s.GetTutorialRun(ctx, devrel("bob"), aliceRun.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("bob GET alice run: want ErrForbidden, got %v", err)
	}
	// Bob's list is empty; Alice sees her own.
	if runs, _ := s.ListTutorialRuns(ctx, devrel("bob")); len(runs) != 0 {
		t.Errorf("bob list = %d runs, want 0", len(runs))
	}
	if runs, _ := s.ListTutorialRuns(ctx, devrel("alice")); len(runs) != 1 {
		t.Errorf("alice list = %d runs, want 1", len(runs))
	}
}

func TestAdminSeesAllRuns(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	s.LaunchTutorialRun(ctx, devrel("alice"), LaunchRequest{Topic: "a"})
	s.LaunchTutorialRun(ctx, devrel("bob"), LaunchRequest{Topic: "b"})

	runs, err := s.ListTutorialRuns(ctx, admin("root"))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Errorf("admin list = %d runs, want 2", len(runs))
	}
	// Admin can read either owner's run (clause 1).
	for _, r := range runs {
		if _, err := s.GetTutorialRun(ctx, admin("root"), r.ID); err != nil {
			t.Errorf("admin GET run %s: %v", r.ID, err)
		}
	}
}

func TestGetMissingRun(t *testing.T) {
	s := newStore()
	if _, err := s.GetTutorialRun(context.Background(), admin("root"), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// failEngine always errors to exercise the fail-closed persistence path.
type failEngine struct{}

func (failEngine) Orchestrate(context.Context, orchestrator.RunContext) (orchestrator.Mix, error) {
	return orchestrator.Mix{}, errors.New("engine down")
}

func TestFailedOrchestrationPersistsOwnerTaggedRun(t *testing.T) {
	s := New(NewMemoryRepo(), failEngine{}, audit.NewSlogLogger(nil))
	ctx := context.Background()
	run, err := s.LaunchTutorialRun(ctx, devrel("alice"), LaunchRequest{Topic: "t"})
	if err != nil {
		t.Fatalf("launch should not hard-error on skill failure: %v", err)
	}
	if run.Status != StatusFailed || run.Mix != nil {
		t.Errorf("want failed run with no Mix, got status=%q mix=%v", run.Status, run.Mix)
	}
	// The failed run is still owner-tagged, persisted, and §6.3-isolated.
	if got, _ := s.GetTutorialRun(ctx, devrel("alice"), run.ID); got.Status != StatusFailed {
		t.Errorf("failed run not persisted/readable by owner")
	}
	if _, err := s.GetTutorialRun(ctx, devrel("bob"), run.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("failed run must still be owner-isolated, got %v", err)
	}
}
