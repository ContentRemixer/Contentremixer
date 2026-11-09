package review

// schemaDDL is the forward-only, idempotent migration for the scraper-review
// tables (plan §3.3: Booth-owned Postgres schema in the live pgvector PG,
// ISI-7148). Every statement is CREATE … IF NOT EXISTS so Open can apply it on
// every startup, exactly like the persona_chunks schema (ISI-7146).
//
// These tables are the review working set only; accepted chunks are copied into
// persona_chunks (the retrieval corpus) by the accept path, they do not live
// here as vectors.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS scrape_jobs (
    id              TEXT         PRIMARY KEY,
    owner_subject   TEXT         NOT NULL,
    reference_repo  TEXT         NOT NULL,
    persona_name    TEXT         NOT NULL,
    source_url      TEXT         NOT NULL,
    source_kind     TEXT         NOT NULL,
    status          TEXT         NOT NULL,
    k8s_job_name    TEXT         NOT NULL DEFAULT '',
    error           TEXT         NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS scrape_jobs_owner_idx ON scrape_jobs (owner_subject);

CREATE TABLE IF NOT EXISTS scraped_sources (
    id              TEXT         PRIMARY KEY,
    job_id          TEXT         NOT NULL,
    owner_subject   TEXT         NOT NULL,
    reference_repo  TEXT         NOT NULL,
    persona_name    TEXT         NOT NULL,
    source_url      TEXT         NOT NULL,
    source_kind     TEXT         NOT NULL,
    title           TEXT         NOT NULL DEFAULT '',
    status          TEXT         NOT NULL,
    chunk_count     INT          NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS scraped_sources_owner_status_idx
    ON scraped_sources (owner_subject, status);
CREATE INDEX IF NOT EXISTS scraped_sources_job_idx ON scraped_sources (job_id);

CREATE TABLE IF NOT EXISTS scraped_chunks (
    id          TEXT   PRIMARY KEY,
    source_id   TEXT   NOT NULL REFERENCES scraped_sources (id) ON DELETE CASCADE,
    ordinal     INT    NOT NULL,
    text        TEXT   NOT NULL,
    metadata    JSONB  NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS scraped_chunks_source_idx ON scraped_chunks (source_id, ordinal);
`
