package content

import (
	"context"
	"errors"
	"testing"

	appcontent "github.com/ContentRemixer/Contentremixer/app/content"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
)

// TestIngestSeam is the ISI-7968 C1 check: the Deposit/Backfill seam is the one
// server-side entry point C2/C3/C5 use, it stamps provenance + target at
// creation, back-fills scrub findings and the PR link afterwards, audits under
// the system actor, and rejects an ownerless deposit. If the seam logic breaks,
// this fails.
func TestIngestSeam(t *testing.T) {
	ctx := context.Background()
	s, repo, aud := newTestGate()

	// Recycle deposit: C2's path. Provenance + target are stamped and survive.
	dep, err := s.Deposit(ctx, DepositRequest{
		Owner:      "alice",
		Source:     appcontent.SourceRecycle,
		Title:      "Recycled draft",
		BodyMD:     "# body",
		Origin:     &appcontent.Origin{Repo: "Org/Repo", Issue: 42},
		TargetRepo: "Org/Content",
		TargetPath: "posts/recycled.md",
	})
	if err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	if dep.Source != appcontent.SourceRecycle || dep.Status != appcontent.ContentDraft {
		t.Fatalf("deposit source/status = %q/%q", dep.Source, dep.Status)
	}
	if dep.Origin == nil || dep.Origin.Issue != 42 || dep.TargetPath != "posts/recycled.md" {
		t.Fatalf("deposit origin/target not stamped: %+v", dep)
	}
	if e := aud.find(verbCreate, kindContent, dep.ID, string(audit.DecisionAllow)); e == nil || e.Subject != systemActor {
		t.Fatalf("deposit not audited under system actor: %+v", e)
	}

	// Back-fill: C2 scrub findings then C3 PR link + published status.
	findings := []appcontent.ScrubFinding{{Rule: "aws-key", Severity: "block", Message: "found"}}
	if _, err := s.Backfill(ctx, dep.ID, BackfillRequest{ScrubFindings: findings}); err != nil {
		t.Fatalf("Backfill scrub: %v", err)
	}
	pr := "https://github.com/Org/Content/pull/7"
	pub := appcontent.ContentPublished
	got, err := s.Backfill(ctx, dep.ID, BackfillRequest{PRURL: &pr, Status: &pub})
	if err != nil {
		t.Fatalf("Backfill pr: %v", err)
	}
	if got.PRURL != pr || got.Status != appcontent.ContentPublished || len(got.ScrubFindings) != 1 {
		t.Fatalf("backfill result wrong: %+v", got)
	}
	if e := aud.find(verbUpdate, kindContent, dep.ID, string(audit.DecisionAllow)); e == nil || e.Subject != systemActor {
		t.Fatalf("backfill not audited under system actor: %+v", e)
	}

	// Ownerless deposit is rejected (no orphan drafts).
	if _, err := s.Deposit(ctx, DepositRequest{Source: appcontent.SourceManual, Title: "x"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("ownerless deposit: want ErrBadRequest, got %v", err)
	}

	// Manual create still funnels through the seam and defaults source=manual.
	man, err := s.CreateContent(ctx, devrel("bob"), CreateContentRequest{Title: "manual"})
	if err != nil {
		t.Fatalf("CreateContent: %v", err)
	}
	if man.Source != appcontent.SourceManual {
		t.Fatalf("manual source = %q, want manual", man.Source)
	}
	if _, ok := repo.contents[man.ID]; !ok {
		t.Fatalf("manual draft not persisted")
	}
}

// TestManualDraftTargetBinding is the ISI-7971 C5 check: manual drafts carry a
// target and ride the same PR-eligibility rule as recycled ones. A manual draft
// with no target stays editable but cannot be approved onto the PR path; once a
// target is bound (at create or via a later edit) the approve succeeds — the
// identical review → approve → PR path a recycled draft follows.
func TestManualDraftTargetBinding(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newTestGate()
	bob := devrel("bob")

	// A manual draft with no target is editable (title/body/in_review all fine)
	// but is NOT PR-eligible: the approve is rejected with a bad-request, not a
	// silent no-op, so the UI can explain why.
	noTarget, err := s.CreateContent(ctx, bob, CreateContentRequest{Title: "untargeted"})
	if err != nil {
		t.Fatalf("CreateContent: %v", err)
	}
	if noTarget.TargetRepo != "" || noTarget.TargetPath != "" {
		t.Fatalf("unexpected target on untargeted draft: %+v", noTarget)
	}
	review := appcontent.ContentInReview
	if _, err := s.UpdateContent(ctx, bob, noTarget.ID, UpdateContentRequest{Status: &review}); err != nil {
		t.Fatalf("in_review on untargeted draft should be allowed: %v", err)
	}
	approved := appcontent.ContentApproved
	if _, err := s.UpdateContent(ctx, bob, noTarget.ID, UpdateContentRequest{Status: &approved}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("approve without target: want ErrBadRequest, got %v", err)
	}

	// Binding the target in the SAME approve request satisfies the invariant.
	repo2 := "Org/Content"
	path2 := "posts/untargeted.md"
	bound, err := s.UpdateContent(ctx, bob, noTarget.ID, UpdateContentRequest{
		Status: &approved, TargetRepo: &repo2, TargetPath: &path2,
	})
	if err != nil {
		t.Fatalf("approve with target bound in-request: %v", err)
	}
	if bound.Status != appcontent.ContentApproved || bound.TargetPath != path2 {
		t.Fatalf("bound approve wrong: %+v", bound)
	}

	// A partial target (repo but no path) is still not PR-eligible.
	onlyRepo := "Org/Content"
	half, err := s.CreateContent(ctx, bob, CreateContentRequest{Title: "half", TargetRepo: onlyRepo})
	if err != nil {
		t.Fatalf("CreateContent half: %v", err)
	}
	if _, err := s.UpdateContent(ctx, bob, half.ID, UpdateContentRequest{Status: &approved}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("approve with only a repo: want ErrBadRequest, got %v", err)
	}

	// A manual draft targeted at CREATE time approves directly — the one-shot
	// authoring path.
	full, err := s.CreateContent(ctx, bob, CreateContentRequest{
		Title: "targeted", TargetRepo: "Org/Content", TargetPath: "posts/targeted.md",
	})
	if err != nil {
		t.Fatalf("CreateContent full: %v", err)
	}
	if full.Source != appcontent.SourceManual {
		t.Fatalf("source = %q, want manual", full.Source)
	}
	if _, err := s.UpdateContent(ctx, bob, full.ID, UpdateContentRequest{Status: &approved}); err != nil {
		t.Fatalf("approve pre-targeted manual draft: %v", err)
	}

	// Review F3: a whitespace-only target is not a target — a direct API caller
	// cannot approve past the server gate with blanks.
	blankRepo, blankPath := "   ", "\t"
	ws, err := s.CreateContent(ctx, bob, CreateContentRequest{Title: "ws"})
	if err != nil {
		t.Fatalf("CreateContent ws: %v", err)
	}
	if _, err := s.UpdateContent(ctx, bob, ws.ID, UpdateContentRequest{
		Status: &approved, TargetRepo: &blankRepo, TargetPath: &blankPath,
	}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("approve with whitespace target: want ErrBadRequest, got %v", err)
	}

	// Review F2: the system Backfill seam re-applies the invariant — it cannot
	// publish a targetless draft, but publishing a targeted one is fine.
	pub := appcontent.ContentPublished
	bare, err := s.Deposit(ctx, DepositRequest{Owner: "carol", Source: appcontent.SourceManual, Title: "bare"})
	if err != nil {
		t.Fatalf("Deposit bare: %v", err)
	}
	if _, err := s.Backfill(ctx, bare.ID, BackfillRequest{Status: &pub}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("Backfill publish without target: want ErrBadRequest, got %v", err)
	}
	pr := "https://github.com/Org/Content/pull/9"
	if _, err := s.Backfill(ctx, full.ID, BackfillRequest{Status: &pub, PRURL: &pr}); err != nil {
		t.Fatalf("Backfill publish a targeted draft: %v", err)
	}
}
