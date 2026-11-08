package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned by Store methods when the addressed row does not
// exist. The Booth gate surfaces it as HTTP 404.
var ErrNotFound = errors.New("content: not found")

// Store is the Postgres persistence for the content-collaboration domain. It
// performs NO authorization (see the package comment); the §6.3 gate lives in
// booth/internal/content.
type Store struct {
	pool *pgxpool.Pool
}

// Open dials the DSN, applies the idempotent schema (a dedicated "booth"
// schema), and returns a ready Store. The caller owns Close.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("content: parsing DSN: %w", err)
	}
	// Resolve unqualified table names against the booth schema first.
	cfg.ConnConfig.RuntimeParams["search_path"] = "booth,public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("content: connecting: %w", err)
	}
	if _, err := pool.Exec(ctx, schemaDDL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("content: applying schema: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the connection pool.
func (s *Store) Close() { s.pool.Close() }

// --- contents -------------------------------------------------------------

// InsertContent stores a new row as-is (ids and owners are assigned by the
// caller — the Booth gate stamps them from the session).
func (s *Store) InsertContent(ctx context.Context, c Content) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO booth.contents (id, owner_subject, title, body_md, status, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, now(), now())`,
		c.ID, c.OwnerSubject, c.Title, c.BodyMD, string(c.Status))
	if err != nil {
		return fmt.Errorf("content: insert content: %w", err)
	}
	return nil
}

// GetContent returns one row by id, or ErrNotFound.
func (s *Store) GetContent(ctx context.Context, id string) (Content, error) {
	row := s.pool.QueryRow(ctx, `
SELECT id, owner_subject, title, body_md, status, created_at, updated_at
  FROM booth.contents WHERE id = $1`, id)
	var c Content
	var status string
	if err := row.Scan(&c.ID, &c.OwnerSubject, &c.Title, &c.BodyMD, &status, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Content{}, ErrNotFound
		}
		return Content{}, fmt.Errorf("content: get content: %w", err)
	}
	c.Status = ContentStatus(status)
	return c, nil
}

// ListContents returns rows ordered newest-first. ownerFilter "" means all
// owners; the §6.3 filter is decided by the gate, which passes the caller's
// subject for a DevRel and "" for an Admin.
func (s *Store) ListContents(ctx context.Context, ownerFilter string) ([]Content, error) {
	q := `SELECT id, owner_subject, title, body_md, status, created_at, updated_at
  FROM booth.contents`
	args := []any{}
	if ownerFilter != "" {
		args = append(args, ownerFilter)
		q += fmt.Sprintf(" WHERE owner_subject = $%d", len(args))
	}
	q += " ORDER BY updated_at DESC"
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("content: list contents: %w", err)
	}
	defer rows.Close()
	var out []Content
	for rows.Next() {
		var c Content
		var status string
		if err := rows.Scan(&c.ID, &c.OwnerSubject, &c.Title, &c.BodyMD, &status, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("content: scan content: %w", err)
		}
		c.Status = ContentStatus(status)
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateContent patches the mutable columns of one row (owner is immutable —
// re-homing tenancy is not a supported operation) and returns the updated row.
func (s *Store) UpdateContent(ctx context.Context, id string, title, bodyMD *string, status *ContentStatus) (Content, error) {
	cur, err := s.GetContent(ctx, id)
	if err != nil {
		return Content{}, err
	}
	if title != nil {
		cur.Title = *title
	}
	if bodyMD != nil {
		cur.BodyMD = *bodyMD
	}
	if status != nil {
		cur.Status = *status
	}
	if _, err := s.pool.Exec(ctx, `
UPDATE booth.contents
   SET title = $2, body_md = $3, status = $4, updated_at = now()
 WHERE id = $1`,
		id, cur.Title, cur.BodyMD, string(cur.Status)); err != nil {
		return Content{}, fmt.Errorf("content: update content: %w", err)
	}
	return s.GetContent(ctx, id)
}

// DeleteContent removes one row; dependent comments/threads/requests cascade.
func (s *Store) DeleteContent(ctx context.Context, id string) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM booth.contents WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("content: delete content: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- comments -------------------------------------------------------------

// InsertComment stores a new anchored comment as-is.
func (s *Store) InsertComment(ctx context.Context, c Comment) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO booth.comments (id, content_id, author_subject, body, anchor_start, anchor_end, anchor_quote, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())`,
		c.ID, c.ContentID, c.AuthorSubject, c.Body, c.Anchor.Start, c.Anchor.End, c.Anchor.Quote)
	if err != nil {
		return fmt.Errorf("content: insert comment: %w", err)
	}
	return nil
}

// ListComments returns one document's comments oldest-first.
func (s *Store) ListComments(ctx context.Context, contentID string) ([]Comment, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, content_id, author_subject, body, anchor_start, anchor_end, anchor_quote, resolved, created_at
  FROM booth.comments WHERE content_id = $1 ORDER BY created_at ASC`, contentID)
	if err != nil {
		return nil, fmt.Errorf("content: list comments: %w", err)
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.ContentID, &c.AuthorSubject, &c.Body,
			&c.Anchor.Start, &c.Anchor.End, &c.Anchor.Quote, &c.Resolved, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("content: scan comment: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- review threads ---------------------------------------------------------

// InsertThread stores a thread and its first message in one transaction — a
// thread always opens with its opening message.
func (s *Store) InsertThread(ctx context.Context, t ReviewThread, first ReviewMessage) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("content: begin thread tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO booth.review_threads (id, content_id, subject, status, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, now(), now())`,
		t.ID, t.ContentID, t.Subject, string(t.Status), t.CreatedBy); err != nil {
		return fmt.Errorf("content: insert thread: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO booth.review_messages (id, thread_id, author_subject, body, created_at)
VALUES ($1, $2, $3, $4, now())`,
		first.ID, t.ID, first.AuthorSubject, first.Body); err != nil {
		return fmt.Errorf("content: insert opening message: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("content: commit thread tx: %w", err)
	}
	return nil
}

// GetThread returns one thread by id (without messages), or ErrNotFound.
func (s *Store) GetThread(ctx context.Context, id string) (ReviewThread, error) {
	row := s.pool.QueryRow(ctx, `
SELECT id, content_id, subject, status, created_by, created_at, updated_at
  FROM booth.review_threads WHERE id = $1`, id)
	var t ReviewThread
	var status string
	if err := row.Scan(&t.ID, &t.ContentID, &t.Subject, &status, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ReviewThread{}, ErrNotFound
		}
		return ReviewThread{}, fmt.Errorf("content: get thread: %w", err)
	}
	t.Status = ThreadStatus(status)
	return t, nil
}

// ListThreads returns one document's threads newest-first, each with its
// messages attached oldest-first.
func (s *Store) ListThreads(ctx context.Context, contentID string) ([]ReviewThread, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, content_id, subject, status, created_by, created_at, updated_at
  FROM booth.review_threads WHERE content_id = $1 ORDER BY updated_at DESC`, contentID)
	if err != nil {
		return nil, fmt.Errorf("content: list threads: %w", err)
	}
	defer rows.Close()
	var out []ReviewThread
	for rows.Next() {
		var t ReviewThread
		var status string
		if err := rows.Scan(&t.ID, &t.ContentID, &t.Subject, &status, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("content: scan thread: %w", err)
		}
		t.Status = ThreadStatus(status)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	msgs, err := s.messagesFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Messages = msgs[out[i].ID]
	}
	return out, nil
}

// messagesFor fetches the messages of the given threads in one round-trip,
// grouped by thread id, oldest-first.
func (s *Store) messagesFor(ctx context.Context, threadIDs []string) (map[string][]ReviewMessage, error) {
	out := make(map[string][]ReviewMessage)
	if len(threadIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, thread_id, author_subject, body, created_at
  FROM booth.review_messages WHERE thread_id = ANY($1) ORDER BY created_at ASC`, threadIDs)
	if err != nil {
		return nil, fmt.Errorf("content: list messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m ReviewMessage
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.AuthorSubject, &m.Body, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("content: scan message: %w", err)
		}
		out[m.ThreadID] = append(out[m.ThreadID], m)
	}
	return out, rows.Err()
}

// InsertMessage appends one message to a thread and bumps the thread's
// updated_at (the discussion's recency signal).
func (s *Store) InsertMessage(ctx context.Context, m ReviewMessage) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("content: begin message tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO booth.review_messages (id, thread_id, author_subject, body, created_at)
VALUES ($1, $2, $3, $4, now())`,
		m.ID, m.ThreadID, m.AuthorSubject, m.Body); err != nil {
		return fmt.Errorf("content: insert message: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE booth.review_threads SET updated_at = now() WHERE id = $1`, m.ThreadID); err != nil {
		return fmt.Errorf("content: touch thread: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("content: commit message tx: %w", err)
	}
	return nil
}

// SetThreadStatus updates a thread's open/resolved status.
func (s *Store) SetThreadStatus(ctx context.Context, id string, status ThreadStatus) error {
	ct, err := s.pool.Exec(ctx, `UPDATE booth.review_threads SET status = $2, updated_at = now() WHERE id = $1`, id, string(status))
	if err != nil {
		return fmt.Errorf("content: set thread status: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- illustration requests ---------------------------------------------------

// RequestFilter narrows ListIllustrationRequests. Empty fields mean "no
// filter". VisibleTo is the §6.3 work-queue visibility the gate computes: when
// set, only rows the subject owns OR is assigned to are returned ("" means all
// rows — used by the Admin path and the store integration tests).
type RequestFilter struct {
	Owner     string
	Assignee  string
	Status    IllustrationStatus
	VisibleTo string
}

// InsertIllustrationRequest stores a new request as-is.
func (s *Store) InsertIllustrationRequest(ctx context.Context, r IllustrationRequest) error {
	var anchor []byte
	if r.Anchor != nil {
		var err error
		anchor, err = json.Marshal(r.Anchor)
		if err != nil {
			return fmt.Errorf("content: marshal anchor: %w", err)
		}
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO booth.illustration_requests (id, content_id, owner_subject, assigned_agent, status, brief, anchor, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())`,
		r.ID, r.ContentID, r.OwnerSubject, r.AssignedAgent, string(r.Status), r.Brief, anchor); err != nil {
		return fmt.Errorf("content: insert illustration request: %w", err)
	}
	return nil
}

// GetIllustrationRequest returns one row by id, or ErrNotFound.
func (s *Store) GetIllustrationRequest(ctx context.Context, id string) (IllustrationRequest, error) {
	return s.scanRequest(s.pool.QueryRow(ctx, `
SELECT id, content_id, owner_subject, assigned_agent, status, brief, anchor, created_at, updated_at
  FROM booth.illustration_requests WHERE id = $1`, id))
}

// ListIllustrationRequests returns rows matching the filter, newest-first.
func (s *Store) ListIllustrationRequests(ctx context.Context, f RequestFilter) ([]IllustrationRequest, error) {
	q := `SELECT id, content_id, owner_subject, assigned_agent, status, brief, anchor, created_at, updated_at
  FROM booth.illustration_requests`
	var conds []string
	var args []any
	add := func(cond string, val any) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.Owner != "" {
		add("owner_subject = $%d", f.Owner)
	}
	if f.Assignee != "" {
		add("assigned_agent = $%d", f.Assignee)
	}
	if f.Status != "" {
		add("status = $%d", string(f.Status))
	}
	if f.VisibleTo != "" {
		args = append(args, f.VisibleTo)
		conds = append(conds, fmt.Sprintf("(owner_subject = $%d OR assigned_agent = $%d)", len(args), len(args)))
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY updated_at DESC"
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("content: list illustration requests: %w", err)
	}
	defer rows.Close()
	var out []IllustrationRequest
	for rows.Next() {
		r, err := s.scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// scanner is the shared row-scan surface of QueryRow and Rows.
type scanner interface{ Scan(dest ...any) error }

// scanRequest materializes one illustration_request row.
func (s *Store) scanRequest(row scanner) (IllustrationRequest, error) {
	var r IllustrationRequest
	var status string
	var anchor []byte
	if err := row.Scan(&r.ID, &r.ContentID, &r.OwnerSubject, &r.AssignedAgent, &status, &r.Brief, &anchor, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IllustrationRequest{}, ErrNotFound
		}
		return IllustrationRequest{}, fmt.Errorf("content: scan illustration request: %w", err)
	}
	r.Status = IllustrationStatus(status)
	if anchor != nil {
		var a Anchor
		if err := json.Unmarshal(anchor, &a); err != nil {
			return IllustrationRequest{}, fmt.Errorf("content: unmarshal anchor: %w", err)
		}
		r.Anchor = &a
	}
	return r, nil
}

// UpdateIllustrationRequest patches status and/or assignee and returns the
// updated row. Transition legality is the gate's decision, not the store's.
func (s *Store) UpdateIllustrationRequest(ctx context.Context, id string, status *IllustrationStatus, assignedAgent *string) (IllustrationRequest, error) {
	cur, err := s.GetIllustrationRequest(ctx, id)
	if err != nil {
		return IllustrationRequest{}, err
	}
	if status != nil {
		cur.Status = *status
	}
	if assignedAgent != nil {
		cur.AssignedAgent = *assignedAgent
	}
	if _, err := s.pool.Exec(ctx, `
UPDATE booth.illustration_requests
   SET status = $2, assigned_agent = $3, updated_at = now()
 WHERE id = $1`,
		id, string(cur.Status), cur.AssignedAgent); err != nil {
		return IllustrationRequest{}, fmt.Errorf("content: update illustration request: %w", err)
	}
	return s.GetIllustrationRequest(ctx, id)
}
