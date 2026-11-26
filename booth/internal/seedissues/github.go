package seedissues

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultGitHubAPI = "https://api.github.com"
	githubAPIVersion = "2022-11-28"
)

// ghClient is the tiny GitHub REST client the seeder drives with a per-repo
// PAT. Deliberately self-contained — booth cannot import app/internal — but
// mirrors booth/internal/approve's discipline (API-version header,
// "already exists = success").
type ghClient struct {
	token  string
	apiURL string // "" → defaultGitHubAPI
	http   *http.Client
}

func (c *ghClient) base() string {
	if strings.TrimSpace(c.apiURL) != "" {
		return strings.TrimRight(c.apiURL, "/")
	}
	return defaultGitHubAPI
}

func (c *ghClient) client() *http.Client {
	if c.http != nil {
		return c.http
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *ghClient) newReq(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	return req, nil
}

// ensureLabel creates a label, treating an "already exists" 422 as success so
// re-runs (and labels pre-created by the repo) are both fine.
func (c *ghClient) ensureLabel(ctx context.Context, repo, name string) error {
	u := fmt.Sprintf("%s/repos/%s/labels", c.base(), repo)
	payload, _ := json.Marshal(map[string]string{"name": name})
	req, err := c.newReq(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusUnprocessableEntity:
		return nil // label already exists
	default:
		return fmt.Errorf("ensure label %q on %s: %s: %s", name, repo, resp.Status, truncate(data))
	}
}

// ghIssue is the subset of the issue object the seeder reads.
type ghIssue struct {
	Number  int    `json:"number"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
}

// seededIDs returns the set of template ids already present in repo, found by
// listing issues carrying the tracking label (state=all) and matching their
// bodies against each template's marker. Paginates up to maxSeedPages so a repo
// with many prior seeds (or a label collision) can't spin forever.
func (c *ghClient) seededIDs(ctx context.Context, repo string, ids []string) (map[string]bool, error) {
	const maxSeedPages = 10
	found := make(map[string]bool, len(ids))
	for page := 1; page <= maxSeedPages; page++ {
		u := fmt.Sprintf("%s/repos/%s/issues?state=all&labels=%s&per_page=100&page=%d",
			c.base(), repo, TrackingLabel, page)
		req, err := c.newReq(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.client().Do(req)
		if err != nil {
			return nil, err
		}
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("list seeded issues on %s: %s: %s", repo, resp.Status, truncate(data))
		}
		var issues []ghIssue
		if err := json.Unmarshal(data, &issues); err != nil {
			return nil, fmt.Errorf("decoding issues for %s: %w", repo, err)
		}
		for _, is := range issues {
			for _, id := range ids {
				if strings.Contains(is.Body, marker(id)) {
					found[id] = true
				}
			}
		}
		if len(issues) < 100 {
			break
		}
	}
	return found, nil
}

// createIssue opens one issue and returns its number and URL.
func (c *ghClient) createIssue(ctx context.Context, repo, title, body string, labels []string) (ghIssue, error) {
	u := fmt.Sprintf("%s/repos/%s/issues", c.base(), repo)
	payload, _ := json.Marshal(map[string]any{"title": title, "body": body, "labels": labels})
	req, err := c.newReq(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return ghIssue{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return ghIssue{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ghIssue{}, fmt.Errorf("create issue %q on %s: %s: %s", title, repo, resp.Status, truncate(data))
	}
	var is ghIssue
	if err := json.Unmarshal(data, &is); err != nil {
		return ghIssue{}, fmt.Errorf("decoding created issue for %s: %w", repo, err)
	}
	return is, nil
}

func truncate(b []byte) string {
	const max = 300
	b = bytes.TrimSpace(b)
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
