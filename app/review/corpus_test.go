package review

import (
	"context"
	"testing"
)

// The corpus guard branches run before any embedder/pgvector call, so they are
// unit-testable without live services: a missing owner/persona fails closed, and
// an empty chunk set is a no-op (a discarded-then-reaccepted empty source must
// not hit the gateway).
func TestPgvectorCorpus_Guards(t *testing.T) {
	c := &pgvectorCorpus{} // nil stage/vs: guards must return before touching them.

	if err := c.Ingest(context.Background(), ScrapedSource{PersonaName: "p"}, []ScrapedChunk{{Text: "x"}}); err == nil {
		t.Error("missing owner should fail closed")
	}
	if err := c.Ingest(context.Background(), ScrapedSource{OwnerSubject: "o"}, []ScrapedChunk{{Text: "x"}}); err == nil {
		t.Error("missing persona should fail closed")
	}
	if err := c.Ingest(context.Background(), ScrapedSource{OwnerSubject: "o", PersonaName: "p"}, nil); err != nil {
		t.Errorf("empty chunks should be a no-op, got %v", err)
	}
}
