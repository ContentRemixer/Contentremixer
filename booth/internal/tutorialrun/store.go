// Package tutorialrun is the Booth API's §6.3-gating front door to P3-4
// multi-skill tutorial orchestration (ISI-7152). It mirrors the content and CRD
// stores' invariant exactly: every read and write evaluates the frozen authz
// predicate and writes an audit record BEFORE touching the backing store, so a
// new route cannot bypass the gate.
//
// Tenancy model (frozen §6.3): a tutorial run is an owner-scoped class (NOT
// admin-only). Launching a run stamps the owner from the SESSION, never the
// payload — the owner is carried into orchestrator.RunContext.Owner (§4), so
// every orchestrated output, draft, and PR inherits the run's tenancy. Reads
// and lists apply the same clause-2 predicate: a DevRel sees only their own
// runs; an Admin sees all. This package adds NO new authz path — it reuses the
// P3-1 authz.Authorize predicate, which is the whole point of P3-4 building on
// the P3-1 run model.
//
// Orchestration runs synchronously here and the resulting Mix (or the failure)
// is persisted on the owner-tagged run. A skill failure fails the run
// fail-closed (no Mix, no PR) while still recording an owner-tagged, auditable
// run — the same stance as the §4 Checker.
package tutorialrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/authz"
	"github.com/ContentRemixer/Contentremixer/booth/internal/orchestrator"
)

// ErrForbidden is the §6.3 denial. Handlers map it to HTTP 403.
var ErrForbidden = errors.New("tutorialrun: forbidden")

// ErrNotFound is the run-missing sentinel. Handlers map it to 404.
var ErrNotFound = errors.New("tutorialrun: not found")

// ErrBadRequest marks caller-input problems. Handlers map it to 400.
var ErrBadRequest = errors.New("tutorialrun: bad request")

// Run lifecycle states.
const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

const kindTutorialRun = "TutorialRun"

const (
	verbCreate = "create"
	verbGet    = "get"
	verbList   = "list"
)

// Run is one owner-tagged orchestration run and its result.
type Run struct {
	ID           string            `json:"id"`
	OwnerSubject string            `json:"ownerSubject"`
	Topic        string            `json:"topic"`
	Audience     string            `json:"audience,omitempty"`
	Notes        string            `json:"notes,omitempty"`
	SourceURL    string            `json:"sourceUrl,omitempty"`
	Status       string            `json:"status"`
	Mix          *orchestrator.Mix `json:"mix,omitempty"`
	Error        string            `json:"error,omitempty"`
}

// LaunchRequest is the POST /v1/tutorial-runs payload. The owner is NEVER taken
// from the payload — it is stamped from the session (§6.3).
type LaunchRequest struct {
	Topic     string `json:"topic"`
	Audience  string `json:"audience"`
	Notes     string `json:"notes"`
	SourceURL string `json:"sourceUrl"`
}

// Repository is the persistence slice the gate depends on, declared as an
// interface so the gate is unit-testable with a fake. MemoryRepo satisfies it.
type Repository interface {
	Insert(ctx context.Context, r Run) error
	Get(ctx context.Context, id string) (Run, error)
	List(ctx context.Context, ownerFilter string) ([]Run, error)
}

// Engine runs the skill DAG for one RunContext. *orchestrator.Engine satisfies
// it; tests supply a fake.
type Engine interface {
	Orchestrate(ctx context.Context, rc orchestrator.RunContext) (orchestrator.Mix, error)
}

// Store gates tutorial orchestration behind §6.3 + audit.
type Store struct {
	db      Repository
	engine  Engine
	auditor audit.Logger
}

// New builds a gating Store over the run persistence, the orchestration engine,
// and the audit logger.
func New(db Repository, engine Engine, auditor audit.Logger) *Store {
	return &Store{db: db, engine: engine, auditor: auditor}
}

// LaunchTutorialRun stamps the caller as owner (§6.3 clause 2), runs the skill
// DAG synchronously, and persists the owner-tagged run. A skill failure yields a
// persisted failed run (fail-closed), not a 500.
func (s *Store) LaunchTutorialRun(ctx context.Context, sess auth.Session, req LaunchRequest) (Run, error) {
	if req.Topic == "" {
		return Run{}, fmt.Errorf("%w: topic is required", ErrBadRequest)
	}
	run := Run{
		ID:           newID(),
		OwnerSubject: sess.Subject, // §6.3 clause 2: owner == launcher, never the payload
		Topic:        req.Topic,
		Audience:     req.Audience,
		Notes:        req.Notes,
		SourceURL:    req.SourceURL,
	}
	if !s.authorize(sess, run.OwnerSubject) {
		s.audit(ctx, sess, verbCreate, run.ID, false)
		return Run{}, ErrForbidden
	}
	rc := orchestrator.RunContext{
		Owner:     run.OwnerSubject, // §4 carrier: tenancy flows to every orchestrated output
		Topic:     req.Topic,
		Audience:  req.Audience,
		Notes:     req.Notes,
		SourceURL: req.SourceURL,
	}
	mix, err := s.engine.Orchestrate(ctx, rc)
	if err != nil {
		run.Status = StatusFailed
		run.Error = err.Error()
	} else {
		run.Status = StatusSucceeded
		run.Mix = &mix
	}
	if err := s.db.Insert(ctx, run); err != nil {
		s.audit(ctx, sess, verbCreate, run.ID, false)
		return Run{}, err
	}
	s.audit(ctx, sess, verbCreate, run.ID, true)
	return run, nil
}

// GetTutorialRun returns one run if §6.3 admits the caller, else ErrForbidden.
func (s *Store) GetTutorialRun(ctx context.Context, sess auth.Session, id string) (Run, error) {
	r, err := s.db.Get(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if !s.authorize(sess, r.OwnerSubject) {
		s.audit(ctx, sess, verbGet, id, false)
		return Run{}, ErrForbidden
	}
	s.audit(ctx, sess, verbGet, id, true)
	return r, nil
}

// ListTutorialRuns returns the runs §6.3 admits: all for an Admin, only the
// caller's own for a DevRel (server-side filter, §4).
func (s *Store) ListTutorialRuns(ctx context.Context, sess auth.Session) ([]Run, error) {
	ownerFilter := ""
	if sess.Role != auth.RoleAdmin {
		ownerFilter = sess.Subject
	}
	out, err := s.db.List(ctx, ownerFilter)
	if err != nil {
		s.audit(ctx, sess, verbList, "", false)
		return nil, err
	}
	s.audit(ctx, sess, verbList, "", true)
	return out, nil
}

// authorize is the frozen §6.3 predicate over an owner-scoped class.
func (s *Store) authorize(sess auth.Session, ownerSubject string) bool {
	return authz.Authorize(string(sess.Role), sess.Subject, false, ownerSubject)
}

func (s *Store) audit(ctx context.Context, sess auth.Session, verb, name string, allowed bool) {
	decision := audit.DecisionDeny
	if allowed {
		decision = audit.DecisionAllow
	}
	s.auditor.Log(ctx, audit.Entry{
		Subject:      sess.Subject,
		Role:         string(sess.Role),
		Verb:         verb,
		ResourceKind: kindTutorialRun,
		ResourceName: name,
		Decision:     decision,
	})
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("tutorialrun: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// --- in-memory repository ----------------------------------------------------

// MemoryRepo is the default persistence: tutorial runs are in-process and need
// no Postgres, so the orchestration surface is always available (unlike the
// Postgres-gated content routes). Safe for concurrent use.
type MemoryRepo struct {
	mu   sync.RWMutex
	rows map[string]Run
}

// NewMemoryRepo builds an empty in-memory run store.
func NewMemoryRepo() *MemoryRepo { return &MemoryRepo{rows: make(map[string]Run)} }

func (m *MemoryRepo) Insert(_ context.Context, r Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[r.ID] = r
	return nil
}

func (m *MemoryRepo) Get(_ context.Context, id string) (Run, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.rows[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	return r, nil
}

// List returns runs matching ownerFilter (empty == all), in a stable order by
// id so results are deterministic.
func (m *MemoryRepo) List(_ context.Context, ownerFilter string) ([]Run, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Run, 0, len(m.rows))
	for _, r := range m.rows {
		if ownerFilter != "" && r.OwnerSubject != ownerFilter {
			continue
		}
		out = append(out, r)
	}
	sortByID(out)
	return out, nil
}

func sortByID(rs []Run) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j-1].ID > rs[j].ID; j-- {
			rs[j-1], rs[j] = rs[j], rs[j-1]
		}
	}
}
