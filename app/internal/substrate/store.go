package substrate

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNotFound is returned by a Store when an actor id is unknown. Handlers map
// it to 404.
var ErrNotFound = errors.New("substrate: actor not found")

// Store is the durable record layer for actors (contract §4.2: survives
// control-plane pod restarts). The Postgres implementation is the production
// store; MemStore backs the unit tests.
type Store interface {
	// Create persists a new actor. The caller sets ID + CreatedAt/UpdatedAt.
	Create(ctx context.Context, a *Actor) error
	// Get returns the actor by id, or ErrNotFound.
	Get(ctx context.Context, id string) (*Actor, error)
	// Update replaces the mutable status fields of an existing actor.
	Update(ctx context.Context, a *Actor) error
	// Delete removes an actor record. Deleting an unknown id is not an error
	// (idempotent terminate, contract §3.5).
	Delete(ctx context.Context, id string) error
	// Ping checks store liveness for the readiness probe.
	Ping(ctx context.Context) error
}

// clock lets tests inject deterministic timestamps.
type clock func() time.Time

// MemStore is an in-memory Store for tests. Safe for concurrent use.
type MemStore struct {
	mu  sync.Mutex
	m   map[string]Actor
	now clock
}

// NewMemStore builds an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{m: make(map[string]Actor), now: time.Now}
}

func (s *MemStore) Create(_ context.Context, a *Actor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[a.ID]; ok {
		return errors.New("substrate: actor already exists")
	}
	s.m[a.ID] = *a // store a copy so callers can't mutate behind our back
	return nil
}

func (s *MemStore) Get(_ context.Context, id string) (*Actor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.m[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := a
	return &cp, nil
}

func (s *MemStore) Update(_ context.Context, a *Actor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[a.ID]; !ok {
		return ErrNotFound
	}
	s.m[a.ID] = *a
	return nil
}

func (s *MemStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

func (s *MemStore) Ping(_ context.Context) error { return nil }
