import { Fragment, useLayoutEffect, useRef, useState } from "react";
import type { KeyboardEvent, ReactNode } from "react";
import { cx } from "./cx";
import { Avatar } from "./Avatar";
import { Button } from "./Button";
import { Textarea } from "./Field";
import { Popover, PopoverAnchor, PopoverContent } from "./Popover";
import { CommentIcon, SparkleIcon } from "../components/icons";

// --- @mentions (plan §6 P2, ISI-7819) ---------------------------------------

/** A directory entry the composer's @ popover can insert. */
export interface Mentionable {
  id: string;
  name: string;
  role?: string;
  isAgent?: boolean;
  avatarUrl?: string;
}

export type MentionKind = "agent" | "person";

/**
 * A structured mention span emitted alongside the comment body: kind+ref name
 * the directory entry, label is the display name, and [start,end) point at the
 * `[@label](<kind>://<ref>)` machine form in `body`. The server persists these
 * so P3 can resolve mentions without re-parsing prose.
 */
export interface MentionSpan {
  kind: MentionKind;
  ref: string;
  label: string;
  start: number;
  end: number;
}

// The machine form a mention serialises to / parses from: [@Label](kind://ref).
const MENTION_RE = /\[@([^\]]+)\]\((agent|person):\/\/([^)\s]+)\)/g;

/** Extract the structured mention spans from a serialised comment body. */
export function parseMentions(body: string): MentionSpan[] {
  const spans: MentionSpan[] = [];
  MENTION_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = MENTION_RE.exec(body))) {
    spans.push({
      kind: m[2] as MentionKind,
      ref: m[3],
      label: m[1],
      start: m.index,
      end: m.index + m[0].length,
    });
  }
  return spans;
}

/** A styled inline mention chip (Sparkle-ringed avatar for agents). */
function MentionChip({ label, isAgent }: { label: string; isAgent: boolean }) {
  return (
    <span
      className={cx(
        "mx-0.5 inline-flex items-center gap-1 rounded-pill px-1.5 py-0.5 align-baseline text-[0.85em] font-medium",
        isAgent ? "bg-primary/15 text-primary" : "bg-accent/15 text-accent",
      )}
    >
      {isAgent ? <SparkleIcon size={11} /> : null}@{label}
    </span>
  );
}

/**
 * Render a comment body with its `[@Label](kind://ref)` mentions turned into
 * chips (the rest as plain text with newlines preserved). Called by the comment
 * row so a reopened thread re-renders chips from the stored body.
 */
export function renderMentionBody(body: string): ReactNode {
  const out: ReactNode[] = [];
  MENTION_RE.lastIndex = 0;
  let last = 0;
  let m: RegExpExecArray | null;
  let key = 0;
  while ((m = MENTION_RE.exec(body))) {
    if (m.index > last) out.push(<Fragment key={key++}>{body.slice(last, m.index)}</Fragment>);
    out.push(<MentionChip key={key++} label={m[1]} isAgent={m[2] === "agent"} />);
    last = m.index + m[0].length;
  }
  if (last < body.length) out.push(<Fragment key={key++}>{body.slice(last)}</Fragment>);
  return out;
}

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
        <p className="mt-0.5 whitespace-pre-wrap break-words text-sm text-text">
          {renderMentionBody(item.body)}
        </p>
      </div>
    </div>
  );
}

export interface CommentComposerProps {
  placeholder?: string;
  submitLabel?: string;
  busy?: boolean;
  /**
   * Submit handler. `mentions` are the structured spans parsed from `body`
   * (empty when no directory is supplied / nothing was mentioned).
   */
  onSubmit: (body: string, mentions: MentionSpan[]) => void;
  onCancel?: () => void;
  autoFocus?: boolean;
  /** Seed text the box opens with — used to pre-fill a pending @mention. */
  initialValue?: string;
  /** Agents + people the `@` autocomplete offers. Omit to disable mentions. */
  mentionables?: Mentionable[];
}

// The active `@token` under the caret: `from` is the index of the `@`, `text`
// is the typed query after it (no whitespace). null when the caret is not in a
// mention token.
interface MentionQuery {
  from: number;
  text: string;
}

// A seed like P1's "@agent " / "@content " ends in a space that would close the
// token; strip that single trailing space so the popover opens on the seed.
function openSeed(seed: string): string {
  return seed.replace(/(@[^\s@]+) $/, "$1");
}

function activeQuery(value: string, caret: number): MentionQuery | null {
  const m = /(?:^|\s)@([^\s@]*)$/.exec(value.slice(0, caret));
  if (!m) return null;
  return { from: caret - m[1].length - 1, text: m[1] };
}

/** Textarea + submit with `@mention` autocomplete; clears on send. */
export function CommentComposer({
  placeholder = "Add a comment…",
  submitLabel = "Comment",
  busy,
  onSubmit,
  onCancel,
  autoFocus,
  initialValue = "",
  mentionables,
}: CommentComposerProps) {
  const [value, setValue] = useState(() => openSeed(initialValue));
  const [query, setQuery] = useState<MentionQuery | null>(null);
  const [activeIdx, setActiveIdx] = useState(0);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const caretAfterRender = useRef<number | null>(null);
  const canSend = value.trim().length > 0 && !busy;

  const hasDirectory = !!mentionables && mentionables.length > 0;
  const q = query?.text.toLowerCase() ?? "";
  const matches = hasDirectory
    ? mentionables!.filter(
        (m) => m.name.toLowerCase().includes(q) || (m.role ?? "").toLowerCase().includes(q),
      )
    : [];
  const agents = matches.filter((m) => m.isAgent);
  const people = matches.filter((m) => !m.isAgent);
  // Flat, keyboard-navigable order: agents first, then people (matches the
  // AssignAgent split). activeIdx indexes this array.
  const ordered = [...agents, ...people];
  const open = hasDirectory && query !== null && ordered.length > 0;

  // Recompute the active token from the live caret after any edit/move.
  function syncQuery() {
    const el = taRef.current;
    if (!el || !hasDirectory) return;
    const next = activeQuery(el.value, el.selectionStart ?? el.value.length);
    setQuery(next);
    setActiveIdx(0);
  }

  // Restore focus + caret after a mention insert re-renders the textarea, and
  // open the popover for a pre-filled seed on first mount.
  useLayoutEffect(() => {
    const el = taRef.current;
    if (!el) return;
    if (caretAfterRender.current !== null) {
      el.focus();
      el.setSelectionRange(caretAfterRender.current, caretAfterRender.current);
      caretAfterRender.current = null;
      syncQuery();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);

  useLayoutEffect(() => {
    if (autoFocus && /@[^\s@]*$/.test(value)) syncQuery();
    // Open-on-seed is a mount-only concern.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function insertMention(m: Mentionable) {
    const el = taRef.current;
    if (!query || !el) return;
    const kind: MentionKind = m.isAgent ? "agent" : "person";
    const md = `[@${m.name}](${kind}://${m.id})`;
    const caret = el.selectionStart ?? value.length;
    const before = value.slice(0, query.from);
    const after = value.slice(caret);
    const next = `${before}${md} ${after}`;
    caretAfterRender.current = before.length + md.length + 1;
    setQuery(null);
    setValue(next);
  }

  function send() {
    if (!canSend) return;
    const body = value.trim();
    onSubmit(body, parseMentions(body));
    setValue("");
    setQuery(null);
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (open) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setActiveIdx((i) => (i + 1) % ordered.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setActiveIdx((i) => (i - 1 + ordered.length) % ordered.length);
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        insertMention(ordered[Math.min(activeIdx, ordered.length - 1)]);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setQuery(null);
        return;
      }
    }
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      send();
    }
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        send();
      }}
      className="flex flex-col gap-2"
    >
      <Popover open={open} onOpenChange={(o) => !o && setQuery(null)}>
        <PopoverAnchor asChild>
          <Textarea
            ref={taRef}
            autoFocus={autoFocus}
            value={value}
            onChange={(e) => {
              setValue(e.target.value);
              syncQuery();
            }}
            onClick={syncQuery}
            onKeyUp={(e) => {
              // Arrow/Home/End move the caret without an edit — resync, but let
              // the popover-nav keys above own their own keys.
              if (!open || (e.key !== "ArrowDown" && e.key !== "ArrowUp")) syncQuery();
            }}
            placeholder={placeholder}
            rows={2}
            onKeyDown={onKeyDown}
          />
        </PopoverAnchor>
        <PopoverContent
          align="start"
          side="bottom"
          sideOffset={4}
          className="w-64 p-1.5"
          onOpenAutoFocus={(e) => e.preventDefault()}
          onCloseAutoFocus={(e) => e.preventDefault()}
        >
          {agents.length > 0 && (
            <>
              <div className="px-2 pb-1 pt-0.5 text-xs font-medium text-text-muted">Agents</div>
              {agents.map((m) => (
                <MentionOption
                  key={`a:${m.id}`}
                  m={m}
                  active={ordered.indexOf(m) === activeIdx}
                  onPick={() => insertMention(m)}
                  onHover={() => setActiveIdx(ordered.indexOf(m))}
                />
              ))}
            </>
          )}
          {people.length > 0 && (
            <>
              <div className="px-2 pb-1 pt-1.5 text-xs font-medium text-text-muted">People</div>
              {people.map((m) => (
                <MentionOption
                  key={`p:${m.id}`}
                  m={m}
                  active={ordered.indexOf(m) === activeIdx}
                  onPick={() => insertMention(m)}
                  onHover={() => setActiveIdx(ordered.indexOf(m))}
                />
              ))}
            </>
          )}
        </PopoverContent>
      </Popover>
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

function MentionOption({
  m,
  active,
  onPick,
  onHover,
}: {
  m: Mentionable;
  active: boolean;
  onPick: () => void;
  onHover: () => void;
}) {
  return (
    <button
      type="button"
      // onMouseDown (not onClick) so the pick fires before the textarea blurs.
      onMouseDown={(e) => {
        e.preventDefault();
        onPick();
      }}
      onMouseEnter={onHover}
      className={cx(
        "flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left",
        active ? "bg-surface-2" : "hover:bg-surface-2/60",
      )}
    >
      <Avatar name={m.name} src={m.avatarUrl} size="sm" agent={m.isAgent} />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1 truncate text-sm text-text">
          {m.name}
          {m.isAgent ? <SparkleIcon size={12} className="text-primary" /> : null}
        </span>
        {m.role ? <span className="block truncate text-xs text-text-muted">{m.role}</span> : null}
      </span>
    </button>
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
