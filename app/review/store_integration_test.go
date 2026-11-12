package review

import (
	"context"
	"os"
	"testing"
)

// These exercise the real SQL + transactions + accept ordering against a live
// Postgres. They run only when REVIEW_TEST_DSN is set (same discipline as the
// pgvector store's PGVECTOR_TEST_DSN); the tables are plain — no vector
// extension needed — so CI can point this at the same Postgres service.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("REVIEW_TEST_DSN")
	if dsn == "" {
		t.Skip("REVIEW_TEST_DSN not set; skipping review store integration test")
	}
	st, err := Open(context.Background(), dsn, WithCorpus(&fakeCorpus{}))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		// Isolate runs: drop our tables so repeated runs start clean.
		_, _ = st.pool.Exec(context.Background(), `DROP TABLE IF EXISTS style_profiles, scraped_chunks, scraped_sources, scrape_jobs CASCADE`)
		st.Close()
	})
	return st
}

type fakeCorpus struct{ ingested []string }

func (f *fakeCorpus) Ingest(_ context.Context, src ScrapedSource, chunks []ScrapedChunk) error {
	for range chunks {
		f.ingested = append(f.ingested, src.ID)
	}
	return nil
}

func TestStore_JobAndSourceLifecycle(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	job := ScrapeJob{ID: "job1", OwnerSubject: "alice", PersonaName: "p", SourceURL: "https://x/a", SourceKind: "article"}
	if err := st.InsertJob(ctx, job); err != nil {
		t.Fatalf("InsertJob: %v", err)
	}
	if err := st.SetJobStatus(ctx, "job1", JobRunning, ""); err != nil {
		t.Fatalf("SetJobStatus: %v", err)
	}
	got, err := st.GetJob(ctx, "job1")
	if err != nil || got.Status != JobRunning {
		t.Fatalf("GetJob = %+v, %v; want running", got, err)
	}

	src := ScrapedSource{ID: "src1", JobID: "job1", OwnerSubject: "alice", PersonaName: "p", SourceURL: "https://x/a", SourceKind: "article", Title: "A"}
	chunks := []ScrapedChunk{
		{ID: "c0", SourceID: "src1", Ordinal: 0, Text: "hello", Metadata: map[string]any{"k": "v"}},
		{ID: "c1", SourceID: "src1", Ordinal: 1, Text: "world"},
	}
	if err := st.InsertSource(ctx, src, chunks); err != nil {
		t.Fatalf("InsertSource: %v", err)
	}

	// Owner-scoped list finds alice's pending source; bob's does not.
	mine, err := st.ListSources(ctx, "alice", SourcePending)
	if err != nil || len(mine) != 1 || mine[0].ChunkCount != 2 {
		t.Fatalf("ListSources(alice,pending) = %+v, %v", mine, err)
	}
	if bobs, _ := st.ListSources(ctx, "bob", ""); len(bobs) != 0 {
		t.Fatalf("ListSources(bob) should be empty, got %+v", bobs)
	}

	gotChunks, err := st.GetSourceChunks(ctx, "src1")
	if err != nil || len(gotChunks) != 2 || gotChunks[0].Text != "hello" {
		t.Fatalf("GetSourceChunks = %+v, %v", gotChunks, err)
	}
	if gotChunks[0].Metadata["k"] != "v" {
		t.Errorf("chunk metadata not round-tripped: %+v", gotChunks[0].Metadata)
	}
}

func TestStore_AcceptIngestsThenFlips(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	corpus := st.corpus.(*fakeCorpus)

	_ = st.InsertJob(ctx, ScrapeJob{ID: "j", OwnerSubject: "alice", PersonaName: "p", SourceURL: "u", SourceKind: "article"})
	src := ScrapedSource{ID: "s", JobID: "j", OwnerSubject: "alice", PersonaName: "p", SourceURL: "u", SourceKind: "article"}
	if err := st.InsertSource(ctx, src, []ScrapedChunk{{ID: "c0", SourceID: "s", Text: "t"}}); err != nil {
		t.Fatalf("InsertSource: %v", err)
	}

	out, err := st.AcceptSource(ctx, "s")
	if err != nil {
		t.Fatalf("AcceptSource: %v", err)
	}
	if out.Status != SourceAccepted {
		t.Errorf("status = %q, want accepted", out.Status)
	}
	if len(corpus.ingested) != 1 {
		t.Errorf("corpus ingested %d chunks, want 1", len(corpus.ingested))
	}

	// Re-accepting an accepted source is an invalid transition.
	if _, err := st.AcceptSource(ctx, "s"); err == nil {
		t.Error("re-accept should fail the transition guard")
	}
}

func TestStore_DiscardAndRescrapeTransitions(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	_ = st.InsertJob(ctx, ScrapeJob{ID: "j", OwnerSubject: "a", PersonaName: "p", SourceURL: "u", SourceKind: "article"})
	_ = st.InsertSource(ctx, ScrapedSource{ID: "d", JobID: "j", OwnerSubject: "a", PersonaName: "p", SourceURL: "u", SourceKind: "article"}, nil)

	if _, err := st.DiscardSource(ctx, "d"); err != nil {
		t.Fatalf("DiscardSource: %v", err)
	}
	if _, err := st.MarkRescraping(ctx, "d"); err == nil {
		t.Error("rescraping a discarded source should fail the transition guard")
	}
	if _, err := st.GetSource(ctx, "missing"); err != ErrNotFound {
		t.Errorf("GetSource(missing) = %v, want ErrNotFound", err)
	}
}
