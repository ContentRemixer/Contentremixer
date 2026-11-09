// Package scrapejobs is the Booth API's §6.3-gating front door to the
// scraper-review domain (ISI-7207, plan §3.2). It mirrors the CRD store's
// invariant: every read and write evaluates the frozen authz predicate and
// writes an audit record BEFORE touching the backing Postgres (app/review) or
// creating a scrape Job. HTTP handlers reach scraped sources ONLY through this
// type, so a new route cannot bypass the gate.
//
// ScrapeJob and ScrapedSource are owner-scoped classes (NOT admin-only): a
// DevRel sees and acts on their own repos' sources; an Admin sees all. This is
// the §6.3 clause-2 (ownership) path, the same one DevRelPersona uses.
package scrapejobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/ContentRemixer/Contentremixer/app/review"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/authz"
)

// ErrForbidden is the §6.3 denial. Handlers map it to HTTP 403.
var ErrForbidden = errors.New("scrapejobs: forbidden")

// Resource kinds recorded in audit entries.
const (
	kindJob    = "ScrapeJob"
	kindSource = "ScrapedSource"
)

// persistence is the slice of app/review.Store the gating store depends on,
// declared as an interface so the gate is unit-testable with a fake (no
// Postgres). *review.Store satisfies it.
type persistence interface {
	InsertJob(ctx context.Context, j review.ScrapeJob) error
	SetJobK8sName(ctx context.Context, id, k8sName string) error
	SetJobStatus(ctx context.Context, id string, status review.JobStatus, errMsg string) error
	GetJob(ctx context.Context, id string) (review.ScrapeJob, error)
	ListSources(ctx context.Context, ownerFilter string, statusFilter review.SourceStatus) ([]review.ScrapedSource, error)
	GetSource(ctx context.Context, id string) (review.ScrapedSource, error)
	GetSourceChunks(ctx context.Context, sourceID string) ([]review.ScrapedChunk, error)
	AcceptSource(ctx context.Context, id string) (review.ScrapedSource, error)
	DiscardSource(ctx context.Context, id string) (review.ScrapedSource, error)
	MarkRescraping(ctx context.Context, id string) (review.ScrapedSource, error)
}

// Store gates the scraper-review domain behind §6.3 + audit.
type Store struct {
	db       persistence
	launcher Launcher
	auditor  audit.Logger
}

// New builds a gating Store over the review persistence, a Job launcher, and the
// audit logger.
func New(db persistence, launcher Launcher, auditor audit.Logger) *Store {
	return &Store{db: db, launcher: launcher, auditor: auditor}
}

// CreateJobRequest is the enqueue payload (POST /v1/scrape-jobs).
type CreateJobRequest struct {
	ReferenceRepo string `json:"referenceRepo"`
	PersonaName   string `json:"personaName"`
	SourceURL     string `json:"sourceUrl"`
	SourceKind    string `json:"sourceKind"`
}

// CreateJob enqueues a scrape owned by the caller. A DevRel may only create jobs
// owned by themselves (§6.3 clause 2 admits owner==subject); an Admin may too
// (clause 1). The owner is STAMPED from the session — a caller cannot enqueue a
// job on someone else's behalf. It inserts the queued row, launches the Job, and
// records the Job name; a launch failure marks the row failed and surfaces the
// error.
func (s *Store) CreateJob(ctx context.Context, sess auth.Session, req CreateJobRequest) (review.ScrapeJob, error) {
	if req.SourceURL == "" || req.PersonaName == "" {
		return review.ScrapeJob{}, fmt.Errorf("%w: sourceUrl and personaName are required", errBadRequest)
	}
	if !review.ValidSourceKind(req.SourceKind) {
		return review.ScrapeJob{}, fmt.Errorf("%w: unsupported sourceKind %q", errBadRequest, req.SourceKind)
	}
	owner := sess.Subject
	if !s.authorize(sess, owner) {
		s.audit(ctx, sess, verbCreate, kindJob, "", false)
		return review.ScrapeJob{}, ErrForbidden
	}
	job := review.ScrapeJob{
		ID:            newID(),
		OwnerSubject:  owner,
		ReferenceRepo: req.ReferenceRepo,
		PersonaName:   req.PersonaName,
		SourceURL:     req.SourceURL,
		SourceKind:    req.SourceKind,
		Status:        review.JobQueued,
	}
	if err := s.db.InsertJob(ctx, job); err != nil {
		s.audit(ctx, sess, verbCreate, kindJob, job.ID, false)
		return review.ScrapeJob{}, err
	}
	s.audit(ctx, sess, verbCreate, kindJob, job.ID, true)

	name, err := s.launcher.Launch(ctx, JobSpec{
		JobID: job.ID, OwnerSubject: owner, ReferenceRepo: job.ReferenceRepo,
		PersonaName: job.PersonaName, SourceURL: job.SourceURL, SourceKind: job.SourceKind,
	})
	if err != nil {
		_ = s.db.SetJobStatus(ctx, job.ID, review.JobFailed, err.Error())
		job.Status = review.JobFailed
		job.Error = err.Error()
		return job, err
	}
	_ = s.db.SetJobK8sName(ctx, job.ID, name)
	job.K8sJobName = name
	return job, nil
}

// GetJob returns one job if the caller owns it (or is Admin), else ErrForbidden.
func (s *Store) GetJob(ctx context.Context, sess auth.Session, id string) (review.ScrapeJob, error) {
	job, err := s.db.GetJob(ctx, id)
	if err != nil {
		return review.ScrapeJob{}, err
	}
	if !s.authorize(sess, job.OwnerSubject) {
		s.audit(ctx, sess, verbGet, kindJob, id, false)
		return review.ScrapeJob{}, ErrForbidden
	}
	s.audit(ctx, sess, verbGet, kindJob, id, true)
	return job, nil
}

// ListSources returns sources the caller may see, filtered SERVER-SIDE: a DevRel
// is scoped to their own owner subject, an Admin sees all. statusFilter narrows
// to one review state when non-empty.
func (s *Store) ListSources(ctx context.Context, sess auth.Session, statusFilter review.SourceStatus) ([]review.ScrapedSource, error) {
	ownerFilter := sess.Subject
	if sess.Role == auth.RoleAdmin {
		ownerFilter = "" // all owners
	}
	out, err := s.db.ListSources(ctx, ownerFilter, statusFilter)
	if err != nil {
		s.audit(ctx, sess, verbList, kindSource, "", false)
		return nil, err
	}
	s.audit(ctx, sess, verbList, kindSource, "", true)
	return out, nil
}

// GetSourceChunks returns a source's preview chunks if the caller owns the
// source (or is Admin).
func (s *Store) GetSourceChunks(ctx context.Context, sess auth.Session, id string) (review.ScrapedSource, []review.ScrapedChunk, error) {
	src, err := s.gateSource(ctx, sess, verbGet, id)
	if err != nil {
		return review.ScrapedSource{}, nil, err
	}
	chunks, err := s.db.GetSourceChunks(ctx, id)
	if err != nil {
		return src, nil, err
	}
	return src, chunks, nil
}

// Accept ingests a source's chunks into the persona corpus and marks it
// accepted — gated by §6.3 against the source owner, and by the review state
// machine (only pending sources accept).
func (s *Store) Accept(ctx context.Context, sess auth.Session, id string) (review.ScrapedSource, error) {
	if _, err := s.gateSource(ctx, sess, verbApprove, id); err != nil {
		return review.ScrapedSource{}, err
	}
	return s.db.AcceptSource(ctx, id)
}

// Discard rejects a pending source (chunks never reach the corpus).
func (s *Store) Discard(ctx context.Context, sess auth.Session, id string) (review.ScrapedSource, error) {
	if _, err := s.gateSource(ctx, sess, verbUpdate, id); err != nil {
		return review.ScrapedSource{}, err
	}
	return s.db.DiscardSource(ctx, id)
}

// Rescrape marks a pending source rescraping and enqueues a FRESH job for the
// same reference (owned by the original source owner). The old row is left as an
// audit trail; the new job produces a new pending source.
func (s *Store) Rescrape(ctx context.Context, sess auth.Session, id string) (review.ScrapedSource, review.ScrapeJob, error) {
	src, err := s.gateSource(ctx, sess, verbUpdate, id)
	if err != nil {
		return review.ScrapedSource{}, review.ScrapeJob{}, err
	}
	marked, err := s.db.MarkRescraping(ctx, id)
	if err != nil {
		return review.ScrapedSource{}, review.ScrapeJob{}, err
	}
	// Re-enqueue under the source's own owner (NOT the caller — an Admin
	// rescraping a DevRel's source keeps the DevRel's ownership).
	job := review.ScrapeJob{
		ID:            newID(),
		OwnerSubject:  src.OwnerSubject,
		ReferenceRepo: src.ReferenceRepo,
		PersonaName:   src.PersonaName,
		SourceURL:     src.SourceURL,
		SourceKind:    src.SourceKind,
		Status:        review.JobQueued,
	}
	if err := s.db.InsertJob(ctx, job); err != nil {
		return marked, review.ScrapeJob{}, err
	}
	name, err := s.launcher.Launch(ctx, JobSpec{
		JobID: job.ID, OwnerSubject: job.OwnerSubject, ReferenceRepo: job.ReferenceRepo,
		PersonaName: job.PersonaName, SourceURL: job.SourceURL, SourceKind: job.SourceKind,
	})
	if err != nil {
		_ = s.db.SetJobStatus(ctx, job.ID, review.JobFailed, err.Error())
		job.Status = review.JobFailed
		job.Error = err.Error()
		return marked, job, err
	}
	_ = s.db.SetJobK8sName(ctx, job.ID, name)
	job.K8sJobName = name
	return marked, job, nil
}

// gateSource loads a source, evaluates §6.3 against its owner, writes the audit
// record, and returns the source (or ErrForbidden / review.ErrNotFound).
func (s *Store) gateSource(ctx context.Context, sess auth.Session, verb, id string) (review.ScrapedSource, error) {
	src, err := s.db.GetSource(ctx, id)
	if err != nil {
		return review.ScrapedSource{}, err
	}
	if !s.authorize(sess, src.OwnerSubject) {
		s.audit(ctx, sess, verb, kindSource, id, false)
		return review.ScrapedSource{}, ErrForbidden
	}
	s.audit(ctx, sess, verb, kindSource, id, true)
	return src, nil
}

// authorize applies the frozen §6.3 predicate for an owner-scoped class
// (adminOnly=false): Admin always, else owner==subject.
func (s *Store) authorize(sess auth.Session, ownerSubject string) bool {
	return authz.Authorize(string(sess.Role), sess.Subject, false, ownerSubject)
}

func (s *Store) audit(ctx context.Context, sess auth.Session, verb, kind, name string, allowed bool) {
	decision := audit.DecisionDeny
	if allowed {
		decision = audit.DecisionAllow
	}
	s.auditor.Log(ctx, audit.Entry{
		Subject:      sess.Subject,
		Role:         string(sess.Role),
		Verb:         verb,
		ResourceKind: kind,
		ResourceName: name,
		Decision:     decision,
	})
}

// Audit verbs (mirror the CRD store's vocabulary).
const (
	verbGet     = "get"
	verbList    = "list"
	verbCreate  = "create"
	verbUpdate  = "update"
	verbApprove = "approve"
)

// errBadRequest is a 400-class validation error the handler maps to 400.
var errBadRequest = errors.New("scrapejobs: bad request")

// IsBadRequest reports whether err is a request-validation error (→ HTTP 400).
func IsBadRequest(err error) bool { return errors.Is(err, errBadRequest) }

// newID returns a random 128-bit hex id for a job (no uuid dependency needed).
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
