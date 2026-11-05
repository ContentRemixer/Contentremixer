// Package pr implements pipeline subtask H (ISI-7083, design §5 "PR contract
// + Sample-clearance"): the last pipeline stage and the ONLY component that
// mutates git / recycled/ (design §4).
//
// Given a checked-out Sample and a passed Mix, the Assembler:
//
//  1. derives the stable slug <issue>-<outputFormat>-<source-slug> and the
//     branch <PRTarget.BranchPrefix><slug> off PRTarget.BaseBranch;
//  2. writes recycled/<slug>/index.md (the Mix body) and
//     recycled/<slug>/meta.yaml (Mix.Meta + the sample_clearance block, so
//     clearance survives outside the PR);
//  3. opens one draft-able PR via the GitHub REST API using the per-repo PAT,
//     whose body carries the machine-parseable Sample-clearance block
//     (source path, source commit SHA, model, input → output, recycle issue)
//     and links back to the originating issue ("Closes #<n>").
//
// The three clearance fields — source path, source commit SHA, model — are
// MANDATORY (plan acceptance criteria); Assemble fails closed before any
// network call when any is missing.
//
// Re-runs are idempotent: an existing branch is reused (the new commit lands
// on top of it) and an already-open PR for the branch is returned instead of
// duplicated. GitHub redelivers webhooks and fires both `opened` and
// `labeled` for the same issue, so the assembler treats "already exists" as
// success, not as an error.
//
// The Runner (runner.go) is the design-§4 run worker: it chains
// analyzer → writer → checker → assembler for one RunContext, posts the
// blocked-run comment (redacted violations) when Mastering fails closed, and
// never opens a PR for a failed check.
package pr

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

// DefaultBranchPrefix mirrors the ReferenceRepo PRTargetSpec default.
const DefaultBranchPrefix = "recycle/"

// DefaultBaseBranch mirrors the ReferenceRepo PRTargetSpec default.
const DefaultBaseBranch = "main"

// maxSlugSource caps the source-derived part of a slug so whole slugs stay
// readable in branch names and PR titles.
const maxSlugSource = 48

// Assembler is the subtask-H PR assembler: it owns every git/recycled/
// mutation in the system (design §4/§5).
type Assembler struct {
	// GitHub performs the REST calls (git data + pulls). Required.
	GitHub *GitHubClient
}

// AssembleResult reports what the assembler did.
type AssembleResult struct {
	// Slug is the recycled/<slug>/ folder name.
	Slug string
	// Branch is the PR head branch that received the commit.
	Branch string
	// CommitSHA is the SHA of the commit written to the branch.
	CommitSHA string
	// PRNumber and PRURL identify the opened (or pre-existing) PR.
	PRNumber int
	PRURL    string
}

// Assemble writes recycled/<slug>/ on a new branch and opens the PR linked
// back to the recycle issue (design §5). target's empty fields fall back to
// the PRTargetSpec defaults (main / recycle/); token is the per-repo PAT.
func (a *Assembler) Assemble(ctx context.Context, rc pipeline.RunContext, s pipeline.Sample, m pipeline.Mix, target contentremixerv1alpha1.PRTargetSpec, token string) (AssembleResult, error) {
	if a == nil || a.GitHub == nil {
		return AssembleResult{}, fmt.Errorf("pr: no GitHub client configured")
	}
	if err := validateClearance(rc, s, m); err != nil {
		return AssembleResult{}, err
	}

	slug := Slug(rc, s)
	branch := BranchName(target, slug)
	base := strings.TrimSpace(target.BaseBranch)
	if base == "" {
		base = DefaultBaseBranch
	}

	// Start the branch at the base (or reuse it when a previous run left it).
	headSHA, err := a.GitHub.refSHA(ctx, rc.Repo, base, token)
	if err != nil {
		return AssembleResult{}, fmt.Errorf("pr: resolving base branch %q: %w", base, err)
	}
	branchSHA, err := a.GitHub.createBranch(ctx, rc.Repo, branch, headSHA, token)
	if err == errBranchExists {
		branchSHA, err = a.GitHub.refSHA(ctx, rc.Repo, branch, token)
	}
	if err != nil {
		return AssembleResult{}, fmt.Errorf("pr: preparing branch %q: %w", branch, err)
	}

	// recycled/<slug>/{index.md, meta.yaml}
	indexBody := m.Markdown
	if !strings.HasSuffix(indexBody, "\n") {
		indexBody += "\n"
	}
	metaYAML, err := renderMetaYAML(rc, s, m)
	if err != nil {
		return AssembleResult{}, err
	}
	tree, err := a.GitHub.commitTree(ctx, rc.Repo, branchSHA, token)
	if err != nil {
		return AssembleResult{}, fmt.Errorf("pr: reading commit tree of %q: %w", branch, err)
	}
	entries := make([]treeEntry, 0, 2)
	for _, f := range []struct {
		name    string
		content string
	}{
		{slug + "/index.md", indexBody},
		{slug + "/meta.yaml", metaYAML},
	} {
		blobSHA, err := a.GitHub.createBlob(ctx, rc.Repo, f.content, token)
		if err != nil {
			return AssembleResult{}, fmt.Errorf("pr: creating blob recycled/%s: %w", f.name, err)
		}
		entries = append(entries, treeEntry{Path: "recycled/" + f.name, Mode: "100644", Type: "blob", SHA: blobSHA})
	}
	newTree, err := a.GitHub.createTree(ctx, rc.Repo, tree, entries, token)
	if err != nil {
		return AssembleResult{}, fmt.Errorf("pr: creating tree recycled/%s: %w", slug, err)
	}

	commitSHA, err := a.GitHub.createCommit(ctx, rc.Repo, commitMessage(rc, s, m, slug), newTree, []string{branchSHA}, token)
	if err != nil {
		return AssembleResult{}, fmt.Errorf("pr: committing recycled/%s: %w", slug, err)
	}
	if err := a.GitHub.updateRef(ctx, rc.Repo, branch, commitSHA, token); err != nil {
		return AssembleResult{}, fmt.Errorf("pr: advancing branch %q: %w", branch, err)
	}

	prTitle := PRTitle(rc, m)
	prNum, prURL, err := a.GitHub.createPull(ctx, rc.Repo, prTitle, PRBody(rc, s, m), branch, base, target.Draft, token)
	if err == errPRExists {
		prNum, prURL, err = a.GitHub.findOpenPull(ctx, rc.Repo, branch, token)
	}
	if err != nil {
		return AssembleResult{}, fmt.Errorf("pr: opening pull request from %q: %w", branch, err)
	}

	return AssembleResult{Slug: slug, Branch: branch, CommitSHA: commitSHA, PRNumber: prNum, PRURL: prURL}, nil
}

// validateClearance enforces the mandatory Sample-clearance fields (design
// §5) BEFORE any network call: a run that cannot prove its provenance must
// not mutate anything.
func validateClearance(rc pipeline.RunContext, s pipeline.Sample, m pipeline.Mix) error {
	if strings.TrimSpace(rc.Repo) == "" {
		return fmt.Errorf("pr: run context has no repo (issue %d)", rc.IssueNumber)
	}
	if rc.IssueNumber <= 0 {
		return fmt.Errorf("pr: run context has no issue number")
	}
	if strings.TrimSpace(s.SourcePath) == "" {
		return fmt.Errorf("pr: sample has no source path — Sample-clearance is mandatory, refusing to assemble (issue %d)", rc.IssueNumber)
	}
	if strings.TrimSpace(s.SourceCommit) == "" {
		return fmt.Errorf("pr: sample from %q has no source commit SHA — Sample-clearance is mandatory, refusing to assemble (issue %d)", s.SourcePath, rc.IssueNumber)
	}
	if strings.TrimSpace(m.ModelUsed) == "" {
		return fmt.Errorf("pr: mix has no model recorded — Sample-clearance is mandatory, refusing to assemble (issue %d)", rc.IssueNumber)
	}
	if strings.TrimSpace(m.Markdown) == "" {
		return fmt.Errorf("pr: mix body is empty — refusing to assemble (issue %d)", rc.IssueNumber)
	}
	if strings.TrimSpace(m.OutputFormat) == "" {
		return fmt.Errorf("pr: mix has no output format — refusing to assemble (issue %d)", rc.IssueNumber)
	}
	return nil
}

// Slug derives the stable, collision-resistant, greppable folder name
// <issue-number>-<outputFormat>-<source-slug> (design §5). The issue number
// namespaces it; the source slug keeps it traceable to the source item.
func Slug(rc pipeline.RunContext, s pipeline.Sample) string {
	return fmt.Sprintf("%d-%s-%s", rc.IssueNumber, slugify(rc.OutputFormat, 24), sourceSlug(s.SourcePath))
}

// BranchName prefixes the slug with PRTarget.BranchPrefix (default
// "recycle/"), yielding e.g. "recycle/42-blog-2026-11-04-otel-k8s".
func BranchName(target contentremixerv1alpha1.PRTargetSpec, slug string) string {
	prefix := strings.TrimSpace(target.BranchPrefix)
	if prefix == "" {
		prefix = DefaultBranchPrefix
	}
	return prefix + slug
}

// sourceSlug reduces a source path or URL to its identifying segment: the
// item folder for content/ paths ("content/videos/2026/demo-video" →
// "demo-video"), the file basename for standalone sources (extension
// stripped), and host+path for external URLs.
func sourceSlug(sourcePath string) string {
	src := strings.TrimSpace(sourcePath)
	if src == "" {
		return "source"
	}
	if u, err := url.Parse(src); err == nil && u.Host != "" {
		return slugify(u.Host+" "+strings.Trim(u.Path, "/"), maxSlugSource)
	}
	base := path.Base(path.Clean(strings.TrimSuffix(src, "/")))
	if base == "." || base == "/" {
		base = src
	}
	return slugify(strings.TrimSuffix(base, path.Ext(base)), maxSlugSource)
}

// slugify lowercases, keeps [a-z0-9], collapses everything else into single
// dashes, trims, and caps the result at max runes.
func slugify(in string, max int) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(in) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = true
		default:
			if dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = false
			}
		}
		if b.Len() >= max {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// ClearanceBlock renders the machine-parseable Sample-clearance section
// (design §5). Source path, source commit SHA and model are the three
// mandatory fields; input → output and the recycle issue complete the block.
func ClearanceBlock(rc pipeline.RunContext, s pipeline.Sample, m pipeline.Mix) string {
	var b strings.Builder
	b.WriteString("## Sample clearance\n")
	fmt.Fprintf(&b, "- **Source path:** %s\n", s.SourcePath)
	fmt.Fprintf(&b, "- **Source commit:** %s\n", s.SourceCommit)
	fmt.Fprintf(&b, "- **Model:** %s\n", m.ModelUsed)
	fmt.Fprintf(&b, "- **Input → Output:** %s → %s\n", s.DetectedInput, rc.OutputFormat)
	fmt.Fprintf(&b, "- **Recycle issue:** #%d\n", rc.IssueNumber)
	return b.String()
}

// PRTitle is the pull-request title.
func PRTitle(rc pipeline.RunContext, m pipeline.Mix) string {
	title := strings.TrimSpace(m.Title)
	if title == "" {
		title = "recycled " + m.OutputFormat
	}
	return fmt.Sprintf("recycle: %s (%s, #%d)", title, m.OutputFormat, rc.IssueNumber)
}

// PRBody renders the type-aware content PR template (ISI-7119 / plan §4) for
// the generated content type, pre-fills the common fields from the run
// context, and carries the real Sample-clearance block (design §5: "PR links
// back to the issue"). The template picked mirrors the static
// .github/PULL_REQUEST_TEMPLATE/<type>.md chooser so programmatic and
// human-opened PRs follow the same contract.
func PRBody(rc pipeline.RunContext, s pipeline.Sample, m pipeline.Mix) string {
	format := strings.TrimSpace(m.OutputFormat)
	if format == "" {
		format = strings.TrimSpace(rc.OutputFormat)
	}
	ct := ContentType(format)
	pf := Prefill{
		ContentType:  ct,
		Source:       s.SourcePath,
		TargetFormat: format,
		Slug:         Slug(rc, s),
		Summary:      summaryFromMarkdown(m.Markdown),
		LinkedIssue:  fmt.Sprintf("#%d", rc.IssueNumber),
	}
	return renderTemplateBody(ct, pf) + "\n" + ClearanceBlock(rc, s, m)
}

// summaryFromMarkdown pulls a one-line summary from the Mix body: the first
// non-empty paragraph that is not a heading, collapsed to a single line. It is
// best-effort pre-fill — an empty result renders as the template placeholder.
func summaryFromMarkdown(md string) string {
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		return strings.Join(strings.Fields(t), " ")
	}
	return ""
}

// commitMessage is the git commit message on the recycle branch.
func commitMessage(rc pipeline.RunContext, s pipeline.Sample, m pipeline.Mix, slug string) string {
	return fmt.Sprintf("recycle: add %s mix for issue #%d (recycled/%s)\n\nGenerated by ContentRemixer from %s at %s via %s.",
		m.OutputFormat, rc.IssueNumber, slug, s.SourcePath, shortSHA(s.SourceCommit), m.ModelUsed)
}

// shortSHA abbreviates a commit SHA for commit-message readability.
func shortSHA(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}

// renderMetaYAML serializes recycled/<slug>/meta.yaml: Mix.Meta with the
// sample_clearance block (re-)anchored from the authoritative Sample/Mix
// values, so the mandatory clearance fields survive outside the PR even if
// the writer's copy drifted (defence in depth for design §5).
func renderMetaYAML(rc pipeline.RunContext, s pipeline.Sample, m pipeline.Mix) (string, error) {
	meta := map[string]any{}
	for k, v := range m.Meta {
		meta[k] = v
	}
	meta["output_format"] = m.OutputFormat
	meta["sample_clearance"] = map[string]any{
		"source_path":   s.SourcePath,
		"source_commit": s.SourceCommit,
		"model":         m.ModelUsed,
		"input_format":  s.DetectedInput,
		"output_format": rc.OutputFormat,
		"recycle_issue": rc.IssueNumber,
	}
	b, err := yaml.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("pr: rendering recycled meta.yaml: %w", err)
	}
	return string(b), nil
}
