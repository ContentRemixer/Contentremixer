package scrapejobs

import (
	"context"
	"errors"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/review"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
)

// fakeDB is an in-memory persistence satisfying the persistence interface, so
// the §6.3 gate is tested without Postgres.
type fakeDB struct {
	jobs    map[string]review.ScrapeJob
	sources map[string]review.ScrapedSource
	chunks  map[string][]review.ScrapedChunk
	// listOwnerFilter records the ownerFilter the gate passed to ListSources,
	// proving the filter is applied SERVER-SIDE.
	listOwnerFilter string
	accepted        []string
}

func newFakeDB() *fakeDB {
	return &fakeDB{jobs: map[string]review.ScrapeJob{}, sources: map[string]review.ScrapedSource{}, chunks: map[string][]review.ScrapedChunk{}}
}

func (f *fakeDB) InsertJob(_ context.Context, j review.ScrapeJob) error {
	f.jobs[j.ID] = j
	return nil
}
func (f *fakeDB) SetJobK8sName(_ context.Context, id, name string) error {
	j := f.jobs[id]
	j.K8sJobName = name
	f.jobs[id] = j
	return nil
}
func (f *fakeDB) SetJobStatus(_ context.Context, id string, st review.JobStatus, msg string) error {
	j := f.jobs[id]
	j.Status = st
	j.Error = msg
	f.jobs[id] = j
	return nil
}
func (f *fakeDB) GetJob(_ context.Context, id string) (review.ScrapeJob, error) {
	j, ok := f.jobs[id]
	if !ok {
		return review.ScrapeJob{}, review.ErrNotFound
	}
	return j, nil
}
func (f *fakeDB) ListSources(_ context.Context, ownerFilter string, status review.SourceStatus) ([]review.ScrapedSource, error) {
	f.listOwnerFilter = ownerFilter
	var out []review.ScrapedSource
	for _, s := range f.sources {
		if ownerFilter != "" && s.OwnerSubject != ownerFilter {
			continue
		}
		if status != "" && s.Status != status {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}
func (f *fakeDB) GetSource(_ context.Context, id string) (review.ScrapedSource, error) {
	s, ok := f.sources[id]
	if !ok {
		return review.ScrapedSource{}, review.ErrNotFound
	}
	return s, nil
}
func (f *fakeDB) GetSourceChunks(_ context.Context, id string) ([]review.ScrapedChunk, error) {
	return f.chunks[id], nil
}
func (f *fakeDB) AcceptSource(_ context.Context, id string) (review.ScrapedSource, error) {
	s := f.sources[id]
	if !review.CanTransitionSource(s.Status, review.SourceAccepted) {
		return s, review.ErrInvalidTransition{From: s.Status, To: review.SourceAccepted}
	}
	f.accepted = append(f.accepted, id)
	s.Status = review.SourceAccepted
	f.sources[id] = s
	return s, nil
}
func (f *fakeDB) DiscardSource(_ context.Context, id string) (review.ScrapedSource, error) {
	s := f.sources[id]
	s.Status = review.SourceDiscarded
	f.sources[id] = s
	return s, nil
}
func (f *fakeDB) MarkRescraping(_ context.Context, id string) (review.ScrapedSource, error) {
	s := f.sources[id]
	s.Status = review.SourceRescraping
	f.sources[id] = s
	return s, nil
}

// fakeLauncher records launched specs instead of creating K8s Jobs.
type fakeLauncher struct {
	launched []JobSpec
	fail     bool
}

func (l *fakeLauncher) Launch(_ context.Context, spec JobSpec) (string, error) {
	if l.fail {
		return "", errors.New("launch boom")
	}
	l.launched = append(l.launched, spec)
	return "scrape-" + spec.JobID[:4], nil
}

func newStore() (*Store, *fakeDB, *fakeLauncher) {
	db := newFakeDB()
	l := &fakeLauncher{}
	return New(db, l, audit.NewSlogLogger(nil)), db, l
}

var (
	devA  = auth.Session{Subject: "alice", Role: auth.RoleDevRel}
	devB  = auth.Session{Subject: "bob", Role: auth.RoleDevRel}
	admin = auth.Session{Subject: "root", Role: auth.RoleAdmin}
)

func TestCreateJob_StampsOwnerAndLaunches(t *testing.T) {
	s, db, l := newStore()
	job, err := s.CreateJob(context.Background(), devA, CreateJobRequest{
		PersonaName: "p", SourceURL: "https://x/a", SourceKind: "article",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if job.OwnerSubject != "alice" {
		t.Errorf("owner = %q, want alice (stamped from session)", job.OwnerSubject)
	}
	if len(l.launched) != 1 || l.launched[0].OwnerSubject != "alice" {
		t.Errorf("expected one Job launched for alice, got %+v", l.launched)
	}
	if db.jobs[job.ID].K8sJobName == "" {
		t.Error("expected k8s job name recorded")
	}
}

func TestCreateJob_RejectsBadKind(t *testing.T) {
	s, _, _ := newStore()
	_, err := s.CreateJob(context.Background(), devA, CreateJobRequest{
		PersonaName: "p", SourceURL: "https://x", SourceKind: "pdf",
	})
	if !IsBadRequest(err) {
		t.Fatalf("want bad-request for unsupported kind, got %v", err)
	}
}

func TestCreateJob_LaunchFailureMarksFailed(t *testing.T) {
	db := newFakeDB()
	l := &fakeLauncher{fail: true}
	s := New(db, l, audit.NewSlogLogger(nil))
	job, err := s.CreateJob(context.Background(), devA, CreateJobRequest{
		PersonaName: "p", SourceURL: "https://x", SourceKind: "article",
	})
	if err == nil {
		t.Fatal("want launch error")
	}
	if db.jobs[job.ID].Status != review.JobFailed {
		t.Errorf("job status = %q, want failed", db.jobs[job.ID].Status)
	}
}

// The core §6.3 matrix for owner-scoped sources: owner and Admin pass; a
// different DevRel is forbidden on every verb.
func TestSourceAuthz(t *testing.T) {
	seed := func(db *fakeDB) {
		db.sources["s1"] = review.ScrapedSource{ID: "s1", OwnerSubject: "alice", Status: review.SourcePending}
	}
	cases := []struct {
		name string
		sess auth.Session
		deny bool
	}{
		{"owner", devA, false},
		{"admin", admin, false},
		{"other-devrel", devB, true},
	}
	for _, c := range cases {
		t.Run(c.name+"/get-chunks", func(t *testing.T) {
			s, db, _ := newStore()
			seed(db)
			_, _, err := s.GetSourceChunks(context.Background(), c.sess, "s1")
			assertDeny(t, err, c.deny)
		})
		t.Run(c.name+"/discard", func(t *testing.T) {
			s, db, _ := newStore()
			seed(db)
			_, err := s.Discard(context.Background(), c.sess, "s1")
			assertDeny(t, err, c.deny)
		})
		t.Run(c.name+"/accept", func(t *testing.T) {
			s, db, _ := newStore()
			seed(db)
			_, err := s.Accept(context.Background(), c.sess, "s1")
			assertDeny(t, err, c.deny)
		})
	}
}

func assertDeny(t *testing.T, err error, wantDeny bool) {
	t.Helper()
	if wantDeny {
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("want ErrForbidden, got %v", err)
		}
		return
	}
	if errors.Is(err, ErrForbidden) {
		t.Fatalf("unexpected forbidden: %v", err)
	}
}

// ListSources must scope a DevRel to their own owner server-side, but let an
// Admin see all owners (ownerFilter == "").
func TestListSources_ServerSideScoping(t *testing.T) {
	s, db, _ := newStore()
	_, _ = s.ListSources(context.Background(), devA, "")
	if db.listOwnerFilter != "alice" {
		t.Errorf("DevRel ownerFilter = %q, want alice", db.listOwnerFilter)
	}
	_, _ = s.ListSources(context.Background(), admin, "")
	if db.listOwnerFilter != "" {
		t.Errorf("Admin ownerFilter = %q, want empty (all owners)", db.listOwnerFilter)
	}
}

func TestGetJob_Authz(t *testing.T) {
	s, db, _ := newStore()
	db.jobs["j1"] = review.ScrapeJob{ID: "j1", OwnerSubject: "alice"}
	if _, err := s.GetJob(context.Background(), devB, "j1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other DevRel should be forbidden, got %v", err)
	}
	if _, err := s.GetJob(context.Background(), devA, "j1"); err != nil {
		t.Fatalf("owner should see own job, got %v", err)
	}
}

// Rescrape re-enqueues under the SOURCE's owner, not the (Admin) caller's.
func TestRescrape_PreservesSourceOwner(t *testing.T) {
	s, db, l := newStore()
	db.sources["s1"] = review.ScrapedSource{ID: "s1", OwnerSubject: "alice", SourceURL: "u", SourceKind: "article", PersonaName: "p", Status: review.SourcePending}
	_, job, err := s.Rescrape(context.Background(), admin, "s1")
	if err != nil {
		t.Fatalf("Rescrape: %v", err)
	}
	if job.OwnerSubject != "alice" {
		t.Errorf("re-enqueued job owner = %q, want alice (source owner)", job.OwnerSubject)
	}
	if len(l.launched) != 1 {
		t.Errorf("want one re-enqueued Job, got %d", len(l.launched))
	}
	if db.sources["s1"].Status != review.SourceRescraping {
		t.Errorf("source status = %q, want rescraping", db.sources["s1"].Status)
	}
}

// Accepting an already-discarded source is a state-machine conflict, surfaced
// as ErrInvalidTransition (→ HTTP 409), not a silent no-op.
func TestAccept_InvalidTransition(t *testing.T) {
	s, db, _ := newStore()
	db.sources["s1"] = review.ScrapedSource{ID: "s1", OwnerSubject: "alice", Status: review.SourceDiscarded}
	_, err := s.Accept(context.Background(), devA, "s1")
	var invalid review.ErrInvalidTransition
	if !errors.As(err, &invalid) {
		t.Fatalf("want ErrInvalidTransition, got %v", err)
	}
}
