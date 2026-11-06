package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CommentPoster posts an acknowledgement comment on a recycle issue. It is an
// interface so the webhook handler is testable without network I/O.
type CommentPoster interface {
	PostComment(ctx context.Context, repoFullName string, issueNumber int, token, body string) error
}

// GitHubClient posts issue comments via the GitHub REST API using the
// per-repo PAT. Stdlib only — the single call here does not justify pulling in
// google/go-github (design §1 notes it as optional).
type GitHubClient struct {
	// BaseURL defaults to https://api.github.com; overridable for GH
	// Enterprise or tests.
	BaseURL string
	// HTTP is the client used; defaults to a 15s-timeout client.
	HTTP *http.Client
}

// PostComment calls POST /repos/{owner}/{repo}/issues/{n}/comments.
func (c *GitHubClient) PostComment(ctx context.Context, repoFullName string, issueNumber int, token, body string) error {
	base := c.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}

	url := fmt.Sprintf("%s/repos/%s/issues/%d/comments", base, repoFullName, issueNumber)
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("github: post comment %s#%d returned %s: %s", repoFullName, issueNumber, resp.Status, bytes.TrimSpace(snippet))
	}
	return nil
}
