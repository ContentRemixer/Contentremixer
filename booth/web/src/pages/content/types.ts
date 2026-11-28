// TypeScript mirror of the Booth content-collaboration JSON contract
// (app/content/types.go, served by booth/internal/server/content.go, W4/ISI-7208).
// The server is authoritative; these shapes track its JSON tags 1:1 so the B3
// reader, inline comments, peer-review threads, and illustration-request flow
// (plan §3.2 B3) consume the API without a translation layer.

export type ContentStatus = "draft" | "in_review" | "approved" | "published";

// Statuses a user can SELECT in the UI. "published" is a first-class,
// API-returned status (set by the PR path, C3) but is never chosen
// interactively, so it is intentionally excluded from the dropdown array while
// remaining a valid, displayable ContentStatus (ISI-7971 review F1).
export const contentStatuses: ContentStatus[] = ["draft", "in_review", "approved"];

/**
 * ContentSource records how a draft entered /content (ISI-7968): "manual" is
 * authored in the web UI, "recycle" is deposited from a GitHub issue. Absent on
 * pre-redesign rows, which the server reads back as "manual".
 */
export type ContentSource = "manual" | "recycle";

export type ThreadStatus = "open" | "resolved";

export type IllustrationStatus =
  | "requested"
  | "in_progress"
  | "delivered"
  | "accepted"
  | "cancelled";

/**
 * AgentRequestKind types an anchored work request by discipline (ISI-7820):
 * "design" is the original illustration flow; the others route the same tracked
 * work to other agents. Omitting kind on create defaults to "design".
 */
export type AgentRequestKind = "design" | "content" | "review" | "generic";

/** The agent persona a new illustration request defaults to (plan §3.2 B3). */
export const DEFAULT_ILLUSTRATION_ASSIGNEE = "Graphic Designer";

/**
 * Anchor pins a comment (or illustration request) to a text range of BodyMD:
 * inclusive start / exclusive end character offsets into the raw markdown, plus
 * the quoted span so the anchor still renders after minor edits.
 */
export interface Anchor {
  start: number;
  end: number;
  quote?: string;
}

export interface Content {
  id: string;
  owner: string;
  title: string;
  bodyMd: string;
  status: ContentStatus;
  /** How the draft entered /content (ISI-7968). */
  source: ContentSource;
  /** Where the draft is destined to be published — the file C3's PR writes.
   * A draft is PR-eligible (approvable) only once BOTH are set (ISI-7971);
   * empty until a target is bound. */
  targetRepo?: string;
  targetPath?: string;
  createdAt: string;
  updatedAt: string;
}

export interface Comment {
  id: string;
  contentId: string;
  author: string;
  body: string;
  anchor: Anchor;
  resolved: boolean;
  createdAt: string;
}

export interface ReviewMessage {
  id: string;
  threadId: string;
  author: string;
  body: string;
  /** True for an agent-authored message, e.g. a delivered AgentRequest result
   * posted back into the thread (ISI-7820; rendered by ISI-7821/P4). */
  isAgent: boolean;
  createdAt: string;
}

export interface ReviewThread {
  id: string;
  contentId: string;
  subject: string;
  status: ThreadStatus;
  createdBy: string;
  messages: ReviewMessage[];
  createdAt: string;
  updatedAt: string;
}

/**
 * AgentRequest is the generalized anchored work request (ISI-7820). The name
 * IllustrationRequest is kept as an alias: a kind=design request IS the
 * original illustration flow.
 */
export interface AgentRequest {
  id: string;
  contentId: string;
  owner: string;
  assignedAgent: string;
  kind: AgentRequestKind;
  status: IllustrationStatus;
  brief: string;
  anchor?: Anchor;
  /** Set when the request was raised from a mention in a review thread; the
   * agent's deliverable posts back into this thread. */
  threadId?: string;
  createdAt: string;
  updatedAt: string;
}

export type IllustrationRequest = AgentRequest;

// --- request payloads -------------------------------------------------------

export interface CreateContentRequest {
  title: string;
  bodyMd: string;
  /** Optional at authoring time (ISI-7971): bind the publish target now, or
   * leave empty and bind it later via update before approving. */
  targetRepo?: string;
  targetPath?: string;
}

export interface UpdateContentRequest {
  title?: string;
  bodyMd?: string;
  status?: ContentStatus;
  /** Bind/rebind the publish target. A draft cannot be set to "approved"
   * unless both are non-empty (server-enforced, ISI-7971). */
  targetRepo?: string;
  targetPath?: string;
}

export interface CreateCommentRequest {
  body: string;
  anchor: Anchor;
}

export interface CreateReviewRequest {
  /** Empty opens a new thread (subject + body); set appends a message. */
  threadId?: string;
  subject?: string;
  body: string;
}

export interface CreateIllustrationRequest {
  contentId: string;
  brief: string;
  anchor?: Anchor;
  /** Defaults server-side to the Graphic Designer when omitted. */
  assignedAgent?: string;
  /** Defaults server-side to "design" when omitted (ISI-7820). */
  kind?: AgentRequestKind;
  /** Link to the review thread the mention was made in, so the deliverable
   * posts back into it. Send this only when a comment carries an explicit
   * "Request …" action; a bare @mention notifies without a request. */
  threadId?: string;
}

/** Alias: CreateIllustrationRequest is a create of any AgentRequest kind. */
export type CreateAgentRequest = CreateIllustrationRequest;

export interface UpdateIllustrationRequest {
  status?: IllustrationStatus;
  assignedAgent?: string;
  /** The agent's result, posted into the linked thread as an isAgent message.
   * Only valid alongside status:"delivered" on a request that has a threadId. */
  deliverable?: string;
}

export type UpdateAgentRequest = UpdateIllustrationRequest;
