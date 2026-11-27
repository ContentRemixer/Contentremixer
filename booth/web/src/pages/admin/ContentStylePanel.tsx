import { useState } from "react";
import {
  Badge,
  Button,
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  Field,
  Input,
  Textarea,
  useToast,
} from "../../ui";
import { api, ApiError } from "../../app/api";
import { CrPanel, type CrObject } from "./CrPanel";

// The writer's KnownOutputFormats (app/api/v1alpha1/common_types.go) — the same
// set as the ContentStyle CRD enum (W1, contentstyle_types.go). Lockstep change
// when the platform adds a format (see that file's comment).
const OUTPUT_FORMATS = ["blog", "short", "tutorial", "teleprompter"];

// ContentStylesTab is the Admin "Content styles" surface (ISI-7965 W4): one
// markdown style guide per output format, GLOBAL across reference repos. The
// list/create/delete ride the generic CrPanel (same as Model profiles); the one
// bespoke piece is the guide editor — a markdown body needs a real textarea, so
// it is a scoped row dialog like ReferenceRepo's labelRoutes editor (ISI-7514).
export function ContentStylesTab() {
  return (
    <CrPanel
      path="/v1/contentstyles"
      title="Content styles"
      subtitle="Per-format markdown style guides the writer renders against — one per output format, replacing the old styles/*.md repo files."
      createNote={"No style guides yet. Use “New” to create one."}
      columns={[
        { header: "Format", cell: (o) => <Badge tone="info">{str(o, "spec.outputFormat") || "—"}</Badge> },
        {
          header: "Description",
          cell: (o) =>
            str(o, "spec.description") || <span className="text-text-muted">—</span>,
        },
        { header: "Guide", cell: (o) => <GuideCell o={o} /> },
      ]}
      create={{
        fields: [
          {
            key: "outputFormat",
            label: "Output format",
            required: true,
            options: OUTPUT_FORMATS,
            hint: "Exactly one style per format (webhook-enforced).",
          },
          {
            key: "description",
            label: "Description",
            hint: "Human-readable summary shown in the list.",
          },
          {
            key: "guide",
            label: "Guide (markdown)",
            required: true,
            multiline: true,
            rows: 16,
            hint: "The style-guide body the writer renders against for this format.",
          },
        ],
        // CR name = the format (deterministic, greppable; the webhook keys on
        // spec.outputFormat, not the name).
        build: (v) => ({
          metadata: { name: v.outputFormat },
          spec: {
            outputFormat: v.outputFormat,
            guide: v.guide,
            description: v.description || undefined,
          },
        }),
      }}
      rowActions={(o, refresh) => (
        <EditContentStyleButton style={o} path="/v1/contentstyles" onSaved={refresh} />
      )}
    />
  );
}

// GuideCell summarizes the body — the markdown itself is one dialog away.
function GuideCell({ o }: { o: CrObject }) {
  const guide = str(o, "spec.guide");
  if (!guide) return <span className="text-text-muted">—</span>;
  const lines = guide.split("\n").length;
  const words = guide.trim().split(/\s+/).filter(Boolean).length;
  return (
    <span className="text-text-muted">
      {lines} lines · {words} words
    </span>
  );
}

// EditContentStyleButton is the scoped guide editor: description + the markdown
// body. The format is fixed after create (one CR per format is webhook-owned;
// the writer resolves fail-closed by format). GET→PUT round-trip so a fresh
// resourceVersion and every untouched field ride along (AgentForm contract).
function EditContentStyleButton({
  style,
  path,
  onSaved,
}: {
  style: CrObject;
  path: string;
  onSaved: () => void;
}) {
  const { toast } = useToast();
  const [open, setOpen] = useState(false);
  const [guide, setGuide] = useState("");
  const [description, setDescription] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const name = style.metadata.name;
  const format = str(style, "spec.outputFormat") || name;

  function openDialog() {
    // Seed from the row's snapshot; the PUT re-GETs before saving anyway.
    setGuide(str(style, "spec.guide"));
    setDescription(str(style, "spec.description"));
    setErr(null);
    setBusy(false);
    setOpen(true);
  }

  async function submit() {
    setBusy(true);
    setErr(null);
    try {
      const current = await api.get<CrObject>(`${path}/${encodeURIComponent(name)}`);
      await api.put(`${path}/${encodeURIComponent(name)}`, {
        ...current,
        spec: {
          ...(current.spec ?? {}),
          guide,
          description: description || undefined,
        },
      });
      toast({ title: `Saved ${format} style`, tone: "success" });
      setOpen(false);
      onSaved();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : e instanceof Error ? e.message : "request failed");
      setBusy(false);
    }
  }

  // The writer is fail-closed on an empty guide — never let one be saved.
  const invalid = !guide.trim();

  return (
    <>
      <Button size="sm" onClick={openDialog}>
        Edit guide
      </Button>
      {open && (
        <Dialog open onOpenChange={(o) => !o && setOpen(false)}>
          <DialogContent title={`Style guide — ${format}`}>
            <DialogBody className="space-y-4">
              <p className="text-sm text-text-muted">
                Markdown the writer renders against for <Badge tone="info">{format}</Badge>. The output format is
                fixed after create (one style per format); delete and re-create to change it.
              </p>
              <Field label="Description">
                {({ id }) => (
                  <Input
                    id={id}
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                  />
                )}
              </Field>
              <Field
                label="Guide (markdown)"
                required
                hint={`${guide.length} chars — an empty guide is rejected (the writer is fail-closed).`}
              >
                {({ id }) => (
                  <Textarea
                    id={id}
                    rows={18}
                    className="font-mono text-xs"
                    value={guide}
                    onChange={(e) => setGuide(e.target.value)}
                  />
                )}
              </Field>
              {err && <p className="text-sm text-warn">{err}</p>}
            </DialogBody>
            <DialogFooter>
              <Button variant="ghost" onClick={() => setOpen(false)} disabled={busy}>
                Cancel
              </Button>
              <Button onClick={submit} loading={busy} disabled={invalid}>
                Save
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </>
  );
}

// str reads a dotted path off a raw CR and coerces it to a display string
// (same helper AdminPage uses for its CrPanel columns).
function str(o: CrObject, path: string): string {
  const v = path.split(".").reduce<unknown>((acc, k) => {
    if (acc && typeof acc === "object" && k in (acc as Record<string, unknown>)) {
      return (acc as Record<string, unknown>)[k];
    }
    return undefined;
  }, o);
  return v == null ? "" : String(v);
}
