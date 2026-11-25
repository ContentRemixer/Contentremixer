import {
  Button,
  Field,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../../ui";
import type { CrObject } from "./CrPanel";

// Shared labelRoutes editing primitives (design §3). Introduced for ISI-7514's
// in-UI ReferenceRepo.spec.labelRoutes editor and reused by the bespoke
// create/edit form (ISI-7782, ReferenceRepoForm) so there is ONE routes editor,
// not two. A ReferenceRepo's token/webhook Secret refs stay GitOps-authored and
// are never surfaced here; callers that PUT round-trip the full object so the
// Secret refs, prTarget, and owner are preserved untouched. The server stays
// authoritative — the §6.3 store gates writes to Admins and the admission
// webhook (app/internal/webhook/referencerepo_webhook.go) re-validates. The
// client checks below only mirror that for fast feedback.

// Enum sets mirror app/api/v1alpha1/referencerepo_types.go (LabelRoute).
export const INPUT_FORMATS = ["auto", "teleprompter", "livestream", "blog", "podcast"] as const;
export const OUTPUT_FORMATS = ["blog", "short", "tutorial", "teleprompter", "demo", "video"] as const;

export interface Route {
  label: string;
  inputFormat: string;
  outputFormat: string;
}

export function emptyRoute(): Route {
  return { label: "", inputFormat: "auto", outputFormat: "blog" };
}

export function toRoutes(o: CrObject): Route[] {
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

// routesSpec serialises the editor rows into the CRD shape, trimming labels.
export function routesSpec(routes: Route[]): Array<Pick<Route, "label" | "inputFormat" | "outputFormat">> {
  return routes.map((r) => ({
    label: r.label.trim(),
    inputFormat: r.inputFormat,
    outputFormat: r.outputFormat,
  }));
}

// validateRoutes mirrors the webhook + CRD schema: ≥1 route, each label
// non-empty and unique, and a valid output format. Returns null when savable.
export function validateRoutes(routes: Route[]): string | null {
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

// RoutesFields renders the label→format rows with add/remove controls. It is a
// controlled component: the parent owns the routes array and persists it.
export function RoutesFields({
  routes,
  onChange,
}: {
  routes: Route[];
  onChange: (next: Route[]) => void;
}) {
  function patch(i: number, next: Partial<Route>) {
    onChange(routes.map((r, j) => (j === i ? { ...r, ...next } : r)));
  }
  function addRoute() {
    onChange([...routes, emptyRoute()]);
  }
  function removeRoute(i: number) {
    onChange(routes.filter((_, j) => j !== i));
  }

  return (
    <div className="space-y-3">
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
              <Button
                variant="ghost"
                size="sm"
                onClick={() => removeRoute(i)}
                aria-label={`Remove route ${i + 1}`}
              >
                Remove
              </Button>
            </div>
          ))}
        </div>
      )}
      <Button variant="ghost" size="sm" onClick={addRoute}>
        Add route
      </Button>
    </div>
  );
}
