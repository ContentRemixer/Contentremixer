import { useState } from "react";
import {
  Button,
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  Field,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  useToast,
} from "../../ui";
import { api, ApiError } from "../../app/api";
import type { CrObject } from "./CrPanel";

// In-UI editor for ReferenceRepo.spec.labelRoutes (design §3, ISI-7514, follow-up
// to P3-3). A ReferenceRepo's token/webhook Secret refs stay GitOps-authored, so
// this editor touches ONLY spec.labelRoutes: it round-trips the full object
// (GET → replace labelRoutes → PUT) so the Secret refs, prTarget, and owner are
// preserved untouched and never surfaced in the form. The server stays
// authoritative — the §6.3 store gates the PUT to Admins and the admission
// webhook (app/internal/webhook/referencerepo_webhook.go) re-validates. The
// client checks below only mirror that for fast feedback.

// Enum sets mirror app/api/v1alpha1/referencerepo_types.go (LabelRoute).
const INPUT_FORMATS = ["auto", "teleprompter", "livestream", "blog", "podcast"] as const;
const OUTPUT_FORMATS = ["blog", "short", "tutorial", "teleprompter"] as const;

interface Route {
  label: string;
  inputFormat: string;
  outputFormat: string;
}

function toRoutes(o: CrObject): Route[] {
  const raw = (o.spec?.labelRoutes as unknown[]) ?? [];
  return raw.map((r) => {
    const rt = (r ?? {}) as Record<string, unknown>;
    return {
      label: typeof rt.label === "string" ? rt.label : "",
      inputFormat: typeof rt.inputFormat === "string" && rt.inputFormat ? rt.inputFormat : "auto",
      outputFormat: typeof rt.outputFormat === "string" && rt.outputFormat ? rt.outputFormat : "blog",
    };
  });
}

// validate mirrors the webhook + CRD schema: ≥1 route, each label non-empty and
// unique, and a valid output format. Returns null when the routes are savable.
function validate(routes: Route[]): string | null {
  if (routes.length === 0) return "A reference repo needs at least one label route.";
  const seen = new Set<string>();
  for (const r of routes) {
    const label = r.label.trim();
    if (!label) return "Every route needs a label.";
    if (seen.has(label)) return `Label "${label}" appears more than once.`;
    seen.add(label);
    if (!OUTPUT_FORMATS.includes(r.outputFormat as (typeof OUTPUT_FORMATS)[number])) {
      return `Route "${label}" needs an output format.`;
    }
  }
  return null;
}

export function EditRoutesButton({
  repo,
  path,
  onSaved,
}: {
  repo: CrObject;
  path: string;
  onSaved: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
        Edit routes
      </Button>
      {open && (
        <RoutesDialog
          repo={repo}
          path={path}
          onClose={() => setOpen(false)}
          onSaved={() => {
            setOpen(false);
            onSaved();
          }}
        />
      )}
    </>
  );
}

function RoutesDialog({
  repo,
  path,
  onClose,
  onSaved,
}: {
  repo: CrObject;
  path: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { toast } = useToast();
  const [routes, setRoutes] = useState<Route[]>(() => toRoutes(repo));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const invalid = validate(routes);

  function patch(i: number, next: Partial<Route>) {
    setRoutes((rs) => rs.map((r, j) => (j === i ? { ...r, ...next } : r)));
  }
  function addRoute() {
    setRoutes((rs) => [...rs, { label: "", inputFormat: "auto", outputFormat: "blog" }]);
  }
  function removeRoute(i: number) {
    setRoutes((rs) => rs.filter((_, j) => j !== i));
  }

  async function save() {
    const problem = validate(routes);
    if (problem) {
      setErr(problem);
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      // Re-fetch the full object so the PUT carries a fresh resourceVersion and
      // the untouched Secret refs / prTarget / owner — we only swap labelRoutes.
      const name = repo.metadata.name;
      const current = await api.get<CrObject>(`${path}/${encodeURIComponent(name)}`);
      const body = {
        ...current,
        spec: {
          ...(current.spec ?? {}),
          labelRoutes: routes.map((r) => ({
            label: r.label.trim(),
            inputFormat: r.inputFormat,
            outputFormat: r.outputFormat,
          })),
        },
      };
      await api.put(`${path}/${encodeURIComponent(name)}`, body);
      toast({ title: "Routes saved", tone: "success" });
      onSaved();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : e instanceof Error ? e.message : "request failed");
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={`Label routes — ${repo.metadata.name}`}>
        <DialogBody className="space-y-4">
          <p className="text-sm text-text-muted">
            Map each GitHub label to an input/output format. Token &amp; webhook secrets stay
            GitOps-managed and are not editable here.
          </p>
          {routes.length === 0 ? (
            <p className="text-sm text-text-muted">No routes yet — add one below.</p>
          ) : (
            <div className="space-y-3">
              {routes.map((r, i) => (
                <div key={i} className="grid grid-cols-[1fr_auto_auto_auto] items-end gap-2">
                  <Field label={i === 0 ? "Label" : ""}>
                    {({ id }) => (
                      <Input
                        id={id}
                        value={r.label}
                        placeholder="recycle:blog"
                        onChange={(e) => patch(i, { label: e.target.value })}
                      />
                    )}
                  </Field>
                  <Field label={i === 0 ? "Input" : ""}>
                    {() => (
                      <Select value={r.inputFormat} onValueChange={(v) => patch(i, { inputFormat: v })}>
                        <SelectTrigger className="w-36" aria-label={`Input format for route ${i + 1}`}>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {INPUT_FORMATS.map((f) => (
                            <SelectItem key={f} value={f}>
                              {f}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    )}
                  </Field>
                  <Field label={i === 0 ? "Output" : ""}>
                    {() => (
                      <Select value={r.outputFormat} onValueChange={(v) => patch(i, { outputFormat: v })}>
                        <SelectTrigger className="w-36" aria-label={`Output format for route ${i + 1}`}>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {OUTPUT_FORMATS.map((f) => (
                            <SelectItem key={f} value={f}>
                              {f}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    )}
                  </Field>
                  <Button variant="ghost" size="sm" onClick={() => removeRoute(i)} aria-label={`Remove route ${i + 1}`}>
                    Remove
                  </Button>
                </div>
              ))}
            </div>
          )}
          <Button variant="ghost" size="sm" onClick={addRoute}>
            Add route
          </Button>
          {err && <p className="text-sm text-warn">{err}</p>}
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} loading={busy} disabled={!!invalid}>
            Save routes
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
