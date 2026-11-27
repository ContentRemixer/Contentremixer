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
