package content

import (
	"context"
	"os"
	"testing"
)

// These exercise the real SQL, transactions, filters, and cascade deletes
// against a live Postgres. They run only when CONTENT_TEST_DSN is set (same
// discipline as PGVECTOR_TEST_DSN / REVIEW_TEST_DSN); the tables are plain —
// no vector extension — so CI points this at the same Postgres service.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("CONTENT_TEST_DSN")
	if dsn == "" {
		t.Skip("CONTENT_TEST_DSN not set; skipping content store integration test")
	}
	st, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		// Isolate runs: drop the booth schema so repeated runs start clean.
		_, _ = st.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS booth CASCADE")
		st.Close()
	})
	return st
}

func TestStore_ContentLifecycle(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	c := Content{ID: "c1", OwnerSubject: "alice", Title: "T", BodyMD: "b", Status: ContentDraft}
	if err := st.InsertContent(ctx, c); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Open is idempotent (schema IF NOT EXISTS) — reopening must not fail.
	if _, err := Open(ctx, os.Getenv("CONTENT_TEST_DSN")); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	got, err := st.GetContent(ctx, "c1")
	if err != nil || got.Title != "T" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if _, err := st.GetContent(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("missing: want ErrNotFound, got %v", err)
	}

	if err := st.InsertContent(ctx, Content{ID: "c2", OwnerSubject: "bob", Title: "B"}); err != nil {
		t.Fatal(err)
	}
	aliceRows, err := st.ListContents(ctx, "alice")
	if err != nil || len(aliceRows) != 1 {
		t.Fatalf("list filtered: %v %+v", err, aliceRows)
	}
	allRows, err := st.ListContents(ctx, "")
	if err != nil || len(allRows) != 2 {
		t.Fatalf("list all: %v %+v", err, allRows)
	}

	newTitle := "T2"
	review := ContentInReview
	upd, err := st.UpdateContent(ctx, "c1", &newTitle, nil, &review)
	if err != nil || upd.Title != "T2" || upd.Status != review {
		t.Fatalf("update: %v %+v", err, upd)
	}
}

func TestStore_CommentsAndThreads(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.InsertContent(ctx, Content{ID: "c1", OwnerSubject: "alice", Title: "T"}); err != nil {
		t.Fatal(err)
	}

	cm := Comment{ID: "m1", ContentID: "c1", AuthorSubject: "alice", Body: "x", Anchor: Anchor{Start: 0, End: 2, Quote: "xx"}}
	if err := st.InsertComment(ctx, cm); err != nil {
		t.Fatalf("insert comment: %v", err)
	}
	cms, err := st.ListComments(ctx, "c1")
	if err != nil || len(cms) != 1 || cms[0].Anchor.End != 2 {
		t.Fatalf("list comments: %v %+v", err, cms)
	}

	th := ReviewThread{ID: "t1", ContentID: "c1", Subject: "s", Status: ThreadOpen, CreatedBy: "alice"}
	first := ReviewMessage{ID: "g1", ThreadID: "t1", AuthorSubject: "alice", Body: "first"}
	if err := st.InsertThread(ctx, th, first); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	if err := st.InsertMessage(ctx, ReviewMessage{ID: "g2", ThreadID: "t1", AuthorSubject: "bob", Body: "reply"}); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	threads, err := st.ListThreads(ctx, "c1")
	if err != nil || len(threads) != 1 || len(threads[0].Messages) != 2 {
		t.Fatalf("list threads: %v %+v", err, threads)
	}
	if err := st.SetThreadStatus(ctx, "t1", ThreadResolved); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got, err := st.GetThread(ctx, "t1")
	if err != nil || got.Status != ThreadResolved {
		t.Fatalf("get thread: %v %+v", err, got)
	}
}

func TestStore_IllustrationRequests(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.InsertContent(ctx, Content{ID: "c1", OwnerSubject: "alice", Title: "T"}); err != nil {
		t.Fatal(err)
	}

	r := IllustrationRequest{
		ID: "r1", ContentID: "c1", OwnerSubject: "alice",
		AssignedAgent: DefaultAssigneeAgent, Status: IllustrationRequested,
		Brief: "hero", Anchor: &Anchor{Start: 1, End: 4},
	}
	if err := st.InsertIllustrationRequest(ctx, r); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := st.InsertIllustrationRequest(ctx, IllustrationRequest{
		ID: "r2", ContentID: "c1", OwnerSubject: "bob", AssignedAgent: "agent-x",
		Status: IllustrationRequested, Brief: "second",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetIllustrationRequest(ctx, "r1")
	if err != nil || got.Anchor == nil || got.Anchor.Start != 1 {
		t.Fatalf("get (anchor round-trip): %v %+v", err, got)
	}
	if _, err := st.GetIllustrationRequest(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("missing: want ErrNotFound, got %v", err)
	}

	// Work-queue visibility: alice sees r1 (own) but not r2.
	vis, err := st.ListIllustrationRequests(ctx, RequestFilter{VisibleTo: "alice"})
	if err != nil || len(vis) != 1 || vis[0].ID != "r1" {
		t.Fatalf("visible-to: %v %+v", err, vis)
	}
	// Assignee queue.
	queue, err := st.ListIllustrationRequests(ctx, RequestFilter{Assignee: DefaultAssigneeAgent})
	if err != nil || len(queue) != 1 || queue[0].ID != "r1" {
		t.Fatalf("assignee queue: %v %+v", err, queue)
	}
	// Owner + status filters compose.
	delivered := IllustrationDelivered
	if _, err := st.UpdateIllustrationRequest(ctx, "r1", &delivered, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	byStatus, err := st.ListIllustrationRequests(ctx, RequestFilter{Owner: "alice", Status: delivered})
	if err != nil || len(byStatus) != 1 {
		t.Fatalf("owner+status: %v %+v", err, byStatus)
	}

	// Deleting the content cascades comments, threads, and requests.
	if err := st.DeleteContent(ctx, "c1"); err != nil {
		t.Fatalf("delete content: %v", err)
	}
	if _, err := st.GetIllustrationRequest(ctx, "r1"); err != ErrNotFound {
		t.Fatalf("cascade: want ErrNotFound, got %v", err)
	}
	if err := st.DeleteContent(ctx, "c1"); err != ErrNotFound {
		t.Fatalf("double delete: want ErrNotFound, got %v", err)
	}
}
