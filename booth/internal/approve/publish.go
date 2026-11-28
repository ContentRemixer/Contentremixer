package approve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrInvalid marks a publish request that failed input validation BEFORE any
// network call. The approve action wraps it so the HTTP layer can map a bad
// draft to 400 rather than 5xx.
var ErrInvalid = errors.New("publish: invalid request")

const (
	// defaultBranchPrefix is the head-branch prefix used when PublishInput leaves
	// BranchPrefix empty.
	defaultBranchPrefix = "content/"
	// defaultBaseBranch is the base branch used when PublishInput leaves
	// BaseBranch empty.
	defaultBaseBranch = "main"
	// maxSlug caps the path-derived part of a branch name so branches stay
	// readable and within git's ref limits.
	maxSlug = 48
)

// PublishInput is one content-draft publish request.
type PublishInput struct {
	Path          string // repo-relative file path to write, e.g. "content/blog/my-post.md". Required.
	Body          string // file content (markdown). Required (non-empty after TrimSpace).
	Title         string // PR title. Required.
	PRBody        string // PR description body. Optional.
	CommitMessage string // git commit message. Optional (a default is derived from Title when empty).
	BranchPrefix  string // head-branch prefix, default "content/" when empty.
	BaseBranch    string // base branch, default "main" when empty.
	OriginIssue   int    // origin issue number woven into the branch name for a stable, idempotent head; 0 = omitted.
}

// PublishResult reports what the publisher did.
type PublishResult struct {
	Branch     string
	CommitSHA  string
	PRNumber   int
	PRURL      string
	Idempotent bool // true when an already-open PR for the branch was reused
}

// Publisher opens a PR for an approved content draft and (separately) comments
// a link onto an origin issue. It is an interface so the approve action is
// unit-testable without a live GitHub. The origin issue can live in a DIFFERENT
// repo than the PR target, so CommentIssue is addressed independently of
// Publish; the action layer decides whether a comment failure is soft.
type Publisher interface {
	Publish(ctx context.Context, repo string, in PublishInput) (PublishResult, error)
	CommentIssue(ctx context.Context, repo string, issue int, body string) (string, error)
}

// GitHubPublisher is the live Publisher driving the GitHub git-data + pulls REST
// API with a token. Like GitHubReflector it is deliberately tiny and
// self-contained — booth is a separate module and cannot import
// app/internal/pipeline/pr — but mirrors that client's git-data discipline
// (ref → blob → tree → commit → ref, API-version header, "already exists =
// reuse"). The token may be a per-repo PAT today or a GitHub App installation
// token later; the Bearer usage is identical either way.
type GitHubPublisher struct {
	// Token is the per-repo PAT (or App installation token). Required.
	Token string
	// APIURL overrides the GitHub REST base (GitHub Enterprise or a test
	// server). Empty → https://api.github.com.
	APIURL string
	// HTTP is the client used for requests. Nil → a 30s-timeout client.
	HTTP *http.Client
}

// NewGitHubPublisher builds a live publisher. apiURL may be empty for the
// public GitHub.
func NewGitHubPublisher(token, apiURL string) *GitHubPublisher {
	return &GitHubPublisher{
		Token:  token,
		APIURL: apiURL,
		HTTP:   &http.Client{Timeout: 30 * time.Second},
	}
}

var _ Publisher = (*GitHubPublisher)(nil)

// Publish commits in.Body to in.Path on a new (or reused) branch and opens one
// pull request. It does NOT comment onto any origin issue — the origin issue
// may live in a different repo, so the action layer drives CommentIssue
// separately and decides whether a comment failure is soft.
//
// It fails CLOSED before any network call on bad input (wrapping ErrInvalid so
// the caller can map to 400). The git-data sequence mirrors the pipeline
// assembler: resolve base head → create branch (reuse on 422) → read tree →
// blob → tree → commit → update ref → open PR (reuse the open PR on 422,
// surfacing Idempotent=true).
func (g *GitHubPublisher) Publish(ctx context.Context, repo string, in PublishInput) (PublishResult, error) {
	if g == nil || g.Token == "" {
		return PublishResult{}, fmt.Errorf("publish: no GitHub token configured: %w", ErrInvalid)
	}
	if !strings.Contains(repo, "/") {
		return PublishResult{}, fmt.Errorf("publish: repo %q must be owner/name: %w", repo, ErrInvalid)
	}
	if strings.TrimSpace(in.Path) == "" {
		return PublishResult{}, fmt.Errorf("publish: path is required: %w", ErrInvalid)
	}
	if strings.TrimSpace(in.Body) == "" {
		return PublishResult{}, fmt.Errorf("publish: body is required: %w", ErrInvalid)
	}
	if strings.TrimSpace(in.Title) == "" {
		return PublishResult{}, fmt.Errorf("publish: title is required: %w", ErrInvalid)
	}

	base := strings.TrimSpace(in.BaseBranch)
	if base == "" {
		base = defaultBaseBranch
	}
	branch := branchName(in)

	// Start the branch at the base head, or reuse it when a previous publish of
	// the same draft left it (idempotent re-publish).
	headSHA, err := g.refSHA(ctx, repo, base)
	if err != nil {
		return PublishResult{}, fmt.Errorf("publish: resolving base branch %q: %w", base, err)
	}
	branchSHA, exists, err := g.createBranch(ctx, repo, branch, headSHA)
	if err != nil {
		return PublishResult{}, fmt.Errorf("publish: creating branch %q: %w", branch, err)
	}
	if exists {
		if branchSHA, err = g.refSHA(ctx, repo, branch); err != nil {
			return PublishResult{}, fmt.Errorf("publish: resolving existing branch %q: %w", branch, err)
		}
	}

	// Commit the single file on top of the branch tip.
	tree, err := g.commitTree(ctx, repo, branchSHA)
	if err != nil {
		return PublishResult{}, fmt.Errorf("publish: reading commit tree of %q: %w", branch, err)
	}
	body := in.Body
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	blobSHA, err := g.createBlob(ctx, repo, body)
	if err != nil {
		return PublishResult{}, fmt.Errorf("publish: creating blob for %q: %w", in.Path, err)
	}
	newTree, err := g.createTree(ctx, repo, tree, []treeEntry{{Path: in.Path, Mode: "100644", Type: "blob", SHA: blobSHA}})
	if err != nil {
		return PublishResult{}, fmt.Errorf("publish: creating tree for %q: %w", in.Path, err)
	}
	commitSHA, err := g.createCommit(ctx, repo, commitMessage(in), newTree, []string{branchSHA})
	if err != nil {
		return PublishResult{}, fmt.Errorf("publish: committing %q: %w", in.Path, err)
	}
	if err := g.updateRef(ctx, repo, branch, commitSHA); err != nil {
		return PublishResult{}, fmt.Errorf("publish: advancing branch %q: %w", branch, err)
	}

	// Open the PR, or reuse the one already open for this branch.
	prNum, prURL, prExists, err := g.createPull(ctx, repo, in.Title, in.PRBody, branch, base)
	idempotent := false
	if err == nil && prExists {
		prNum, prURL, err = g.findOpenPull(ctx, repo, branch)
		idempotent = true
	}
	if err != nil {
		return PublishResult{}, fmt.Errorf("publish: opening pull request from %q: %w", branch, err)
	}

	return PublishResult{
		Branch:     branch,
		CommitSHA:  commitSHA,
		PRNumber:   prNum,
		PRURL:      prURL,
		Idempotent: idempotent,
	}, nil
}

// CommentIssue posts one comment on an issue (POST /issues/<n>/comments) and
// returns its html_url. The issue may live in a different repo than the PR
// target, so repo is addressed independently. It fails CLOSED before any
// network call on bad input (wrapping ErrInvalid). Whether a failure here is
// soft is the action layer's decision — CommentIssue just returns the error.
func (g *GitHubPublisher) CommentIssue(ctx context.Context, repo string, issue int, body string) (string, error) {
	if g == nil || g.Token == "" {
		return "", fmt.Errorf("publish: no GitHub token configured: %w", ErrInvalid)
	}
	if !strings.Contains(repo, "/") {
		return "", fmt.Errorf("publish: repo %q must be owner/name: %w", repo, ErrInvalid)
	}
	if issue <= 0 {
		return "", fmt.Errorf("publish: issue number must be positive: %w", ErrInvalid)
	}
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	reqBody := map[string]any{"body": body}
	if _, err := g.call(ctx, http.MethodPost, fmt.Sprintf("repos/%s/issues/%d/comments", repo, issue), reqBody, &out); err != nil {
		return "", err
	}
	return out.HTMLURL, nil
}

// treeEntry is one entry of a POST /git/trees request.
type treeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha,omitempty"`
}

// refSHA resolves refs/heads/<branch> to its commit SHA.
func (g *GitHubPublisher) refSHA(ctx context.Context, repo, branch string) (string, error) {
	var out struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if _, err := g.call(ctx, http.MethodGet, "repos/"+repo+"/git/ref/heads/"+branch, nil, &out); err != nil {
		return "", err
	}
	if out.Object.SHA == "" {
		return "", fmt.Errorf("publish: ref heads/%s of %s returned no sha", branch, repo)
	}
	return out.Object.SHA, nil
}

// createBranch points refs/heads/<branch> at sha (POST /git/refs). The bool is
// true when GitHub returned 422 because the ref already exists — the caller
// then re-resolves the branch and continues (idempotent reuse).
func (g *GitHubPublisher) createBranch(ctx context.Context, repo, branch, sha string) (string, bool, error) {
	body := map[string]any{"ref": "refs/heads/" + branch, "sha": sha}
	status, err := g.call(ctx, http.MethodPost, "repos/"+repo+"/git/refs", body, nil)
	if status == http.StatusUnprocessableEntity {
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	return sha, false, nil
}

// createBlob uploads UTF-8 content (POST /git/blobs) and returns its SHA.
func (g *GitHubPublisher) createBlob(ctx context.Context, repo, content string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	body := map[string]any{"content": content, "encoding": "utf-8"}
	if _, err := g.call(ctx, http.MethodPost, "repos/"+repo+"/git/blobs", body, &out); err != nil {
		return "", err
	}
	return out.SHA, nil
}

// commitTree fetches a commit's tree SHA (GET /git/commits/<sha>).
func (g *GitHubPublisher) commitTree(ctx context.Context, repo, commitSHA string) (string, error) {
	var out struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if _, err := g.call(ctx, http.MethodGet, "repos/"+repo+"/git/commits/"+commitSHA, nil, &out); err != nil {
		return "", err
	}
	return out.Tree.SHA, nil
}

// createTree builds a tree on top of baseTree (POST /git/trees) and returns the
// new tree SHA.
func (g *GitHubPublisher) createTree(ctx context.Context, repo, baseTree string, entries []treeEntry) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	body := map[string]any{"base_tree": baseTree, "tree": entries}
	if _, err := g.call(ctx, http.MethodPost, "repos/"+repo+"/git/trees", body, &out); err != nil {
		return "", err
	}
	return out.SHA, nil
}

// createCommit commits tree onto parents (POST /git/commits).
func (g *GitHubPublisher) createCommit(ctx context.Context, repo, message, tree string, parents []string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	body := map[string]any{"message": message, "tree": tree, "parents": parents}
	if _, err := g.call(ctx, http.MethodPost, "repos/"+repo+"/git/commits", body, &out); err != nil {
		return "", err
	}
	return out.SHA, nil
}

// updateRef fast-forwards refs/heads/<branch> to sha (PATCH /git/refs/…).
func (g *GitHubPublisher) updateRef(ctx context.Context, repo, branch, sha string) error {
	body := map[string]any{"sha": sha, "force": false}
	_, err := g.call(ctx, http.MethodPatch, "repos/"+repo+"/git/refs/heads/"+branch, body, nil)
	return err
}

// createPull opens the PR (POST /pulls) and returns its number + HTML URL. The
// bool is true when GitHub returned 422 because a PR is already open for the
// head branch — the caller then reuses it (Idempotent).
func (g *GitHubPublisher) createPull(ctx context.Context, repo, title, body, head, base string) (int, string, bool, error) {
	var out struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	reqBody := map[string]any{"title": title, "body": body, "head": head, "base": base}
	status, err := g.call(ctx, http.MethodPost, "repos/"+repo+"/pulls", reqBody, &out)
	if status == http.StatusUnprocessableEntity {
		return 0, "", true, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	if out.HTMLURL == "" {
		out.HTMLURL = fmt.Sprintf("https://github.com/%s/pull/%d", repo, out.Number)
	}
	return out.Number, out.HTMLURL, false, nil
}

// findOpenPull returns the open PR whose head is branch, if any.
func (g *GitHubPublisher) findOpenPull(ctx context.Context, repo, branch string) (int, string, error) {
	var out []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	q := url.Values{"head": []string{branch}, "state": []string{"open"}}
	if _, err := g.call(ctx, http.MethodGet, "repos/"+repo+"/pulls?"+q.Encode(), nil, &out); err != nil {
		return 0, "", err
	}
	if len(out) == 0 {
		return 0, "", fmt.Errorf("publish: no open pull request for head %s of %s", branch, repo)
	}
	if out[0].HTMLURL == "" {
		out[0].HTMLURL = fmt.Sprintf("https://github.com/%s/pull/%d", repo, out[0].Number)
	}
	return out[0].Number, out[0].HTMLURL, nil
}

// call performs one REST call with the standard GitHub headers, mirroring
// GitHubReflector.setHeaders. It returns the HTTP status (so callers can act on
// 422 for idempotent reuse) and an error on any non-2xx that is not a 422
// handled by the caller.
func (g *GitHubPublisher) call(ctx context.Context, method, pathURL string, reqBody, out any) (int, error) {
	var payload io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return 0, err
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.base()+"/"+pathURL, payload)
	if err != nil {
		return 0, err
	}
	g.setHeaders(req)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := g.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("publish: %s %s: %s: %s", method, pathURL, resp.Status, truncate(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("publish: %s %s: decoding response: %w", method, pathURL, err)
		}
	}
	return resp.StatusCode, nil
}

func (g *GitHubPublisher) base() string {
	if strings.TrimSpace(g.APIURL) != "" {
		return strings.TrimRight(g.APIURL, "/")
	}
	return defaultGitHubAPI
}

func (g *GitHubPublisher) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (g *GitHubPublisher) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
}

// branchName derives the deterministic head branch for a draft so re-publishing
// the same draft reuses the branch (idempotent): BranchPrefix + slug(path) +
// "-" + OriginIssue (when > 0).
func branchName(in PublishInput) string {
	prefix := strings.TrimSpace(in.BranchPrefix)
	if prefix == "" {
		prefix = defaultBranchPrefix
	}
	name := prefix + slugify(in.Path)
	if in.OriginIssue > 0 {
		name += fmt.Sprintf("-%d", in.OriginIssue)
	}
	return name
}

// commitMessage returns in.CommitMessage, or a default derived from Title.
func commitMessage(in PublishInput) string {
	if m := strings.TrimSpace(in.CommitMessage); m != "" {
		return m
	}
	return "content: publish " + strings.TrimSpace(in.Title)
}

// slugify reduces a repo path to its identifying basename slug: lowercases,
// keeps [a-z0-9], collapses every other run into a single dash, trims, and caps
// at maxSlug runes. Deterministic so the same path always yields the same slug.
func slugify(path string) string {
	src := strings.TrimSpace(path)
	src = strings.Trim(src, "/")
	if i := strings.LastIndex(src, "/"); i >= 0 {
		src = src[i+1:]
	}
	if dot := strings.LastIndex(src, "."); dot > 0 {
		src = src[:dot]
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(src) {
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
		if b.Len() >= maxSlug {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "draft"
	}
	return s
}
