import { cx, CommentComposer, CommentThread, Button } from "../../ui";
import { timeAgo } from "./time";
import type { Comment, Anchor } from "./types";

// Comments rail for the B3 reader: the list of anchored inline comments plus,
// when the reader has a live selection, a composer to file a new one against it.
// Comments are standalone anchored notes (no replies — peer-review threads
// cover discussion); each can be resolved/reopened individually (ISI-7821 P4).

export interface CommentsRailProps {
  comments: Comment[];
  /** Pending selection the composer will anchor a new comment to. */
  pendingAnchor: Anchor | null;
  activeCommentId: string | null;
  busy: boolean;
  /** Comment currently awaiting a resolve/reopen round-trip. */
  busyCommentId: string | null;
  onFocus: (commentId: string) => void;
  onSubmit: (body: string) => void;
  onCancelCompose: () => void;
  /** Toggle a comment's resolved flag (ISI-7821 P4). */
  onResolve: (commentId: string, resolved: boolean) => void;
}

export function CommentsRail({
  comments,
  pendingAnchor,
  activeCommentId,
  busy,
  busyCommentId,
  onFocus,
  onSubmit,
  onCancelCompose,
  onResolve,
}: CommentsRailProps) {
  return (
    <div className="flex flex-col gap-3">
      {pendingAnchor ? (
        <div className="rounded-lg border border-accent/40 bg-surface-raised p-3 shadow-card">
          {pendingAnchor.quote ? (
            <p className="mb-2 line-clamp-2 border-l-2 border-accent/50 pl-2 text-xs italic text-text-muted">
              “{pendingAnchor.quote}”
            </p>
          ) : null}
          <CommentComposer
            autoFocus
            submitLabel="Comment"
            busy={busy}
            onSubmit={onSubmit}
            onCancel={onCancelCompose}
          />
        </div>
      ) : null}

      {comments.length === 0 && !pendingAnchor ? (
        <p className="rounded-lg border border-dashed border-border px-4 py-8 text-center text-sm text-text-muted">
          No comments yet. Select text in the document to add one.
        </p>
      ) : null}

      {comments.map((c) => (
        <div
          key={c.id}
          className={cx(
            "rounded-lg transition-shadow",
            activeCommentId === c.id && "ring-2 ring-accent/50",
          )}
        >
          <CommentThread
            quote={c.anchor.quote}
            resolved={c.resolved}
            busy={busyCommentId === c.id}
            className="w-full"
            comments={[
              {
                id: c.id,
                author: { name: c.author, isAgent: false },
                body: c.body,
                timeLabel: timeAgo(c.createdAt),
              },
            ]}
            onResolve={c.resolved ? undefined : () => onResolve(c.id, true)}
            onReopen={c.resolved ? () => onResolve(c.id, false) : undefined}
          />
          <div className="mt-1 px-1">
            <Button variant="ghost" size="sm" onClick={() => onFocus(c.id)}>
              Jump to text
            </Button>
          </div>
        </div>
      ))}
    </div>
  );
}
