// Package store is P2-C's (ISI-7146) storage stage (design §4): the pgvector
// implementation of rag.VectorStore (decision #6). All persona voice chunks are
// stored in, and retrieved from, one Postgres table behind this adapter, and
// per-persona tenant isolation is a plain SQL `WHERE owner_subject = $1`
// predicate present in EVERY read and delete (§8). A future Qdrant swap
// replaces only this package.
//
// The operator embeds no vector-DB client beyond pgx/v5 + pgvector-go. Vectors
// are passed as pgvector.Vector parameters with an explicit `::vector` cast so
// pgx needs no custom type registration; the frozen retrieval SELECT (§4.1)
// never reads a vector back.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

// DefaultDim is the embedding/column dimension the schema is built for
// (§4.1/§6): 1536 = text-embedding-3-small. It MUST equal embed.DefaultDim (the
// embed stage's output dimension) or stored and query vectors will not share a
// space; the two are kept in sync by the controller that wires them, not by a
// cross-stage import (stages stay independent per the rag contract).
const DefaultDim = 1536

// Store is the pgvector-backed rag.VectorStore.
type Store struct {
	pool *pgxpool.Pool
	dim  int
}

var _ rag.VectorStore = (*Store)(nil)

// Open dials the DSN, builds a connection pool, applies the idempotent schema
// migration (§4.2), and returns a ready Store. dim <= 0 defaults to DefaultDim;
// it must equal the embedding model's output dimension (§6). The caller owns
// Close.
func Open(ctx context.Context, dsn string, dim int) (*Store, error) {
	if dim <= 0 {
		dim = DefaultDim
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: parsing DSN: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: opening pool: %w", err)
	}
	s := &Store{pool: pool, dim: dim}
	if err := s.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// NewWithPool wraps an existing pool (for tests / shared pools). It does NOT run
// the migration; call Migrate explicitly. dim <= 0 defaults to DefaultDim.
func NewWithPool(pool *pgxpool.Pool, dim int) *Store {
	if dim <= 0 {
		dim = DefaultDim
	}
	return &Store{pool: pool, dim: dim}
}

// Dim reports the vector dimension this store enforces.
func (s *Store) Dim() int { return s.dim }

// Close releases the pool.
func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

// Migrate applies the forward-only, idempotent schema (§4.2): the vector
// extension, the persona_chunks table at this store's dimension, the tenancy
// indexes, and the HNSW cosine ANN index. Safe to call on every operator
// startup.
func (s *Store) Migrate(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("store: Migrate on nil pool")
	}
	if _, err := s.pool.Exec(ctx, schemaDDL(s.dim)); err != nil {
		return fmt.Errorf("store: applying schema: %w", err)
	}
	return nil
}

// Upsert implements rag.VectorStore: replace-by-source within a single
// transaction. Validation (non-empty owner, dimension) runs up front over all
// chunks so a bad batch is rejected before any row is touched (fail closed).
func (s *Store) Upsert(ctx context.Context, chunks []rag.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	// Up-front validation — reject the whole batch before touching the pool or
	// mutating anything. The tenancy (owner) guard is a fail-closed security
	// invariant and is evaluated before the pool is even dereferenced.
	for i := range chunks {
		c := &chunks[i]
		if strings.TrimSpace(c.PersonaOwner) == "" {
			return fmt.Errorf("store: chunk %q has empty PersonaOwner (tenancy key required)", c.ID)
		}
		if strings.TrimSpace(c.PersonaName) == "" {
			return fmt.Errorf("store: chunk %q has empty PersonaName", c.ID)
		}
		if len(c.Embedding) != s.dim {
			return fmt.Errorf("store: chunk %q embedding dimension %d != store dimension %d", c.ID, len(c.Embedding), s.dim)
		}
	}
	if s == nil || s.pool == nil {
		return fmt.Errorf("store: Upsert on nil pool")
	}

	// Distinct (owner, persona, source_url) tuples whose existing rows are
	// cleared before re-insert (replace-by-source, §2.3 step 4).
	type srcKey struct{ owner, persona, url string }
	seen := map[srcKey]bool{}
	var sources []srcKey
	for i := range chunks {
		k := srcKey{chunks[i].PersonaOwner, chunks[i].PersonaName, chunks[i].SourceURL}
		if !seen[k] {
			seen[k] = true
			sources = append(sources, k)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback is a no-op after commit

	for _, k := range sources {
		if _, err := tx.Exec(ctx,
			`DELETE FROM persona_chunks WHERE owner_subject = $1 AND persona_name = $2 AND source_url = $3`,
			k.owner, k.persona, k.url,
		); err != nil {
			return fmt.Errorf("store: clearing source %q: %w", k.url, err)
		}
	}

	for i := range chunks {
		c := &chunks[i]
		meta := c.Metadata
		if meta == nil {
			meta = map[string]any{}
		}
		metaJSON, err := json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("store: marshalling metadata for chunk %q: %w", c.ID, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO persona_chunks
			   (id, owner_subject, persona_name, source_url, source_kind, ordinal, text, embedding, metadata)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8::vector,$9)
			 ON CONFLICT (id) DO UPDATE SET
			   owner_subject = EXCLUDED.owner_subject,
			   persona_name  = EXCLUDED.persona_name,
			   source_url    = EXCLUDED.source_url,
			   source_kind   = EXCLUDED.source_kind,
			   ordinal       = EXCLUDED.ordinal,
			   text          = EXCLUDED.text,
			   embedding     = EXCLUDED.embedding,
			   metadata      = EXCLUDED.metadata,
			   indexed_at    = now()`,
			c.ID, c.PersonaOwner, c.PersonaName, c.SourceURL, c.SourceKind, c.Ordinal, c.Text,
			pgvector.NewVector(c.Embedding), string(metaJSON),
		); err != nil {
			return fmt.Errorf("store: inserting chunk %q: %w", c.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// Query implements rag.VectorStore: the frozen owner-scoped SELECT (§4.1). The
// owner_subject predicate is always present; an empty OwnerSubject fails closed
// before any SQL is built.
func (s *Store) Query(ctx context.Context, q rag.Query) ([]rag.Match, error) {
	if strings.TrimSpace(q.OwnerSubject) == "" {
		return nil, fmt.Errorf("store: Query with empty OwnerSubject (refusing cross-tenant scan)")
	}
	if strings.TrimSpace(q.PersonaName) == "" {
		return nil, fmt.Errorf("store: Query with empty PersonaName")
	}
	if len(q.Embedding) != s.dim {
		return nil, fmt.Errorf("store: Query embedding dimension %d != store dimension %d", len(q.Embedding), s.dim)
	}
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("store: Query on nil pool")
	}
	topK := q.TopK
	if topK <= 0 {
		topK = 1
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, owner_subject, persona_name, source_url, source_kind, ordinal, text, metadata,
		        1 - (embedding <=> $3::vector) AS score
		   FROM persona_chunks
		  WHERE owner_subject = $1
		    AND persona_name  = $2
		    AND 1 - (embedding <=> $3::vector) >= $5
		  ORDER BY embedding <=> $3::vector
		  LIMIT $4`,
		q.OwnerSubject, q.PersonaName, pgvector.NewVector(q.Embedding), topK, q.MinScore,
	)
	if err != nil {
		return nil, fmt.Errorf("store: query: %w", err)
	}
	defer rows.Close()

	var matches []rag.Match
	for rows.Next() {
		var (
			m        rag.Match
			metaJSON []byte
		)
		if err := rows.Scan(
			&m.ID, &m.PersonaOwner, &m.PersonaName, &m.SourceURL,
			&m.SourceKind, &m.Ordinal, &m.Text, &metaJSON, &m.Score,
		); err != nil {
			return nil, fmt.Errorf("store: scanning match: %w", err)
		}
		if len(metaJSON) > 0 {
			_ = json.Unmarshal(metaJSON, &m.Metadata)
		}
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating matches: %w", err)
	}
	return matches, nil
}

// DeletePersona implements rag.VectorStore: purge all chunks for one persona
// (finalizer path). Empty ownerSubject fails closed.
func (s *Store) DeletePersona(ctx context.Context, ownerSubject, personaName string) error {
	if strings.TrimSpace(ownerSubject) == "" {
		return fmt.Errorf("store: DeletePersona with empty ownerSubject (refusing cross-tenant delete)")
	}
	if strings.TrimSpace(personaName) == "" {
		return fmt.Errorf("store: DeletePersona with empty personaName")
	}
	if s == nil || s.pool == nil {
		return fmt.Errorf("store: DeletePersona on nil pool")
	}
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM persona_chunks WHERE owner_subject = $1 AND persona_name = $2`,
		ownerSubject, personaName,
	); err != nil {
		return fmt.Errorf("store: delete persona: %w", err)
	}
	return nil
}

// CountPersona implements rag.VectorStore: stored chunk count for
// status.chunkCount. Empty ownerSubject fails closed.
func (s *Store) CountPersona(ctx context.Context, ownerSubject, personaName string) (int, error) {
	if strings.TrimSpace(ownerSubject) == "" {
		return 0, fmt.Errorf("store: CountPersona with empty ownerSubject (refusing cross-tenant scan)")
	}
	if strings.TrimSpace(personaName) == "" {
		return 0, fmt.Errorf("store: CountPersona with empty personaName")
	}
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("store: CountPersona on nil pool")
	}
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM persona_chunks WHERE owner_subject = $1 AND persona_name = $2`,
		ownerSubject, personaName,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count persona: %w", err)
	}
	return n, nil
}
