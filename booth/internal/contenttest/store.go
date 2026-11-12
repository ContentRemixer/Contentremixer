// Package contenttest is the Booth API's §6.3-gating front door to the
// interactive persona content-test + persona-validate actions (ISI-7359, plan
// §5). It mirrors the scraper-review and content-collaboration gating stores:
// every action resolves the caller's authorization against the persona's owner
// (via the CRD store's §6.3 gate) and writes an audit record BEFORE it runs an
// LLM generation or flips a validation flag. HTTP handlers reach these actions
// ONLY through this type.
//
// Two actions, both owner-scoped (a DevRel acts on their OWN persona; an Admin
// on any — §6.3 clause 1/2):
//
//   - RunTest: load the persona's synthesized StyleProfile (D3) + spec
//     conditioning, run a PR-less writer-conditioning generation, return the
//     sample. Guarded by a per-subject rate/cost limiter because each call is an
//     LLM generation.
//   - Validate: flip StyleProfile.Validated in Postgres (source of truth) AND the
//     DevRelPersona `Validated` status condition (its declarative mirror) in one
//     operation (ISI-7356 §4).
package contenttest

import (
	"context"
	"errors"
	"fmt"

	apptest "github.com/ContentRemixer/Contentremixer/app/contenttest"
	"github.com/ContentRemixer/Contentremixer/app/review"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	bstore "github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrForbidden is the §6.3 denial (→ 403). It aliases the CRD store's
	// ErrForbidden so a denial surfaced by the persona gate maps uniformly.
	ErrForbidden = errors.New("contenttest: forbidden")
	// ErrNotFound means the persona does not exist (→ 404).
	ErrNotFound = errors.New("contenttest: not found")
	// ErrBadRequest is a request-validation failure (→ 400).
	ErrBadRequest = errors.New("contenttest: bad request")
	// ErrRateLimited means the caller exceeded the content-test rate guard (→ 429).
	ErrRateLimited = errors.New("contenttest: rate limit exceeded")
)

// Audit verbs (mirror the other gating stores' vocabulary).
const (
	verbTest     = "content-test"
	verbValidate = "validate"
	kindPersona  = "DevRelPersona"
)

// profiles is the slice of app/review.Store the gate needs — the StyleProfile
// read + validate. Declared as an interface so the store is unit-testable
// without Postgres. *review.Store satisfies it.
type profiles interface {
	GetStyleProfile(ctx context.Context, owner, persona string) (review.StyleProfile, error)
	ValidateStyleProfile(ctx context.Context, owner, persona string) (review.StyleProfile, error)
}

// personas is the slice of the CRD store the gate needs: the §6.3-gated persona
// conditioning read and the Validated-condition write. *store.Store satisfies it.
type personas interface {
	GetPersonaConditioning(ctx context.Context, sess auth.Session, name string) (bstore.PersonaConditioning, error)
	SetPersonaValidated(ctx context.Context, sess auth.Session, name string, validated bool) error
}

// runner runs the writer-conditioning generation. *app/contenttest.Runner
// satisfies it; tests inject a stub.
type runner interface {
	Run(ctx context.Context, req apptest.Request) (apptest.Result, error)
}

// rateGuard caps per-subject content-test calls. *Limiter satisfies it.
type rateGuard interface {
	Allow(subject string) bool
}

// Store gates the content-test + validate actions behind §6.3 + audit.
type Store struct {
	profiles profiles
	personas personas
	runner   runner
	limiter  rateGuard
	auditor  audit.Logger
}

// New builds a gating Store. limiter may be nil (no rate guard).
func New(p profiles, persona personas, r runner, limiter rateGuard, auditor audit.Logger) *Store {
	return &Store{profiles: p, personas: persona, runner: r, limiter: limiter, auditor: auditor}
}

// TestRequest is the POST /v1/content-test payload.
type TestRequest struct {
	PersonaName string `json:"personaName"`
	Topic       string `json:"topic"`
}

// TestResult is the content-test response: the generated sample plus the context
// the onboarding surface renders alongside it.
type TestResult struct {
	PersonaName string `json:"personaName"`
	Sample      string `json:"sample"`
	ModelUsed   string `json:"modelUsed"`
	// Validated echoes whether the profile the test conditioned on is already
	// owner-validated (so the UI can show "validate" vs "already validated").
	Validated bool `json:"validated"`
}

// RunTest runs an interactive content test for the caller's persona. Order:
// validate the request, resolve + §6.3-gate the persona (which also yields its
// real owner and conditioning inputs), apply the rate/cost guard, load the
// synthesized StyleProfile, then generate. A persona with no synthesized profile
// yet is a 400 (synthesis D3 must run first), not a 500.
func (s *Store) RunTest(ctx context.Context, sess auth.Session, req TestRequest) (TestResult, error) {
	if req.PersonaName == "" || req.Topic == "" {
		return TestResult{}, fmt.Errorf("%w: personaName and topic are required", ErrBadRequest)
	}
	pc, err := s.personas.GetPersonaConditioning(ctx, sess, req.PersonaName)
	if err != nil {
		return TestResult{}, mapPersonaErr(err)
	}
	// Rate/cost guard AFTER the §6.3 gate (so an unauthorized caller cannot burn
	// another subject's budget) but BEFORE the LLM call.
	if s.limiter != nil && !s.limiter.Allow(sess.Subject) {
		s.audit(ctx, sess, verbTest, req.PersonaName, false)
		return TestResult{}, ErrRateLimited
	}
	sp, err := s.profiles.GetStyleProfile(ctx, pc.OwnerSubject, req.PersonaName)
	if errors.Is(err, review.ErrNotFound) {
		s.audit(ctx, sess, verbTest, req.PersonaName, false)
		return TestResult{}, fmt.Errorf("%w: persona %q has no synthesized style profile yet", ErrBadRequest, req.PersonaName)
	}
	if err != nil {
		return TestResult{}, err
	}
	out, err := s.runner.Run(ctx, apptest.Request{
		PersonaName:        req.PersonaName,
		Topic:              req.Topic,
		PrimaryContentKind: pc.PrimaryContentKind,
		StyleTraits:        sp.Traits,
		SelfDescription:    pc.SelfDescription,
	})
	if err != nil {
		s.audit(ctx, sess, verbTest, req.PersonaName, false)
		return TestResult{}, err
	}
	s.audit(ctx, sess, verbTest, req.PersonaName, true)
	return TestResult{
		PersonaName: req.PersonaName,
		Sample:      out.Sample,
		ModelUsed:   out.ModelUsed,
		Validated:   sp.Validated,
	}, nil
}

// Validate flips the owner's accept decision for a persona's style profile. It
// writes Postgres FIRST (the content source of truth) then mirrors to the CRD
// `Validated` condition (the cheap declarative gate the writer-conditioner
// reads). If the CRD write fails after the Postgres flip, the error is returned;
// the operation is idempotent, so a retry re-sets both. A persona with no
// profile to validate is a 400. Authorization is enforced by the persona gate.
func (s *Store) Validate(ctx context.Context, sess auth.Session, personaName string) (review.StyleProfile, error) {
	if personaName == "" {
		return review.StyleProfile{}, fmt.Errorf("%w: personaName is required", ErrBadRequest)
	}
	pc, err := s.personas.GetPersonaConditioning(ctx, sess, personaName)
	if err != nil {
		return review.StyleProfile{}, mapPersonaErr(err)
	}
	sp, err := s.profiles.ValidateStyleProfile(ctx, pc.OwnerSubject, personaName)
	if errors.Is(err, review.ErrNotFound) {
		s.audit(ctx, sess, verbValidate, personaName, false)
		return review.StyleProfile{}, fmt.Errorf("%w: persona %q has no style profile to validate", ErrBadRequest, personaName)
	}
	if err != nil {
		return review.StyleProfile{}, err
	}
	// Mirror to the CRD condition (ISI-7356 §4). This re-gates §6.3 and audits
	// under verbValidate; a failure here leaves the Postgres flag set but the
	// declarative gate unflipped (safe: the writer-conditioner refuses to serve
	// until the condition is True), and a retry is idempotent.
	if err := s.personas.SetPersonaValidated(ctx, sess, personaName, true); err != nil {
		return sp, mapPersonaErr(err)
	}
	s.audit(ctx, sess, verbValidate, personaName, true)
	return sp, nil
}

// mapPersonaErr translates the CRD store's sentinels into this package's.
func mapPersonaErr(err error) error {
	switch {
	case errors.Is(err, bstore.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, bstore.ErrNotFound):
		return ErrNotFound
	default:
		return err
	}
}

// IsBadRequest / IsRateLimited let the HTTP layer map sentinels without importing
// the error values directly in a switch on wrapped errors.
func IsBadRequest(err error) bool  { return errors.Is(err, ErrBadRequest) }
func IsRateLimited(err error) bool { return errors.Is(err, ErrRateLimited) }

func (s *Store) audit(ctx context.Context, sess auth.Session, verb, name string, allowed bool) {
	decision := audit.DecisionDeny
	if allowed {
		decision = audit.DecisionAllow
	}
	s.auditor.Log(ctx, audit.Entry{
		Subject:      sess.Subject,
		Role:         string(sess.Role),
		Verb:         verb,
		ResourceKind: kindPersona,
		ResourceName: name,
		Decision:     decision,
	})
}
