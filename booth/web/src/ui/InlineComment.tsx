import { useState } from "react";
import type { ReactNode } from "react";
import { cx } from "./cx";
import { Avatar } from "./Avatar";
import { Button } from "./Button";
import { Textarea } from "./Field";
import { CommentIcon } from "../components/icons";

// Google-Docs-style anchored inline comments (B3). Presentational only — the
// anchor range model + persistence land in W5 (POST /v1/content/{id}/comments);
// these components take plain data + callbacks so the UI is review-ready now.

export interface InlineCommentAuthor {
  name: string;
  avatarUrl?: string;
  isAgent?: boolean;
}

export interface InlineCommentItem {
  id: string;
  author: InlineCommentAuthor;
  body: string;
  /** Pre-formatted relative time ("2h ago"); formatting is the caller's job. */
  timeLabel?: string;
}

/**
 * CommentMark wraps a span of document text as a comment anchor — tinted
 * highlight, active ring when its thread is open, click to focus the thread.
 */
export function CommentMark({
  children,
  active,
  resolved,
  onClick,
}: {
  children: ReactNode;
  active?: boolean;
  resolved?: boolean;
  onClick?: () => void;
}) {
  return (
    <mark
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onClick?.();
        }
      }}
      className={cx(
        "cursor-pointer rounded-sm bg-transparent px-0.5 transition-colors",
        resolved
          ? "underline decoration-dotted decoration-text-muted/50"
          : "bg-accent/15 hover:bg-accent/25 decoration-clone",
        active && !resolved && "bg-accent/30 ring-1 ring-accent/50",
      )}
    >
      {children}
    </mark>
  );
}

function CommentRow({ item }: { item: InlineCommentItem }) {
  return (
    <div className="flex gap-2.5">
      <Avatar name={item.author.name} src={item.author.avatarUrl} size="sm" agent={item.author.isAgent} />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className="truncate text-xs font-semibold text-text">{item.author.name}</span>
          {item.timeLabel ? <span className="text-[11px] text-text-muted">{item.timeLabel}</span> : null}
        </div>
        <p className="mt-0.5 whitespace-pre-wrap break-words text-sm text-text">{item.body}</p>
      </div>
    </div>
  );
}

export interface CommentComposerProps {
  placeholder?: string;
  submitLabel?: string;
  busy?: boolean;
  onSubmit: (body: string) => void;
  onCancel?: () => void;
  autoFocus?: boolean;
}

/** Textarea + submit; clears on send. Reused by the thread reply box. */
export function CommentComposer({
  placeholder = "Add a comment…",
  submitLabel = "Comment",
  busy,
  onSubmit,
  onCancel,
  autoFocus,
}: CommentComposerProps) {
  const [value, setValue] = useState("");
  const canSend = value.trim().length > 0 && !busy;
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (!canSend) return;
        onSubmit(value.trim());
        setValue("");
      }}
      className="flex flex-col gap-2"
    >
      <Textarea
        autoFocus={autoFocus}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder={placeholder}
        rows={2}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && canSend) {
            e.preventDefault();
            onSubmit(value.trim());
            setValue("");
          }
        }}
      />
      <div className="flex items-center justify-end gap-2">
        {onCancel ? (
          <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
            Cancel
          </Button>
        ) : null}
        <Button type="submit" size="sm" loading={busy} disabled={!canSend}>
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}

export interface CommentThreadProps {
  comments: InlineCommentItem[];
  /** The anchored text this thread is attached to (shown as a quote). */
  quote?: string;
  resolved?: boolean;
  busy?: boolean;
  onReply?: (body: string) => void;
  onResolve?: () => void;
  onReopen?: () => void;
  className?: string;
}

/** A single comment thread card: quote + comments + reply composer + resolve. */
export function CommentThread({
  comments,
  quote,
  resolved,
  busy,
  onReply,
  onResolve,
  onReopen,
  className,
}: CommentThreadProps) {
  return (
    <div className={cx("w-72 rounded-lg border border-border bg-surface-raised p-3 shadow-card", className)}>
      {quote ? (
        <div className="mb-2 flex gap-2 border-l-2 border-accent/50 pl-2">
          <CommentIcon size={13} className="mt-0.5 shrink-0 text-accent" />
          <p className="line-clamp-2 text-xs italic text-text-muted">“{quote}”</p>
        </div>
      ) : null}
      <div className="flex flex-col gap-3">
        {comments.map((c) => (
          <CommentRow key={c.id} item={c} />
        ))}
      </div>
      {resolved ? (
        <div className="mt-3 flex items-center justify-between">
          <span className="text-xs text-text-muted">Resolved</span>
          {onReopen ? (
            <Button variant="ghost" size="sm" onClick={onReopen}>
              Reopen
            </Button>
          ) : null}
        </div>
      ) : (
        <div className="mt-3 flex flex-col gap-2">
          {onReply ? <CommentComposer submitLabel="Reply" placeholder="Reply…" busy={busy} onSubmit={onReply} /> : null}
          {onResolve ? (
            <Button variant="secondary" size="sm" className="self-start" onClick={onResolve}>
              Resolve
            </Button>
          ) : null}
        </div>
      )}
    </div>
  );
}
