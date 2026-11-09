import type { ReactNode } from "react";
import { cx } from "./cx";
import { Avatar } from "./Avatar";
import { Badge } from "./Badge";
import type { BadgeTone } from "./Badge";
import { Button } from "./Button";
import { CommentComposer } from "./InlineComment";
import type { InlineCommentAuthor } from "./InlineComment";

// Peer-review thread (B4): reviewer verdicts + a decision footer. Maps to the
// DB ReviewThread entity (W4/W5). Presentational — callbacks only.

export type ReviewVerdict = "approved" | "changes-requested" | "commented" | "pending";

const verdictMeta: Record<ReviewVerdict, { label: string; tone: BadgeTone }> = {
  approved: { label: "Approved", tone: "success" },
  "changes-requested": { label: "Changes requested", tone: "warning" },
  commented: { label: "Commented", tone: "info" },
  pending: { label: "Pending", tone: "neutral" },
};

export function ReviewStatusBadge({ verdict }: { verdict: ReviewVerdict }) {
  const m = verdictMeta[verdict];
  return <Badge tone={m.tone} dot>{m.label}</Badge>;
}

export interface ReviewEntry {
  id: string;
  reviewer: InlineCommentAuthor;
  verdict: ReviewVerdict;
  note?: string;
  timeLabel?: string;
}

export interface ReviewPanelProps {
  reviews: ReviewEntry[];
  /** Overall roll-up shown in the header. */
  summary?: ReviewVerdict;
  /** Right-aligned header slot (e.g. an AssignAgent trigger). */
  headerAction?: ReactNode;
  busy?: boolean;
  /** Current user submits a verdict with an optional note. */
  onSubmitReview?: (verdict: Exclude<ReviewVerdict, "pending">, note: string) => void;
  className?: string;
}

export function ReviewPanel({
  reviews,
  summary,
  headerAction,
  busy,
  onSubmitReview,
  className,
}: ReviewPanelProps) {
  return (
    <section className={cx("rounded-lg border border-border bg-surface shadow-card", className)}>
      <header className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-semibold text-text">Peer review</h3>
          {summary ? <ReviewStatusBadge verdict={summary} /> : null}
        </div>
        {headerAction}
      </header>

      <ul className="divide-y divide-border">
        {reviews.length === 0 ? (
          <li className="px-4 py-6 text-center text-sm text-text-muted">No reviewers yet.</li>
        ) : (
          reviews.map((r) => (
            <li key={r.id} className="flex gap-3 px-4 py-3">
              <Avatar name={r.reviewer.name} src={r.reviewer.avatarUrl} size="sm" agent={r.reviewer.isAgent} />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-medium text-text">{r.reviewer.name}</span>
                  <ReviewStatusBadge verdict={r.verdict} />
                  {r.timeLabel ? <span className="text-[11px] text-text-muted">{r.timeLabel}</span> : null}
                </div>
                {r.note ? <p className="mt-1 whitespace-pre-wrap text-sm text-text-muted">{r.note}</p> : null}
              </div>
            </li>
          ))
        )}
      </ul>

      {onSubmitReview ? (
        <div className="border-t border-border px-4 py-3">
          <CommentComposer
            placeholder="Leave a review note…"
            submitLabel="Comment"
            busy={busy}
            onSubmit={(note) => onSubmitReview("commented", note)}
          />
          <div className="mt-2 flex items-center gap-2">
            <Button
              variant="primary"
              size="sm"
              disabled={busy}
              onClick={() => onSubmitReview("approved", "")}
            >
              Approve
            </Button>
            <Button
              variant="secondary"
              size="sm"
              disabled={busy}
              onClick={() => onSubmitReview("changes-requested", "")}
            >
              Request changes
            </Button>
          </div>
        </div>
      ) : null}
    </section>
  );
}

export default ReviewPanel;
