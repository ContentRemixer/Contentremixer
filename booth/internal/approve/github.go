package approve

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

// GitHub REST constants. The API version header pins the schema the way the
// pipeline's own client (app/internal/pipeline/pr) does.
const (
	defaultGitHubAPI = "https://api.github.com"
	githubAPIVersion = "2022-11-28"

	// boothApprovalMarker prefixes the machine-parseable HTML comment the Booth
	// writes into every reflected review body. The composing merge gate greps
	// for it to recognise a Booth approval even when GitHub could not record a
	// formal APPROVE (self-approval case). The per-(PR, approver) key follows the
	// marker so re-reflection is idempotent.
	boothApprovalMarker = "booth-approval:"
)

// Mode is how an in-Booth approval was reflected onto the GitHub PR.
type Mode string

const (
	// ModeApprove is a formal GitHub APPROVE review — it counts toward branch
	// protection's required-review gate.
	ModeApprove Mode = "approve"
	// ModeComment is the COMMENT fallback used when the PAT identity cannot file
	// a formal APPROVE (GitHub forbids approving your own pull request, which is
	// exactly the bot-authored-PR case). The Booth-approval block still lands on
	// the PR so the decision is recorded and attributable.
	ModeComment Mode = "comment"
)

// Approval is the in-Booth decision being reflected onto a GitHub PR.
type Approval struct {
	// Subject and Role identify the in-Booth approver (so the GitHub record is
	// attributable to the person, not just the bot PAT).
	Subject string
	Role    string
	// Note is an optional free-text reviewer comment.
	Note string
	// Key is the stable idempotency key "<repo>#<number>:<subject>" — one Booth
	// approval per (PR, approver). Re-reflecting the same key is a no-op.
	Key string
}

// Reflection reports what the reflector did.
type Reflection struct {
	Mode Mode
	// ReviewID and URL identify the created (or pre-existing) GitHub review.
	ReviewID int64
	URL      string
	// Idempotent is true when an existing Booth review for this Key was reused
	// instead of a new one being created.
	Idempotent bool
}

// Reflector reflects an authorized in-Booth approval onto a GitHub PR. It is an
// interface so the approve gate is unit-testable without a live GitHub (and so
// the reflection surface stays swappable).
type Reflector interface {
	Reflect(ctx context.Context, repo string, prNumber int, ap Approval) (Reflection, error)
}

// GitHubReflector is the live Reflector: it drives the GitHub PR reviews REST
// API with a per-repo PAT. It is deliberately tiny and self-contained — booth
// is a separate module and cannot import app/internal/pipeline/pr — but mirrors
// that client's discipline (API-version header, "already exists = success").
type GitHubReflector struct {
	// Token is the per-repo PAT. Required.
	Token string
	// APIURL overrides the GitHub REST base (GitHub Enterprise or a test
	// server). Empty → https://api.github.com.
	APIURL string
	// HTTP is the client used for requests. Nil → a 30s-timeout client.
	HTTP *http.Client
}

// NewGitHubReflector builds a live reflector. apiURL may be empty for the
// public GitHub.
func NewGitHubReflector(token, apiURL string) *GitHubReflector {
	return &GitHubReflector{
		Token:  token,
		APIURL: apiURL,
		HTTP:   &http.Client{Timeout: 30 * time.Second},
	}
}

// ghReview is the subset of the GitHub review object the reflector reads.
type ghReview struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
}

// Reflect reflects ap onto repo's PR #prNumber. Order:
//
//  1. list existing reviews and short-circuit if this exact Booth approval
//     (matched by its Key marker) is already there — idempotent re-approve;
//  2. otherwise file a formal APPROVE review;
//  3. if GitHub rejects the APPROVE because the PAT cannot approve its own PR
//     (422), fall back to a COMMENT review carrying the same Booth-approval
//     block, so the decision is still recorded and greppable.
func (g *GitHubReflector) Reflect(ctx context.Context, repo string, prNumber int, ap Approval) (Reflection, error) {
	if g == nil || g.Token == "" {
		return Reflection{}, fmt.Errorf("approve: no GitHub token configured")
	}
	if existing, ok, err := g.find(ctx, repo, prNumber, ap.Key); err != nil {
		return Reflection{}, err
	} else if ok {
		return Reflection{Mode: stateToMode(existing.State), ReviewID: existing.ID, URL: existing.HTMLURL, Idempotent: true}, nil
	}

	body := approvalBody(ap)
	rev, status, err := g.createReview(ctx, repo, prNumber, "APPROVE", body)
	if err == nil {
		return Reflection{Mode: ModeApprove, ReviewID: rev.ID, URL: rev.HTMLURL}, nil
	}
	// GitHub returns 422 when the authenticated identity cannot APPROVE (most
	// commonly: it authored the PR). Fall back to a COMMENT review — the
	// Booth-approval block still lands, just without a formal APPROVE state.
	if status == http.StatusUnprocessableEntity {
		rev, _, cerr := g.createReview(ctx, repo, prNumber, "COMMENT", body)
		if cerr != nil {
			return Reflection{}, cerr
		}
		return Reflection{Mode: ModeComment, ReviewID: rev.ID, URL: rev.HTMLURL}, nil
	}
	return Reflection{}, err
}

// find returns the existing Booth review for key, if any. A review is "ours"
// when its body carries the Booth-approval marker followed by this key.
func (g *GitHubReflector) find(ctx context.Context, repo string, prNumber int, key string) (ghReview, bool, error) {
	u := fmt.Sprintf("%s/repos/%s/pulls/%d/reviews?per_page=100", g.base(), repo, prNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ghReview{}, false, err
	}
	g.setHeaders(req)
	resp, err := g.client().Do(req)
	if err != nil {
		return ghReview{}, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return ghReview{}, false, fmt.Errorf("approve: list reviews for %s#%d: %s: %s", repo, prNumber, resp.Status, truncate(data))
	}
	var reviews []ghReview
	if err := json.Unmarshal(data, &reviews); err != nil {
		return ghReview{}, false, fmt.Errorf("approve: decoding reviews for %s#%d: %w", repo, prNumber, err)
	}
	want := boothApprovalMarker + key
	for _, r := range reviews {
		if strings.Contains(r.Body, want) {
			return r, true, nil
		}
	}
	return ghReview{}, false, nil
}

// createReview posts one review with the given event ("APPROVE"/"COMMENT") and
// body. It returns the created review, the HTTP status (so the caller can act
// on 422), and an error on any non-2xx.
func (g *GitHubReflector) createReview(ctx context.Context, repo string, prNumber int, event, body string) (ghReview, int, error) {
	u := fmt.Sprintf("%s/repos/%s/pulls/%d/reviews", g.base(), repo, prNumber)
	payload, _ := json.Marshal(map[string]string{"event": event, "body": body})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return ghReview{}, 0, err
	}
	g.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client().Do(req)
	if err != nil {
		return ghReview{}, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ghReview{}, resp.StatusCode, fmt.Errorf("approve: %s review on %s#%d: %s: %s", event, repo, prNumber, resp.Status, truncate(data))
	}
	var rev ghReview
	if err := json.Unmarshal(data, &rev); err != nil {
		return ghReview{}, resp.StatusCode, fmt.Errorf("approve: decoding %s review for %s#%d: %w", event, repo, prNumber, err)
	}
	return rev, resp.StatusCode, nil
}

func (g *GitHubReflector) base() string {
	if strings.TrimSpace(g.APIURL) != "" {
		return strings.TrimRight(g.APIURL, "/")
	}
	return defaultGitHubAPI
}

func (g *GitHubReflector) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (g *GitHubReflector) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
}

// approvalBody renders the review body: the machine-parseable Booth-approval
// HTML-comment marker (greppable by the merge gate) plus a human line and the
// optional reviewer note.
func approvalBody(ap Approval) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- %s%s -->\n", boothApprovalMarker, ap.Key)
	fmt.Fprintf(&b, "Approved in Booth by `%s` (%s).", ap.Subject, ap.Role)
	if n := strings.TrimSpace(ap.Note); n != "" {
		b.WriteString("\n\n")
		b.WriteString(n)
	}
	return b.String()
}

// stateToMode maps a GitHub review state onto the Booth reflection mode for a
// re-used (idempotent) review.
func stateToMode(state string) Mode {
	if strings.EqualFold(state, "APPROVED") {
		return ModeApprove
	}
	return ModeComment
}

// truncate bounds an error-embedded GitHub response body so logs stay readable.
func truncate(b []byte) string {
	const max = 300
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

var _ Reflector = (*GitHubReflector)(nil)
