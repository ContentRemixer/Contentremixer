// Package content is the Phase-4 content-collaboration domain (ISI-7208, plan
// §3.2/§3.3): working/draft Content documents, anchored inline comments,
// peer-review threads, and IllustrationRequests with their assign-to-agent work
// queue. Published artifacts live in GitHub via the existing PR path (ISI-7085);
// this package holds only the DB working state and the collaboration data
// around it (board decision §0.3: DB = content + collaboration, GitHub =
// published artifacts).
//
// It is a PUBLIC app package for the same reason app/review is: the Booth
// module (a separate Go module over `replace`) reaches these types and this
// store through this contract. This package performs NO authorization — the
// frozen §6.3 predicate is applied by the Booth gating store that wraps it
// (booth/internal/content), the same split the CRD store and app/review use.
package content

import "time"

// ContentStatus is the working lifecycle of a content document (plan §3.3: the
// DB holds working/draft state). "approved" hands a document off to the GitHub
// PR publishing path (ISI-7085) — publishing itself is out of scope here.
type ContentStatus string

const (
	// ContentDraft — working document, not yet shared for peer review.
	ContentDraft ContentStatus = "draft"
	// ContentInReview — shared; comments and review threads expected.
	ContentInReview ContentStatus = "in_review"
	// ContentApproved — review passed; ready for the GitHub PR path.
	ContentApproved ContentStatus = "approved"
)

// ValidContentStatus reports whether s is a known content status.
func ValidContentStatus(s ContentStatus) bool {
	switch s {
	case ContentDraft, ContentInReview, ContentApproved:
		return true
	}
	return false
}

// ThreadStatus is the lifecycle of a peer-review thread.
type ThreadStatus string

const (
	// ThreadOpen — the thread is still an active discussion.
	ThreadOpen ThreadStatus = "open"
	// ThreadResolved — the discussion concluded.
	ThreadResolved ThreadStatus = "resolved"
)

// ValidThreadStatus reports whether s is a known thread status.
func ValidThreadStatus(s ThreadStatus) bool {
	switch s {
	case ThreadOpen, ThreadResolved:
		return true
	}
	return false
}

// IllustrationStatus is the lifecycle of an illustration request (plan §3.2
// B3): requested by a DevRel, worked by the assigned agent, accepted back by
// the requester.
type IllustrationStatus string

const (
	// IllustrationRequested — queued for the assigned agent.
	IllustrationRequested IllustrationStatus = "requested"
	// IllustrationInProgress — the assigned agent is working the item.
	IllustrationInProgress IllustrationStatus = "in_progress"
	// IllustrationDelivered — the agent delivered; awaiting the owner's call.
	IllustrationDelivered IllustrationStatus = "delivered"
	// IllustrationAccepted — the owner accepted the delivery.
	IllustrationAccepted IllustrationStatus = "accepted"
	// IllustrationCancelled — the owner cancelled the request.
	IllustrationCancelled IllustrationStatus = "cancelled"
)

// ValidIllustrationStatus reports whether s is a known illustration status.
func ValidIllustrationStatus(s IllustrationStatus) bool {
	switch s {
	case IllustrationRequested, IllustrationInProgress, IllustrationDelivered,
		IllustrationAccepted, IllustrationCancelled:
		return true
	}
	return false
}

// DefaultAssigneeAgent is the agent persona a new illustration request is
// assigned to when the caller does not name one (plan §3.2 B3: "request
// illustration" assigns the Graphic Designer).
const DefaultAssigneeAgent = "Graphic Designer"

// Content is one working document (the B3 reader model). OwnerSubject is the
// tenancy key the Booth gate evaluates §6.3 against.
type Content struct {
	ID           string        `json:"id"`
	OwnerSubject string        `json:"owner"`
	Title        string        `json:"title"`
	BodyMD       string        `json:"bodyMd"`
	Status       ContentStatus `json:"status"`
	CreatedAt    time.Time     `json:"createdAt"`
	UpdatedAt    time.Time     `json:"updatedAt"`
}

// Anchor pins a comment (or illustration request) to a text range of the
// document body: inclusive start / exclusive end character offsets into BodyMD
// plus the quoted span, so the anchor still renders meaningfully after minor
// edits (Google-Docs-style inline comments, plan §3.2 B3).
type Anchor struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Quote string `json:"quote,omitempty"`
}

// Comment is one anchored inline comment. AuthorSubject is who wrote it; the
// §6.3 gate reads the PARENT content's owner for visibility (peer review:
// anyone who can read the document can read and add comments).
type Comment struct {
	ID            string    `json:"id"`
	ContentID     string    `json:"contentId"`
	AuthorSubject string    `json:"author"`
	Body          string    `json:"body"`
	Anchor        Anchor    `json:"anchor"`
	Resolved      bool      `json:"resolved"`
	CreatedAt     time.Time `json:"createdAt"`
}

// ReviewThread is one peer-review thread on a document. Messages carry the
// conversation; the gate returns threads with their messages attached.
type ReviewThread struct {
	ID        string          `json:"id"`
	ContentID string          `json:"contentId"`
	Subject   string          `json:"subject"`
	Status    ThreadStatus    `json:"status"`
	CreatedBy string          `json:"createdBy"`
	Messages  []ReviewMessage `json:"messages"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// ReviewMessage is one reply inside a peer-review thread.
type ReviewMessage struct {
	ID            string    `json:"id"`
	ThreadID      string    `json:"threadId"`
	AuthorSubject string    `json:"author"`
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"createdAt"`
}

// IllustrationRequest is one "request illustration" work item (plan §3.2 B3).
// OwnerSubject is the requester; AssignedAgent is the agent persona working the
// item — the agent work queue (GET /v1/illustration-requests?assignee=…) lists
// by it, and the gate treats the assignee as a §6.3 clause-2 owner of the work
// item itself.
type IllustrationRequest struct {
	ID            string             `json:"id"`
	ContentID     string             `json:"contentId"`
	OwnerSubject  string             `json:"owner"`
	AssignedAgent string             `json:"assignedAgent"`
	Status        IllustrationStatus `json:"status"`
	Brief         string             `json:"brief"`
	Anchor        *Anchor            `json:"anchor,omitempty"`
	CreatedAt     time.Time          `json:"createdAt"`
	UpdatedAt     time.Time          `json:"updatedAt"`
}
