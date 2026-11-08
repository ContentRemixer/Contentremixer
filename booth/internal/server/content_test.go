package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	appv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	appcontent "github.com/ContentRemixer/Contentremixer/app/content"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/content"
	"github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// memRepo is a minimal in-memory content.Repository so the HTTP layer can be
// tested without Postgres; the SQL half is covered by app/content tests.
type memRepo struct {
	fail bool // ListContents errors when set (drives the 500 path)

	contents map[string]appcontent.Content
	comments map[string]appcontent.Comment
	threads  map[string]appcontent.ReviewThread
	messages map[string][]appcontent.ReviewMessage
	requests map[string]appcontent.IllustrationRequest
}

var errBoom = errors.New("boom")

func newMemRepo() *memRepo {
	return &memRepo{
		contents: map[string]appcontent.Content{},
		comments: map[string]appcontent.Comment{},
		threads:  map[string]appcontent.ReviewThread{},
		messages: map[string][]appcontent.ReviewMessage{},
		requests: map[string]appcontent.IllustrationRequest{},
	}
}

func (f *memRepo) InsertContent(_ context.Context, c appcontent.Content) error {
	f.contents[c.ID] = c
	return nil
}

func (f *memRepo) GetContent(_ context.Context, id string) (appcontent.Content, error) {
	c, ok := f.contents[id]
	if !ok {
		return appcontent.Content{}, appcontent.ErrNotFound
	}
	return c, nil
}

func (f *memRepo) ListContents(_ context.Context, ownerFilter string) ([]appcontent.Content, error) {
	if f.fail {
		return nil, errBoom
	}
	var out []appcontent.Content
	for _, c := range f.contents {
		if ownerFilter == "" || c.OwnerSubject == ownerFilter {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *memRepo) UpdateContent(_ context.Context, id string, title, bodyMD *string, status *appcontent.ContentStatus) (appcontent.Content, error) {
	c, ok := f.contents[id]
	if !ok {
		return appcontent.Content{}, appcontent.ErrNotFound
	}
	if title != nil {
		c.Title = *title
	}
	if bodyMD != nil {
		c.BodyMD = *bodyMD
	}
	if status != nil {
		c.Status = *status
	}
	f.contents[id] = c
	return c, nil
}

func (f *memRepo) DeleteContent(_ context.Context, id string) error {
	delete(f.contents, id)
	return nil
}

func (f *memRepo) InsertComment(_ context.Context, c appcontent.Comment) error {
	f.comments[c.ID] = c
	return nil
}

func (f *memRepo) ListComments(_ context.Context, contentID string) ([]appcontent.Comment, error) {
	var out []appcontent.Comment
	for _, c := range f.comments {
		if c.ContentID == contentID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *memRepo) InsertThread(_ context.Context, t appcontent.ReviewThread, first appcontent.ReviewMessage) error {
	f.threads[t.ID] = t
	f.messages[t.ID] = append(f.messages[t.ID], first)
	return nil
}

func (f *memRepo) GetThread(_ context.Context, id string) (appcontent.ReviewThread, error) {
	t, ok := f.threads[id]
	if !ok {
		return appcontent.ReviewThread{}, appcontent.ErrNotFound
	}
	t.Messages = f.messages[id]
	return t, nil
}

func (f *memRepo) ListThreads(_ context.Context, contentID string) ([]appcontent.ReviewThread, error) {
	var out []appcontent.ReviewThread
	for _, t := range f.threads {
		if t.ContentID == contentID {
			t.Messages = f.messages[t.ID]
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *memRepo) InsertMessage(_ context.Context, m appcontent.ReviewMessage) error {
	f.messages[m.ThreadID] = append(f.messages[m.ThreadID], m)
	return nil
}

func (f *memRepo) SetThreadStatus(_ context.Context, id string, status appcontent.ThreadStatus) error {
	t, ok := f.threads[id]
	if !ok {
		return appcontent.ErrNotFound
	}
	t.Status = status
	f.threads[id] = t
	return nil
}

func (f *memRepo) InsertIllustrationRequest(_ context.Context, r appcontent.IllustrationRequest) error {
	f.requests[r.ID] = r
	return nil
}

func (f *memRepo) GetIllustrationRequest(_ context.Context, id string) (appcontent.IllustrationRequest, error) {
	r, ok := f.requests[id]
	if !ok {
		return appcontent.IllustrationRequest{}, appcontent.ErrNotFound
	}
	return r, nil
}

func (f *memRepo) ListIllustrationRequests(_ context.Context, flt appcontent.RequestFilter) ([]appcontent.IllustrationRequest, error) {
	var out []appcontent.IllustrationRequest
	for _, r := range f.requests {
		if flt.Owner != "" && r.OwnerSubject != flt.Owner {
			continue
		}
		if flt.Assignee != "" && r.AssignedAgent != flt.Assignee {
			continue
		}
		if flt.Status != "" && r.Status != flt.Status {
			continue
		}
		if flt.VisibleTo != "" && r.OwnerSubject != flt.VisibleTo && r.AssignedAgent != flt.VisibleTo {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *memRepo) UpdateIllustrationRequest(_ context.Context, id string, status *appcontent.IllustrationStatus, assignedAgent *string) (appcontent.IllustrationRequest, error) {
	r, ok := f.requests[id]
	if !ok {
		return appcontent.IllustrationRequest{}, appcontent.ErrNotFound
	}
	if status != nil {
		r.Status = *status
	}
	if assignedAgent != nil {
		r.AssignedAgent = *assignedAgent
	}
	f.requests[id] = r
	return r, nil
}

var _ content.Repository = (*memRepo)(nil)

// memRepoForTest captures the last memRepo built for tests that inject
// failures.
var memRepoForTest *memRepo

// newContentTestServer wires a Server whose content routes are backed by the
// §6.3 gate over an in-memory repo.
func newContentTestServer(t *testing.T) (http.Handler, *auth.TokenCodec, *content.Store) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := appv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	st := store.New(c, ns, audit.NewSlogLogger(nil))

	adminHash, err := auth.HashPassword("admin-pw")
	if err != nil {
		t.Fatal(err)
	}
	prov, err := auth.Bootstrap(auth.Config{
		SessionKey:    testKey,
		SessionTTL:    time.Hour,
		AdminUsername: "admin",
		AdminHash:     adminHash,
		SecureCookie:  false,
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := newMemRepo()
	memRepoForTest = repo
	cs := content.New(repo, audit.NewSlogLogger(nil))
	return New(prov, st, cs, nil).Handler(), prov.Codec, cs
}

// TestContentRoutes_Unauthenticated covers the middleware wrap: no session, no
// content access.
func TestContentRoutes_Unauthenticated(t *testing.T) {
	h, _, _ := newContentTestServer(t)
	for _, method := range []string{"GET", "POST"} {
		rec := do(t, h, method, "/v1/content", "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s /v1/content without session: want 401, got %d", method, rec.Code)
		}
	}
	rec := do(t, h, "GET", "/v1/illustration-requests", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET illustration-requests without session: want 401, got %d", rec.Code)
	}
}

// TestContentRoutes_Lifecycle walks the whole B3 collaboration flow over HTTP:
// create a document, comment on an anchor, open a review thread, request an
// illustration (default-assigning the Graphic Designer), work the agent queue
// through delivery, and accept it as the owner.
func TestContentRoutes_Lifecycle(t *testing.T) {
	h, codec, _ := newContentTestServer(t)
	alice := tokenFor(t, codec, "alice", auth.RoleDevRel)
	agent := tokenFor(t, codec, appcontent.DefaultAssigneeAgent, auth.RoleDevRel)

	rec := do(t, h, "POST", "/v1/content", alice, `{"title":"K8s show notes","bodyMd":"# intro"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create content: %d %s", rec.Code, rec.Body.String())
	}
	var doc appcontent.Content
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode content: %v", err)
	}
	if doc.OwnerSubject != "alice" || doc.Status != appcontent.ContentDraft {
		t.Fatalf("content wrong: %+v", doc)
	}

	rec = do(t, h, "GET", "/v1/content", alice, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), doc.ID) {
		t.Fatalf("list content: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, "GET", "/v1/content/"+doc.ID, alice, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get content: %d", rec.Code)
	}
	rec = do(t, h, "PUT", "/v1/content/"+doc.ID, alice, `{"status":"in_review"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update content: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "PUT", "/v1/content/"+doc.ID, alice, `{"status":"bogus"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bogus status: want 400, got %d", rec.Code)
	}
	rec = do(t, h, "GET", "/v1/content/missing", alice, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing content: want 404, got %d", rec.Code)
	}

	rec = do(t, h, "POST", "/v1/content/"+doc.ID+"/comments", alice, `{"body":"tighten","anchor":{"start":0,"end":6,"quote":"# intro"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "GET", "/v1/content/"+doc.ID+"/comments", alice, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "tighten") {
		t.Fatalf("list comments: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "POST", "/v1/content/"+doc.ID+"/comments", alice, `{"body":"","anchor":{"start":0,"end":1}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty comment: want 400, got %d", rec.Code)
	}

	rec = do(t, h, "POST", "/v1/content/"+doc.ID+"/reviews", alice, `{"subject":"structure","body":"reorder"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create review: %d %s", rec.Code, rec.Body.String())
	}
	var thread appcontent.ReviewThread
	if err := json.Unmarshal(rec.Body.Bytes(), &thread); err != nil {
		t.Fatal(err)
	}
	rec = do(t, h, "POST", "/v1/content/"+doc.ID+"/reviews", alice, `{"threadId":"`+thread.ID+`","body":"done"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reply review: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "POST", "/v1/content/"+doc.ID+"/reviews/"+thread.ID+"/resolve", alice, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve review: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, "POST", "/v1/illustration-requests", alice, `{"contentId":"`+doc.ID+`","brief":"hero diagram","anchor":{"start":0,"end":6}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create illustration: %d %s", rec.Code, rec.Body.String())
	}
	var req appcontent.IllustrationRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatal(err)
	}
	if req.AssignedAgent != appcontent.DefaultAssigneeAgent {
		t.Fatalf("assignee not defaulted: %+v", req)
	}

	rec = do(t, h, "GET", "/v1/illustration-requests?assignee="+url.QueryEscape(appcontent.DefaultAssigneeAgent), agent, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), req.ID) {
		t.Fatalf("agent queue: %d %s", rec.Code, rec.Body.String())
	}
	bob := tokenFor(t, codec, "bob", auth.RoleDevRel)
	rec = do(t, h, "GET", "/v1/illustration-requests", bob, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), req.ID) {
		t.Fatalf("stranger queue leak: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "GET", "/v1/illustration-requests?status=bogus", alice, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bogus status filter: want 400, got %d", rec.Code)
	}

	rec = do(t, h, "PATCH", "/v1/illustration-requests/"+req.ID, agent, `{"status":"in_progress"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("agent start: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "PATCH", "/v1/illustration-requests/"+req.ID, agent, `{"status":"delivered"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("agent deliver: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "PATCH", "/v1/illustration-requests/"+req.ID, bob, `{"status":"accepted"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("stranger transition: want 403, got %d", rec.Code)
	}
	rec = do(t, h, "PATCH", "/v1/illustration-requests/"+req.ID, alice, `{"status":"accepted"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner accept: %d %s", rec.Code, rec.Body.String())
	}
}

// TestContentRoutes_Scoping covers the §6.3 denial over HTTP: another DevRel
// cannot read, comment on, or delete alice's document.
func TestContentRoutes_Scoping(t *testing.T) {
	h, codec, cs := newContentTestServer(t)
	alice := tokenFor(t, codec, "alice", auth.RoleDevRel)
	bob := tokenFor(t, codec, "bob", auth.RoleDevRel)

	doc, err := cs.CreateContent(t.Context(), auth.Session{Subject: "alice", Role: auth.RoleDevRel}, content.CreateContentRequest{Title: "A"})
	if err != nil {
		t.Fatal(err)
	}

	if rec := do(t, h, "GET", "/v1/content/"+doc.ID, bob, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("bob get: want 403, got %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/v1/content/"+doc.ID+"/comments", bob, `{"body":"hi"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("bob comment: want 403, got %d", rec.Code)
	}
	if rec := do(t, h, "DELETE", "/v1/content/"+doc.ID, bob, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("bob delete: want 403, got %d", rec.Code)
	}
	if rec := do(t, h, "DELETE", "/v1/content/"+doc.ID, alice, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("alice delete: want 204, got %d", rec.Code)
	}
}

// TestContentRoutes_BadBodies covers the JSON decode rejections and the
// not-found parent paths.
func TestContentRoutes_BadBodies(t *testing.T) {
	h, codec, cs := newContentTestServer(t)
	alice := tokenFor(t, codec, "alice", auth.RoleDevRel)
	doc, err := cs.CreateContent(t.Context(), auth.Session{Subject: "alice", Role: auth.RoleDevRel}, content.CreateContentRequest{Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	bad := []struct{ method, path string }{
		{"POST", "/v1/content"},
		{"PUT", "/v1/content/" + doc.ID},
		{"POST", "/v1/content/" + doc.ID + "/comments"},
		{"POST", "/v1/content/" + doc.ID + "/reviews"},
		{"POST", "/v1/illustration-requests"},
		{"PATCH", "/v1/illustration-requests/x"},
	}
	for _, tc := range bad {
		rec := do(t, h, tc.method, tc.path, alice, "{")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: want 400, got %d", tc.method, tc.path, rec.Code)
		}
	}
	rec := do(t, h, "POST", "/v1/content/missing/comments", alice, `{"body":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("comment on missing content: want 404, got %d", rec.Code)
	}
	rec = do(t, h, "GET", "/v1/illustration-requests/missing", alice, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing illustration: want 404, got %d", rec.Code)
	}
}

// TestContentRoutes_StoreError covers the 500 mapping when the gate surfaces a
// raw persistence error.
func TestContentRoutes_StoreError(t *testing.T) {
	h, codec, _ := newContentTestServer(t)
	alice := tokenFor(t, codec, "alice", auth.RoleDevRel)
	memRepoForTest.fail = true
	rec := do(t, h, "GET", "/v1/content", alice, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("store error: want 500, got %d %s", rec.Code, rec.Body.String())
	}
}
