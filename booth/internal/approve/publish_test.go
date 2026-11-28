package approve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pubStub is a minimal-but-correct GitHub git-data + pulls stub for the
// publisher happy path. It records the tree entries it was sent and can be told
// to reject branch/PR creation with 422 (the idempotent re-publish case) and to
// 500 the issue-comment endpoint (the soft-failure case).
type pubStub struct {
	rejectBranch422 bool // POST /git/refs → 422 (branch already exists)
	rejectPR422     bool // POST /pulls → 422 (PR already open)
	failComment500  bool // POST /issues/{n}/comments → 500

	treeEntries   []treeEntry // entries seen on POST /git/trees
	commentBodies []string    // bodies seen on POST /issues/{n}/comments
	commentCalls  int
}

func (s *pubStub) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /repos/{owner}/{repo}/git/ref/heads/{branch...}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": "basesha"}})
	})
	mux.HandleFunc("POST /repos/{owner}/{repo}/git/refs", func(w http.ResponseWriter, r *http.Request) {
		if s.rejectBranch422 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Reference already exists"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/x"})
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/git/commits/{sha}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tree": map[string]any{"sha": "basetree"}})
	})
	mux.HandleFunc("POST /repos/{owner}/{repo}/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "blobsha"})
	})
	mux.HandleFunc("POST /repos/{owner}/{repo}/git/trees", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Tree []treeEntry `json:"tree"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &in)
		s.treeEntries = in.Tree
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "newtree"})
	})
	mux.HandleFunc("POST /repos/{owner}/{repo}/git/commits", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "commitsha"})
	})
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/git/refs/heads/{branch...}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/x"})
	})
	mux.HandleFunc("POST /repos/{owner}/{repo}/pulls", func(w http.ResponseWriter, r *http.Request) {
		if s.rejectPR422 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"A pull request already exists"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 7, "html_url": "https://gh/pull/7"})
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"number": 7, "html_url": "https://gh/pull/7"}})
	})
	mux.HandleFunc("POST /repos/{owner}/{repo}/issues/{num}/comments", func(w http.ResponseWriter, r *http.Request) {
		s.commentCalls++
		body, _ := io.ReadAll(r.Body)
		var in map[string]string
		_ = json.Unmarshal(body, &in)
		s.commentBodies = append(s.commentBodies, in["body"])
		if s.failComment500 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"html_url": "https://gh/issuecomment/1"})
	})
	return mux
}

func newStubPublisher(t *testing.T, s *pubStub) *GitHubPublisher {
	t.Helper()
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return NewGitHubPublisher("tok", srv.URL)
}

func sampleInput() PublishInput {
	return PublishInput{
		Path:        "content/blog/my-post.md",
		Body:        "# Hello\n\nbody",
		Title:       "My Post",
		PRBody:      "please review",
		OriginIssue: 42,
	}
}

func TestPublishHappyPath(t *testing.T) {
	s := &pubStub{}
	p := newStubPublisher(t, s)
	res, err := p.Publish(context.Background(), "o/r", sampleInput())
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.PRURL != "https://gh/pull/7" || res.PRNumber != 7 {
		t.Fatalf("unexpected PR in result: %+v", res)
	}
	if res.CommitSHA != "commitsha" {
		t.Fatalf("commit sha = %q, want commitsha", res.CommitSHA)
	}
	if res.Idempotent {
		t.Fatalf("fresh publish must not be idempotent")
	}
	// The tree carries exactly one entry at the requested path.
	if len(s.treeEntries) != 1 {
		t.Fatalf("tree entries = %+v, want exactly one", s.treeEntries)
	}
	e := s.treeEntries[0]
	if e.Path != "content/blog/my-post.md" || e.Mode != "100644" || e.Type != "blob" || e.SHA != "blobsha" {
		t.Fatalf("unexpected tree entry: %+v", e)
	}
	// Branch is deterministic: prefix + slug(basename) + "-" + issue.
	if res.Branch != "content/my-post-42" {
		t.Fatalf("branch = %q, want content/my-post-42", res.Branch)
	}
	// Publish commits + opens the PR only — it must NOT comment on any issue.
	if s.commentCalls != 0 {
		t.Fatalf("Publish must not comment on an issue, got %d comment calls", s.commentCalls)
	}
}

func TestPublishIsIdempotentOnExistingBranchAndPR(t *testing.T) {
	s := &pubStub{rejectBranch422: true, rejectPR422: true}
	p := newStubPublisher(t, s)
	res, err := p.Publish(context.Background(), "o/r", sampleInput())
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !res.Idempotent {
		t.Fatalf("expected idempotent reuse of the open PR, got %+v", res)
	}
	if res.PRNumber != 7 || res.PRURL != "https://gh/pull/7" {
		t.Fatalf("expected reused PR #7, got %+v", res)
	}
}

func TestPublishBranchHasNoIssueSuffixWhenNoOriginIssue(t *testing.T) {
	s := &pubStub{}
	p := newStubPublisher(t, s)
	in := sampleInput()
	in.OriginIssue = 0
	res, err := p.Publish(context.Background(), "o/r", in)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Branch has no issue suffix.
	if res.Branch != "content/my-post" {
		t.Fatalf("branch = %q, want content/my-post", res.Branch)
	}
}

func TestCommentIssueHappyPath(t *testing.T) {
	s := &pubStub{}
	p := newStubPublisher(t, s)
	url, err := p.CommentIssue(context.Background(), "other/origin", 99, "📄 Published: https://gh/pull/7")
	if err != nil {
		t.Fatalf("comment issue: %v", err)
	}
	if url != "https://gh/issuecomment/1" {
		t.Fatalf("comment html_url = %q, want https://gh/issuecomment/1", url)
	}
	if s.commentCalls != 1 || len(s.commentBodies) != 1 || !strings.Contains(s.commentBodies[0], "https://gh/pull/7") {
		t.Fatalf("comment not posted correctly: calls=%d bodies=%+v", s.commentCalls, s.commentBodies)
	}
}

func TestCommentIssueReturnsErrorOnServerFailure(t *testing.T) {
	s := &pubStub{failComment500: true}
	p := newStubPublisher(t, s)
	if _, err := p.CommentIssue(context.Background(), "other/origin", 99, "body"); err == nil {
		t.Fatal("expected an error when the comment endpoint 500s (softness is the action layer's call)")
	}
}

func TestCommentIssueValidationFailsClosed(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	t.Cleanup(srv.Close)

	cases := []struct {
		name      string
		publisher *GitHubPublisher
		repo      string
		issue     int
	}{
		{"bad repo", NewGitHubPublisher("tok", srv.URL), "no-slash", 1},
		{"zero issue", NewGitHubPublisher("tok", srv.URL), "o/r", 0},
		{"negative issue", NewGitHubPublisher("tok", srv.URL), "o/r", -3},
		{"empty token", NewGitHubPublisher("", srv.URL), "o/r", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.publisher.CommentIssue(context.Background(), tc.repo, tc.issue, "body")
			if err == nil {
				t.Fatalf("expected an error")
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
	if hit {
		t.Fatalf("CommentIssue validation must fail CLOSED before any network call")
	}
}

func TestPublishValidationFailsClosed(t *testing.T) {
	// A server that records any hit, so we can assert no network call happens.
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	t.Cleanup(srv.Close)

	cases := []struct {
		name       string
		publisher  *GitHubPublisher
		in         PublishInput
		repo       string
		wantErrInv bool
	}{
		{
			name:       "empty path",
			publisher:  NewGitHubPublisher("tok", srv.URL),
			in:         PublishInput{Path: "  ", Body: "b", Title: "t"},
			repo:       "o/r",
			wantErrInv: true,
		},
		{
			name:       "empty body",
			publisher:  NewGitHubPublisher("tok", srv.URL),
			in:         PublishInput{Path: "p.md", Body: "  ", Title: "t"},
			repo:       "o/r",
			wantErrInv: true,
		},
		{
			name:       "empty title",
			publisher:  NewGitHubPublisher("tok", srv.URL),
			in:         PublishInput{Path: "p.md", Body: "b", Title: "  "},
			repo:       "o/r",
			wantErrInv: true,
		},
		{
			name:       "empty token",
			publisher:  NewGitHubPublisher("", srv.URL),
			in:         PublishInput{Path: "p.md", Body: "b", Title: "t"},
			repo:       "o/r",
			wantErrInv: true,
		},
		{
			name:       "bad repo",
			publisher:  NewGitHubPublisher("tok", srv.URL),
			in:         PublishInput{Path: "p.md", Body: "b", Title: "t"},
			repo:       "no-slash",
			wantErrInv: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.publisher.Publish(context.Background(), tc.repo, tc.in)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if tc.wantErrInv && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
	if hit {
		t.Fatalf("input validation must fail CLOSED before any network call")
	}
}
