package substrate

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgSchemaDDL is the forward-only, idempotent migration for the actor store.
// The whole actor (spec + status) rides as JSONB so the walking skeleton can
// evolve the shape without a column migration; the scalar columns that index
// the hot lookups (id, state) are promoted out of the blob.
const pgSchemaDDL = `
CREATE TABLE IF NOT EXISTS substrate_actors (
    id          TEXT        PRIMARY KEY,
    state       TEXT        NOT NULL,
    doc         JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS substrate_actors_state_idx ON substrate_actors (state);
`

// PGStore is the Postgres-backed durable Store (contract §4.2). It reuses the
// repo's pgx/v5 + pgxpool convention (see internal/rag/store).
type PGStore struct {
	pool *pgxpool.Pool
}

// OpenPG connects, applies the idempotent schema, and returns a ready store.
func OpenPG(ctx context.Context, dsn string) (*PGStore, error) {
	boot, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("substrate store: connect: %w", err)
	}
	if _, err := boot.Exec(ctx, pgSchemaDDL); err != nil {
		_ = boot.Close(ctx)
		return nil, fmt.Errorf("substrate store: migrate: %w", err)
	}
	_ = boot.Close(ctx)

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("substrate store: parse dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("substrate store: pool: %w", err)
	}
	return &PGStore{pool: pool}, nil
}

// Close releases the pool.
func (s *PGStore) Close() { s.pool.Close() }

func (s *PGStore) Create(ctx context.Context, a *Actor) error {
	doc, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("substrate store: marshal: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO substrate_actors (id, state, doc, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		a.ID, string(a.State), doc, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("substrate store: insert: %w", err)
	}
	return nil
}

func (s *PGStore) Get(ctx context.Context, id string) (*Actor, error) {
	var doc []byte
	err := s.pool.QueryRow(ctx, `SELECT doc FROM substrate_actors WHERE id = $1`, id).Scan(&doc)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("substrate store: select: %w", err)
	}
	var a Actor
	if err := json.Unmarshal(doc, &a); err != nil {
		return nil, fmt.Errorf("substrate store: unmarshal: %w", err)
	}
	return &a, nil
}

func (s *PGStore) Update(ctx context.Context, a *Actor) error {
	a.UpdatedAt = time.Now()
	doc, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("substrate store: marshal: %w", err)
	}
	ct, err := s.pool.Exec(ctx,
		`UPDATE substrate_actors SET state = $2, doc = $3, updated_at = $4 WHERE id = $1`,
		a.ID, string(a.State), doc, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("substrate store: update: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PGStore) Delete(ctx context.Context, id string) error {
	// Idempotent: deleting an unknown id is not an error (contract §3.5).
	_, err := s.pool.Exec(ctx, `DELETE FROM substrate_actors WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("substrate store: delete: %w", err)
	}
	return nil
}

func (s *PGStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
