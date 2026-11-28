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
// DB holds working/draft state). The redesign (ISI-7968 / ISI-7887 Part 2)
// makes /content the hub for recycled + manual content, so the lifecycle runs
// one step further than the original B3 model: "approved" hands a draft off to
// the GitHub PR path (C3), and "published" is the terminal state C3 back-fills
// once the PR is open/merged.
type ContentStatus string

const (
	// ContentDraft — working document, not yet shared for peer review.
	ContentDraft ContentStatus = "draft"
	// ContentInReview — shared; comments and review threads expected.
	ContentInReview ContentStatus = "in_review"
	// ContentApproved — review passed; ready for the GitHub PR path (C3).
	ContentApproved ContentStatus = "approved"
	// ContentPublished — C3 opened/merged the PR; terminal working state.
	ContentPublished ContentStatus = "published"
)

// ValidContentStatus reports whether s is a known content status.
func ValidContentStatus(s ContentStatus) bool {
	switch s {
	case ContentDraft, ContentInReview, ContentApproved, ContentPublished:
		return true
	}
	return false
}

// IsPRHandoff reports whether s is a state that hands a draft to the GitHub PR
// path (C3): "approved" enters it, "published" is its terminal state. The
// PR-eligibility invariant — a draft needs a publish target before it may reach
// these states (ISI-7971) — is enforced at the write seam
// (booth/internal/content) and is source-agnostic: manual and recycled drafts
// obey the same rule, which is exactly what lets a manual draft ride the same
// review → approve → PR path as a recycled one.
func IsPRHandoff(s ContentStatus) bool {
	return s == ContentApproved || s == ContentPublished
}

// ContentSource records how a draft entered /content (ISI-7968). It is the
// discriminator the ingest seam stamps and is immutable after creation.
type ContentSource string

const (
	// SourceManual — authored in the web UI (the original B3 path). This is the
	// back-compat default: every row that predates ISI-7968 is a manual draft.
	SourceManual ContentSource = "manual"
	// SourceRecycle — deposited by the recycle flow (C2) from a GitHub issue.
	SourceRecycle ContentSource = "recycle"
)

// ValidContentSource reports whether s is a known content source.
func ValidContentSource(s ContentSource) bool {
	switch s {
	case SourceManual, SourceRecycle:
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

// DefaultAssigneeAgent is the agent persona a new agent request is assigned to
// when the caller does not name one (plan §3.2 B3: "request illustration"
// assigns the Graphic Designer; a kind=design request keeps that default).
const DefaultAssigneeAgent = "Graphic Designer"

// AgentRequestKind types an anchored work request by the agent discipline it
// routes to (ISI-7820 / ISI-7780 plan §6 P3). It is the generalization of the
// original illustration-only request: kind=design IS today's illustration flow,
// and the other kinds route the same anchored-work machinery to other agents.
type AgentRequestKind string

const (
	// KindDesign — an illustration/graphics request (the original flow;
	// defaults to the Graphic Designer). Back-compat: a request with no kind
	// is treated as design.
	KindDesign AgentRequestKind = "design"
	// KindContent — a writing/content request (e.g. the DevRel Writer).
	KindContent AgentRequestKind = "content"
	// KindReview — a review request (e.g. the Code Reviewer).
	KindReview AgentRequestKind = "review"
	// KindGeneric — an untyped request routed purely by assigned agent.
	KindGeneric AgentRequestKind = "generic"
)

// ValidAgentRequestKind reports whether k is a known request kind.
func ValidAgentRequestKind(k AgentRequestKind) bool {
	switch k {
	case KindDesign, KindContent, KindReview, KindGeneric:
		return true
	}
	return false
}

// Content is one working draft in /content — the hub for recycled + manual
// content (ISI-7968). OwnerSubject is the tenancy key the Booth gate evaluates
// §6.3 against. The fields after Status are the ISI-7968 additions; they are
// optional and zero-valued for pre-redesign (manual) rows, which is how the
// model stays back-compatible with the original B3 web-UI documents.
//
// Content is the ContentDraft entity the ISI-7968 redesign refers to: there is
// one table (booth.contents) and one reader model for recycled + manual drafts,
// not a parallel ContentDraft table. Downstream workstreams (C2/C3/C5) deposit
// and read it through the ingest seam (booth/internal/content.Deposit).
type Content struct {
	ID           string        `json:"id"`
	OwnerSubject string        `json:"owner"`
	Title        string        `json:"title"`
	BodyMD       string        `json:"bodyMd"`
	Status       ContentStatus `json:"status"`

	// Source is how the draft entered /content; stamped by the ingest seam and
	// immutable thereafter. Empty is read back as SourceManual (back-compat).
	Source ContentSource `json:"source"`
	// Origin is where a recycled draft came from (nil for manual drafts); set
	// at ingest by C2, immutable thereafter.
	Origin *Origin `json:"origin,omitempty"`
	// TargetRepo / TargetPath name where the draft is destined to be published
	// (the file C3's PR writes). Empty until known.
	TargetRepo string `json:"targetRepo,omitempty"`
	TargetPath string `json:"targetPath,omitempty"`
	// ScrubFindings are secret/PII-scrub annotations populated by C2. C1 owns
	// only the column; the finding shape is intentionally minimal and additive.
	ScrubFindings []ScrubFinding `json:"scrubFindings,omitempty"`
	// PRURL is the publishing PR, back-filled by C3 when the draft is shipped.
	PRURL string `json:"prUrl,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Origin locates the GitHub issue a recycled draft was generated from (C2).
type Origin struct {
	Repo  string `json:"repo"`            // "owner/name"
	Issue int    `json:"issue,omitempty"` // issue number, 0 if not issue-scoped
}

// ScrubFinding is one secret/PII-scrub annotation on a draft body. C2 owns the
// detection and populates these; C1 defines a minimal, forward-compatible shape
// so the column exists and round-trips. Anchor pins the finding to BodyMD when
// the finding is span-local (nil for document-level findings).
type ScrubFinding struct {
	Rule     string  `json:"rule"`               // detector id, e.g. "aws-access-key"
	Severity string  `json:"severity,omitempty"` // C2-defined, e.g. "block"/"warn"
	Message  string  `json:"message,omitempty"`  // human-readable detail
	Anchor   *Anchor `json:"anchor,omitempty"`   // span in BodyMD, if local
}

// ContentPatch is the set of mutable draft columns UpdateContent can set. A nil
// field keeps the stored value, so one struct serves both the interactive PUT
// (title/body/status) and the internal back-fill seams (target, scrubFindings,
// prUrl, status) C2/C3 call. Source and Origin are deliberately absent: they
// are stamped once at ingest and never patched.
type ContentPatch struct {
	Title         *string
	BodyMD        *string
	Status        *ContentStatus
	TargetRepo    *string
	TargetPath    *string
	ScrubFindings []ScrubFinding // non-nil replaces the stored slice
	PRURL         *string
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

// ReviewMessage is one reply inside a peer-review thread. IsAgent marks a
// message authored by an agent — in particular the deliverable an assigned
// agent posts back into the thread when it completes an AgentRequest (ISI-7820
// result callback). Its inline rendering is ISI-7821 (P4); P3 only persists it.
type ReviewMessage struct {
	ID            string    `json:"id"`
	ThreadID      string    `json:"threadId"`
	AuthorSubject string    `json:"author"`
	Body          string    `json:"body"`
	IsAgent       bool      `json:"isAgent"`
	CreatedAt     time.Time `json:"createdAt"`
}

// AgentRequest is one anchored, tracked work request routed to an agent's
// work-queue (ISI-7820; the generalization of the original illustration
// request, plan §3.2 B3 + §6 P3). Kind types the discipline (design/content/
// review/generic); kind=design reproduces the original illustration behavior.
// OwnerSubject is the requester; AssignedAgent is the agent persona working the
// item — the agent work queue (GET /v1/agent-requests?assignee=…) lists by it,
// and the gate treats the assignee as a §6.3 clause-2 owner of the work item.
// ThreadID, when set, links the request to the peer-review thread the mention
// was made in, so the agent's deliverable posts back into that same thread.
type AgentRequest struct {
	ID            string             `json:"id"`
	ContentID     string             `json:"contentId"`
	OwnerSubject  string             `json:"owner"`
	AssignedAgent string             `json:"assignedAgent"`
	Kind          AgentRequestKind   `json:"kind"`
	Status        IllustrationStatus `json:"status"`
	Brief         string             `json:"brief"`
	Anchor        *Anchor            `json:"anchor,omitempty"`
	ThreadID      string             `json:"threadId,omitempty"`
	CreatedAt     time.Time          `json:"createdAt"`
	UpdatedAt     time.Time          `json:"updatedAt"`
}

// IllustrationRequest is the back-compat name for AgentRequest: the original
// illustration flow is an AgentRequest with Kind=design. Existing callers,
// tests, and the FE type keep compiling against this alias.
type IllustrationRequest = AgentRequest
