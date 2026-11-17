package substrate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStoreCRUD(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	now := time.Now()
	a := &Actor{ID: "act_1", State: StateRunning, Phase: PhaseSpawning, CreatedAt: now, UpdatedAt: now}

	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Create(ctx, a); err == nil {
		t.Fatalf("duplicate create should error")
	}

	got, err := s.Get(ctx, "act_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != StateRunning {
		t.Fatalf("got state %q", got.State)
	}
	// Get returns a copy — mutating it must not affect the store.
	got.State = StateFailed
	again, _ := s.Get(ctx, "act_1")
	if again.State != StateRunning {
		t.Fatalf("Get must return a defensive copy")
	}

	a.State = StateSuspended
	if err := s.Update(ctx, a); err != nil {
		t.Fatalf("update: %v", err)
	}
	upd, _ := s.Get(ctx, "act_1")
	if upd.State != StateSuspended {
		t.Fatalf("update not reflected: %q", upd.State)
	}

	if err := s.Update(ctx, &Actor{ID: "ghost"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update unknown: want ErrNotFound, got %v", err)
	}
	if _, err := s.Get(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get unknown: want ErrNotFound, got %v", err)
	}

	if err := s.Delete(ctx, "act_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Delete(ctx, "act_1"); err != nil {
		t.Fatalf("delete idempotent: %v", err)
	}
	if _, err := s.Get(ctx, "act_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}
