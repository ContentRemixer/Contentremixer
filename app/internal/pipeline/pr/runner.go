// Runner is the design-§4 run worker: it chains analyzer → writer → checker
// for one RunContext and hands a passing Mix to the assembler. FAIL-CLOSED:
// when Mastering blocks (or any stage errors), no PR is opened and the run
// is reported on the recycle issue with a comment.
package pr

import (
	"context"
	"fmt"
	"strings"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	appcontent "github.com/ContentRemixer/Contentremixer/app/content"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

// IssueCommenter posts a comment on a recycle issue. Satisfied by
// *githubapp.GitHubClient (structural — the pr package does not import the
// webhook package; layers point inward, design §1).
type IssueCommenter interface {
	PostComment(ctx context.Context, repoFullName string, issueNumber int, token, body string) error
}

// Depositor is the C1 ingest seam (ISI-7968) the recycle terminus deposits a
// ContentDraft through (C2, ISI-7969). Satisfied structurally by
// *appcontent.Store, which the operator opens against the shared booth.contents
// DSN — the pr package imports only the shared app/content types, never Booth.
type Depositor interface {
	Deposit(ctx context.Context, req appcontent.DepositRequest) (appcontent.Content, error)
	Backfill(ctx context.Context, id string, req appcontent.BackfillRequest) (appcontent.Content, error)
}

// Runner executes one recycle route end-to-end.
type Runner struct {
	// Analyzer is pipeline stage E. Required.
	Analyzer pipeline.Analyzer
	// Writer is pipeline stage F. Required.
	Writer pipeline.Writer
	// SEO is the post-write SEO enrichment stage (ISI-7806). Optional and
	// DEGRADE-NEVER-FAIL: when nil the run is unchanged; when set it runs
	// between Write and Check and never blocks the run.
	SEO pipeline.SEOOptimizer
	// Checker is pipeline stage G (Mastering). Required.
	Checker pipeline.Checker
	// Assembler is pipeline stage H (this package). It owns every git/PR
	// mutation and is PRESERVED for C3 (approval → PR, ISI-7970): when a
	// Deposit seam is wired the recycle terminus no longer calls it (C2 deposits
	// a draft instead of opening a PR), but the code stays live for the
	// nil-Deposit fallback and for C3 to drive on approval. Required.
	Assembler *Assembler
	// Deposit is the C1 ingest seam (ISI-7968). When wired, the recycle terminus
	// DEPOSITS a ContentDraft into /content instead of opening a PR (C2,
	// ISI-7969) — the acceptance path. When nil, the runner falls back to the
	// pre-C2 assemble-a-PR behaviour so an operator without the content DSN
	// configured still works unchanged.
	Deposit Depositor
	// DraftOwner is the owner subject stamped on deposited recycle drafts — the
	// §6.3 tenant the draft is visible to (an Admin sees all drafts; a matching
	// DevRel session sees its own). Recycle runs are platform-owned in Phase-1,
	// so this is a configured sentinel/DevRel subject, never empty: an empty
	// owner is invisible to everyone but an Admin and is rejected by the seam.
	// Required when Deposit is wired.
	DraftOwner string
	// Comments reports blocked/failed runs and opened PRs on the issue.
	// When nil, the runner only logs via the returned error.
	Comments IssueCommenter
}

// RunOutcome reports what one run did.
type RunOutcome struct {
	// Blocked is true when Mastering failed the Mix closed and no PR was
	// opened (design §4). Only the nil-Deposit (assemble) fallback can set it:
	// the C2 deposit path never blocks — scrub findings become annotations on
	// the draft, not a hard gate (ISI-7969, the fix for the #7 disappearance).
	Blocked bool
	// DraftID is set when a ContentDraft was deposited (the C2 path).
	DraftID string
	// PRURL/PRNumber/Branch/Slug are set when a PR was opened (or already
	// existed for the branch) on the nil-Deposit fallback path.
	PRURL    string
	PRNumber int
	Branch   string
	Slug     string
}

// Run executes analyze → write → check → (deposit | assemble) for one route and
// keeps the issue informed. token is the per-repo PAT (ReferenceRepo tokenRef).
func (r *Runner) Run(ctx context.Context, rc pipeline.RunContext, target contentremixerv1alpha1.PRTargetSpec, token string) (RunOutcome, error) {
	if r == nil || r.Analyzer == nil || r.Writer == nil || r.Checker == nil || r.Assembler == nil {
		return RunOutcome{}, fmt.Errorf("pr: runner is not fully wired (analyzer, writer, checker, assembler)")
	}

	sample, err := r.Analyzer.Analyze(ctx, rc)
	if err != nil {
		r.reportFailure(ctx, rc, token, "analyzing the source", err)
		return RunOutcome{}, err
	}
	mix, err := r.Writer.Write(ctx, rc, sample)
	if err != nil {
		r.reportFailure(ctx, rc, token, "writing the "+rc.OutputFormat+" draft", err)
		return RunOutcome{}, err
	}
	// SEO enrichment (ISI-7806) between Write and Check. DEGRADE-NEVER-FAIL:
	// the optimizer returns the Mix unchanged on any failure, and even a
	// returned error is non-fatal here — SEO never blocks a run, so we keep
	// the pre-optimize Mix and carry on.
	if r.SEO != nil {
		if enriched, serr := r.SEO.Optimize(ctx, rc, mix); serr == nil {
			mix = enriched
		}
	}
	check, err := r.Checker.Check(ctx, mix)
	if err != nil {
		r.reportFailure(ctx, rc, token, "mastering check", err)
		return RunOutcome{}, err
	}

	// C2 (ISI-7969): when the ingest seam is wired, deposit a ContentDraft into
	// /content instead of opening a PR. The scrubber's findings become
	// ScrubFindings ANNOTATIONS on the draft — they no longer fail the run
	// closed, so nothing silently disappears (the fix for the #7 disappearance).
	if r.Deposit != nil {
		return r.deposit(ctx, rc, token, sample, mix, check)
	}

	// Fallback (no content DSN configured): the pre-C2 assemble-a-PR path, which
	// is also the commit/PR logic C3 reuses on approval. It stays fail-closed.
	if !check.Passed {
		r.reportBlocked(ctx, rc, token, check)
		return RunOutcome{Blocked: true}, nil
	}
	res, err := r.Assembler.Assemble(ctx, rc, sample, mix, target, token)
	if err != nil {
		r.reportFailure(ctx, rc, token, "assembling the pull request", err)
		return RunOutcome{}, err
	}
	r.comment(ctx, rc, token, openedComment(rc, sample, res))
	return RunOutcome{PRURL: res.PRURL, PRNumber: res.PRNumber, Branch: res.Branch, Slug: res.Slug}, nil
}

// deposit lands the generated Mix as a ContentDraft (source=recycle) in
// /content, annotates it with any scrub findings, and reports the draft back on
// the recycle issue. It NEVER opens a PR and NEVER blocks on scrub findings
// (C2): the draft is the deliverable, ready for human review; C3 opens the PR on
// approval using the preserved Assembler.
func (r *Runner) deposit(ctx context.Context, rc pipeline.RunContext, token string, sample pipeline.Sample, mix pipeline.Mix, check pipeline.CheckResult) (RunOutcome, error) {
	slug := Slug(rc, sample)
	title := strings.TrimSpace(mix.Title)
	if title == "" {
		title = "recycled " + mix.OutputFormat
	}
	// TargetRepo/TargetPath record where C3's PR will publish this draft: the
	// reference repo, at the same recycled/<slug>/index.md the Assembler writes.
	draft, err := r.Deposit.Deposit(ctx, appcontent.DepositRequest{
		Owner:      r.DraftOwner,
		Source:     appcontent.SourceRecycle,
		Title:      title,
		BodyMD:     mix.Markdown,
		Origin:     &appcontent.Origin{Repo: rc.Repo, Issue: rc.IssueNumber},
		TargetRepo: rc.Repo,
		TargetPath: "recycled/" + slug + "/index.md",
	})
	if err != nil {
		r.reportFailure(ctx, rc, token, "depositing the content draft", err)
		return RunOutcome{}, err
	}
	// Attach scrub findings as annotations (never a hard block). Both block- and
	// warn-severity findings are recorded so a human reviewer sees everything the
	// scrubber caught; the evidence is already redacted (Violation.Match).
	findings := scrubFindings(check.Violations)
	if len(findings) > 0 {
		if _, err := r.Deposit.Backfill(ctx, draft.ID, appcontent.BackfillRequest{ScrubFindings: findings}); err != nil {
			// A back-fill failure must not lose the draft: it is already in
			// /content. Surface the degraded state on the issue and carry on.
			r.comment(ctx, rc, token, fmt.Sprintf("🎛️ **ContentRemixer** deposited draft `%s` but could not attach %d scrub finding(s): %s\n", draft.ID, len(findings), truncate(err.Error(), 400)))
		}
	}
	r.comment(ctx, rc, token, depositedComment(rc, sample, draft, findings))
	return RunOutcome{DraftID: draft.ID, Slug: slug}, nil
}

// scrubFindings maps the checker's redacted Violations onto draft annotations.
func scrubFindings(vs []pipeline.Violation) []appcontent.ScrubFinding {
	if len(vs) == 0 {
		return nil
	}
	out := make([]appcontent.ScrubFinding, 0, len(vs))
	for _, v := range vs {
		msg := v.Match
		if v.Where != "" {
			msg = v.Where + ": " + v.Match
		}
		out = append(out, appcontent.ScrubFinding{
			Rule:     v.Rule,
			Severity: v.Severity,
			Message:  msg,
		})
	}
	return out
}

// depositedComment reports the deposited draft back on the recycle issue,
// summarising any scrub findings a reviewer should weigh.
func depositedComment(rc pipeline.RunContext, s pipeline.Sample, draft appcontent.Content, findings []appcontent.ScrubFinding) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "🎛️ **ContentRemixer** deposited a draft in `/content` for review — **no PR was opened** (C2).\n\n")
	_, _ = fmt.Fprintf(&b, "- Draft `%s` · status `%s` · target `%s`/`%s`\n", draft.ID, draft.Status, draft.TargetRepo, draft.TargetPath)
	_, _ = fmt.Fprintf(&b, "- Source `%s` @ `%s`\n", s.SourcePath, shortSHA(s.SourceCommit))
	if len(findings) == 0 {
		b.WriteString("- Scrubber: no findings.\n")
	} else {
		_, _ = fmt.Fprintf(&b, "- Scrubber: %d finding(s) recorded as annotations on the draft (not a block):\n", len(findings))
		for _, f := range findings {
			_, _ = fmt.Fprintf(&b, "  - `%s` (**%s**): %s\n", f.Rule, f.Severity, f.Message)
		}
	}
	b.WriteString("\nReview and approve the draft to publish it via the GitHub PR path (C3).\n")
	return b.String()
}

// reportBlocked posts the fail-closed Mastering outcome on the issue. The
// checker already redacted the evidence (Violation.Match); the comment never
// re-leaks what was caught.
func (r *Runner) reportBlocked(ctx context.Context, rc pipeline.RunContext, token string, check pipeline.CheckResult) {
	var b strings.Builder
	b.WriteString("🎛️ **ContentRemixer** run blocked by Mastering — **no PR was opened** (fail-closed).\n\n")
	b.WriteString("Violations found in the generated draft:\n")
	for _, v := range check.Violations {
		_, _ = fmt.Fprintf(&b, "- `%s` (**%s**, %s): %s\n", v.Rule, v.Severity, v.Where, v.Match)
	}
	b.WriteString("\nFix the source or the generation guidance, then remove and re-apply the `recycle:*` label to retry.\n")
	r.comment(ctx, rc, token, b.String())
}

// openedComment reports the assembled PR back on the recycle issue.
func openedComment(rc pipeline.RunContext, s pipeline.Sample, res AssembleResult) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "🎛️ **ContentRemixer** opened #%d: %s\n\n", res.PRNumber, res.PRURL)
	_, _ = fmt.Fprintf(&b, "- Branch `%s` · folder `recycled/%s/`\n", res.Branch, res.Slug)
	_, _ = fmt.Fprintf(&b, "- Source `%s` @ `%s`\n", s.SourcePath, shortSHA(s.SourceCommit))
	b.WriteString("- Sample clearance is in the PR body and in `recycled/" + res.Slug + "/meta.yaml`.\n")
	return b.String()
}

// reportFailure posts a stage failure on the issue before the error travels
// up (fail-visible: nothing silently disappears on a public issue).
func (r *Runner) reportFailure(ctx context.Context, rc pipeline.RunContext, token, stage string, err error) {
	r.comment(ctx, rc, token, fmt.Sprintf("🎛️ **ContentRemixer** run failed while %s — no PR was opened.\n\n```\n%s\n```\n", stage, truncate(err.Error(), 800)))
}

// comment posts unless commenting is not wired.
func (r *Runner) comment(ctx context.Context, rc pipeline.RunContext, token, body string) {
	if r.Comments == nil {
		return
	}
	_ = r.Comments.PostComment(ctx, rc.Repo, rc.IssueNumber, token, body)
}

// truncate bounds quoted error text in issue comments.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
