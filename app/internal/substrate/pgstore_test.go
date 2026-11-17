package substrate

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// TestPGStoreCRUD exercises the Postgres store against a live database. It is
// skipped unless PGVECTOR_TEST_DSN is set (CI provides a postgres service; see
// .github/workflows/ci.yml). The substrate_actors table is created idempotently
// by OpenPG, so it coexists with the pgvector schema on the same database.
func TestPGStoreCRUD(t *testing.T) {
	dsn := os.Getenv("PGVECTOR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGVECTOR_TEST_DSN not set; skipping live substrate store test")
	}
	ctx := context.Background()
	s, err := OpenPG(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Unique id so repeated CI runs on the same DB don't collide.
	id := "act_test_" + time.Now().Format("20060102150405.000000")
	now := time.Now().UTC().Truncate(time.Millisecond)
	a := &Actor{
		ID: id, ActorTemplate: "media", Image: "img", Skill: "s",
		WorkOrder: WorkOrder{Repo: "o/r", IssueNumber: 3, Inputs: Inputs{OutputFormat: "demo"}},
		MemoryRef: MemoryRef{Owner: "o", Persona: "p"},
		State:     StateRunning, Phase: PhaseSpawning, CreatedAt: now, UpdatedAt: now,
	}
	t.Cleanup(func() { _ = s.Delete(context.Background(), id) })

	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.WorkOrder.Repo != "o/r" || got.MemoryRef.Persona != "p" || got.State != StateRunning {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}

	a.State, a.Phase, a.JobName = StateSuspended, PhaseSuspended, "actor-x"
	if err := s.Update(ctx, a); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = s.Get(ctx, id)
	if got.State != StateSuspended || got.JobName != "actor-x" {
		t.Fatalf("update not persisted: %+v", got)
	}

	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
	// Idempotent delete + unknown-id update.
	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("delete idempotent: %v", err)
	}
	if err := s.Update(ctx, &Actor{ID: "ghost_" + id}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update unknown: want ErrNotFound, got %v", err)
	}
}
