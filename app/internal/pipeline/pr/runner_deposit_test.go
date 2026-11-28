package pr

import (
	"context"
	"strings"
	"sync"
	"testing"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	appcontent "github.com/ContentRemixer/Contentremixer/app/content"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/writer"
)

// fakeDepositor is an in-memory C1 ingest seam: it records deposited drafts and
// back-fills without any Postgres, so the deposit terminus (C2) is testable.
type fakeDepositor struct {
	mu        sync.Mutex
	deposited []appcontent.DepositRequest
	backfills map[string]appcontent.BackfillRequest
	store     map[string]appcontent.Content
	seq       int
}

func newFakeDepositor() *fakeDepositor {
	return &fakeDepositor{backfills: map[string]appcontent.BackfillRequest{}, store: map[string]appcontent.Content{}}
}

func (f *fakeDepositor) Deposit(_ context.Context, req appcontent.DepositRequest) (appcontent.Content, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := "draft-" + string(rune('0'+f.seq))
	f.deposited = append(f.deposited, req)
	c := appcontent.Content{
		ID:           id,
		OwnerSubject: req.Owner,
		Title:        req.Title,
		BodyMD:       req.BodyMD,
		Status:       appcontent.ContentDraft,
		Source:       req.Source,
		Origin:       req.Origin,
		TargetRepo:   req.TargetRepo,
		TargetPath:   req.TargetPath,
	}
	f.store[id] = c
	return c, nil
}

func (f *fakeDepositor) Backfill(_ context.Context, id string, req appcontent.BackfillRequest) (appcontent.Content, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.backfills[id] = req
	c := f.store[id]
	c.ScrubFindings = req.ScrubFindings
	f.store[id] = c
	return c, nil
}

// TestRunnerDepositsDraftInsteadOfPR proves the C2 acceptance: with the ingest
// seam wired, a recycle run deposits a visible ContentDraft (source=recycle,
// origin + target recorded) and opens NO PR.
func TestRunnerDepositsDraftInsteadOfPR(t *testing.T) {
	fake := &fakeGitHub{}
	srv := fake.server(t)
	comments := &recordingComments{}
	runner := newRunner(writer.GeneratorFunc(func(context.Context, writer.GenerateRequest) (writer.GenerateResponse, error) {
		return writer.GenerateResponse{
			Content: "# The boring fix for leaking secrets\n\nRotate early.\n\n## Steps\n\nOne at a time.\n",
			Model:   "anthropic/claude-opus-4",
		}, nil
	}), comments, srv.URL)
	dep := newFakeDepositor()
	runner.Deposit = dep
	runner.DraftOwner = "system:recycle"

	out, err := runner.Run(context.Background(), runContext(), contentremixerv1alpha1.PRTargetSpec{}, testPAT)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Blocked {
		t.Fatalf("deposit path must never block")
	}
	if out.DraftID == "" || out.PRNumber != 0 || out.PRURL != "" {
		t.Fatalf("expected a deposited draft and no PR: %+v", out)
	}
	if n := fake.countCalls("/pulls"); n != 0 {
		t.Fatalf("deposit path must not open a PR; got %d /pulls calls", n)
	}
	if n := fake.countCalls("/git/"); n != 0 {
		t.Fatalf("deposit path must not mutate git; got %d /git/ calls", n)
	}
	if len(dep.deposited) != 1 {
		t.Fatalf("expected exactly one deposit, got %d", len(dep.deposited))
	}
	got := dep.deposited[0]
	if got.Source != appcontent.SourceRecycle {
		t.Errorf("source = %q, want recycle", got.Source)
	}
	if got.Owner != "system:recycle" {
		t.Errorf("owner = %q, want system:recycle", got.Owner)
	}
	if got.Origin == nil || got.Origin.Repo != testRepo || got.Origin.Issue != testIssue {
		t.Errorf("origin = %+v, want {%s #%d}", got.Origin, testRepo, testIssue)
	}
	if got.TargetRepo != testRepo || got.TargetPath != "recycled/42-blog-demo-video/index.md" {
		t.Errorf("target = %s/%s, want %s/recycled/42-blog-demo-video/index.md", got.TargetRepo, got.TargetPath, testRepo)
	}
	if !strings.Contains(got.BodyMD, "boring fix for leaking secrets") {
		t.Errorf("deposited body is not the generated Mix: %q", got.BodyMD)
	}
	// This fixture carries no secrets, so any findings the checker emits are
	// advisory (seo-meta warnings) — they annotate the draft and never block it.
	for _, bf := range dep.backfills {
		for _, f := range bf.ScrubFindings {
			if f.Severity == "block" {
				t.Errorf("clean fixture produced an unexpected block finding: %+v", f)
			}
		}
	}
	bodies := comments.bodies()
	if len(bodies) == 0 || !strings.Contains(bodies[len(bodies)-1], "deposited a draft") {
		t.Errorf("expected a draft-deposited issue comment, got %v", bodies)
	}
}

// TestRunnerDepositRecordsScrubFindingsNotBlock proves the #7-disappearance
// fix: a draft the scrubber flags is STILL deposited, and the findings become
// annotations (Backfill) rather than a fail-closed hard block.
func TestRunnerDepositRecordsScrubFindingsNotBlock(t *testing.T) {
	fake := &fakeGitHub{}
	srv := fake.server(t)
	comments := &recordingComments{}
	runner := newRunner(writer.GeneratorFunc(func(context.Context, writer.GenerateRequest) (writer.GenerateResponse, error) {
		return writer.GenerateResponse{
			Content: "# Leaky draft\n\nKey sk-proj-0123456789abcdefghijklmnop and host 10.0.0.5 in prose.\n",
			Model:   "anthropic/claude-opus-4",
		}, nil
	}), comments, srv.URL)
	dep := newFakeDepositor()
	runner.Deposit = dep
	runner.DraftOwner = "system:recycle"

	out, err := runner.Run(context.Background(), runContext(), contentremixerv1alpha1.PRTargetSpec{}, testPAT)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Blocked {
		t.Fatalf("scrub findings must NOT block the deposit path (the #7 fix)")
	}
	if out.DraftID == "" {
		t.Fatalf("the flagged draft must still be deposited: %+v", out)
	}
	if n := fake.countCalls("/pulls"); n != 0 {
		t.Fatalf("deposit path must not open a PR; got %d /pulls calls", n)
	}
	bf, ok := dep.backfills[out.DraftID]
	if !ok || len(bf.ScrubFindings) == 0 {
		t.Fatalf("expected scrub findings back-filled onto the draft, got %+v", dep.backfills)
	}
	var rules []string
	for _, f := range bf.ScrubFindings {
		rules = append(rules, f.Rule)
		if strings.Contains(f.Message, "sk-proj-0123456789abcdefghijklmnop") {
			t.Errorf("finding message must not re-leak the raw secret: %q", f.Message)
		}
	}
	joined := strings.Join(rules, ",")
	for _, want := range []string{"token", "ip"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected a %q finding, got rules %v", want, rules)
		}
	}
	// The issue comment surfaces the findings but never re-leaks the secret.
	last := comments.bodies()[len(comments.bodies())-1]
	if !strings.Contains(last, "annotations on the draft") {
		t.Errorf("deposit comment should explain findings are annotations, got:\n%s", last)
	}
	if strings.Contains(last, "sk-proj-0123456789abcdefghijklmnop") {
		t.Errorf("deposit comment must not re-leak the caught secret:\n%s", last)
	}
}
