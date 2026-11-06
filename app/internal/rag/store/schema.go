package store

import "fmt"

// schemaDDL returns the forward-only, idempotent migration (design §4.1/§4.2) at
// the given vector dimension. Every statement is CREATE … IF NOT EXISTS so the
// operator can apply it on every startup. The embedding column width is the one
// knob that needs a rebuild + full re-index to change (§6/§11); it is baked into
// the DDL here so the column always matches the store's configured dimension.
func schemaDDL(dim int) string {
	return fmt.Sprintf(`
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS persona_chunks (
    id             TEXT         PRIMARY KEY,
    owner_subject  TEXT         NOT NULL,
    persona_name   TEXT         NOT NULL,
    source_url     TEXT         NOT NULL,
    source_kind    TEXT         NOT NULL,
    ordinal        INT          NOT NULL,
    text           TEXT         NOT NULL,
    embedding      vector(%d)   NOT NULL,
    metadata       JSONB        NOT NULL DEFAULT '{}',
    indexed_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Tenancy + replace-by-source lookups (the hot path for Upsert/Delete/Count).
CREATE INDEX IF NOT EXISTS persona_chunks_owner_persona_idx
    ON persona_chunks (owner_subject, persona_name);
CREATE INDEX IF NOT EXISTS persona_chunks_owner_persona_source_idx
    ON persona_chunks (owner_subject, persona_name, source_url);

-- ANN index for cosine similarity retrieval.
CREATE INDEX IF NOT EXISTS persona_chunks_embedding_idx
    ON persona_chunks USING hnsw (embedding vector_cosine_ops);
`, dim)
}
