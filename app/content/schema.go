package content

// schemaDDL is the forward-only, idempotent migration for the content-
// collaboration tables (ISI-7208, plan §3.3: app-domain data in a Booth-owned
// Postgres schema, reusing the live pgvector instance, ISI-7148). Every
// statement is CREATE … IF NOT EXISTS so Booth can apply it on every startup —
// the same discipline as the rag and review stores.
//
// All tables live in the dedicated "booth" schema so the Booth-owned domain
// stays separate from the operator's persona corpus in public.
const schemaDDL = `
CREATE SCHEMA IF NOT EXISTS booth;

-- Working/draft documents (the DB half of §0.3; published artifacts go to
-- GitHub via the PR path, ISI-7085).
CREATE TABLE IF NOT EXISTS booth.contents (
    id            TEXT        PRIMARY KEY,
    owner_subject TEXT        NOT NULL,
    title         TEXT        NOT NULL,
    body_md       TEXT        NOT NULL DEFAULT '',
    status        TEXT        NOT NULL DEFAULT 'draft',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS contents_owner_idx ON booth.contents (owner_subject);

-- Anchored inline comments: text-range anchors over contents.body_md.
CREATE TABLE IF NOT EXISTS booth.comments (
    id             TEXT        PRIMARY KEY,
    content_id     TEXT        NOT NULL REFERENCES booth.contents(id) ON DELETE CASCADE,
    author_subject TEXT        NOT NULL,
    body           TEXT        NOT NULL,
    anchor_start   INT         NOT NULL,
    anchor_end     INT         NOT NULL,
    anchor_quote   TEXT        NOT NULL DEFAULT '',
    resolved       BOOLEAN     NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS comments_content_idx ON booth.comments (content_id);

-- Peer-review threads and their messages.
CREATE TABLE IF NOT EXISTS booth.review_threads (
    id         TEXT        PRIMARY KEY,
    content_id TEXT        NOT NULL REFERENCES booth.contents(id) ON DELETE CASCADE,
    subject    TEXT        NOT NULL,
    status     TEXT        NOT NULL DEFAULT 'open',
    created_by TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS review_threads_content_idx ON booth.review_threads (content_id);

CREATE TABLE IF NOT EXISTS booth.review_messages (
    id             TEXT        PRIMARY KEY,
    thread_id      TEXT        NOT NULL REFERENCES booth.review_threads(id) ON DELETE CASCADE,
    author_subject TEXT        NOT NULL,
    body           TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS review_messages_thread_idx ON booth.review_messages (thread_id);

-- Illustration requests + the assign-to-agent work queue (assigned_agent is
-- the queue key: GET /v1/illustration-requests?assignee=…).
CREATE TABLE IF NOT EXISTS booth.illustration_requests (
    id             TEXT        PRIMARY KEY,
    content_id     TEXT        NOT NULL REFERENCES booth.contents(id) ON DELETE CASCADE,
    owner_subject  TEXT        NOT NULL,
    assigned_agent TEXT        NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'requested',
    brief          TEXT        NOT NULL,
    anchor         JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS illustration_requests_assignee_idx
    ON booth.illustration_requests (assigned_agent, status);
CREATE INDEX IF NOT EXISTS illustration_requests_owner_idx
    ON booth.illustration_requests (owner_subject);
`
