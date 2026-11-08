package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ContentRemixer/Contentremixer/app/review"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/scrapejobs"
)

// fakeReviewDB is an in-memory persistence for the server-level handler tests.
type fakeReviewDB struct {
	jobs    map[string]review.ScrapeJob
	sources map[string]review.ScrapedSource
}

func (f *fakeReviewDB) InsertJob(_ context.Context, j review.ScrapeJob) error {
	f.jobs[j.ID] = j
	return nil
}
func (f *fakeReviewDB) SetJobK8sName(_ context.Context, id, n string) error {
	j := f.jobs[id]
	j.K8sJobName = n
	f.jobs[id] = j
	return nil
}
func (f *fakeReviewDB) SetJobStatus(_ context.Context, id string, s review.JobStatus, m string) error {
	j := f.jobs[id]
	j.Status, j.Error = s, m
	f.jobs[id] = j
	return nil
}
func (f *fakeReviewDB) GetJob(_ context.Context, id string) (review.ScrapeJob, error) {
	j, ok := f.jobs[id]
	if !ok {
		return review.ScrapeJob{}, review.ErrNotFound
	}
	return j, nil
}
func (f *fakeReviewDB) ListSources(_ context.Context, owner string, st review.SourceStatus) ([]review.ScrapedSource, error) {
	var out []review.ScrapedSource
	for _, s := range f.sources {
		if owner != "" && s.OwnerSubject != owner {
			continue
		}
		if st != "" && s.Status != st {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}
func (f *fakeReviewDB) GetSource(_ context.Context, id string) (review.ScrapedSource, error) {
	s, ok := f.sources[id]
	if !ok {
		return review.ScrapedSource{}, review.ErrNotFound
	}
	return s, nil
}
func (f *fakeReviewDB) GetSourceChunks(_ context.Context, id string) ([]review.ScrapedChunk, error) {
	return []review.ScrapedChunk{{ID: "c0", SourceID: id, Text: "t"}}, nil
}
func (f *fakeReviewDB) AcceptSource(_ context.Context, id string) (review.ScrapedSource, error) {
	s := f.sources[id]
	if !review.CanTransitionSource(s.Status, review.SourceAccepted) {
		return s, review.ErrInvalidTransition{From: s.Status, To: review.SourceAccepted}
	}
	s.Status = review.SourceAccepted
	f.sources[id] = s
	return s, nil
}
func (f *fakeReviewDB) DiscardSource(_ context.Context, id string) (review.ScrapedSource, error) {
	s := f.sources[id]
	s.Status = review.SourceDiscarded
	f.sources[id] = s
	return s, nil
}
func (f *fakeReviewDB) MarkRescraping(_ context.Context, id string) (review.ScrapedSource, error) {
	s := f.sources[id]
	s.Status = review.SourceRescraping
	f.sources[id] = s
	return s, nil
}

type noopLauncher struct{}

func (noopLauncher) Launch(_ context.Context, spec scrapejobs.JobSpec) (string, error) {
	return "scrape-" + spec.JobID[:4], nil
}

func newScrapeServer(t *testing.T) (http.Handler, *auth.TokenCodec, *fakeReviewDB) {
	t.Helper()
	adminHash, err := auth.HashPassword("admin-pw")
	if err != nil {
		t.Fatal(err)
	}
	prov, err := auth.Bootstrap(auth.Config{
		SessionKey: testKey, SessionTTL: time.Hour, AdminUsername: "admin", AdminHash: adminHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	db := &fakeReviewDB{jobs: map[string]review.ScrapeJob{}, sources: map[string]review.ScrapedSource{}}
	// Seed a pending source owned by alice.
	db.sources["s1"] = review.ScrapedSource{ID: "s1", OwnerSubject: "alice", SourceURL: "u", SourceKind: "article", PersonaName: "p", Status: review.SourcePending}
	scrape := scrapejobs.New(db, noopLauncher{}, audit.NewSlogLogger(nil))
	return New(prov, nil, scrape, nil, nil).Handler(), prov.Codec, db
}

func TestScrapeJob_CreateAndGet(t *testing.T) {
	h, codec, _ := newScrapeServer(t)
	alice := tokenFor(t, codec, "alice", auth.RoleDevRel)

	w := do(t, h, "POST", "/v1/scrape-jobs", alice, `{"personaName":"p","sourceUrl":"https://x/a","sourceKind":"article"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var job review.ScrapeJob
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.OwnerSubject != "alice" {
		t.Errorf("owner = %q, want alice", job.OwnerSubject)
	}
	if w := do(t, h, "GET", "/v1/scrape-jobs/"+job.ID, alice, ""); w.Code != http.StatusOK {
		t.Fatalf("get job = %d, want 200", w.Code)
	}
	// Another DevRel cannot read alice's job.
	bob := tokenFor(t, codec, "bob", auth.RoleDevRel)
	if w := do(t, h, "GET", "/v1/scrape-jobs/"+job.ID, bob, ""); w.Code != http.StatusForbidden {
		t.Fatalf("bob get alice job = %d, want 403", w.Code)
	}
}

func TestScrapeJob_CreateBadKind(t *testing.T) {
	h, codec, _ := newScrapeServer(t)
	alice := tokenFor(t, codec, "alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/scrape-jobs", alice, `{"personaName":"p","sourceUrl":"u","sourceKind":"pdf"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad kind = %d, want 400", w.Code)
	}
	if w := do(t, h, "POST", "/v1/scrape-jobs", alice, "{not json"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad json = %d, want 400", w.Code)
	}
}

func TestScrapedSources_ListAndChunks(t *testing.T) {
	h, codec, _ := newScrapeServer(t)
	alice := tokenFor(t, codec, "alice", auth.RoleDevRel)
	bob := tokenFor(t, codec, "bob", auth.RoleDevRel)

	// Alice sees her pending source; bob sees none (server-side scoping).
	w := do(t, h, "GET", "/v1/scraped-sources?status=pending", alice, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d", w.Code)
	}
	var list []review.ScrapedSource
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("alice list = %d rows, want 1", len(list))
	}
	w = do(t, h, "GET", "/v1/scraped-sources", bob, "")
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 0 {
		t.Fatalf("bob list = %d rows, want 0", len(list))
	}

	// Chunk preview gated: alice 200, bob 403.
	if w := do(t, h, "GET", "/v1/scraped-sources/s1/chunks", alice, ""); w.Code != http.StatusOK {
		t.Fatalf("alice chunks = %d, want 200", w.Code)
	}
	if w := do(t, h, "GET", "/v1/scraped-sources/s1/chunks", bob, ""); w.Code != http.StatusForbidden {
		t.Fatalf("bob chunks = %d, want 403", w.Code)
	}
	// Missing source → 404.
	if w := do(t, h, "GET", "/v1/scraped-sources/nope/chunks", alice, ""); w.Code != http.StatusNotFound {
		t.Fatalf("missing source chunks = %d, want 404", w.Code)
	}
}

func TestScrapedSources_ReviewActions(t *testing.T) {
	h, codec, db := newScrapeServer(t)
	alice := tokenFor(t, codec, "alice", auth.RoleDevRel)

	// Rescrape (pending → rescraping) returns 202 + a new job.
	if w := do(t, h, "POST", "/v1/scraped-sources/s1/rescrape", alice, ""); w.Code != http.StatusAccepted {
		t.Fatalf("rescrape = %d, want 202", w.Code)
	}

	// Reset to pending and discard.
	db.sources["s1"] = review.ScrapedSource{ID: "s1", OwnerSubject: "alice", Status: review.SourcePending}
	if w := do(t, h, "POST", "/v1/scraped-sources/s1/discard", alice, ""); w.Code != http.StatusOK {
		t.Fatalf("discard = %d, want 200", w.Code)
	}
	// Accepting a now-discarded source is a 409 conflict.
	if w := do(t, h, "POST", "/v1/scraped-sources/s1/accept", alice, ""); w.Code != http.StatusConflict {
		t.Fatalf("accept discarded = %d, want 409", w.Code)
	}

	// Admin can accept a pending source (ingest path via nil corpus would fail,
	// so seed an already-accepted-safe pending with a fake that accepts cleanly).
	db.sources["s2"] = review.ScrapedSource{ID: "s2", OwnerSubject: "carol", Status: review.SourcePending}
	admin := tokenFor(t, codec, "root", auth.RoleAdmin)
	if w := do(t, h, "POST", "/v1/scraped-sources/s2/accept", admin, ""); w.Code != http.StatusOK {
		t.Fatalf("admin accept = %d, want 200", w.Code)
	}
}

func TestScrapeRoutes_Unauthenticated(t *testing.T) {
	h, _, _ := newScrapeServer(t)
	if w := do(t, h, "GET", "/v1/scraped-sources", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", w.Code)
	}
}
