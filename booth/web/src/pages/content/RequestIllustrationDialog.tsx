import { useEffect, useState } from "react";
import {
  Button,
  Dialog,
  DialogContent,
  DialogBody,
  DialogFooter,
  Field,
  Input,
  Textarea,
  useToast,
} from "../../ui";
import { ApiError } from "../../app/api";
import { contentApi } from "./api";
import { DEFAULT_ILLUSTRATION_ASSIGNEE } from "./types";
import type { Anchor, IllustrationRequest } from "./types";

// "Request illustration" flow (plan §3.2 B3): a DevRel describes what they need
// and it is filed as an IllustrationRequest assigned, by default, to the Graphic
// Designer agent. An optional text anchor carries the selected passage so the
// agent knows where the art belongs.

export interface RequestIllustrationDialogProps {
  contentId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Passage the request is anchored to (from a reader selection). */
  anchor?: Anchor;
  onCreated: (req: IllustrationRequest) => void;
}

export function RequestIllustrationDialog({
  contentId,
  open,
  onOpenChange,
  anchor,
  onCreated,
}: RequestIllustrationDialogProps) {
  const { toast } = useToast();
  const [brief, setBrief] = useState("");
  const [assignee, setAssignee] = useState(DEFAULT_ILLUSTRATION_ASSIGNEE);
  const [busy, setBusy] = useState(false);

  // Reset the form each time the dialog opens.
  useEffect(() => {
    if (open) {
      setBrief("");
      setAssignee(DEFAULT_ILLUSTRATION_ASSIGNEE);
      setBusy(false);
    }
  }, [open]);

  async function submit() {
    const trimmed = brief.trim();
    if (!trimmed || busy) return;
    setBusy(true);
    try {
      const req = await contentApi.requestIllustration({
        contentId,
        brief: trimmed,
        anchor,
        assignedAgent: assignee.trim() || DEFAULT_ILLUSTRATION_ASSIGNEE,
      });
      onCreated(req);
      toast({
        tone: "success",
        title: "Illustration requested",
        description: `Assigned to ${req.assignedAgent}.`,
      });
      onOpenChange(false);
    } catch (err) {
      toast({
        tone: "warning",
        title: "Could not request illustration",
        description: err instanceof ApiError ? err.message : "Please try again.",
      });
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        title="Request an illustration"
        description="Describe the visual you need. It is filed to the assigned agent's work queue."
      >
        <DialogBody className="flex flex-col gap-4">
          {anchor?.quote ? (
            <div className="flex gap-2 border-l-2 border-accent/50 pl-3">
              <p className="line-clamp-3 text-sm italic text-text-muted">“{anchor.quote}”</p>
            </div>
          ) : null}

          <Field label="Brief" required>
            {({ id, invalid }) => (
              <Textarea
                id={id}
                invalid={invalid}
                autoFocus
                rows={4}
                value={brief}
                onChange={(e) => setBrief(e.target.value)}
                placeholder="e.g. A hero diagram showing the recycle pipeline, brand palette, 16:9."
              />
            )}
          </Field>

          <Field label="Assign to" hint="Defaults to the Graphic Designer agent.">
            {({ id }) => (
              <Input id={id} value={assignee} onChange={(e) => setAssignee(e.target.value)} />
            )}
          </Field>
        </DialogBody>

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button loading={busy} disabled={!brief.trim()} onClick={submit}>
            Request illustration
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
