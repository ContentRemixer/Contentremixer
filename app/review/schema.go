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

-- style_profiles holds the synthesized, DevRel-reviewable writing-style profile
-- per (owner, persona) (plan §10 decision 1, ISI-7356 §4). Style synthesis (D3,
-- ISI-7358) upserts it; the content-test endpoint (D4, ISI-7359) reads Traits to
-- condition a test generation, and the persona-validate action (D4/D7) flips
-- validated — the Postgres source of truth whose cheap declarative mirror is the
-- DevRelPersona "Validated" status condition. One row per (owner, persona).
CREATE TABLE IF NOT EXISTS style_profiles (
    owner_subject   TEXT         NOT NULL,
    persona_name    TEXT         NOT NULL,
    reference_repo  TEXT         NOT NULL DEFAULT '',
    traits          TEXT         NOT NULL DEFAULT '',
    sample          TEXT         NOT NULL DEFAULT '',
    validated       BOOLEAN      NOT NULL DEFAULT false,
    validated_at    TIMESTAMPTZ,
    source_count    INT          NOT NULL DEFAULT 0,
    synthesized_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_subject, persona_name)
);
`
