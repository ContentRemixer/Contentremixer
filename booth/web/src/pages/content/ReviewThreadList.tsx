import { useState } from "react";
import { Badge, Button, CommentThread, Field, Input, Textarea } from "../../ui";
import { timeAgo } from "./time";
import type { ReviewThread } from "./types";

// Peer-review threads rail (B3): discussion threads on the document. Each thread
// carries a subject + a message conversation and an open/resolved lifecycle —
// composed from the W7 thread primitive (reply + resolve) plus a "start a
// thread" form. Maps to GET/POST /v1/content/{id}/reviews and …/resolve.

export interface ReviewThreadListProps {
  threads: ReviewThread[];
  busyThreadId: string | null;
  creating: boolean;
  onReply: (threadId: string, body: string) => void;
  onResolve: (threadId: string) => void;
  onCreate: (subject: string, body: string) => void;
}

export function ReviewThreadList({
  threads,
  busyThreadId,
  creating,
  onReply,
  onResolve,
  onCreate,
}: ReviewThreadListProps) {
  const [open, setOpen] = useState(false);
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");

  const canCreate = subject.trim().length > 0 && body.trim().length > 0 && !creating;

  function submit() {
    if (!canCreate) return;
    onCreate(subject.trim(), body.trim());
    setSubject("");
    setBody("");
    setOpen(false);
  }

  return (
    <div className="flex flex-col gap-3">
      {open ? (
        <div className="flex flex-col gap-3 rounded-lg border border-border bg-surface-raised p-3 shadow-card">
          <Field label="Topic" required>
            {({ id, invalid }) => (
              <Input
                id={id}
                invalid={invalid}
                autoFocus
                value={subject}
                onChange={(e) => setSubject(e.target.value)}
                placeholder="e.g. Reorder the intro"
              />
            )}
          </Field>
          <Field label="Opening note" required>
            {({ id, invalid }) => (
              <Textarea
                id={id}
                invalid={invalid}
                rows={3}
                value={body}
                onChange={(e) => setBody(e.target.value)}
                placeholder="What should the author consider?"
              />
            )}
          </Field>
          <div className="flex items-center justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button size="sm" loading={creating} disabled={!canCreate} onClick={submit}>
              Start thread
            </Button>
          </div>
        </div>
      ) : (
        <Button variant="secondary" size="sm" className="self-start" onClick={() => setOpen(true)}>
          Start a review thread
        </Button>
      )}

      {threads.length === 0 ? (
        <p className="rounded-lg border border-dashed border-border px-4 py-8 text-center text-sm text-text-muted">
          No review threads yet.
        </p>
      ) : (
        threads.map((t) => {
          const resolved = t.status === "resolved";
          return (
            <div key={t.id} className="flex flex-col gap-1">
              <div className="flex items-center justify-between gap-2 px-1">
                <span className="truncate text-sm font-semibold text-text">{t.subject}</span>
                <Badge tone={resolved ? "success" : "warning"} dot>
                  {resolved ? "Resolved" : "Open"}
                </Badge>
              </div>
              <CommentThread
                className="w-full"
                resolved={resolved}
                busy={busyThreadId === t.id}
                comments={t.messages.map((m) => ({
                  id: m.id,
                  author: { name: m.author, isAgent: false },
                  body: m.body,
                  timeLabel: timeAgo(m.createdAt),
                }))}
                onReply={resolved ? undefined : (b) => onReply(t.id, b)}
                onResolve={resolved ? undefined : () => onResolve(t.id)}
              />
            </div>
          );
        })
      )}
    </div>
  );
}
