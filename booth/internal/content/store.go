// Package content is the Booth API's §6.3-gating front door to the
// content-collaboration domain (ISI-7208, plan §3.2/§3.3). It mirrors the CRD
// store's invariant: every read and write evaluates the frozen authz predicate
// and writes an audit record BEFORE touching the backing Postgres
// (app/content). HTTP handlers reach contents, comments, review threads, and
// illustration requests ONLY through this type, so a new route cannot bypass
// the gate.
//
// Tenancy model (frozen §6.3, applied per entity):
//
//   - Content is an owner-scoped class (NOT admin-only): a DevRel works on
//     their own documents, an Admin sees everything — clause 2, the same path
//     scrapejobs and DevRelPersona use. Creation stamps the owner from the
//     session; a caller can never create or re-home someone else's document.
//   - Comments and review threads are visible to anyone authorized for the
//     PARENT document (peer review): read and write gate on the content's
//     owner. Authors are stamped from the session for display, not for authz.
//   - IllustrationRequest extends clause 2 to work items: the row is visible
//     to its owner, to Admins, and to its ASSIGNED AGENT — the subject match
//     on assigned_agent is what backs the assign-to-agent work queue
//     (GET /v1/illustration-requests?assignee=…). Empty owner/assignee can
//     never match a real session subject (the §6.3 empty-subject rule).
package content

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	appcontent "github.com/ContentRemixer/Contentremixer/app/content"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/authz"
)

// ErrForbidden is the §6.3 denial. Handlers map it to HTTP 403.
var ErrForbidden = errors.New("content: forbidden")

// ErrNotFound is the row-missing sentinel, re-exported from the persistence
// layer so handlers depend only on this package. Handlers map it to 404.
var ErrNotFound = appcontent.ErrNotFound

// ErrBadRequest marks caller-input problems (empty body, bad anchor, unknown
// status, illegal transition). Handlers map it to HTTP 400.
var ErrBadRequest = errors.New("content: bad request")

// Resource kinds recorded in audit entries (§6.4).
const (
	kindContent      = "Content"
	kindComment      = "Comment"
	kindThread       = "ReviewThread"
	kindIllustration = "IllustrationRequest"
)

// Verbs recorded in audit entries.
const (
	verbCreate = "create"
	verbGet    = "get"
	verbList   = "list"
	verbUpdate = "update"
	verbDelete = "delete"
)

// Repository is the persistence slice the gate depends on, declared as an
// interface so the gate is unit-testable with a fake (no Postgres).
// *appcontent.Store satisfies it.
type Repository interface {
	InsertContent(ctx context.Context, c appcontent.Content) error
	GetContent(ctx context.Context, id string) (appcontent.Content, error)
	ListContents(ctx context.Context, ownerFilter string) ([]appcontent.Content, error)
	UpdateContent(ctx context.Context, id string, title, bodyMD *string, status *appcontent.ContentStatus) (appcontent.Content, error)
	DeleteContent(ctx context.Context, id string) error

	InsertComment(ctx context.Context, c appcontent.Comment) error
	ListComments(ctx context.Context, contentID string) ([]appcontent.Comment, error)

	InsertThread(ctx context.Context, t appcontent.ReviewThread, first appcontent.ReviewMessage) error
	GetThread(ctx context.Context, id string) (appcontent.ReviewThread, error)
	ListThreads(ctx context.Context, contentID string) ([]appcontent.ReviewThread, error)
	InsertMessage(ctx context.Context, m appcontent.ReviewMessage) error
	SetThreadStatus(ctx context.Context, id string, status appcontent.ThreadStatus) error

	InsertIllustrationRequest(ctx context.Context, r appcontent.IllustrationRequest) error
	GetIllustrationRequest(ctx context.Context, id string) (appcontent.IllustrationRequest, error)
	ListIllustrationRequests(ctx context.Context, f appcontent.RequestFilter) ([]appcontent.IllustrationRequest, error)
	UpdateIllustrationRequest(ctx context.Context, id string, status *appcontent.IllustrationStatus, assignedAgent *string) (appcontent.IllustrationRequest, error)
}

// Store gates the content-collaboration domain behind §6.3 + audit.
type Store struct {
	db      Repository
	auditor audit.Logger
}

// New builds a gating Store over the content persistence and the audit logger.
func New(db Repository, auditor audit.Logger) *Store {
	return &Store{db: db, auditor: auditor}
}

// --- request payloads -------------------------------------------------------

// CreateContentRequest is the POST /v1/content payload.
type CreateContentRequest struct {
	Title  string `json:"title"`
	BodyMD string `json:"bodyMd"`
}

// UpdateContentRequest is the PUT /v1/content/{id} payload. Nil fields keep
// the stored value.
type UpdateContentRequest struct {
	Title  *string                   `json:"title"`
	BodyMD *string                   `json:"bodyMd"`
	Status *appcontent.ContentStatus `json:"status"`
}

// CreateCommentRequest is the POST /v1/content/{id}/comments payload.
type CreateCommentRequest struct {
	Body   string            `json:"body"`
	Anchor appcontent.Anchor `json:"anchor"`
}

// CreateReviewRequest is the POST /v1/content/{id}/reviews payload. An empty
// ThreadID opens a new thread (Subject + Body required); a set ThreadID appends
// Body as a message to that thread.
type CreateReviewRequest struct {
	ThreadID string `json:"threadId"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
}

// CreateIllustrationRequestPayload is the POST /v1/illustration-requests
// payload. ContentID addresses the document (the collection route carries no
// path id). AssignedAgent defaults to the Graphic Designer (plan §3.2 B3).
type CreateIllustrationRequestPayload struct {
	ContentID     string             `json:"contentId"`
	Brief         string             `json:"brief"`
	Anchor        *appcontent.Anchor `json:"anchor"`
	AssignedAgent string             `json:"assignedAgent"`
}

// UpdateIllustrationRequestPayload is the PATCH /v1/illustration-requests/{id}
// payload. Nil fields keep the stored value.
type UpdateIllustrationRequestPayload struct {
	Status        *appcontent.IllustrationStatus `json:"status"`
	AssignedAgent *string                        `json:"assignedAgent"`
}

// --- contents ---------------------------------------------------------------

// CreateContent stores a new working document owned by the CALLER (§6.3: the
// owner is stamped from the session, never taken from the payload).
func (s *Store) CreateContent(ctx context.Context, sess auth.Session, req CreateContentRequest) (appcontent.Content, error) {
	if req.Title == "" {
		return appcontent.Content{}, fmt.Errorf("%w: title is required", ErrBadRequest)
	}
	c := appcontent.Content{
		ID:           newID(),
		OwnerSubject: sess.Subject, // §6.3 clause 2: owner == creator
		Title:        req.Title,
		BodyMD:       req.BodyMD,
		Status:       appcontent.ContentDraft,
	}
	if !s.authorize(sess, c.OwnerSubject) {
		s.audit(ctx, sess, verbCreate, kindContent, c.ID, false)
		return appcontent.Content{}, ErrForbidden
	}
	if err := s.db.InsertContent(ctx, c); err != nil {
		s.audit(ctx, sess, verbCreate, kindContent, c.ID, false)
		return appcontent.Content{}, err
	}
	s.audit(ctx, sess, verbCreate, kindContent, c.ID, true)
	return s.db.GetContent(ctx, c.ID)
}

// GetContent returns one document if §6.3 admits the caller, else
// ErrForbidden. NotFound is safe to reveal (no owner to leak).
func (s *Store) GetContent(ctx context.Context, sess auth.Session, id string) (appcontent.Content, error) {
	c, err := s.db.GetContent(ctx, id)
	if err != nil {
		return appcontent.Content{}, err
	}
	if !s.authorize(sess, c.OwnerSubject) {
		s.audit(ctx, sess, verbGet, kindContent, id, false)
		return appcontent.Content{}, ErrForbidden
	}
	s.audit(ctx, sess, verbGet, kindContent, id, true)
	return c, nil
}

// ListContents returns the documents §6.3 admits: all of them for an Admin,
// only the caller's own for a DevRel (server-side filter, §4).
func (s *Store) ListContents(ctx context.Context, sess auth.Session) ([]appcontent.Content, error) {
	ownerFilter := ""
	if sess.Role != auth.RoleAdmin {
		ownerFilter = sess.Subject
	}
	out, err := s.db.ListContents(ctx, ownerFilter)
	if err != nil {
		s.audit(ctx, sess, verbList, kindContent, "", false)
		return nil, err
	}
	s.audit(ctx, sess, verbList, kindContent, "", true)
	return out, nil
}

// UpdateContent patches a document the caller owns (or is Admin over). The
// owner field is not patchable — tenancy never moves.
func (s *Store) UpdateContent(ctx context.Context, sess auth.Session, id string, req UpdateContentRequest) (appcontent.Content, error) {
	cur, err := s.db.GetContent(ctx, id)
	if err != nil {
		return appcontent.Content{}, err
	}
	if !s.authorize(sess, cur.OwnerSubject) {
		s.audit(ctx, sess, verbUpdate, kindContent, id, false)
		return appcontent.Content{}, ErrForbidden
	}
	if req.Status != nil && !appcontent.ValidContentStatus(*req.Status) {
		return appcontent.Content{}, fmt.Errorf("%w: unknown content status %q", ErrBadRequest, *req.Status)
	}
	out, err := s.db.UpdateContent(ctx, id, req.Title, req.BodyMD, req.Status)
	if err != nil {
		s.audit(ctx, sess, verbUpdate, kindContent, id, false)
		return appcontent.Content{}, err
	}
	s.audit(ctx, sess, verbUpdate, kindContent, id, true)
	return out, nil
}

// DeleteContent removes a document the caller owns (or is Admin over); the
// schema cascades comments, threads, and illustration requests.
func (s *Store) DeleteContent(ctx context.Context, sess auth.Session, id string) error {
	cur, err := s.db.GetContent(ctx, id)
	if err != nil {
		return err
	}
	if !s.authorize(sess, cur.OwnerSubject) {
		s.audit(ctx, sess, verbDelete, kindContent, id, false)
		return ErrForbidden
	}
	if err := s.db.DeleteContent(ctx, id); err != nil {
		s.audit(ctx, sess, verbDelete, kindContent, id, false)
		return err
	}
	s.audit(ctx, sess, verbDelete, kindContent, id, true)
	return nil
}

// --- comments ---------------------------------------------------------------

// ListComments returns a document's inline comments to anyone §6.3 admits to
// the document.
func (s *Store) ListComments(ctx context.Context, sess auth.Session, contentID string) ([]appcontent.Comment, error) {
	if _, err := s.authorizedContent(ctx, sess, contentID, verbList, kindComment); err != nil {
		return nil, err
	}
	return s.db.ListComments(ctx, contentID)
}

// CreateComment adds an anchored inline comment. The author is stamped from
// the session; the anchor must be a sane text range (0 ≤ start ≤ end).
func (s *Store) CreateComment(ctx context.Context, sess auth.Session, contentID string, req CreateCommentRequest) (appcontent.Comment, error) {
	if _, err := s.authorizedContent(ctx, sess, contentID, verbCreate, kindComment); err != nil {
		return appcontent.Comment{}, err
	}
	if req.Body == "" {
		return appcontent.Comment{}, fmt.Errorf("%w: comment body is required", ErrBadRequest)
	}
	if req.Anchor.Start < 0 || req.Anchor.End < req.Anchor.Start {
		return appcontent.Comment{}, fmt.Errorf("%w: anchor must satisfy 0 <= start <= end", ErrBadRequest)
	}
	c := appcontent.Comment{
		ID:            newID(),
		ContentID:     contentID,
		AuthorSubject: sess.Subject,
		Body:          req.Body,
		Anchor:        req.Anchor,
	}
	if err := s.db.InsertComment(ctx, c); err != nil {
		s.audit(ctx, sess, verbCreate, kindComment, c.ID, false)
		return appcontent.Comment{}, err
	}
	s.audit(ctx, sess, verbCreate, kindComment, c.ID, true)
	return c, nil
}

// --- review threads -----------------------------------------------------------

// ListReviewThreads returns a document's peer-review threads (with messages)
// to anyone §6.3 admits to the document.
func (s *Store) ListReviewThreads(ctx context.Context, sess auth.Session, contentID string) ([]appcontent.ReviewThread, error) {
	if _, err := s.authorizedContent(ctx, sess, contentID, verbList, kindThread); err != nil {
		return nil, err
	}
	return s.db.ListThreads(ctx, contentID)
}

// CreateReview opens a new thread on the document, or — when req.ThreadID is
// set — appends req.Body as a message to that thread. Both paths gate on the
// parent document's §6.3 owner; authors are stamped from the session.
func (s *Store) CreateReview(ctx context.Context, sess auth.Session, contentID string, req CreateReviewRequest) (appcontent.ReviewThread, error) {
	if _, err := s.authorizedContent(ctx, sess, contentID, verbCreate, kindThread); err != nil {
		return appcontent.ReviewThread{}, err
	}
	if req.Body == "" {
		return appcontent.ReviewThread{}, fmt.Errorf("%w: review body is required", ErrBadRequest)
	}
	if req.ThreadID == "" {
		if req.Subject == "" {
			return appcontent.ReviewThread{}, fmt.Errorf("%w: thread subject is required", ErrBadRequest)
		}
		t := appcontent.ReviewThread{
			ID:        newID(),
			ContentID: contentID,
			Subject:   req.Subject,
			Status:    appcontent.ThreadOpen,
			CreatedBy: sess.Subject,
		}
		first := appcontent.ReviewMessage{
			ID:            newID(),
			ThreadID:      t.ID,
			AuthorSubject: sess.Subject,
			Body:          req.Body,
		}
		if err := s.db.InsertThread(ctx, t, first); err != nil {
			s.audit(ctx, sess, verbCreate, kindThread, t.ID, false)
			return appcontent.ReviewThread{}, err
		}
		s.audit(ctx, sess, verbCreate, kindThread, t.ID, true)
		return s.db.GetThread(ctx, t.ID)
	}
	// Reply path: the thread must belong to the addressed document.
	t, err := s.db.GetThread(ctx, req.ThreadID)
	if err != nil {
		return appcontent.ReviewThread{}, err
	}
	if t.ContentID != contentID {
		return appcontent.ReviewThread{}, fmt.Errorf("%w: thread %s does not belong to content %s", ErrBadRequest, req.ThreadID, contentID)
	}
	m := appcontent.ReviewMessage{
		ID:            newID(),
		ThreadID:      t.ID,
		AuthorSubject: sess.Subject,
		Body:          req.Body,
	}
	if err := s.db.InsertMessage(ctx, m); err != nil {
		s.audit(ctx, sess, verbCreate, kindThread, t.ID, false)
		return appcontent.ReviewThread{}, err
	}
	s.audit(ctx, sess, verbCreate, kindThread, t.ID, true)
	return s.db.GetThread(ctx, t.ID)
}

// ResolveReviewThread marks a thread resolved. The thread must belong to the
// document; the caller must be §6.3-authorized for the document.
func (s *Store) ResolveReviewThread(ctx context.Context, sess auth.Session, contentID, threadID string) (appcontent.ReviewThread, error) {
	if _, err := s.authorizedContent(ctx, sess, contentID, verbUpdate, kindThread); err != nil {
		return appcontent.ReviewThread{}, err
	}
	t, err := s.db.GetThread(ctx, threadID)
	if err != nil {
		return appcontent.ReviewThread{}, err
	}
	if t.ContentID != contentID {
		return appcontent.ReviewThread{}, fmt.Errorf("%w: thread %s does not belong to content %s", ErrBadRequest, threadID, contentID)
	}
	if err := s.db.SetThreadStatus(ctx, threadID, appcontent.ThreadResolved); err != nil {
		s.audit(ctx, sess, verbUpdate, kindThread, threadID, false)
		return appcontent.ReviewThread{}, err
	}
	s.audit(ctx, sess, verbUpdate, kindThread, threadID, true)
	return s.db.GetThread(ctx, threadID)
}

// --- illustration requests ------------------------------------------------------

// CreateIllustrationRequest files a new illustration request on a document the
// caller is §6.3-authorized for. The requester is stamped as owner; the
// assigned agent defaults to the Graphic Designer (plan §3.2 B3).
func (s *Store) CreateIllustrationRequest(ctx context.Context, sess auth.Session, contentID string, req CreateIllustrationRequestPayload) (appcontent.IllustrationRequest, error) {
	if _, err := s.authorizedContent(ctx, sess, contentID, verbCreate, kindIllustration); err != nil {
		return appcontent.IllustrationRequest{}, err
	}
	if req.Brief == "" {
		return appcontent.IllustrationRequest{}, fmt.Errorf("%w: brief is required", ErrBadRequest)
	}
	if req.Anchor != nil && (req.Anchor.Start < 0 || req.Anchor.End < req.Anchor.Start) {
		return appcontent.IllustrationRequest{}, fmt.Errorf("%w: anchor must satisfy 0 <= start <= end", ErrBadRequest)
	}
	assignee := req.AssignedAgent
	if assignee == "" {
		assignee = appcontent.DefaultAssigneeAgent
	}
	r := appcontent.IllustrationRequest{
		ID:            newID(),
		ContentID:     contentID,
		OwnerSubject:  sess.Subject,
		AssignedAgent: assignee,
		Status:        appcontent.IllustrationRequested,
		Brief:         req.Brief,
		Anchor:        req.Anchor,
	}
	if err := s.db.InsertIllustrationRequest(ctx, r); err != nil {
		s.audit(ctx, sess, verbCreate, kindIllustration, r.ID, false)
		return appcontent.IllustrationRequest{}, err
	}
	s.audit(ctx, sess, verbCreate, kindIllustration, r.ID, true)
	return s.db.GetIllustrationRequest(ctx, r.ID)
}

// ListIllustrationRequests is the work-queue read (GET /v1/illustration-
// requests). §6.3 visibility: an Admin sees every row; anyone else sees rows
// they own OR are assigned to. assigneeFilter/statusFilter narrow within that
// visible set (the agent work queue is assigneeFilter == the agent's subject).
func (s *Store) ListIllustrationRequests(ctx context.Context, sess auth.Session, assigneeFilter string, statusFilter appcontent.IllustrationStatus) ([]appcontent.IllustrationRequest, error) {
	if statusFilter != "" && !appcontent.ValidIllustrationStatus(statusFilter) {
		return nil, fmt.Errorf("%w: unknown illustration status %q", ErrBadRequest, statusFilter)
	}
	f := appcontent.RequestFilter{Assignee: assigneeFilter, Status: statusFilter}
	if sess.Role != auth.RoleAdmin {
		f.VisibleTo = sess.Subject
	}
	out, err := s.db.ListIllustrationRequests(ctx, f)
	if err != nil {
		s.audit(ctx, sess, verbList, kindIllustration, "", false)
		return nil, err
	}
	s.audit(ctx, sess, verbList, kindIllustration, "", true)
	return out, nil
}

// GetIllustrationRequest returns one request to its owner, its assigned agent,
// or an Admin (§6.3 clause-2 extension for work items).
func (s *Store) GetIllustrationRequest(ctx context.Context, sess auth.Session, id string) (appcontent.IllustrationRequest, error) {
	r, err := s.db.GetIllustrationRequest(ctx, id)
	if err != nil {
		return appcontent.IllustrationRequest{}, err
	}
	if !s.authorizeWorkItem(sess, r) {
		s.audit(ctx, sess, verbGet, kindIllustration, id, false)
		return appcontent.IllustrationRequest{}, ErrForbidden
	}
	s.audit(ctx, sess, verbGet, kindIllustration, id, true)
	return r, nil
}

// UpdateIllustrationRequest applies status transitions and reassignments.
// Transition rights (who may move a request where):
//
//	requested  → in_progress : the assigned agent (or Admin)
//	requested  → delivered   : the assigned agent (or Admin) — small items may
//	                           be delivered without an explicit start
//	in_progress→ delivered   : the assigned agent (or Admin)
//	delivered  → accepted    : the owner (or Admin)
//	any        → cancelled   : the owner (or Admin)
//
// Reassignment (changing assigned_agent) is owner/Admin-only — an agent cannot
// re-route its own queue. Everything else is a 403.
func (s *Store) UpdateIllustrationRequest(ctx context.Context, sess auth.Session, id string, req UpdateIllustrationRequestPayload) (appcontent.IllustrationRequest, error) {
	r, err := s.db.GetIllustrationRequest(ctx, id)
	if err != nil {
		return appcontent.IllustrationRequest{}, err
	}
	isOwner := sess.Subject != "" && sess.Subject == r.OwnerSubject
	isAssignee := sess.Subject != "" && sess.Subject == r.AssignedAgent
	isAdmin := sess.Role == auth.RoleAdmin
	if !isOwner && !isAssignee && !isAdmin {
		s.audit(ctx, sess, verbUpdate, kindIllustration, id, false)
		return appcontent.IllustrationRequest{}, ErrForbidden
	}
	if req.Status != nil && !appcontent.ValidIllustrationStatus(*req.Status) {
		return appcontent.IllustrationRequest{}, fmt.Errorf("%w: unknown illustration status %q", ErrBadRequest, *req.Status)
	}
	if req.AssignedAgent != nil && *req.AssignedAgent != r.AssignedAgent && !isOwner && !isAdmin {
		s.audit(ctx, sess, verbUpdate, kindIllustration, id, false)
		return appcontent.IllustrationRequest{}, ErrForbidden
	}
	if req.Status != nil && !allowedTransition(isAdmin, isOwner, isAssignee, r.Status, *req.Status) {
		s.audit(ctx, sess, verbUpdate, kindIllustration, id, false)
		return appcontent.IllustrationRequest{}, fmt.Errorf("%w: cannot move illustration request from %s to %s", ErrBadRequest, r.Status, *req.Status)
	}
	out, err := s.db.UpdateIllustrationRequest(ctx, id, req.Status, req.AssignedAgent)
	if err != nil {
		s.audit(ctx, sess, verbUpdate, kindIllustration, id, false)
		return appcontent.IllustrationRequest{}, err
	}
	s.audit(ctx, sess, verbUpdate, kindIllustration, id, true)
	return out, nil
}

// allowedTransition encodes the UpdateIllustrationRequest rights table.
func allowedTransition(isAdmin, isOwner, isAssignee bool, from, to appcontent.IllustrationStatus) bool {
	if isAdmin {
		return true // clause 1: Admins may move a request anywhere legal-by-enum
	}
	switch to {
	case appcontent.IllustrationInProgress:
		return isAssignee && from == appcontent.IllustrationRequested
	case appcontent.IllustrationDelivered:
		return isAssignee && (from == appcontent.IllustrationRequested || from == appcontent.IllustrationInProgress)
	case appcontent.IllustrationAccepted:
		return isOwner && from == appcontent.IllustrationDelivered
	case appcontent.IllustrationCancelled:
		return isOwner
	default: // no path back to "requested" for non-admins
		return false
	}
}

// --- shared gate helpers -------------------------------------------------------

// authorizedContent loads a document and returns it when §6.3 admits the
// caller; otherwise it records the deny and returns ErrForbidden.
func (s *Store) authorizedContent(ctx context.Context, sess auth.Session, contentID, verb, kind string) (appcontent.Content, error) {
	c, err := s.db.GetContent(ctx, contentID)
	if err != nil {
		return appcontent.Content{}, err
	}
	if !s.authorize(sess, c.OwnerSubject) {
		s.audit(ctx, sess, verb, kind, contentID, false)
		return appcontent.Content{}, ErrForbidden
	}
	s.audit(ctx, sess, verb, kind, contentID, true)
	return c, nil
}

// authorize is the frozen §6.3 predicate over an owner-scoped class.
func (s *Store) authorize(sess auth.Session, ownerSubject string) bool {
	return authz.Authorize(string(sess.Role), sess.Subject, false, ownerSubject)
}

// authorizeWorkItem is §6.3 for illustration requests: clause 1 (Admin), or
// clause 2 on EITHER tenancy key — the owner (requester) or the assigned agent
// (the work item's worker). Empty subjects never match, per §6.3.
func (s *Store) authorizeWorkItem(sess auth.Session, r appcontent.IllustrationRequest) bool {
	if sess.Role == auth.RoleAdmin {
		return true
	}
	return sess.Subject != "" && (sess.Subject == r.OwnerSubject || sess.Subject == r.AssignedAgent)
}

// audit records one §6.4 entry.
func (s *Store) audit(ctx context.Context, sess auth.Session, verb, kind, name string, allowed bool) {
	decision := audit.DecisionDeny
	if allowed {
		decision = audit.DecisionAllow
	}
	s.auditor.Log(ctx, audit.Entry{
		Subject:      sess.Subject,
		Role:         string(sess.Role),
		Verb:         verb,
		ResourceKind: kind,
		ResourceName: name,
		Decision:     decision,
	})
}

// newID returns a random hex id, the same discipline as scrapejobs.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("content: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
