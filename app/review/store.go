package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound means the named job or source does not exist. The Booth gating
// store maps it to HTTP 404.
var ErrNotFound = errors.New("review: not found")

// Corpus is the accept-time sink: the retrieval corpus that accepted chunks
// flow into (the persona pgvector store, ISI-7146). It is an interface so the
// review store is testable without a live embedder/pgvector, and so booth —
// which cannot construct the app/internal embed+store stages — wires the real
// one through NewCorpus (this package, app module) rather than naming them.
type Corpus interface {
	// Ingest embeds and upserts the source's chunks into the corpus under
	// (src.OwnerSubject, src.PersonaName). It MUST be idempotent per source
	// (replace-by-source upsert) so a retried accept is safe.
	Ingest(ctx context.Context, src ScrapedSource, chunks []ScrapedChunk) error
}

// Store is the pgx-backed persistence for the scraper-review tables. It applies
// NO authorization — the Booth gating store wraps it with the §6.3 predicate
// and audit. A nil Corpus disables AcceptSource (returns an error), which is the
// correct posture for the scrape-job writer that never accepts.
type Store struct {
	pool   *pgxpool.Pool
	corpus Corpus
}

// Option configures a Store at Open time.
type Option func(*Store)

// WithCorpus wires the accept-time corpus sink. The Booth API passes a real
// NewCorpus; the scrape-job writer omits it.
func WithCorpus(c Corpus) Option { return func(s *Store) { s.corpus = c } }

// Open dials the DSN, builds a pool, applies the idempotent schema, and returns
// a ready Store. The caller owns Close.
func Open(ctx context.Context, dsn string, opts ...Option) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("review: opening pool: %w", err)
	}
	s := &Store{pool: pool}
	for _, o := range opts {
		o(s)
	}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the pool.
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schemaDDL); err != nil {
		return fmt.Errorf("review: applying schema: %w", err)
	}
	return nil
}

// InsertJob persists a freshly-enqueued job (status queued). Owner must be set
// by the caller to the authorized subject.
func (s *Store) InsertJob(ctx context.Context, j ScrapeJob) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO scrape_jobs (id, owner_subject, reference_repo, persona_name, source_url, source_kind, status)
VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		j.ID, j.OwnerSubject, j.ReferenceRepo, j.PersonaName, j.SourceURL, j.SourceKind, string(JobQueued))
	if err != nil {
		return fmt.Errorf("review: insert job: %w", err)
	}
	return nil
}

// SetJobK8sName records the launched Job's name (observability / correlation).
func (s *Store) SetJobK8sName(ctx context.Context, id, k8sName string) error {
	return s.touchJob(ctx, id, `UPDATE scrape_jobs SET k8s_job_name=$2, updated_at=now() WHERE id=$1`, id, k8sName)
}

// SetJobStatus advances a job's lifecycle (used by the scrape-job pod). errMsg
// is recorded only for the failed status.
func (s *Store) SetJobStatus(ctx context.Context, id string, status JobStatus, errMsg string) error {
	return s.touchJob(ctx, id, `UPDATE scrape_jobs SET status=$2, error=$3, updated_at=now() WHERE id=$1`,
		id, string(status), errMsg)
}

func (s *Store) touchJob(ctx context.Context, id, sql string, args ...any) error {
	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("review: update job %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetJob loads one job by id.
func (s *Store) GetJob(ctx context.Context, id string) (ScrapeJob, error) {
	var j ScrapeJob
	var status string
	err := s.pool.QueryRow(ctx, `
SELECT id, owner_subject, reference_repo, persona_name, source_url, source_kind, status, k8s_job_name, error, created_at, updated_at
FROM scrape_jobs WHERE id=$1`, id).Scan(
		&j.ID, &j.OwnerSubject, &j.ReferenceRepo, &j.PersonaName, &j.SourceURL, &j.SourceKind,
		&status, &j.K8sJobName, &j.Error, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScrapeJob{}, ErrNotFound
	}
	if err != nil {
		return ScrapeJob{}, fmt.Errorf("review: get job %s: %w", id, err)
	}
	j.Status = JobStatus(status)
	return j, nil
}

// InsertSource persists one reviewable source and its preview chunks in a single
// transaction (status pending). chunk_count is derived from len(chunks).
func (s *Store) InsertSource(ctx context.Context, src ScrapedSource, chunks []ScrapedChunk) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("review: begin insert source: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
INSERT INTO scraped_sources (id, job_id, owner_subject, reference_repo, persona_name, source_url, source_kind, title, status, chunk_count)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		src.ID, src.JobID, src.OwnerSubject, src.ReferenceRepo, src.PersonaName,
		src.SourceURL, src.SourceKind, src.Title, string(SourcePending), len(chunks)); err != nil {
		return fmt.Errorf("review: insert source: %w", err)
	}
	for _, c := range chunks {
		meta, err := marshalMeta(c.Metadata)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO scraped_chunks (id, source_id, ordinal, text, metadata)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (id) DO UPDATE SET source_id=EXCLUDED.source_id, ordinal=EXCLUDED.ordinal, text=EXCLUDED.text, metadata=EXCLUDED.metadata`,
			c.ID, src.ID, c.Ordinal, c.Text, meta); err != nil {
			return fmt.Errorf("review: insert chunk %s: %w", c.ID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("review: commit insert source: %w", err)
	}
	return nil
}

// ListSources returns sources, newest first. ownerFilter scopes to one owner
// (the DevRel path); an empty ownerFilter returns every owner's rows (the Admin
// path). statusFilter, when non-empty, narrows to one review state. The owner
// predicate is applied in SQL — never by filtering a broad scan in Go.
func (s *Store) ListSources(ctx context.Context, ownerFilter string, statusFilter SourceStatus) ([]ScrapedSource, error) {
	sql := `SELECT id, job_id, owner_subject, reference_repo, persona_name, source_url, source_kind, title, status, chunk_count, created_at, updated_at FROM scraped_sources`
	var conds []string
	var args []any
	if ownerFilter != "" {
		args = append(args, ownerFilter)
		conds = append(conds, fmt.Sprintf("owner_subject=$%d", len(args)))
	}
	if statusFilter != "" {
		args = append(args, string(statusFilter))
		conds = append(conds, fmt.Sprintf("status=$%d", len(args)))
	}
	for i, c := range conds {
		if i == 0 {
			sql += " WHERE "
		} else {
			sql += " AND "
		}
		sql += c
	}
	sql += " ORDER BY created_at DESC"

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("review: list sources: %w", err)
	}
	defer rows.Close()

	var out []ScrapedSource
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// GetSource loads one source by id.
func (s *Store) GetSource(ctx context.Context, id string) (ScrapedSource, error) {
	row := s.pool.QueryRow(ctx, `
SELECT id, job_id, owner_subject, reference_repo, persona_name, source_url, source_kind, title, status, chunk_count, created_at, updated_at
FROM scraped_sources WHERE id=$1`, id)
	src, err := scanSource(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScrapedSource{}, ErrNotFound
	}
	return src, err
}

// GetSourceChunks returns the preview chunks of a source, in ordinal order.
func (s *Store) GetSourceChunks(ctx context.Context, sourceID string) ([]ScrapedChunk, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, source_id, ordinal, text, metadata FROM scraped_chunks WHERE source_id=$1 ORDER BY ordinal`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("review: get source chunks: %w", err)
	}
	defer rows.Close()

	var out []ScrapedChunk
	for rows.Next() {
		var c ScrapedChunk
		var meta []byte
		if err := rows.Scan(&c.ID, &c.SourceID, &c.Ordinal, &c.Text, &meta); err != nil {
			return nil, fmt.Errorf("review: scan chunk: %w", err)
		}
		if len(meta) > 0 {
			_ = json.Unmarshal(meta, &c.Metadata)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// setSourceStatus flips a source's state after validating the transition against
// the current server-side status (not the caller's assumption).
func (s *Store) setSourceStatus(ctx context.Context, id string, to SourceStatus) (ScrapedSource, error) {
	src, err := s.GetSource(ctx, id)
	if err != nil {
		return ScrapedSource{}, err
	}
	if !CanTransitionSource(src.Status, to) {
		return src, ErrInvalidTransition{From: src.Status, To: to}
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE scraped_sources SET status=$2, updated_at=now() WHERE id=$1`, id, string(to)); err != nil {
		return src, fmt.Errorf("review: set source status: %w", err)
	}
	src.Status = to
	src.UpdatedAt = time.Now()
	return src, nil
}

// DiscardSource marks a pending source discarded; its chunks never reach the corpus.
func (s *Store) DiscardSource(ctx context.Context, id string) (ScrapedSource, error) {
	return s.setSourceStatus(ctx, id, SourceDiscarded)
}

// MarkRescraping marks a pending source rescraping. The Booth store enqueues a
// fresh Job alongside this; the old row is left as an audit trail.
func (s *Store) MarkRescraping(ctx context.Context, id string) (ScrapedSource, error) {
	return s.setSourceStatus(ctx, id, SourceRescraping)
}

// AcceptSource ingests a pending source's chunks into the persona corpus, then
// marks it accepted. It fails if no Corpus is wired. Ingest runs BEFORE the
// status flip so a failed embedding leaves the source pending (retryable);
// Ingest is idempotent per source, so a retry after a flip failure is safe.
//
// ponytail: the corpus upsert (pgvector) and the status flip (review DB) are two
// stores and this is not a distributed transaction — a crash between them leaves
// a source pending with chunks already in the corpus; the next accept re-upserts
// idempotently and flips. Upgrade path: an outbox/2-phase marker if that window
// ever matters.
func (s *Store) AcceptSource(ctx context.Context, id string) (ScrapedSource, error) {
	if s.corpus == nil {
		return ScrapedSource{}, errors.New("review: accept unavailable (no corpus configured)")
	}
	src, err := s.GetSource(ctx, id)
	if err != nil {
		return ScrapedSource{}, err
	}
	if !CanTransitionSource(src.Status, SourceAccepted) {
		return src, ErrInvalidTransition{From: src.Status, To: SourceAccepted}
	}
	chunks, err := s.GetSourceChunks(ctx, id)
	if err != nil {
		return src, err
	}
	if err := s.corpus.Ingest(ctx, src, chunks); err != nil {
		return src, fmt.Errorf("review: ingest accepted source %s: %w", id, err)
	}
	return s.setSourceStatus(ctx, id, SourceAccepted)
}

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSource(r rowScanner) (ScrapedSource, error) {
	var src ScrapedSource
	var status string
	if err := r.Scan(&src.ID, &src.JobID, &src.OwnerSubject, &src.ReferenceRepo, &src.PersonaName,
		&src.SourceURL, &src.SourceKind, &src.Title, &status, &src.ChunkCount, &src.CreatedAt, &src.UpdatedAt); err != nil {
		return ScrapedSource{}, err
	}
	src.Status = SourceStatus(status)
	return src, nil
}

func marshalMeta(m map[string]any) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("review: marshal chunk metadata: %w", err)
	}
	return b, nil
}
