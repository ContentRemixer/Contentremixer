// TypeScript mirror of the Booth content-collaboration JSON contract
// (app/content/types.go, served by booth/internal/server/content.go, W4/ISI-7208).
// The server is authoritative; these shapes track its JSON tags 1:1 so the B3
// reader, inline comments, peer-review threads, and illustration-request flow
// (plan §3.2 B3) consume the API without a translation layer.

export type ContentStatus = "draft" | "in_review" | "approved";

export const contentStatuses: ContentStatus[] = ["draft", "in_review", "approved"];

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
}

export interface UpdateContentRequest {
  title?: string;
  bodyMd?: string;
  status?: ContentStatus;
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
