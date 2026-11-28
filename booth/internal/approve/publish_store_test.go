package approve

import (
	"context"
	"errors"
	"testing"

	appcontent "github.com/ContentRemixer/Contentremixer/app/content"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	bcontent "github.com/ContentRemixer/Contentremixer/booth/internal/content"
)

// fakeDrafts is a draftStore double: GetContent returns the seeded draft (or a
// seeded error), Backfill records the patch and applies status/prUrl so a
// second GetContent reflects a published draft.
type fakeDrafts struct {
	draft    appcontent.Content
	getErr   error
	backfill *bcontent.BackfillRequest
	backErr  error
}

func (f *fakeDrafts) GetContent(_ context.Context, _ auth.Session, _ string) (appcontent.Content, error) {
	if f.getErr != nil {
		return appcontent.Content{}, f.getErr
	}
	return f.draft, nil
}

func (f *fakeDrafts) Backfill(_ context.Context, _ string, req bcontent.BackfillRequest) (appcontent.Content, error) {
	f.backfill = &req
	if f.backErr != nil {
		return appcontent.Content{}, f.backErr
	}
	out := f.draft
	if req.Status != nil {
		out.Status = *req.Status
	}
	if req.PRURL != nil {
		out.PRURL = *req.PRURL
	}
	return out, nil
}

// fakePublisher records Publish/CommentIssue calls and returns seeded results.
type fakePublisher struct {
	pubResult   PublishResult
	pubErr      error
	pubCalls    int
	lastInput   PublishInput
	lastRepo    string
	commentCall struct {
		repo  string
		issue int
		body  string
		calls int
	}
	commentErr error
}

func (p *fakePublisher) Publish(_ context.Context, repo string, in PublishInput) (PublishResult, error) {
	p.pubCalls++
	p.lastRepo = repo
	p.lastInput = in
	if p.pubErr != nil {
		return PublishResult{}, p.pubErr
	}
	return p.pubResult, nil
}

func (p *fakePublisher) CommentIssue(_ context.Context, repo string, issue int, body string) (string, error) {
	p.commentCall.repo, p.commentCall.issue, p.commentCall.body = repo, issue, body
	p.commentCall.calls++
	if p.commentErr != nil {
		return "", p.commentErr
	}
	return "https://github.com/owner/src/issues/" + "comment", nil
}

func reviewedDraft() appcontent.Content {
	return appcontent.Content{
		ID:           "c-1",
		OwnerSubject: "henrik",
		Title:        "My Post",
		BodyMD:       "# Hello\n",
		Status:       appcontent.ContentInReview,
		TargetRepo:   "owner/site",
		TargetPath:   "content/blog/my-post.md",
		Origin:       &appcontent.Origin{Repo: "owner/src", Issue: 42},
	}
}

func sess() auth.Session { return auth.Session{Subject: "henrik", Role: auth.RoleAdmin} }

func TestPublishDraft_HappyPath(t *testing.T) {
	drafts := &fakeDrafts{draft: reviewedDraft()}
	pub := &fakePublisher{pubResult: PublishResult{Branch: "content/my-post-42", PRNumber: 7, PRURL: "https://github.com/owner/site/pull/7"}}
	aud := &capAudit{}
	g := NewDraftGate(drafts, pub, aud)

	out, err := g.PublishDraft(context.Background(), sess(), "c-1")
	if err != nil {
		t.Fatalf("PublishDraft: %v", err)
	}
	if out.PRURL != "https://github.com/owner/site/pull/7" || out.PRNumber != 7 {
		t.Fatalf("unexpected result: %+v", out)
	}
	if pub.pubCalls != 1 {
		t.Fatalf("want 1 publish call, got %d", pub.pubCalls)
	}
	if pub.lastRepo != "owner/site" || pub.lastInput.Path != "content/blog/my-post.md" {
		t.Fatalf("publish target wrong: repo=%q path=%q", pub.lastRepo, pub.lastInput.Path)
	}
	// Back-fill records published status + PR link.
	if drafts.backfill == nil || drafts.backfill.Status == nil || *drafts.backfill.Status != appcontent.ContentPublished {
		t.Fatalf("expected published back-fill, got %+v", drafts.backfill)
	}
	if drafts.backfill.PRURL == nil || *drafts.backfill.PRURL != out.PRURL {
		t.Fatalf("expected PR url back-fill")
	}
	// Origin comment lands on the origin repo+issue, NOT the target repo.
	if pub.commentCall.calls != 1 || pub.commentCall.repo != "owner/src" || pub.commentCall.issue != 42 {
		t.Fatalf("origin comment wrong: %+v", pub.commentCall)
	}
	if aud.last.Decision != "allow" || aud.last.Verb != verbPublish {
		t.Fatalf("audit: %+v", aud.last)
	}
}

func TestPublishDraft_ScrubGateBlocks(t *testing.T) {
	d := reviewedDraft()
	d.ScrubFindings = []appcontent.ScrubFinding{{Rule: "aws-key", Severity: "block"}}
	drafts := &fakeDrafts{draft: d}
	pub := &fakePublisher{}
	aud := &capAudit{}
	g := NewDraftGate(drafts, pub, aud)

	_, err := g.PublishDraft(context.Background(), sess(), "c-1")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
	if pub.pubCalls != 0 {
		t.Fatal("must not publish while scrub gate is closed")
	}
	if aud.last.Decision != "deny" {
		t.Fatalf("expected deny audit, got %+v", aud.last)
	}
}

func TestPublishDraft_WaivedFindingPasses(t *testing.T) {
	d := reviewedDraft()
	d.ScrubFindings = []appcontent.ScrubFinding{{Rule: "aws-key", Severity: "block", Waived: true, WaivedBy: "admin"}}
	drafts := &fakeDrafts{draft: d}
	pub := &fakePublisher{pubResult: PublishResult{PRURL: "u"}}
	g := NewDraftGate(drafts, pub, &capAudit{})
	if _, err := g.PublishDraft(context.Background(), sess(), "c-1"); err != nil {
		t.Fatalf("waived finding should pass the gate: %v", err)
	}
	if pub.pubCalls != 1 {
		t.Fatal("expected publish after waiver")
	}
}

func TestPublishDraft_MissingTarget(t *testing.T) {
	d := reviewedDraft()
	d.TargetPath = ""
	drafts := &fakeDrafts{draft: d}
	pub := &fakePublisher{}
	g := NewDraftGate(drafts, pub, &capAudit{})
	_, err := g.PublishDraft(context.Background(), sess(), "c-1")
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("want ErrBadRequest for missing target, got %v", err)
	}
	if pub.pubCalls != 0 {
		t.Fatal("must not publish without a target")
	}
}

func TestPublishDraft_AlreadyPublishedIsIdempotent(t *testing.T) {
	d := reviewedDraft()
	d.Status = appcontent.ContentPublished
	d.PRURL = "https://github.com/owner/site/pull/7"
	drafts := &fakeDrafts{draft: d}
	pub := &fakePublisher{}
	out, err := NewDraftGate(drafts, pub, &capAudit{}).PublishDraft(context.Background(), sess(), "c-1")
	if err != nil {
		t.Fatalf("published draft should be a no-op, got %v", err)
	}
	if !out.Idempotent || out.PRURL != d.PRURL {
		t.Fatalf("expected idempotent no-op with existing PR, got %+v", out)
	}
	if pub.pubCalls != 0 {
		t.Fatal("must not re-open a PR for an already-published draft")
	}
}

func TestPublishDraft_ForbiddenMapsThrough(t *testing.T) {
	drafts := &fakeDrafts{getErr: bcontent.ErrForbidden}
	g := NewDraftGate(drafts, &fakePublisher{}, &capAudit{})
	if _, err := g.PublishDraft(context.Background(), sess(), "c-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestPublishDraft_SoftOriginCommentFailure(t *testing.T) {
	drafts := &fakeDrafts{draft: reviewedDraft()}
	pub := &fakePublisher{pubResult: PublishResult{PRURL: "u", PRNumber: 1}, commentErr: errors.New("boom")}
	out, err := NewDraftGate(drafts, pub, &capAudit{}).PublishDraft(context.Background(), sess(), "c-1")
	if err != nil {
		t.Fatalf("origin-comment failure must be soft, got %v", err)
	}
	if out.PRURL != "u" {
		t.Fatalf("publish should still succeed: %+v", out)
	}
}
