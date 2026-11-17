package approve

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ghStub is a tiny GitHub reviews API stub: it records created reviews and can
// be told to reject APPROVE with 422 (the self-approval case).
type ghStub struct {
	existing     []ghReview // returned by the list endpoint
	created      []map[string]string
	reject422    bool // APPROVE → 422, forcing the COMMENT fallback
	nextID       int64
	lastListPath string
}

func (g *ghStub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/{num}/reviews", func(w http.ResponseWriter, r *http.Request) {
		g.lastListPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(g.existing)
	})
	mux.HandleFunc("POST /repos/{owner}/{repo}/pulls/{num}/reviews", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in map[string]string
		_ = json.Unmarshal(body, &in)
		if in["event"] == "APPROVE" && g.reject422 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Can not approve your own pull request"}`))
			return
		}
		g.created = append(g.created, in)
		g.nextID++
		state := "COMMENTED"
		if in["event"] == "APPROVE" {
			state = "APPROVED"
		}
		_ = json.NewEncoder(w).Encode(ghReview{ID: g.nextID, Body: in["body"], State: state, HTMLURL: "https://gh/review"})
	})
	return mux
}

func newStubReflector(t *testing.T, g *ghStub) *GitHubReflector {
	t.Helper()
	srv := httptest.NewServer(g.handler())
	t.Cleanup(srv.Close)
	return NewGitHubReflector("tok", srv.URL)
}

func sampleApproval() Approval {
	return Approval{Subject: "sub-alice", Role: "DevRel", Note: "ship it", Key: "o/r#42:sub-alice"}
}

func TestReflectFilesApproveReview(t *testing.T) {
	g := &ghStub{}
	r := newStubReflector(t, g)
	refl, err := r.Reflect(context.Background(), "o/r", 42, sampleApproval())
	if err != nil {
		t.Fatalf("reflect: %v", err)
	}
	if refl.Mode != ModeApprove {
		t.Fatalf("mode = %q, want approve", refl.Mode)
	}
	if len(g.created) != 1 || g.created[0]["event"] != "APPROVE" {
		t.Fatalf("created = %+v, want one APPROVE", g.created)
	}
	// The body carries the machine-parseable marker (greppable by the merge
	// gate) and the human attribution line + note.
	b := g.created[0]["body"]
	if !strings.Contains(b, boothApprovalMarker+"o/r#42:sub-alice") {
		t.Fatalf("body missing booth-approval marker: %q", b)
	}
	if !strings.Contains(b, "`sub-alice` (DevRel)") || !strings.Contains(b, "ship it") {
		t.Fatalf("body missing attribution/note: %q", b)
	}
}

func TestReflectFallsBackToCommentOnSelfApproval(t *testing.T) {
	g := &ghStub{reject422: true}
	r := newStubReflector(t, g)
	refl, err := r.Reflect(context.Background(), "o/r", 42, sampleApproval())
	if err != nil {
		t.Fatalf("reflect: %v", err)
	}
	if refl.Mode != ModeComment {
		t.Fatalf("mode = %q, want comment fallback", refl.Mode)
	}
	if len(g.created) != 1 || g.created[0]["event"] != "COMMENT" {
		t.Fatalf("created = %+v, want one COMMENT after the 422 APPROVE", g.created)
	}
	// Even on fallback the greppable marker lands.
	if !strings.Contains(g.created[0]["body"], boothApprovalMarker) {
		t.Fatalf("fallback body missing marker: %q", g.created[0]["body"])
	}
}

func TestReflectIsIdempotent(t *testing.T) {
	ap := sampleApproval()
	g := &ghStub{existing: []ghReview{
		{ID: 99, State: "APPROVED", HTMLURL: "https://gh/existing", Body: "<!-- " + boothApprovalMarker + ap.Key + " -->\nAlready approved"},
	}}
	r := newStubReflector(t, g)
	refl, err := r.Reflect(context.Background(), "o/r", 42, ap)
	if err != nil {
		t.Fatalf("reflect: %v", err)
	}
	if !refl.Idempotent || refl.ReviewID != 99 {
		t.Fatalf("expected idempotent reuse of review 99, got %+v", refl)
	}
	if len(g.created) != 0 {
		t.Fatalf("idempotent re-approve must not create a new review (created %d)", len(g.created))
	}
}

// A different approver's existing review must NOT be treated as this caller's —
// the key, not just the marker, gates idempotency.
func TestReflectDistinctApproverIsNotIdempotent(t *testing.T) {
	g := &ghStub{existing: []ghReview{
		{ID: 50, State: "APPROVED", Body: "<!-- " + boothApprovalMarker + "o/r#42:sub-bob -->\nBob approved"},
	}}
	r := newStubReflector(t, g)
	refl, err := r.Reflect(context.Background(), "o/r", 42, sampleApproval()) // alice
	if err != nil {
		t.Fatalf("reflect: %v", err)
	}
	if refl.Idempotent {
		t.Fatalf("alice must not reuse bob's review")
	}
	if len(g.created) != 1 {
		t.Fatalf("alice's approval should create a new review (created %d)", len(g.created))
	}
}

func TestReflectNoTokenFails(t *testing.T) {
	r := &GitHubReflector{}
	if _, err := r.Reflect(context.Background(), "o/r", 1, sampleApproval()); err == nil {
		t.Fatal("expected error with no token configured")
	}
}
