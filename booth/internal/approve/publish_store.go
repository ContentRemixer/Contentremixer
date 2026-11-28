package approve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	appcontent "github.com/ContentRemixer/Contentremixer/app/content"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	bcontent "github.com/ContentRemixer/Contentremixer/booth/internal/content"
)

// ErrBlocked is the scrub-gate refusal (ISI-7970 C3): the draft still carries
// unresolved (unwaived) scrub findings, so publish is refused before any GitHub
// call. The HTTP layer maps it to 409 Conflict.
var ErrBlocked = errors.New("approve: publish blocked by unresolved scrub findings")

// Audit verb + resource kind for the publish terminal action (§6.4).
const (
	verbPublish = "publish"
	kindDraft   = "ContentDraft"
)

// draftStore is the slice of the content gating store the publish action needs:
// the §6.3-gated read (authorization + existence) and the system-side back-fill
// seam that records the PR link and the published status. *content.Store
// satisfies it.
type draftStore interface {
	GetContent(ctx context.Context, sess auth.Session, id string) (appcontent.Content, error)
	Backfill(ctx context.Context, id string, req bcontent.BackfillRequest) (appcontent.Content, error)
}

// DraftGate is the §6.3-gated approval→publish terminal action (ISI-7970 C3):
// a reviewed ContentDraft is approved by a human, and that approval — and only
// that approval — opens the publishing PR. It composes the merged C1 content
// model (the draft), the scrub gate (app/content.UnresolvedFindings), and the
// booth-native Publisher, mirroring the dual-approval store's gate→act→audit
// discipline (ISI-7153). See docs/design/isi-7970-approval-pr-publish.md.
type DraftGate struct {
	drafts    draftStore
	publisher Publisher
	auditor   audit.Logger
}

// NewDraftGate builds the publish action. drafts is the content gating store,
// publisher opens the PR, auditor records the §6.4 decision.
func NewDraftGate(drafts draftStore, publisher Publisher, auditor audit.Logger) *DraftGate {
	return &DraftGate{drafts: drafts, publisher: publisher, auditor: auditor}
}

// PublishDraftResult is the POST /v1/content/{id}/publish response.
type PublishDraftResult struct {
	ContentID  string `json:"contentId"`
	Repo       string `json:"repo"`
	Path       string `json:"path"`
	PRNumber   int    `json:"prNumber,omitempty"`
	PRURL      string `json:"prUrl"`
	Branch     string `json:"branch,omitempty"`
	Idempotent bool   `json:"idempotent"`
	Approver   string `json:"approver"`
}

// PublishDraft is the terminal action. Order: resolve+§6.3-gate → state/target
// guard → scrub gate → publish (commit + one PR) → back-fill PR link + status
// published → comment the origin issue (soft) → audit. A gate refusal or a
// pre-publish guard failure is audited as a denied mutation and surfaced before
// any GitHub call; a GitHub fault after the gate passes is ErrReflect (→ 502).
func (g *DraftGate) PublishDraft(ctx context.Context, sess auth.Session, id string) (PublishDraftResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return PublishDraftResult{}, fmt.Errorf("%w: content id required", ErrBadRequest)
	}

	// 1. Resolve + authorize through the content store's §6.3 owner gate.
	c, err := g.drafts.GetContent(ctx, sess, id)
	if err != nil {
		return PublishDraftResult{}, mapContentErr(err)
	}

	// 1a. Already published → idempotent no-op; return the recorded PR.
	if c.Status == appcontent.ContentPublished {
		return PublishDraftResult{
			ContentID: c.ID, Repo: c.TargetRepo, Path: c.TargetPath,
			PRURL: c.PRURL, Idempotent: true, Approver: sess.Subject,
		}, nil
	}

	// 2. State + target guards (C2 deposits drafts with target set; fail closed
	// rather than guess a path).
	if c.Status != appcontent.ContentInReview && c.Status != appcontent.ContentApproved {
		g.audit(ctx, sess, id, false)
		return PublishDraftResult{}, fmt.Errorf("%w: draft must be in_review or approved to publish (is %q)", ErrBadRequest, c.Status)
	}
	repo := strings.TrimSpace(c.TargetRepo)
	path := strings.TrimSpace(c.TargetPath)
	if repo == "" || !strings.Contains(strings.Trim(repo, "/"), "/") || path == "" {
		g.audit(ctx, sess, id, false)
		return PublishDraftResult{}, fmt.Errorf("%w: draft has no publish target (targetRepo/targetPath)", ErrBadRequest)
	}

	// 3. Scrub gate — refuse before any GitHub call while unresolved findings
	// remain (ISI-7970 acceptance; app/content.UnresolvedFindings is the shared
	// C3/C4 contract).
	if blockers := appcontent.UnresolvedFindings(c.ScrubFindings); len(blockers) > 0 {
		g.audit(ctx, sess, id, false)
		return PublishDraftResult{}, fmt.Errorf("%w: %d finding(s) must be resolved or waived", ErrBlocked, len(blockers))
	}

	// 4. Publish: commit the body to the target path and open exactly one PR.
	in := PublishInput{
		Path:   path,
		Body:   c.BodyMD,
		Title:  publishTitle(c),
		PRBody: publishBody(c),
	}
	if c.Origin != nil && c.Origin.Issue > 0 {
		in.OriginIssue = c.Origin.Issue // disambiguates the branch name across issues
	}
	res, err := g.publisher.Publish(ctx, repo, in)
	if err != nil {
		g.audit(ctx, sess, id, false)
		if errors.Is(err, ErrInvalid) {
			return PublishDraftResult{}, fmt.Errorf("%w: %v", ErrBadRequest, err)
		}
		return PublishDraftResult{}, fmt.Errorf("%w: %v", ErrReflect, err)
	}

	// 5. Back-fill the PR link + published status through the system seam.
	status := appcontent.ContentPublished
	if _, err := g.drafts.Backfill(ctx, id, bcontent.BackfillRequest{Status: &status, PRURL: &res.PRURL}); err != nil {
		g.audit(ctx, sess, id, false)
		return PublishDraftResult{}, fmt.Errorf("%w: recording PR on draft failed: %v", ErrReflect, err)
	}

	// 6. Comment the PR link onto the origin issue (which may live in a different
	// repo than the publish target). Soft: the PR is already open and recorded,
	// so a flaky issue comment must not fail the published draft.
	if c.Origin != nil && strings.Contains(strings.Trim(c.Origin.Repo, "/"), "/") && c.Origin.Issue > 0 {
		_, _ = g.publisher.CommentIssue(ctx, c.Origin.Repo, c.Origin.Issue, "📄 Published from Booth: "+res.PRURL)
	}

	g.audit(ctx, sess, id, true)
	return PublishDraftResult{
		ContentID:  c.ID,
		Repo:       repo,
		Path:       path,
		PRNumber:   res.PRNumber,
		PRURL:      res.PRURL,
		Branch:     res.Branch,
		Idempotent: res.Idempotent,
		Approver:   sess.Subject,
	}, nil
}

// publishTitle is the PR title for a published draft.
func publishTitle(c appcontent.Content) string {
	t := strings.TrimSpace(c.Title)
	if t == "" {
		t = "content: " + c.ID
	}
	return "publish: " + t
}

// publishBody is the PR description. It links the originating issue and, when
// the origin issue lives in the SAME repo as the PR target, adds a "Closes #n"
// so merging the PR closes the recycle issue (GitHub only honours Closes within
// the PR's own repo).
func publishBody(c appcontent.Content) string {
	var b strings.Builder
	b.WriteString("Published from Booth in-product approval (ISI-7970).\n")
	if c.Origin != nil && c.Origin.Issue > 0 {
		origin := strings.TrimSpace(c.Origin.Repo)
		if origin == strings.TrimSpace(c.TargetRepo) {
			fmt.Fprintf(&b, "\nCloses #%d\n", c.Origin.Issue)
		} else if strings.Contains(strings.Trim(origin, "/"), "/") {
			fmt.Fprintf(&b, "\nOrigin: %s#%d\n", origin, c.Origin.Issue)
		}
	}
	return b.String()
}

// mapContentErr translates the content gating store's sentinels onto the
// approve action's sentinels so the HTTP layer maps them uniformly.
func mapContentErr(err error) error {
	switch {
	case errors.Is(err, bcontent.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, bcontent.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, bcontent.ErrBadRequest):
		return fmt.Errorf("%w: %v", ErrBadRequest, err)
	default:
		return err
	}
}

// audit records one §6.4 entry for the publish verb against the draft.
func (g *DraftGate) audit(ctx context.Context, sess auth.Session, id string, allowed bool) {
	decision := audit.DecisionDeny
	if allowed {
		decision = audit.DecisionAllow
	}
	g.auditor.Log(ctx, audit.Entry{
		Subject:      sess.Subject,
		Role:         string(sess.Role),
		Verb:         verbPublish,
		ResourceKind: kindDraft,
		ResourceName: id,
		Decision:     decision,
	})
}
