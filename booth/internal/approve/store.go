// Package approve is the Booth API's §6.3-gating front door to the dual-approval
// action (ISI-7153, P3-5): a DevRel/Admin approves a generated-content PR in
// the Booth, and the Booth reflects that decision onto the GitHub PR as a
// review, composing with the GitHub-side review gate rather than replacing it.
//
// It mirrors the other gating stores (promote, content, scrapejobs): the action
// resolves the caller's authorization against the owning persona via the CRD
// store's §6.3 gate and writes an audit record BEFORE it touches GitHub. HTTP
// handlers reach the action ONLY through this type, so the §6.3 invariant
// (design docs/design/phase1-operator-design.md §6.3) cannot be bypassed by a
// new route.
//
// Authorization anchor (frozen §6.3): a generated-content PR is tagged with its
// owning persona (design §6.2 — every run/draft/PR carries the owning persona,
// and thus owner). Approving a PR therefore gates on that persona's owner:
//
//   - DevRel may approve only their OWN persona's PR (owner.subject == subject);
//     approving an unowned persona's PR → 403, never a silent no-op.
//   - Admin may approve ANY PR (clause 1).
//   - A platform-owned PR (no persona / empty owner) is Admin-only — clause 2
//     cannot match an empty owner against a real subject.
//
// Reflection does NOT merge: it records the approval as a GitHub review (a
// formal APPROVE, or a COMMENT fallback carrying a machine-parseable
// Booth-approval block when the PAT cannot approve its own PR). Branch
// protection (ISI-7123) remains the authoritative merge gate; the Booth
// approval composes with — never bypasses — it.
package approve

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/authz"
	bstore "github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrForbidden is the §6.3 denial (→ 403).
	ErrForbidden = errors.New("approve: forbidden")
	// ErrNotFound means the named persona does not exist (→ 404).
	ErrNotFound = errors.New("approve: not found")
	// ErrBadRequest is a request-validation failure (→ 400).
	ErrBadRequest = errors.New("approve: bad request")
	// ErrReflect means the GitHub reflection failed after authorization passed
	// (→ 502: an upstream fault, not a caller error).
	ErrReflect = errors.New("approve: reflecting to GitHub failed")
)

// Audit verb + resource kind (§6.4; verb matches the audit vocabulary).
const (
	verbApprove = "approve"
	kindPR      = "PullRequest"
)

// personaGate is the slice of the CRD store the action needs: the §6.3-gated
// persona read that doubles as the authorization check and existence probe.
// *store.Store satisfies it.
type personaGate interface {
	GetPersonaConditioning(ctx context.Context, sess auth.Session, name string) (bstore.PersonaConditioning, error)
}

// Store gates the dual-approval action behind §6.3 + audit, then reflects.
type Store struct {
	personas  personaGate
	reflector Reflector
	auditor   audit.Logger
}

// New builds a gating Store. reflector performs the GitHub reflection; auditor
// records the §6.4 entry.
func New(personas personaGate, reflector Reflector, auditor audit.Logger) *Store {
	return &Store{personas: personas, reflector: reflector, auditor: auditor}
}

// Request is the POST /v1/approve payload.
type Request struct {
	// Repo is the GitHub "owner/name" the PR lives in. Required.
	Repo string `json:"repo"`
	// PRNumber is the pull-request number. Required (> 0).
	PRNumber int `json:"prNumber"`
	// PersonaName is the owning persona of the PR — the §6.3 authorization
	// anchor. Empty means a platform-owned PR (Admin-only).
	PersonaName string `json:"personaName"`
	// Note is an optional free-text reviewer comment reflected onto the PR.
	Note string `json:"note,omitempty"`
}

// Result is the POST /v1/approve response.
type Result struct {
	Repo     string `json:"repo"`
	PRNumber int    `json:"prNumber"`
	// Mode is "approve" (formal GitHub APPROVE) or "comment" (fallback).
	Mode string `json:"mode"`
	// ReviewID / ReviewURL identify the GitHub review that recorded the approval.
	ReviewID  int64  `json:"reviewId,omitempty"`
	ReviewURL string `json:"reviewUrl,omitempty"`
	// Idempotent is true when the approval was already present and reused.
	Idempotent bool `json:"idempotent"`
	// Approver is the in-Booth subject credited for the approval.
	Approver string `json:"approver"`
}

// Approve authorizes the caller to approve the PR (§6.3, anchored on the owning
// persona), audits the decision, then reflects the approval onto the GitHub PR.
// Order: validate → §6.3 gate → reflect → audit allow. A denied caller is
// audited and 403'd before any GitHub call; a reflection fault is audited as a
// denied (un-recorded) mutation and surfaced as ErrReflect.
func (s *Store) Approve(ctx context.Context, sess auth.Session, req Request) (Result, error) {
	repo := strings.TrimSpace(req.Repo)
	if repo == "" || !strings.Contains(strings.Trim(repo, "/"), "/") {
		return Result{}, fmt.Errorf("%w: repo must be \"owner/name\"", ErrBadRequest)
	}
	if req.PRNumber <= 0 {
		return Result{}, fmt.Errorf("%w: prNumber must be > 0", ErrBadRequest)
	}
	name := repo + "#" + strconv.Itoa(req.PRNumber)

	if err := s.authorizePR(ctx, sess, strings.TrimSpace(req.PersonaName)); err != nil {
		s.audit(ctx, sess, name, false)
		return Result{}, err
	}

	ap := Approval{
		Subject: sess.Subject,
		Role:    string(sess.Role),
		Note:    req.Note,
		Key:     name + ":" + sess.Subject,
	}
	refl, err := s.reflector.Reflect(ctx, repo, req.PRNumber, ap)
	if err != nil {
		s.audit(ctx, sess, name, false)
		return Result{}, fmt.Errorf("%w: %v", ErrReflect, err)
	}

	s.audit(ctx, sess, name, true)
	return Result{
		Repo:       repo,
		PRNumber:   req.PRNumber,
		Mode:       string(refl.Mode),
		ReviewID:   refl.ReviewID,
		ReviewURL:  refl.URL,
		Idempotent: refl.Idempotent,
		Approver:   sess.Subject,
	}, nil
}

// authorizePR evaluates §6.3 for the approve verb. An empty persona is a
// platform-owned PR → Admin-only (the §6.3 empty-owner rule). A named persona
// is resolved through the §6.3-gated store read, which returns ErrForbidden for
// an unowned persona and ErrNotFound when it does not exist.
func (s *Store) authorizePR(ctx context.Context, sess auth.Session, personaName string) error {
	if personaName == "" {
		if !authz.Authorize(string(sess.Role), sess.Subject, false, "") {
			return ErrForbidden
		}
		return nil
	}
	if _, err := s.personas.GetPersonaConditioning(ctx, sess, personaName); err != nil {
		switch {
		case errors.Is(err, bstore.ErrForbidden):
			return ErrForbidden
		case errors.Is(err, bstore.ErrNotFound):
			return ErrNotFound
		default:
			return err
		}
	}
	return nil
}

// audit records one §6.4 entry for the approve verb.
func (s *Store) audit(ctx context.Context, sess auth.Session, name string, allowed bool) {
	decision := audit.DecisionDeny
	if allowed {
		decision = audit.DecisionAllow
	}
	s.auditor.Log(ctx, audit.Entry{
		Subject:      sess.Subject,
		Role:         string(sess.Role),
		Verb:         verbApprove,
		ResourceKind: kindPR,
		ResourceName: name,
		Decision:     decision,
	})
}
