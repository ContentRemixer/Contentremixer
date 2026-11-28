package content

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrBadRequest is returned when a Deposit/Backfill request violates an input
// invariant (e.g. an empty owner). The Booth gate surfaces it as HTTP 400; the
// operator logs it and reports the run as failed on the recycle issue.
var ErrBadRequest = fmt.Errorf("content: bad request")

// DepositRequest is the ingest seam's input — the single shape every new draft
// is deposited with. Owner, Source, and Title are required. It is the app-module
// twin of booth/internal/content.DepositRequest: Booth's gating Store wraps the
// SAME validate-and-insert for the session path (CreateContent) with §6.3 +
// audit, while a trusted server-side workstream (the recycle pipeline, C2) runs
// here directly against the shared booth.contents table. Keep the two invariant
// sets (owner/title/source required) in lockstep.
type DepositRequest struct {
	Owner      string
	Source     ContentSource
	Title      string
	BodyMD     string
	Origin     *Origin
	TargetRepo string
	TargetPath string
}

// BackfillRequest is the back-fill seam's input: the system-side annotation
// C2/C3 apply after a draft exists. Nil fields keep the stored value; a non-nil
// ScrubFindings replaces the stored slice. Source and Origin are deliberately
// absent — provenance is stamped once at Deposit and never patched.
type BackfillRequest struct {
	Status        *ContentStatus
	TargetRepo    *string
	TargetPath    *string
	ScrubFindings []ScrubFinding
	PRURL         *string
}

// Deposit inserts a new draft through the ingest seam and returns the stored
// row. It performs NO authz (this package never does — see the Store comment);
// the caller is a trusted server-side workstream. The single invariant it
// enforces is input validity and the stamping of defaults (status=draft, a
// random id, source normalised to manual when empty), identical to the Booth
// seam so a recycle draft and a session draft are indistinguishable at rest.
//
// Deposit is IDEMPOTENT per origin for recycled drafts (C2): GitHub redelivers
// a recycle issue's webhook and fires both `opened` and `labeled`, so the same
// origin (repo + issue) can drive the pipeline more than once. Rather than
// spawn a duplicate draft each time — the pre-C2 PR path converged on one
// branch/PR per issue — a repeat deposit for an origin that already has a draft
// REFRESHES that draft's title/body/target in place and returns it. The stored
// Status is deliberately preserved so a re-delivery can never regress a draft a
// human already moved to in_review/approved. Manual drafts (nil Origin) are
// never deduplicated.
func (s *Store) Deposit(ctx context.Context, req DepositRequest) (Content, error) {
	if req.Owner == "" {
		return Content{}, fmt.Errorf("%w: owner is required", ErrBadRequest)
	}
	if req.Title == "" {
		return Content{}, fmt.Errorf("%w: title is required", ErrBadRequest)
	}
	source := req.Source
	if source == "" {
		source = SourceManual
	}
	if !ValidContentSource(source) {
		return Content{}, fmt.Errorf("%w: unknown content source %q", ErrBadRequest, source)
	}
	if source == SourceRecycle && req.Origin != nil {
		if existing, err := s.findByOrigin(ctx, req.Origin.Repo, req.Origin.Issue); err == nil {
			// Refresh the existing draft; Status is left nil so the human's
			// lifecycle position is never clobbered by a re-delivery.
			return s.UpdateContent(ctx, existing.ID, ContentPatch{
				Title:      &req.Title,
				BodyMD:     &req.BodyMD,
				TargetRepo: &req.TargetRepo,
				TargetPath: &req.TargetPath,
			})
		} else if !errors.Is(err, ErrNotFound) {
			return Content{}, err
		}
	}
	c := Content{
		ID:           newID(),
		OwnerSubject: req.Owner,
		Title:        req.Title,
		BodyMD:       req.BodyMD,
		Status:       ContentDraft,
		Source:       source,
		Origin:       req.Origin,
		TargetRepo:   req.TargetRepo,
		TargetPath:   req.TargetPath,
	}
	if err := s.InsertContent(ctx, c); err != nil {
		return Content{}, err
	}
	return s.GetContent(ctx, c.ID)
}

// findByOrigin returns the most recent recycled draft for a given GitHub origin
// (repo + issue), or ErrNotFound. It scopes to source=recycle so it can never
// collide with a manual draft (manual drafts carry no origin).
func (s *Store) findByOrigin(ctx context.Context, repo string, issue int) (Content, error) {
	return scanContent(s.pool.QueryRow(ctx,
		`SELECT `+contentCols+` FROM booth.contents
		 WHERE source = $1 AND origin_repo = $2 AND origin_issue = $3
		 ORDER BY created_at ASC LIMIT 1`,
		string(SourceRecycle), repo, issue))
}

// Backfill applies a system-side annotation to an existing draft (C2 scrub
// findings, C3 PR link / status) and returns the updated row. Source and Origin
// are not patchable (UpdateContent ignores them); a nil field keeps the stored
// value.
func (s *Store) Backfill(ctx context.Context, id string, req BackfillRequest) (Content, error) {
	if req.Status != nil && !ValidContentStatus(*req.Status) {
		return Content{}, fmt.Errorf("%w: unknown content status %q", ErrBadRequest, *req.Status)
	}
	return s.UpdateContent(ctx, id, ContentPatch{
		Status:        req.Status,
		TargetRepo:    req.TargetRepo,
		TargetPath:    req.TargetPath,
		ScrubFindings: req.ScrubFindings,
		PRURL:         req.PRURL,
	})
}

// newID returns a random hex id — the same discipline as the Booth seam, so
// ids from both deposit paths share a format.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("content: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
