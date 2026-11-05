// GitHub REST client for the PR assembler (subtask H): the git-data calls
// (ref → blob → tree → commit → ref) plus the pull-request calls, all via
// the per-repo PAT. Stdlib only, matching internal/githubapp.GitHubClient —
// the handful of calls here does not justify google/go-github (design §1).
package pr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// errBranchExists marks POST /git/refs returning 422 because the ref already
// exists — the assembler treats it as "reuse the branch".
var errBranchExists = fmt.Errorf("branch already exists")

// errPRExists marks POST /pulls returning 422 because a PR is already open
// for the head branch — the assembler returns that PR instead.
var errPRExists = fmt.Errorf("pull request already exists")

// GitHubClient performs the assembler's REST calls.
type GitHubClient struct {
	// BaseURL defaults to https://api.github.com; overridable for GH
	// Enterprise or tests.
	BaseURL string
	// HTTP is the client used; defaults to a 60s-timeout client (a full
	// assemble is several sequential calls, not one long one).
	HTTP *http.Client
}

// treeEntry is one entry of a POST /git/trees request.
type treeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha,omitempty"`
}

// refSHA resolves refs/heads/<branch> to its commit SHA.
func (c *GitHubClient) refSHA(ctx context.Context, repo, branch, token string) (string, error) {
	var out struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.call(ctx, http.MethodGet, "repos/"+repo+"/git/ref/heads/"+branch, token, nil, &out); err != nil {
		return "", err
	}
	if out.Object.SHA == "" {
		return "", fmt.Errorf("github: ref heads/%s of %s returned no sha", branch, repo)
	}
	return out.Object.SHA, nil
}

// createBranch points refs/heads/<branch> at sha (POST /git/refs). It
// returns errBranchExists when the ref is already there.
func (c *GitHubClient) createBranch(ctx context.Context, repo, branch, sha, token string) (string, error) {
	body := map[string]any{"ref": "refs/heads/" + branch, "sha": sha}
	err := c.call(ctx, http.MethodPost, "repos/"+repo+"/git/refs", token, body, nil)
	if err == errBranchExists {
		return "", err
	}
	return sha, err
}

// createBlob uploads UTF-8 content (POST /git/blobs) and returns its SHA.
func (c *GitHubClient) createBlob(ctx context.Context, repo, content, token string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	body := map[string]any{"content": content, "encoding": "utf-8"}
	if err := c.call(ctx, http.MethodPost, "repos/"+repo+"/git/blobs", token, body, &out); err != nil {
		return "", err
	}
	return out.SHA, nil
}

// commitSHA fetches a commit's tree SHA.
func (c *GitHubClient) commitTree(ctx context.Context, repo, commitSHA, token string) (string, error) {
	var out struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.call(ctx, http.MethodGet, "repos/"+repo+"/git/commits/"+commitSHA, token, nil, &out); err != nil {
		return "", err
	}
	return out.Tree.SHA, nil
}

// createTree builds a tree on top of baseTree (POST /git/trees) with the
// given entries and returns the new tree SHA.
func (c *GitHubClient) createTree(ctx context.Context, repo, baseTree string, entries []treeEntry, token string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	body := map[string]any{"base_tree": baseTree, "tree": entries}
	if err := c.call(ctx, http.MethodPost, "repos/"+repo+"/git/trees", token, body, &out); err != nil {
		return "", err
	}
	return out.SHA, nil
}

// createCommit commits tree onto parents (POST /git/commits).
func (c *GitHubClient) createCommit(ctx context.Context, repo, message, tree string, parents []string, token string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	body := map[string]any{"message": message, "tree": tree, "parents": parents}
	if err := c.call(ctx, http.MethodPost, "repos/"+repo+"/git/commits", token, body, &out); err != nil {
		return "", err
	}
	return out.SHA, nil
}

// updateRef fast-forwards refs/heads/<branch> to sha (PATCH /git/refs/…).
func (c *GitHubClient) updateRef(ctx context.Context, repo, branch, sha, token string) error {
	body := map[string]any{"sha": sha, "force": false}
	return c.call(ctx, http.MethodPatch, "repos/"+repo+"/git/refs/heads/"+branch, token, body, nil)
}

// createPull opens the PR (POST /pulls) and returns its number + HTML URL.
// errPRExists signals a 422 "pull request already exists".
func (c *GitHubClient) createPull(ctx context.Context, repo, title, body, head, base string, draft bool, token string) (int, string, error) {
	var out struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	reqBody := map[string]any{"title": title, "body": body, "head": head, "base": base}
	if draft {
		reqBody["draft"] = true
	}
	if err := c.call(ctx, http.MethodPost, "repos/"+repo+"/pulls", token, reqBody, &out); err != nil {
		return 0, "", err
	}
	if out.HTMLURL == "" {
		out.HTMLURL = fmt.Sprintf("https://github.com/%s/pull/%d", repo, out.Number)
	}
	return out.Number, out.HTMLURL, nil
}

// findOpenPull returns the open PR whose head is branch, if any.
func (c *GitHubClient) findOpenPull(ctx context.Context, repo, branch, token string) (int, string, error) {
	var out []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	q := url.Values{"head": []string{branch}, "state": []string{"open"}}
	if err := c.call(ctx, http.MethodGet, "repos/"+repo+"/pulls?"+q.Encode(), token, nil, &out); err != nil {
		return 0, "", err
	}
	if len(out) == 0 {
		return 0, "", fmt.Errorf("github: no open pull request for head %s of %s", branch, repo)
	}
	if out[0].HTMLURL == "" {
		out[0].HTMLURL = fmt.Sprintf("https://github.com/%s/pull/%d", repo, out[0].Number)
	}
	return out[0].Number, out[0].HTMLURL, nil
}

// call performs one REST call with the standard GitHub headers. 2xx
// succeeds; 422 on refs/pulls maps to the sentinel exists-errors; other
// statuses fail with a bounded body snippet.
func (c *GitHubClient) call(ctx context.Context, method, pathURL, token string, reqBody, out any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}

	var payload io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+"/"+pathURL, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnprocessableEntity {
		return existsErrorFor(method, pathURL)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("github: %s %s returned %s: %s", method, pathURL, resp.Status, bytes.TrimSpace(snippet))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
			return fmt.Errorf("github: %s %s: decoding response: %w", method, pathURL, err)
		}
	}
	return nil
}

// existsErrorFor classifies a 422 by endpoint into the sentinel errors the
// assembler handles idempotently.
func existsErrorFor(method, pathURL string) error {
	if strings.Contains(pathURL, "/git/refs") {
		return errBranchExists
	}
	if strings.HasSuffix(pathURL, "/pulls") {
		return errPRExists
	}
	return fmt.Errorf("github: %s %s returned 422 Unprocessable Entity", method, pathURL)
}
