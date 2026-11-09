import { Fragment } from "react";
import type { ReactNode } from "react";
import { CommentMark } from "../../ui";

// Minimal, dependency-free markdown reader for the B3 content-detail view, with
// Google-Docs-style inline-comment highlighting. The W4 Anchor model pins a
// comment to a [start,end) CHARACTER RANGE of the raw markdown (bodyMd), so this
// renderer tracks each line's absolute offset and wraps the covered spans in a
// <CommentMark> — keeping highlights exactly aligned with the server's anchors.
//
// Scope is deliberately small (headings, paragraphs, blockquotes, unordered/
// ordered lists, fenced code, inline bold/italic/code/links). It is a reader,
// not an editor; richer rendering can grow later without changing the anchor
// contract.

export interface Highlight {
  start: number;
  end: number;
  commentId: string;
  active?: boolean;
  resolved?: boolean;
}

interface RenderOpts {
  highlights: Highlight[];
  onMarkClick?: (commentId: string) => void;
}

// --- inline formatting ------------------------------------------------------

// Order matters: code spans first (their contents are literal), then links,
// then bold, then italic.
const INLINE = [
  { re: /`([^`]+)`/, kind: "code" as const },
  { re: /\[([^\]]+)\]\(([^)\s]+)\)/, kind: "link" as const },
  { re: /\*\*([^*]+)\*\*/, kind: "bold" as const },
  { re: /(?:\*([^*]+)\*|_([^_]+)_)/, kind: "italic" as const },
];

function formatInline(text: string, keyBase: string): ReactNode[] {
  if (!text) return [];
  // Find the earliest-matching inline marker; recurse on the surrounding text.
  let best: { index: number; len: number; node: ReactNode } | null = null;
  for (const { re, kind } of INLINE) {
    const m = re.exec(text);
    if (!m) continue;
    if (best && m.index >= best.index) continue;
    const key = `${keyBase}-${kind}-${m.index}`;
    let node: ReactNode;
    switch (kind) {
      case "code":
        node = (
          <code key={key} className="rounded bg-surface-2 px-1 py-0.5 font-mono text-[0.85em] text-text">
            {m[1]}
          </code>
        );
        break;
      case "link":
        node = (
          <a
            key={key}
            href={m[2]}
            target="_blank"
            rel="noreferrer"
            className="text-primary underline decoration-primary/40 underline-offset-2 hover:decoration-primary"
          >
            {m[1]}
          </a>
        );
        break;
      case "bold":
        node = (
          <strong key={key} className="font-semibold text-text">
            {formatInline(m[1], key)}
          </strong>
        );
        break;
      default:
        node = (
          <em key={key} className="italic">
            {formatInline(m[1] ?? m[2] ?? "", key)}
          </em>
        );
    }
    best = { index: m.index, len: m[0].length, node };
  }

  if (!best) return [text];
  return [
    ...formatInline(text.slice(0, best.index), `${keyBase}<`),
    best.node,
    ...formatInline(text.slice(best.index + best.len), `${keyBase}>`),
  ];
}

// --- highlight-aware line rendering -----------------------------------------

// Split one source line into maximal segments of constant highlight coverage,
// using ABSOLUTE offsets, and format each segment's text inline. Highlighted
// segments are wrapped in a clickable <CommentMark>.
function renderLine(
  lineStart: number,
  lineText: string,
  { highlights, onMarkClick }: RenderOpts,
  keyBase: string,
): ReactNode[] {
  if (!lineText) return [];
  const len = lineText.length;
  const cuts = new Set<number>([0, len]);
  for (const h of highlights) {
    const s = Math.max(0, Math.min(len, h.start - lineStart));
    const e = Math.max(0, Math.min(len, h.end - lineStart));
    if (e > s) {
      cuts.add(s);
      cuts.add(e);
    }
  }
  const points = [...cuts].sort((a, b) => a - b);
  const out: ReactNode[] = [];
  for (let i = 0; i < points.length - 1; i++) {
    const a = points[i];
    const b = points[i + 1];
    if (b <= a) continue;
    const abs = lineStart + a;
    const hl = highlights.find((h) => h.start <= abs && abs < h.end);
    const segKey = `${keyBase}:${a}`;
    const formatted = formatInline(lineText.slice(a, b), segKey);
    if (hl) {
      out.push(
        <CommentMark
          key={segKey}
          active={hl.active}
          resolved={hl.resolved}
          onClick={onMarkClick ? () => onMarkClick(hl.commentId) : undefined}
        >
          {formatted}
        </CommentMark>,
      );
    } else {
      out.push(<Fragment key={segKey}>{formatted}</Fragment>);
    }
  }
  return out;
}

// --- block parsing ----------------------------------------------------------

/**
 * Render raw markdown into themed, highlight-aware React nodes. Offsets passed
 * in `highlights` index `body` directly (the W4 Anchor contract).
 */
export function renderMarkdown(body: string, opts: RenderOpts): ReactNode {
  // Split keeping offsets: iterate lines, tracking the running char offset
  // (including the trailing "\n" consumed between lines).
  const lines: { text: string; start: number }[] = [];
  let off = 0;
  for (const text of body.split("\n")) {
    lines.push({ text, start: off });
    off += text.length + 1; // +1 for the "\n"
  }

  const blocks: ReactNode[] = [];
  let i = 0;
  let key = 0;
  const nextKey = () => `b${key++}`;

  while (i < lines.length) {
    const { text, start } = lines[i];
    const trimmed = text.trim();

    if (trimmed === "") {
      i++;
      continue;
    }

    // Fenced code block.
    const fence = /^```/.exec(trimmed);
    if (fence) {
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i].text.trim())) {
        codeLines.push(lines[i].text);
        i++;
      }
      i++; // closing fence
      blocks.push(
        <pre
          key={nextKey()}
          className="my-4 overflow-x-auto rounded-lg border border-border bg-surface-2 p-4 text-sm"
        >
          <code className="font-mono text-text">{codeLines.join("\n")}</code>
        </pre>,
      );
      continue;
    }

    // Heading.
    const heading = /^(#{1,6})\s+(.*)$/.exec(text);
    if (heading) {
      const level = heading[1].length;
      const contentStart = start + heading[0].length - heading[2].length;
      const inner = renderLine(contentStart, heading[2], opts, nextKey());
      const cls = [
        "mt-6 mb-3 text-2xl font-semibold text-text",
        "mt-6 mb-3 text-xl font-semibold text-text",
        "mt-5 mb-2 text-lg font-semibold text-text",
        "mt-4 mb-2 text-base font-semibold text-text",
        "mt-4 mb-1 text-sm font-semibold text-text",
        "mt-4 mb-1 text-sm font-semibold text-text-muted",
      ][level - 1];
      const Tag = (`h${level}` as unknown) as "h1";
      blocks.push(
        <Tag key={nextKey()} className={cls}>
          {inner}
        </Tag>,
      );
      i++;
      continue;
    }

    // Blockquote.
    if (/^>\s?/.test(text)) {
      const prefixLen = text.length - text.replace(/^>\s?/, "").length;
      const inner = renderLine(start + prefixLen, text.replace(/^>\s?/, ""), opts, nextKey());
      blocks.push(
        <blockquote
          key={nextKey()}
          className="my-3 border-l-2 border-accent/50 pl-4 text-text-muted italic"
        >
          {inner}
        </blockquote>,
      );
      i++;
      continue;
    }

    // List (consecutive -, *, or ordered items).
    const listMatch = /^(\s*)([-*]|\d+\.)\s+(.*)$/.exec(text);
    if (listMatch) {
      const ordered = /\d+\./.test(listMatch[2]);
      const items: ReactNode[] = [];
      while (i < lines.length) {
        const m = /^(\s*)([-*]|\d+\.)\s+(.*)$/.exec(lines[i].text);
        if (!m) break;
        const markerLen = lines[i].text.length - m[3].length;
        items.push(
          <li key={nextKey()} className="leading-relaxed text-text">
            {renderLine(lines[i].start + markerLen, m[3], opts, nextKey())}
          </li>,
        );
        i++;
      }
      blocks.push(
        ordered ? (
          <ol key={nextKey()} className="my-3 ml-5 list-decimal space-y-1">
            {items}
          </ol>
        ) : (
          <ul key={nextKey()} className="my-3 ml-5 list-disc space-y-1">
            {items}
          </ul>
        ),
      );
      continue;
    }

    // Paragraph: gather consecutive plain lines into one <p>, each line a run.
    const runs: ReactNode[] = [];
    let first = true;
    while (i < lines.length) {
      const ln = lines[i];
      const t = ln.text.trim();
      if (
        t === "" ||
        /^```/.test(t) ||
        /^#{1,6}\s/.test(ln.text) ||
        /^>\s?/.test(ln.text) ||
        /^(\s*)([-*]|\d+\.)\s+/.test(ln.text)
      ) {
        break;
      }
      if (!first) runs.push(<Fragment key={nextKey()}> </Fragment>);
      runs.push(<Fragment key={nextKey()}>{renderLine(ln.start, ln.text, opts, nextKey())}</Fragment>);
      first = false;
      i++;
    }
    blocks.push(
      <p key={nextKey()} className="my-3 leading-relaxed text-text">
        {runs}
      </p>,
    );
  }

  return <div className="max-w-none">{blocks}</div>;
}
