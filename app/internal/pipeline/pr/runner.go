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
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

// IssueCommenter posts a comment on a recycle issue. Satisfied by
// *githubapp.GitHubClient (structural — the pr package does not import the
// webhook package; layers point inward, design §1).
type IssueCommenter interface {
	PostComment(ctx context.Context, repoFullName string, issueNumber int, token, body string) error
}

// Runner executes one recycle route end-to-end.
type Runner struct {
	// Analyzer is pipeline stage E. Required.
	Analyzer pipeline.Analyzer
	// Writer is pipeline stage F. Required.
	Writer pipeline.Writer
	// Checker is pipeline stage G (Mastering). Required.
	Checker pipeline.Checker
	// Assembler is pipeline stage H (this package). Required.
	Assembler *Assembler
	// Comments reports blocked/failed runs and opened PRs on the issue.
	// When nil, the runner only logs via the returned error.
	Comments IssueCommenter
}

// RunOutcome reports what one run did.
type RunOutcome struct {
	// Blocked is true when Mastering failed the Mix closed and no PR was
	// opened (design §4).
	Blocked bool
	// PRURL/PRNumber/Branch/Slug are set when a PR was opened (or already
	// existed for the branch).
	PRURL    string
	PRNumber int
	Branch   string
	Slug     string
}

// Run executes analyze → write → check → assemble for one route and keeps
// the issue informed. token is the per-repo PAT (ReferenceRepo tokenRef).
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
	check, err := r.Checker.Check(ctx, mix)
	if err != nil {
		r.reportFailure(ctx, rc, token, "mastering check", err)
		return RunOutcome{}, err
	}
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
