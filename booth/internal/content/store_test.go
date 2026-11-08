package content

import (
	"context"
	"errors"
	"fmt"
	"testing"

	appcontent "github.com/ContentRemixer/Contentremixer/app/content"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
)

// fakeRepo is an in-memory Repository so the §6.3 gate is testable without
// Postgres. It performs NO authorization — persistence only.
type fakeRepo struct {
	fail     bool // every method errors when set (drives the gate's error paths)
	contents map[string]appcontent.Content
	comments map[string]appcontent.Comment
	threads  map[string]appcontent.ReviewThread
	messages map[string][]appcontent.ReviewMessage
	requests map[string]appcontent.IllustrationRequest
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		contents: map[string]appcontent.Content{},
		comments: map[string]appcontent.Comment{},
		threads:  map[string]appcontent.ReviewThread{},
		messages: map[string][]appcontent.ReviewMessage{},
		requests: map[string]appcontent.IllustrationRequest{},
	}
}

func (f *fakeRepo) InsertContent(_ context.Context, c appcontent.Content) error {
	if f.fail {
		return errRepo
	}
	f.contents[c.ID] = c
	return nil
}

func (f *fakeRepo) GetContent(_ context.Context, id string) (appcontent.Content, error) {
	if f.fail {
		return appcontent.Content{}, errRepo
	}
	c, ok := f.contents[id]
	if !ok {
		return appcontent.Content{}, appcontent.ErrNotFound
	}
	return c, nil
}

func (f *fakeRepo) ListContents(_ context.Context, ownerFilter string) ([]appcontent.Content, error) {
	if f.fail {
		return nil, errRepo
	}
	var out []appcontent.Content
	for _, c := range f.contents {
		if ownerFilter == "" || c.OwnerSubject == ownerFilter {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeRepo) UpdateContent(_ context.Context, id string, title, bodyMD *string, status *appcontent.ContentStatus) (appcontent.Content, error) {
	if f.fail {
		return appcontent.Content{}, errRepo
	}
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

func (f *fakeRepo) DeleteContent(_ context.Context, id string) error {
	if f.fail {
		return errRepo
	}
	if _, ok := f.contents[id]; !ok {
		return appcontent.ErrNotFound
	}
	delete(f.contents, id)
	for cid, c := range f.comments {
		if c.ContentID == id {
			delete(f.comments, cid)
		}
	}
	for tid, t := range f.threads {
		if t.ContentID == id {
			delete(f.threads, tid)
			delete(f.messages, tid)
		}
	}
	for rid, r := range f.requests {
		if r.ContentID == id {
			delete(f.requests, rid)
		}
	}
	return nil
}

func (f *fakeRepo) InsertComment(_ context.Context, c appcontent.Comment) error {
	if f.fail {
		return errRepo
	}
	f.comments[c.ID] = c
	return nil
}

func (f *fakeRepo) ListComments(_ context.Context, contentID string) ([]appcontent.Comment, error) {
	if f.fail {
		return nil, errRepo
	}
	var out []appcontent.Comment
	for _, c := range f.comments {
		if c.ContentID == contentID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeRepo) InsertThread(_ context.Context, t appcontent.ReviewThread, first appcontent.ReviewMessage) error {
	if f.fail {
		return errRepo
	}
	f.threads[t.ID] = t
	f.messages[t.ID] = append(f.messages[t.ID], first)
	return nil
}

func (f *fakeRepo) GetThread(_ context.Context, id string) (appcontent.ReviewThread, error) {
	if f.fail {
		return appcontent.ReviewThread{}, errRepo
	}
	t, ok := f.threads[id]
	if !ok {
		return appcontent.ReviewThread{}, appcontent.ErrNotFound
	}
	t.Messages = f.messages[t.ID]
	return t, nil
}

func (f *fakeRepo) ListThreads(_ context.Context, contentID string) ([]appcontent.ReviewThread, error) {
	if f.fail {
		return nil, errRepo
	}
	var out []appcontent.ReviewThread
	for _, t := range f.threads {
		if t.ContentID == contentID {
			t.Messages = f.messages[t.ID]
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeRepo) InsertMessage(_ context.Context, m appcontent.ReviewMessage) error {
	if f.fail {
		return errRepo
	}
	f.messages[m.ThreadID] = append(f.messages[m.ThreadID], m)
	return nil
}

func (f *fakeRepo) SetThreadStatus(_ context.Context, id string, status appcontent.ThreadStatus) error {
	if f.fail {
		return errRepo
	}
	t, ok := f.threads[id]
	if !ok {
		return appcontent.ErrNotFound
	}
	t.Status = status
	f.threads[id] = t
	return nil
}

func (f *fakeRepo) InsertIllustrationRequest(_ context.Context, r appcontent.IllustrationRequest) error {
	if f.fail {
		return errRepo
	}
	f.requests[r.ID] = r
	return nil
}

func (f *fakeRepo) GetIllustrationRequest(_ context.Context, id string) (appcontent.IllustrationRequest, error) {
	if f.fail {
		return appcontent.IllustrationRequest{}, errRepo
	}
	r, ok := f.requests[id]
	if !ok {
		return appcontent.IllustrationRequest{}, appcontent.ErrNotFound
	}
	return r, nil
}

func (f *fakeRepo) ListIllustrationRequests(_ context.Context, flt appcontent.RequestFilter) ([]appcontent.IllustrationRequest, error) {
	if f.fail {
		return nil, errRepo
	}
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

func (f *fakeRepo) UpdateIllustrationRequest(_ context.Context, id string, status *appcontent.IllustrationStatus, assignedAgent *string) (appcontent.IllustrationRequest, error) {
	if f.fail {
		return appcontent.IllustrationRequest{}, errRepo
	}
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

// --- test scaffolding --------------------------------------------------------

type captureAuditor struct{ entries []audit.Entry }

func (c *captureAuditor) Log(_ context.Context, e audit.Entry) { c.entries = append(c.entries, e) }

func (c *captureAuditor) find(verb, kind, name, decision string) *audit.Entry {
	for i := range c.entries {
		e := &c.entries[i]
		if e.Verb == verb && e.ResourceKind == kind && e.ResourceName == name && e.Decision == decision {
			return e
		}
	}
	return nil
}

var _ audit.Logger = (*captureAuditor)(nil)

func devrel(subject string) auth.Session {
	return auth.Session{Subject: subject, Role: auth.RoleDevRel}
}
func admin(subject string) auth.Session { return auth.Session{Subject: subject, Role: auth.RoleAdmin} }

func newTestGate() (*Store, *fakeRepo, *captureAuditor) {
	repo := newFakeRepo()
	aud := &captureAuditor{}
	return New(repo, aud), repo, aud
}

func strPtr(s string) *string { return &s }

// mustCreate makes a document owned by owner and returns its id.
func mustCreate(t *testing.T, s *Store, owner auth.Session, title string) appcontent.Content {
	t.Helper()
	c, err := s.CreateContent(context.Background(), owner, CreateContentRequest{Title: title, BodyMD: "# " + title})
	if err != nil {
		t.Fatalf("CreateContent: %v", err)
	}
	return c
}

// --- §6.3 matrix -------------------------------------------------------------

func TestGate_ContentOwnership(t *testing.T) {
	s, _, aud := newTestGate()
	ctx := context.Background()

	mine, err := s.CreateContent(ctx, devrel("alice"), CreateContentRequest{Title: "Mine"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if mine.OwnerSubject != "alice" {
		t.Fatalf("owner not stamped from session: %q", mine.OwnerSubject)
	}
	if mine.Status != appcontent.ContentDraft {
		t.Fatalf("new content not draft: %q", mine.Status)
	}
	if aud.find(verbCreate, kindContent, mine.ID, audit.DecisionAllow) == nil {
		t.Fatal("create allow not audited")
	}

	if _, err := s.CreateContent(ctx, devrel("alice"), CreateContentRequest{}); err == nil {
		t.Fatal("empty title accepted")
	}

	if _, err := s.GetContent(ctx, devrel("bob"), mine.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bob reading alice's content: want ErrForbidden, got %v", err)
	}
	if aud.find(verbGet, kindContent, mine.ID, audit.DecisionDeny) == nil {
		t.Fatal("deny not audited")
	}
	if _, err := s.GetContent(ctx, devrel("alice"), mine.ID); err != nil {
		t.Fatalf("alice reading own: %v", err)
	}
	if _, err := s.GetContent(ctx, admin("root"), mine.ID); err != nil {
		t.Fatalf("admin reading alice's: %v", err)
	}
	if _, err := s.GetContent(ctx, devrel("alice"), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing content: want ErrNotFound, got %v", err)
	}
}

func TestGate_ContentListFilteredServerSide(t *testing.T) {
	s, _, _ := newTestGate()
	ctx := context.Background()
	mustCreate(t, s, devrel("alice"), "A1")
	mustCreate(t, s, devrel("bob"), "B1")

	aliceRows, err := s.ListContents(ctx, devrel("alice"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(aliceRows) != 1 || aliceRows[0].OwnerSubject != "alice" {
		t.Fatalf("devrel list not owner-filtered: %+v", aliceRows)
	}
	adminRows, err := s.ListContents(ctx, admin("root"))
	if err != nil {
		t.Fatalf("admin list: %v", err)
	}
	if len(adminRows) != 2 {
		t.Fatalf("admin list should see all: %+v", adminRows)
	}
}

func TestGate_ContentUpdateDelete(t *testing.T) {
	s, _, _ := newTestGate()
	ctx := context.Background()
	mine := mustCreate(t, s, devrel("alice"), "A1")

	if _, err := s.UpdateContent(ctx, devrel("bob"), mine.ID, UpdateContentRequest{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bob updating alice's: want ErrForbidden, got %v", err)
	}
	bad := appcontent.ContentStatus("nope")
	if _, err := s.UpdateContent(ctx, devrel("alice"), mine.ID, UpdateContentRequest{Status: &bad}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("invalid status: want ErrBadRequest, got %v", err)
	}
	review := appcontent.ContentInReview
	got, err := s.UpdateContent(ctx, admin("root"), mine.ID, UpdateContentRequest{Status: &review})
	if err != nil {
		t.Fatalf("admin update: %v", err)
	}
	if got.Status != review {
		t.Fatalf("status not applied: %q", got.Status)
	}

	if err := s.DeleteContent(ctx, devrel("bob"), mine.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bob deleting alice's: want ErrForbidden, got %v", err)
	}
	if err := s.DeleteContent(ctx, devrel("alice"), mine.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
}

func TestGate_CommentsGateOnParentContent(t *testing.T) {
	s, _, _ := newTestGate()
	ctx := context.Background()
	mine := mustCreate(t, s, devrel("alice"), "A1")

	if _, err := s.CreateComment(ctx, devrel("bob"), mine.ID, CreateCommentRequest{Body: "hi"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger commenting: want ErrForbidden, got %v", err)
	}
	if _, err := s.CreateComment(ctx, devrel("alice"), mine.ID, CreateCommentRequest{Body: ""}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("empty body: want ErrBadRequest, got %v", err)
	}
	if _, err := s.CreateComment(ctx, devrel("alice"), mine.ID, CreateCommentRequest{Body: "x", Anchor: appcontent.Anchor{Start: -1, End: 2}}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("negative anchor: want ErrBadRequest, got %v", err)
	}
	c, err := s.CreateComment(ctx, devrel("alice"), mine.ID, CreateCommentRequest{
		Body: "tighten this", Anchor: appcontent.Anchor{Start: 4, End: 10, Quote: "intro"},
	})
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if c.AuthorSubject != "alice" || c.Anchor.Start != 4 {
		t.Fatalf("comment not stamped/anchored: %+v", c)
	}
	if _, err := s.ListComments(ctx, devrel("bob"), mine.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger listing comments: want ErrForbidden, got %v", err)
	}
	got, err := s.ListComments(ctx, admin("root"), mine.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("admin list comments: %v %+v", err, got)
	}
}

func TestGate_ReviewThreads(t *testing.T) {
	s, _, _ := newTestGate()
	ctx := context.Background()
	mine := mustCreate(t, s, devrel("alice"), "A1")

	th, err := s.CreateReview(ctx, devrel("alice"), mine.ID, CreateReviewRequest{Subject: "structure", Body: "reorder sections"})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	if th.Status != appcontent.ThreadOpen || th.CreatedBy != "alice" {
		t.Fatalf("thread wrong: %+v", th)
	}
	th, err = s.CreateReview(ctx, devrel("alice"), mine.ID, CreateReviewRequest{ThreadID: th.ID, Body: "agreed"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if len(th.Messages) != 2 {
		t.Fatalf("thread should carry 2 messages: %+v", th.Messages)
	}

	other := mustCreate(t, s, devrel("bob"), "B1")
	if _, err := s.CreateReview(ctx, devrel("alice"), other.ID, CreateReviewRequest{ThreadID: th.ID, Body: "cross-doc"}); err == nil {
		t.Fatal("thread reply on wrong document accepted")
	}
	if _, err := s.ResolveReviewThread(ctx, devrel("bob"), mine.ID, th.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger resolving: want ErrForbidden, got %v", err)
	}
	resolved, err := s.ResolveReviewThread(ctx, devrel("alice"), mine.ID, th.ID)
	if err != nil || resolved.Status != appcontent.ThreadResolved {
		t.Fatalf("resolve: %v %+v", err, resolved)
	}
}

func TestGate_IllustrationWorkQueue(t *testing.T) {
	s, repo, _ := newTestGate()
	ctx := context.Background()
	mine := mustCreate(t, s, devrel("alice"), "A1")

	r, err := s.CreateIllustrationRequest(ctx, devrel("alice"), mine.ID, CreateIllustrationRequestPayload{Brief: "hero diagram"})
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if r.AssignedAgent != appcontent.DefaultAssigneeAgent {
		t.Fatalf("assignee not defaulted: %q", r.AssignedAgent)
	}
	if r.OwnerSubject != "alice" || r.Status != appcontent.IllustrationRequested {
		t.Fatalf("request wrong: %+v", r)
	}

	// A second request owned by bob but assigned to alice (seeded directly —
	// the gate stamps owners, the queue rule is what's under test).
	repo.requests["seeded"] = appcontent.IllustrationRequest{
		ID: "seeded", ContentID: mine.ID, OwnerSubject: "bob",
		AssignedAgent: "alice", Status: appcontent.IllustrationRequested, Brief: "x",
	}

	// The agent work queue: the assigned agent sees its items.
	aliceQ, err := s.ListIllustrationRequests(ctx, devrel("alice"), "", "")
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(aliceQ) != 2 {
		t.Fatalf("alice should see own + assigned: %+v", aliceQ)
	}
	bobQ, err := s.ListIllustrationRequests(ctx, devrel("bob"), "", "")
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(bobQ) != 1 || bobQ[0].ID != "seeded" {
		t.Fatalf("bob should see only his own: %+v", bobQ)
	}
	adminQ, err := s.ListIllustrationRequests(ctx, admin("root"), "", "")
	if err != nil || len(adminQ) != 2 {
		t.Fatalf("admin queue: %v %+v", err, adminQ)
	}

	// ?assignee= narrows within the visible set.
	byAgent, err := s.ListIllustrationRequests(ctx, admin("root"), appcontent.DefaultAssigneeAgent, "")
	if err != nil || len(byAgent) != 1 || byAgent[0].ID != r.ID {
		t.Fatalf("assignee filter: %v %+v", err, byAgent)
	}
	bad := appcontent.IllustrationStatus("nope")
	if _, err := s.ListIllustrationRequests(ctx, admin("root"), "", bad); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("bad status filter: want ErrBadRequest, got %v", err)
	}
}

func TestGate_IllustrationTransitions(t *testing.T) {
	s, repo, _ := newTestGate()
	ctx := context.Background()
	mine := mustCreate(t, s, devrel("alice"), "A1")
	r, err := s.CreateIllustrationRequest(ctx, devrel("alice"), mine.ID, CreateIllustrationRequestPayload{Brief: "hero"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	agent := devrel(appcontent.DefaultAssigneeAgent)
	owner := devrel("alice")
	stranger := devrel("mallory")

	inProgress := appcontent.IllustrationInProgress
	delivered := appcontent.IllustrationDelivered
	accepted := appcontent.IllustrationAccepted
	cancelled := appcontent.IllustrationCancelled

	if _, err := s.UpdateIllustrationRequest(ctx, stranger, r.ID, UpdateIllustrationRequestPayload{Status: &inProgress}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger transition: want ErrForbidden, got %v", err)
	}
	// The agent may start and deliver…
	if _, err := s.UpdateIllustrationRequest(ctx, agent, r.ID, UpdateIllustrationRequestPayload{Status: &inProgress}); err != nil {
		t.Fatalf("agent start: %v", err)
	}
	if _, err := s.UpdateIllustrationRequest(ctx, agent, r.ID, UpdateIllustrationRequestPayload{Status: &delivered}); err != nil {
		t.Fatalf("agent deliver: %v", err)
	}
	// …but may not accept or re-route.
	if _, err := s.UpdateIllustrationRequest(ctx, agent, r.ID, UpdateIllustrationRequestPayload{Status: &accepted}); err == nil {
		t.Fatal("agent accept accepted")
	}
	if _, err := s.UpdateIllustrationRequest(ctx, agent, r.ID, UpdateIllustrationRequestPayload{AssignedAgent: strPtr("Other Agent")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent reassign: want ErrForbidden, got %v", err)
	}
	// The owner accepts; a fresh request can be cancelled by its owner.
	if _, err := s.UpdateIllustrationRequest(ctx, owner, r.ID, UpdateIllustrationRequestPayload{Status: &accepted}); err != nil {
		t.Fatalf("owner accept: %v", err)
	}
	bobDoc := mustCreate(t, s, devrel("bob"), "B1")
	r2, err := s.CreateIllustrationRequest(ctx, devrel("bob"), bobDoc.ID, CreateIllustrationRequestPayload{Brief: "second"})
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if _, err := s.UpdateIllustrationRequest(ctx, devrel("bob"), r2.ID, UpdateIllustrationRequestPayload{Status: &cancelled}); err != nil {
		t.Fatalf("owner cancel: %v", err)
	}
	// Admin moves anything anywhere legal-by-enum.
	repo.requests["admin-case"] = appcontent.IllustrationRequest{
		ID: "admin-case", ContentID: mine.ID, OwnerSubject: "bob",
		AssignedAgent: "agent-x", Status: appcontent.IllustrationRequested, Brief: "x",
	}
	if _, err := s.UpdateIllustrationRequest(ctx, admin("root"), "admin-case", UpdateIllustrationRequestPayload{Status: &delivered, AssignedAgent: strPtr("agent-y")}); err != nil {
		t.Fatalf("admin move: %v", err)
	}
	// Owner cannot jump the queue: requested → accepted is illegal.
	if _, err := s.UpdateIllustrationRequest(ctx, devrel("bob"), r2.ID, UpdateIllustrationRequestPayload{Status: &accepted}); err == nil {
		t.Fatal("owner skipping delivery accepted")
	}
	// GetIllustrationRequest visibility follows the same rule.
	if _, err := s.GetIllustrationRequest(ctx, stranger, r.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger get: want ErrForbidden, got %v", err)
	}
	if _, err := s.GetIllustrationRequest(ctx, agent, r.ID); err != nil {
		t.Fatalf("agent get: %v", err)
	}
}

func TestGate_BadRequests(t *testing.T) {
	s, _, _ := newTestGate()
	ctx := context.Background()
	mine := mustCreate(t, s, devrel("alice"), "A1")

	if _, err := s.CreateIllustrationRequest(ctx, devrel("alice"), mine.ID, CreateIllustrationRequestPayload{}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("empty brief: want ErrBadRequest, got %v", err)
	}
	if _, err := s.CreateIllustrationRequest(ctx, devrel("alice"), mine.ID, CreateIllustrationRequestPayload{Brief: "x", Anchor: &appcontent.Anchor{Start: 5, End: 1}}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("inverted anchor: want ErrBadRequest, got %v", err)
	}
	if _, err := s.CreateReview(ctx, devrel("alice"), mine.ID, CreateReviewRequest{Body: "no subject"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("thread without subject: want ErrBadRequest, got %v", err)
	}
	if _, err := s.CreateContent(ctx, admin("root"), CreateContentRequest{Title: fmt.Sprintf("%d", 1)}); err != nil {
		t.Fatalf("admin create: %v", err)
	}
}

var errRepo = errors.New("repo down")

// TestGate_RepoFailures walks every gate operation against a failing repo:
// each must surface the persistence error (never a silent success).
func TestGate_RepoFailures(t *testing.T) {
	s, repo, _ := newTestGate()
	ctx := context.Background()
	doc := mustCreate(t, s, devrel("alice"), "A1")
	thread, err := s.CreateReview(ctx, devrel("alice"), doc.ID, CreateReviewRequest{Subject: "s", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := s.CreateIllustrationRequest(ctx, devrel("alice"), doc.ID, CreateIllustrationRequestPayload{Brief: "b"})
	if err != nil {
		t.Fatal(err)
	}
	repo.fail = true

	ops := []struct {
		name string
		call func() error
	}{
		{"create content", func() error {
			_, err := s.CreateContent(ctx, devrel("alice"), CreateContentRequest{Title: "x"})
			return err
		}},
		{"get content", func() error { _, err := s.GetContent(ctx, devrel("alice"), doc.ID); return err }},
		{"list contents", func() error { _, err := s.ListContents(ctx, devrel("alice")); return err }},
		{"update content", func() error {
			_, err := s.UpdateContent(ctx, devrel("alice"), doc.ID, UpdateContentRequest{})
			return err
		}},
		{"delete content", func() error { return s.DeleteContent(ctx, devrel("alice"), doc.ID) }},
		{"list comments", func() error { _, err := s.ListComments(ctx, devrel("alice"), doc.ID); return err }},
		{"create comment", func() error {
			_, err := s.CreateComment(ctx, devrel("alice"), doc.ID, CreateCommentRequest{Body: "x"})
			return err
		}},
		{"list threads", func() error { _, err := s.ListReviewThreads(ctx, devrel("alice"), doc.ID); return err }},
		{"create thread", func() error {
			_, err := s.CreateReview(ctx, devrel("alice"), doc.ID, CreateReviewRequest{Subject: "s", Body: "b"})
			return err
		}},
		{"reply thread", func() error {
			_, err := s.CreateReview(ctx, devrel("alice"), doc.ID, CreateReviewRequest{ThreadID: thread.ID, Body: "r"})
			return err
		}},
		{"resolve thread", func() error { _, err := s.ResolveReviewThread(ctx, devrel("alice"), doc.ID, thread.ID); return err }},
		{"create illustration", func() error {
			_, err := s.CreateIllustrationRequest(ctx, devrel("alice"), doc.ID, CreateIllustrationRequestPayload{Brief: "b"})
			return err
		}},
		{"list illustrations", func() error { _, err := s.ListIllustrationRequests(ctx, devrel("alice"), "", ""); return err }},
		{"get illustration", func() error { _, err := s.GetIllustrationRequest(ctx, devrel("alice"), req.ID); return err }},
		{"update illustration", func() error {
			inProgress := appcontent.IllustrationInProgress
			_, err := s.UpdateIllustrationRequest(ctx, devrel(appcontent.DefaultAssigneeAgent), req.ID, UpdateIllustrationRequestPayload{Status: &inProgress})
			return err
		}},
	}
	for _, op := range ops {
		if err := op.call(); err == nil {
			t.Fatalf("%s: silently succeeded against a failing repo", op.name)
		}
	}
}

// TestGate_MissingParent covers the not-found passthroughs for child entities.
func TestGate_MissingParent(t *testing.T) {
	s, _, _ := newTestGate()
	ctx := context.Background()
	if _, err := s.ListComments(ctx, devrel("alice"), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("comments on missing content: %v", err)
	}
	if _, err := s.CreateComment(ctx, devrel("alice"), "missing", CreateCommentRequest{Body: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("comment on missing content: %v", err)
	}
	if _, err := s.ListReviewThreads(ctx, devrel("alice"), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("threads on missing content: %v", err)
	}
	if _, err := s.CreateReview(ctx, devrel("alice"), "missing", CreateReviewRequest{Subject: "s", Body: "b"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("thread on missing content: %v", err)
	}
	if _, err := s.CreateIllustrationRequest(ctx, devrel("alice"), "missing", CreateIllustrationRequestPayload{Brief: "b"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("illustration on missing content: %v", err)
	}
	if _, err := s.GetIllustrationRequest(ctx, devrel("alice"), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing illustration: %v", err)
	}
	if _, err := s.UpdateContent(ctx, devrel("alice"), "missing", UpdateContentRequest{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing content: %v", err)
	}
	if err := s.DeleteContent(ctx, devrel("alice"), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing content: %v", err)
	}
	if _, err := s.UpdateIllustrationRequest(ctx, devrel("alice"), "missing", UpdateIllustrationRequestPayload{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing illustration: %v", err)
	}
	if _, err := s.ResolveReviewThread(ctx, devrel("alice"), "c", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolve missing thread: %v", err)
	}
}
