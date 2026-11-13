// Package promote is the Booth API's §6.3-gating front door to the promotion
// suggestion action (ISI-7369, plan §5 O8b/P4): given a persona and a piece of
// its validated content, generate a suggested LinkedIn promo post in the
// persona's voice. It mirrors the content-test / scraper-review gating stores:
// the action resolves the caller's authorization against the persona's owner
// (via the CRD store's §6.3 gate) and writes an audit record BEFORE it runs an
// LLM generation. HTTP handlers reach the action ONLY through this type.
//
// The action is owner-scoped (a DevRel promotes their OWN persona's content; an
// Admin any — §6.3 clause 1/2) and additionally requires the persona's style
// profile to be VALIDATED first: promotion is the step AFTER a content test is
// accepted (plan §5), so promoting an unvalidated persona is a 409. Each call is
// an LLM generation, so it is capped by a per-subject rate/cost guard.
//
// v1 is suggestion-only (plan §10 decision 5): no LinkedIn OAuth, no
// auto-publish. The response is editable draft copy; nothing leaves Booth.
package promote

import (
	"context"
	"errors"
	"fmt"

	apppromote "github.com/ContentRemixer/Contentremixer/app/promote"
	"github.com/ContentRemixer/Contentremixer/app/review"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	bstore "github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrForbidden is the §6.3 denial (→ 403).
	ErrForbidden = errors.New("promote: forbidden")
	// ErrNotFound means the persona does not exist (→ 404).
	ErrNotFound = errors.New("promote: not found")
	// ErrBadRequest is a request-validation failure (→ 400).
	ErrBadRequest = errors.New("promote: bad request")
	// ErrNotValidated means the persona's style profile is not validated yet, so
	// there is nothing accepted to promote (→ 409).
	ErrNotValidated = errors.New("promote: persona style profile is not validated")
	// ErrRateLimited means the caller exceeded the promote rate guard (→ 429).
	ErrRateLimited = errors.New("promote: rate limit exceeded")
)

// Audit verb + resource kind (mirror the other gating stores' vocabulary).
const (
	verbPromote = "promote"
	kindPersona = "DevRelPersona"
)

// profiles is the slice of app/review.Store the gate needs: the StyleProfile
// read. Declared as an interface so the store is unit-testable without Postgres.
// *review.Store satisfies it.
type profiles interface {
	GetStyleProfile(ctx context.Context, owner, persona string) (review.StyleProfile, error)
}

// personas is the slice of the CRD store the gate needs: the §6.3-gated persona
// conditioning read. *store.Store satisfies it.
type personas interface {
	GetPersonaConditioning(ctx context.Context, sess auth.Session, name string) (bstore.PersonaConditioning, error)
}

// suggester runs the writer-conditioning generation. *app/promote.Writer
// satisfies it; tests inject a stub.
type suggester interface {
	Suggest(ctx context.Context, req apppromote.Request) (apppromote.Suggestion, error)
}

// rateGuard caps per-subject promote calls. contenttest.Limiter satisfies it;
// declared locally so this package does not depend on contenttest.
type rateGuard interface {
	Allow(subject string) bool
}

// Store gates the promote action behind §6.3 + validated-precondition + audit.
type Store struct {
	profiles  profiles
	personas  personas
	suggester suggester
	limiter   rateGuard
	auditor   audit.Logger
}

// New builds a gating Store. limiter may be nil (no rate guard).
func New(p profiles, persona personas, s suggester, limiter rateGuard, auditor audit.Logger) *Store {
	return &Store{profiles: p, personas: persona, suggester: s, limiter: limiter, auditor: auditor}
}

// Request is the POST /v1/promote payload.
type Request struct {
	PersonaName string `json:"personaName"`
	// Content is the validated content to promote. Required.
	Content string `json:"content"`
	// Title and URL are optional context woven into the editable draft.
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Result is the promote response: the editable LinkedIn draft plus its parts and
// the model that served it.
type Result struct {
	PersonaName string `json:"personaName"`
	Hook        string `json:"hook"`
	Body        string `json:"body"`
	CTA         string `json:"cta"`
	// Post is the full assembled, ready-to-edit LinkedIn post (the authoritative
	// editable deliverable; hook/body/cta are the decomposed parts).
	Post      string `json:"post"`
	ModelUsed string `json:"modelUsed"`
}

// Suggest generates a promotion suggestion for the caller's persona. Order:
// validate the request, resolve + §6.3-gate the persona (which also yields its
// real owner and conditioning inputs), apply the rate/cost guard, load the
// synthesized StyleProfile, require it to be validated, then generate. A persona
// with no synthesized profile is a 400 (synthesis D3 must run first); an
// unvalidated one is a 409 (validate it first, plan §5).
func (s *Store) Suggest(ctx context.Context, sess auth.Session, req Request) (Result, error) {
	if req.PersonaName == "" || req.Content == "" {
		return Result{}, fmt.Errorf("%w: personaName and content are required", ErrBadRequest)
	}
	pc, err := s.personas.GetPersonaConditioning(ctx, sess, req.PersonaName)
	if err != nil {
		return Result{}, mapPersonaErr(err)
	}
	// Rate/cost guard AFTER the §6.3 gate (so an unauthorized caller cannot burn
	// another subject's budget) but BEFORE the LLM call.
	if s.limiter != nil && !s.limiter.Allow(sess.Subject) {
		s.audit(ctx, sess, req.PersonaName, false)
		return Result{}, ErrRateLimited
	}
	sp, err := s.profiles.GetStyleProfile(ctx, pc.OwnerSubject, req.PersonaName)
	if errors.Is(err, review.ErrNotFound) {
		s.audit(ctx, sess, req.PersonaName, false)
		return Result{}, fmt.Errorf("%w: persona %q has no synthesized style profile yet", ErrBadRequest, req.PersonaName)
	}
	if err != nil {
		return Result{}, err
	}
	if !sp.Validated {
		s.audit(ctx, sess, req.PersonaName, false)
		return Result{}, fmt.Errorf("%w: validate persona %q before promoting its content", ErrNotValidated, req.PersonaName)
	}
	out, err := s.suggester.Suggest(ctx, apppromote.Request{
		PersonaName:        req.PersonaName,
		Content:            req.Content,
		Title:              req.Title,
		URL:                req.URL,
		PrimaryContentKind: pc.PrimaryContentKind,
		StyleTraits:        sp.Traits,
		SelfDescription:    pc.SelfDescription,
	})
	if err != nil {
		s.audit(ctx, sess, req.PersonaName, false)
		return Result{}, err
	}
	s.audit(ctx, sess, req.PersonaName, true)
	return Result{
		PersonaName: req.PersonaName,
		Hook:        out.Hook,
		Body:        out.Body,
		CTA:         out.CTA,
		Post:        out.Post,
		ModelUsed:   out.ModelUsed,
	}, nil
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

// IsBadRequest / IsNotValidated / IsRateLimited let the HTTP layer map sentinels
// without importing the error values directly in a switch on wrapped errors.
func IsBadRequest(err error) bool   { return errors.Is(err, ErrBadRequest) }
func IsNotValidated(err error) bool { return errors.Is(err, ErrNotValidated) }
func IsRateLimited(err error) bool  { return errors.Is(err, ErrRateLimited) }

func (s *Store) audit(ctx context.Context, sess auth.Session, name string, allowed bool) {
	decision := audit.DecisionDeny
	if allowed {
		decision = audit.DecisionAllow
	}
	s.auditor.Log(ctx, audit.Entry{
		Subject:      sess.Subject,
		Role:         string(sess.Role),
		Verb:         verbPromote,
		ResourceKind: kindPersona,
		ResourceName: name,
		Decision:     decision,
	})
}
