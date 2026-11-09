import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Badge,
  Button,
  Card,
  Dialog,
  DialogContent,
  DialogBody,
  DialogFooter,
  Field,
  Input,
  Textarea,
  useToast,
} from "../../ui";
import type { BadgeTone } from "../../ui";
import { PlusIcon } from "../../components/icons";
import { FullScreenLoader } from "../../components/Loader";
import { api, ApiError } from "../../app/api";
import { contentApi } from "./api";
import { timeAgo } from "./time";
import type { Content, ContentStatus } from "./types";

const statusMeta: Record<ContentStatus, { label: string; tone: BadgeTone }> = {
  draft: { label: "Draft", tone: "neutral" },
  in_review: { label: "In review", tone: "warning" },
  approved: { label: "Approved", tone: "success" },
};

export function ContentListPage() {
  const navigate = useNavigate();
  const { toast } = useToast();
  const [items, setItems] = useState<Content[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState("");
  const [bodyMd, setBodyMd] = useState("");
  const [creating, setCreating] = useState(false);

  const reload = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setItems(await contentApi.list());
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to load content.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  async function create() {
    const t = title.trim();
    if (!t || creating) return;
    setCreating(true);
    try {
      const doc = await api.post<Content>("/v1/content", { title: t, bodyMd });
      setOpen(false);
      setTitle("");
      setBodyMd("");
      navigate(`/content/${doc.id}`);
    } catch (err) {
      toast({
        tone: "warning",
        title: "Could not create document",
        description: err instanceof ApiError ? err.message : "Please try again.",
      });
      setCreating(false);
    }
  }

  if (loading) return <FullScreenLoader label="Loading content…" />;

  return (
    <section className="flex flex-col gap-5">
      <header className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold text-text">Content</h1>
          <p className="mt-1 text-sm text-text-muted">
            Draft, review, and request illustrations before publishing.
          </p>
        </div>
        <Button iconLeft={<PlusIcon size={16} />} onClick={() => setOpen(true)}>
          New document
        </Button>
      </header>

      {error ? (
        <div className="rounded-lg border border-border bg-surface p-4 text-sm text-text-muted">
          {error}{" "}
          <button className="text-primary underline" onClick={() => void reload()}>
            Retry
          </button>
        </div>
      ) : items.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border px-6 py-16 text-center">
          <p className="text-sm font-medium text-text">No documents yet</p>
          <p className="mt-1 text-sm text-text-muted">Create your first document to get started.</p>
          <Button className="mt-4" iconLeft={<PlusIcon size={16} />} onClick={() => setOpen(true)}>
            New document
          </Button>
        </div>
      ) : (
        <ul className="grid gap-3 sm:grid-cols-2">
          {items.map((c) => {
            const meta = statusMeta[c.status];
            return (
              <li key={c.id}>
                <Card
                  interactive
                  onClick={() => navigate(`/content/${c.id}`)}
                  className="h-full cursor-pointer p-4"
                >
                  <div className="flex items-start justify-between gap-2">
                    <h2 className="min-w-0 flex-1 truncate font-medium text-text">{c.title}</h2>
                    <Badge tone={meta.tone} dot>
                      {meta.label}
                    </Badge>
                  </div>
                  <p className="mt-2 text-xs text-text-muted">
                    {c.owner} · updated {timeAgo(c.updatedAt)}
                  </p>
                </Card>
              </li>
            );
          })}
        </ul>
      )}

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent title="New document" description="Start a draft. You can edit and request review next.">
          <DialogBody className="flex flex-col gap-4">
            <Field label="Title" required>
              {({ id, invalid }) => (
                <Input
                  id={id}
                  invalid={invalid}
                  autoFocus
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  placeholder="e.g. Kubernetes show notes"
                />
              )}
            </Field>
            <Field label="Body" hint="Markdown — headings, lists, code, and emphasis.">
              {({ id }) => (
                <Textarea
                  id={id}
                  rows={8}
                  value={bodyMd}
                  onChange={(e) => setBodyMd(e.target.value)}
                  placeholder={"# Intro\n\nWrite your draft here…"}
                />
              )}
            </Field>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button loading={creating} disabled={!title.trim()} onClick={create}>
              Create document
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
