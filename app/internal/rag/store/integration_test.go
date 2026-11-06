package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

// Integration test against a live Postgres with the `vector` extension. Skipped
// unless PGVECTOR_TEST_DSN is set (e.g. a CI postgres service or a local
// container). It proves the frozen SQL end-to-end: migrate, replace-by-source
// upsert, owner-scoped retrieval, cross-tenant isolation, count, and delete.
//
//	docker run --rm -e POSTGRES_PASSWORD=pw -p 5432:5432 pgvector/pgvector:pg16
//	PGVECTOR_TEST_DSN='postgres://postgres:pw@localhost:5432/postgres' go test ./internal/rag/store/...
func TestIntegration_PgvectorRoundTrip(t *testing.T) {
	dsn := os.Getenv("PGVECTOR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGVECTOR_TEST_DSN not set; skipping live pgvector integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const dim = 4
	s, err := Open(ctx, dsn, dim)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	// Clean slate for the two tenants this test uses.
	for _, o := range []string{"alice", "bob"} {
		_ = s.DeletePersona(ctx, o, "devrel")
	}

	vec := func(a, b, c, d float32) []float32 { return []float32{a, b, c, d} }
	aliceChunks := []rag.Chunk{
		{ID: "a1", PersonaOwner: "alice", PersonaName: "devrel", SourceURL: "u1", SourceKind: "article", Ordinal: 0, Text: "near", Embedding: vec(1, 0, 0, 0), Metadata: map[string]any{"title": "t"}},
		{ID: "a2", PersonaOwner: "alice", PersonaName: "devrel", SourceURL: "u1", SourceKind: "article", Ordinal: 1, Text: "far", Embedding: vec(0, 1, 0, 0)},
	}
	bobChunks := []rag.Chunk{
		{ID: "b1", PersonaOwner: "bob", PersonaName: "devrel", SourceURL: "u1", SourceKind: "article", Ordinal: 0, Text: "bob-near", Embedding: vec(1, 0, 0, 0)},
	}
	if err := s.Upsert(ctx, aliceChunks); err != nil {
		t.Fatalf("Upsert alice: %v", err)
	}
	if err := s.Upsert(ctx, bobChunks); err != nil {
		t.Fatalf("Upsert bob: %v", err)
	}

	// Owner-scoped query: alice must never see bob's row even with identical vec.
	matches, err := s.Query(ctx, rag.Query{OwnerSubject: "alice", PersonaName: "devrel", Embedding: vec(1, 0, 0, 0), TopK: 10})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 alice matches, got %d", len(matches))
	}
	if matches[0].ID != "a1" {
		t.Errorf("nearest should be a1, got %s", matches[0].ID)
	}
	for _, m := range matches {
		if m.PersonaOwner != "alice" {
			t.Fatalf("cross-tenant leak: got owner %q", m.PersonaOwner)
		}
	}
	if matches[0].Score < 0.99 {
		t.Errorf("identical vector should score ~1.0, got %f", matches[0].Score)
	}

	// Count is owner-scoped.
	if n, _ := s.CountPersona(ctx, "alice", "devrel"); n != 2 {
		t.Errorf("alice count = %d, want 2", n)
	}
	if n, _ := s.CountPersona(ctx, "bob", "devrel"); n != 1 {
		t.Errorf("bob count = %d, want 1", n)
	}

	// Replace-by-source: re-index u1 with a single chunk → alice drops to 1.
	if err := s.Upsert(ctx, aliceChunks[:1]); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if n, _ := s.CountPersona(ctx, "alice", "devrel"); n != 1 {
		t.Errorf("after replace-by-source alice count = %d, want 1", n)
	}

	// MinScore filters out the orthogonal direction.
	far, _ := s.Query(ctx, rag.Query{OwnerSubject: "alice", PersonaName: "devrel", Embedding: vec(0, 0, 1, 0), TopK: 10, MinScore: 0.5})
	if len(far) != 0 {
		t.Errorf("MinScore should drop orthogonal matches, got %d", len(far))
	}

	// DeletePersona purges only that tenant.
	if err := s.DeletePersona(ctx, "alice", "devrel"); err != nil {
		t.Fatalf("DeletePersona: %v", err)
	}
	if n, _ := s.CountPersona(ctx, "alice", "devrel"); n != 0 {
		t.Errorf("alice count after delete = %d, want 0", n)
	}
	if n, _ := s.CountPersona(ctx, "bob", "devrel"); n != 1 {
		t.Errorf("bob must be untouched by alice delete, count = %d", n)
	}
	_ = s.DeletePersona(ctx, "bob", "devrel")
}
