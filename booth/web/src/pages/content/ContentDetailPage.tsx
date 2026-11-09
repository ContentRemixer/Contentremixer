import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useParams, Link } from "react-router-dom";
import {
  Badge,
  Button,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  useToast,
} from "../../ui";
import type { BadgeTone } from "../../ui";
import { ChevronRightIcon } from "../../components/icons";
import { FullScreenLoader } from "../../components/Loader";
import { ApiError } from "../../app/api";
import { contentApi } from "./api";
import { renderMarkdown } from "./markdown";
import type { Highlight } from "./markdown";
import { CommentsRail } from "./CommentsRail";
import { ReviewThreadList } from "./ReviewThreadList";
import { IllustrationList } from "./IllustrationList";
import { RequestIllustrationDialog } from "./RequestIllustrationDialog";
import { contentStatuses } from "./types";
import type {
  Anchor,
  Comment,
  Content,
  ContentStatus,
  IllustrationRequest,
  IllustrationStatus,
  ReviewThread,
} from "./types";
import { timeAgo } from "./time";

const statusMeta: Record<ContentStatus, { label: string; tone: BadgeTone }> = {
  draft: { label: "Draft", tone: "neutral" },
  in_review: { label: "In review", tone: "warning" },
  approved: { label: "Approved", tone: "success" },
};

interface Selection {
  anchor: Anchor;
  top: number;
  left: number;
}

export function ContentDetailPage() {
  const { id = "" } = useParams();
  const { toast } = useToast();
  const readerRef = useRef<HTMLDivElement>(null);

  const [content, setContent] = useState<Content | null>(null);
  const [comments, setComments] = useState<Comment[]>([]);
  const [reviews, setReviews] = useState<ReviewThread[]>([]);
  const [illustrations, setIllustrations] = useState<IllustrationRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [tab, setTab] = useState("comments");
  const [activeCommentId, setActiveCommentId] = useState<string | null>(null);
  const [selection, setSelection] = useState<Selection | null>(null);
  const [pendingAnchor, setPendingAnchor] = useState<Anchor | null>(null);
  const [illusAnchor, setIllusAnchor] = useState<Anchor | undefined>(undefined);
  const [illusOpen, setIllusOpen] = useState(false);

  const [commentBusy, setCommentBusy] = useState(false);
  const [busyThreadId, setBusyThreadId] = useState<string | null>(null);
  const [reviewCreating, setReviewCreating] = useState(false);
  const [illusBusyId, setIllusBusyId] = useState<string | null>(null);
  const [statusBusy, setStatusBusy] = useState(false);

  const reload = useCallback(async () => {
    setLoading(true);
    setLoadError(null);
    try {
      const doc = await contentApi.get(id);
      setContent(doc);
      // Collaboration data is best-effort; a failure here shouldn't blank the
      // reader. Each list defaults to [] on error.
      const [cs, rs, ir] = await Promise.all([
        contentApi.listComments(id).catch(() => [] as Comment[]),
        contentApi.listReviews(id).catch(() => [] as ReviewThread[]),
        contentApi.listIllustrationRequests(id).catch(() => [] as IllustrationRequest[]),
      ]);
      setComments(cs);
      setReviews(rs);
      setIllustrations(ir);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setLoadError("not-found");
      } else if (err instanceof ApiError && err.status === 403) {
        setLoadError("forbidden");
      } else {
        setLoadError(err instanceof ApiError ? err.message : "Failed to load content.");
      }
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => {
    void reload();
  }, [reload]);

  // Highlights are derived from the anchored comments plus the live selection
  // the author is about to comment on.
  const highlights = useMemo<Highlight[]>(() => {
    const hs: Highlight[] = comments.map((c) => ({
      start: c.anchor.start,
      end: c.anchor.end,
      commentId: c.id,
      active: c.id === activeCommentId,
      resolved: c.resolved,
    }));
    if (pendingAnchor) {
      hs.push({ start: pendingAnchor.start, end: pendingAnchor.end, commentId: "__pending__", active: true });
    }
    return hs;
  }, [comments, activeCommentId, pendingAnchor]);

  const body = useMemo(
    () =>
      content
        ? renderMarkdown(content.bodyMd, {
            highlights,
            onMarkClick: (commentId) => {
              if (commentId === "__pending__") return;
              setActiveCommentId(commentId);
              setTab("comments");
            },
          })
        : null,
    [content, highlights],
  );

  // Capture a text selection in the reader and compute its anchor into bodyMd.
  // Offsets index the raw markdown (the W4 Anchor contract); indexOf resolves
  // the quote to its first occurrence — consistent with quote re-anchoring.
  const onMouseUp = useCallback(() => {
    if (!content || !readerRef.current) return;
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed || sel.rangeCount === 0) {
      setSelection(null);
      return;
    }
    const text = sel.toString();
    if (!text.trim()) {
      setSelection(null);
      return;
    }
    if (sel.anchorNode && !readerRef.current.contains(sel.anchorNode)) {
      setSelection(null);
      return;
    }
    const start = content.bodyMd.indexOf(text);
    if (start < 0) {
      setSelection(null);
      return;
    }
    const rect = sel.getRangeAt(0).getBoundingClientRect();
    const crect = readerRef.current.getBoundingClientRect();
    setSelection({
      anchor: { start, end: start + text.length, quote: text },
      top: rect.top - crect.top,
      left: rect.left - crect.left + rect.width / 2,
    });
  }, [content]);

  function clearSelection() {
    window.getSelection()?.removeAllRanges();
    setSelection(null);
  }

  function startComment() {
    if (!selection) return;
    setPendingAnchor(selection.anchor);
    setTab("comments");
    clearSelection();
  }

  function startIllustration() {
    const anchor = selection?.anchor;
    setIllusAnchor(anchor);
    setIllusOpen(true);
    clearSelection();
  }

  async function submitComment(text: string) {
    if (!pendingAnchor) return;
    setCommentBusy(true);
    try {
      const created = await contentApi.addComment(id, { body: text, anchor: pendingAnchor });
      setComments((prev) => [...prev, created]);
      setPendingAnchor(null);
      setActiveCommentId(created.id);
    } catch (err) {
      toast({
        tone: "warning",
        title: "Could not add comment",
        description: err instanceof ApiError ? err.message : "Please try again.",
      });
    } finally {
      setCommentBusy(false);
    }
  }

  async function replyReview(threadId: string, text: string) {
    setBusyThreadId(threadId);
    try {
      const updated = await contentApi.addReview(id, { threadId, body: text });
      setReviews((prev) => prev.map((t) => (t.id === updated.id ? updated : t)));
    } catch (err) {
      toast({ tone: "warning", title: "Could not post reply", description: errMsg(err) });
    } finally {
      setBusyThreadId(null);
    }
  }

  async function resolveReview(threadId: string) {
    setBusyThreadId(threadId);
    try {
      const updated = await contentApi.resolveReview(id, threadId);
      setReviews((prev) => prev.map((t) => (t.id === updated.id ? updated : t)));
    } catch (err) {
      toast({ tone: "warning", title: "Could not resolve thread", description: errMsg(err) });
    } finally {
      setBusyThreadId(null);
    }
  }

  async function createReview(subject: string, text: string) {
    setReviewCreating(true);
    try {
      const created = await contentApi.addReview(id, { subject, body: text });
      setReviews((prev) => [...prev, created]);
    } catch (err) {
      toast({ tone: "warning", title: "Could not start thread", description: errMsg(err) });
    } finally {
      setReviewCreating(false);
    }
  }

  async function updateIllustration(reqId: string, status: IllustrationStatus) {
    setIllusBusyId(reqId);
    try {
      const updated = await contentApi.updateIllustration(reqId, { status });
      setIllustrations((prev) => prev.map((r) => (r.id === updated.id ? updated : r)));
    } catch (err) {
      toast({ tone: "warning", title: "Could not update request", description: errMsg(err) });
    } finally {
      setIllusBusyId(null);
    }
  }

  async function changeStatus(next: ContentStatus) {
    if (!content || next === content.status) return;
    setStatusBusy(true);
    try {
      const updated = await contentApi.update(id, { status: next });
      setContent(updated);
      toast({ tone: "success", title: `Moved to ${statusMeta[next].label}` });
    } catch (err) {
      toast({ tone: "warning", title: "Could not change status", description: errMsg(err) });
    } finally {
      setStatusBusy(false);
    }
  }

  if (loading) return <FullScreenLoader label="Loading document…" />;

  if (loadError) {
    const copy =
      loadError === "not-found"
        ? { title: "Document not found", body: "It may have been removed." }
        : loadError === "forbidden"
          ? { title: "Not authorized", body: "You don't have access to this document." }
          : { title: "Couldn't load document", body: loadError };
    return (
      <section className="mx-auto max-w-md py-16 text-center">
        <h1 className="text-lg font-semibold text-text">{copy.title}</h1>
        <p className="mt-2 text-sm text-text-muted">{copy.body}</p>
        <div className="mt-4 flex justify-center gap-2">
          <Button variant="secondary" onClick={() => void reload()}>
            Retry
          </Button>
          <Link
            to="/content"
            className="inline-flex h-10 items-center rounded-md px-4 text-sm font-medium text-text-muted hover:bg-surface-2 hover:text-text"
          >
            Back to content
          </Link>
        </div>
      </section>
    );
  }

  if (!content) return null;
  const meta = statusMeta[content.status];

  return (
    <section className="flex flex-col gap-5">
      {/* Breadcrumb + title + status */}
      <header className="flex flex-col gap-3 border-b border-border pb-4">
        <nav className="flex items-center gap-1 text-xs text-text-muted">
          <Link to="/content" className="hover:text-text">
            Content
          </Link>
          <ChevronRightIcon size={13} />
          <span className="truncate text-text">{content.title}</span>
        </nav>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <h1 className="truncate text-xl font-semibold text-text">{content.title}</h1>
            <p className="mt-1 text-xs text-text-muted">
              {content.owner} · updated {timeAgo(content.updatedAt)}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Badge tone={meta.tone} dot>
              {meta.label}
            </Badge>
            <Select
              value={content.status}
              onValueChange={(v) => void changeStatus(v as ContentStatus)}
              disabled={statusBusy}
            >
              <SelectTrigger className="h-9 w-36" aria-label="Document status">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {contentStatuses.map((s) => (
                  <SelectItem key={s} value={s}>
                    {statusMeta[s].label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
      </header>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_22rem]">
        {/* Reader */}
        <div
          ref={readerRef}
          onMouseUp={onMouseUp}
          className="relative rounded-lg border border-border bg-surface p-6 text-[15px] shadow-card sm:p-8"
        >
          {body}

          {selection ? (
            <div
              className="absolute z-20 -translate-x-1/2 -translate-y-full"
              style={{ top: selection.top - 8, left: selection.left }}
            >
              <div className="flex items-center gap-1 rounded-lg border border-border bg-surface-raised p-1 shadow-card">
                <Button variant="ghost" size="sm" onClick={startComment}>
                  Comment
                </Button>
                <div className="h-4 w-px bg-border" />
                <Button variant="ghost" size="sm" onClick={startIllustration}>
                  Request illustration
                </Button>
              </div>
            </div>
          ) : null}
        </div>

        {/* Collaboration rail */}
        <aside className="lg:sticky lg:top-4 lg:self-start">
          <Tabs value={tab} onValueChange={setTab}>
            <TabsList className="w-full">
              <TabsTrigger value="comments">Comments ({comments.length})</TabsTrigger>
              <TabsTrigger value="review">Review ({reviews.length})</TabsTrigger>
              <TabsTrigger value="art">Art ({illustrations.length})</TabsTrigger>
            </TabsList>

            <TabsContent value="comments" className="mt-4">
              <CommentsRail
                comments={comments}
                pendingAnchor={pendingAnchor}
                activeCommentId={activeCommentId}
                busy={commentBusy}
                onFocus={setActiveCommentId}
                onSubmit={submitComment}
                onCancelCompose={() => setPendingAnchor(null)}
              />
            </TabsContent>

            <TabsContent value="review" className="mt-4">
              <ReviewThreadList
                threads={reviews}
                busyThreadId={busyThreadId}
                creating={reviewCreating}
                onReply={replyReview}
                onResolve={resolveReview}
                onCreate={createReview}
              />
            </TabsContent>

            <TabsContent value="art" className="mt-4">
              <IllustrationList
                requests={illustrations}
                busyId={illusBusyId}
                onUpdateStatus={updateIllustration}
                onRequestNew={() => {
                  setIllusAnchor(undefined);
                  setIllusOpen(true);
                }}
              />
            </TabsContent>
          </Tabs>
        </aside>
      </div>

      <RequestIllustrationDialog
        contentId={id}
        open={illusOpen}
        onOpenChange={setIllusOpen}
        anchor={illusAnchor}
        onCreated={(req) => {
          setIllustrations((prev) => [...prev, req]);
          setTab("art");
        }}
      />
    </section>
  );
}

function errMsg(err: unknown): string {
  return err instanceof ApiError ? err.message : "Please try again.";
}
