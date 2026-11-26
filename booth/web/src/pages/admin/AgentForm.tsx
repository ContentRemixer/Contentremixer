import { useEffect, useState } from "react";
import {
  Badge,
  Button,
  Checkbox,
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
  Textarea,
  useToast,
} from "../../ui";
import { api, ApiError } from "../../app/api";
import type { CrObject } from "./CrPanel";

// Bespoke create + scoped-edit form for Agent (ISI-7803 P2, "add extra agents"
// and "modify skills"). The generic CrPanel flat-create cannot express the
// skill-kind/model Selects or the skillPacks chips, so this form owns them —
// same pattern as ReferenceRepoForm (ISI-7782). No new backend route: create
// POSTs /v1/agents, edit GETs the one Agent and PUTs the round-tripped object
// with ONLY spec.skillPacks swapped (every other field preserved untouched —
// the LabelRoutesEditor GET→swap→PUT discipline). The server stays
// authoritative: the §6.3 store gates writes to Admins and the Agent admission
// webhook re-validates (skillKind, one-default-per-kind, modelProfile resolves);
// its aggregated error surfaces inline below.

// SKILL_KINDS mirrors v1alpha1.KnownSkillKinds (agent_types.go) — a CLOSED set,
// so the create dialog offers a Select, never free text (webhook rule A1).
const SKILL_KINDS = [
  "architect",
  "research",
  "coding",
  "observability",
  "graphic",
  "writer",
] as const;

// ACTOR_TEMPLATES mirrors the ADR-3 ActorTemplate enum.
const ACTOR_TEMPLATES = ["content", "media"] as const;

// NAME_PATTERN is the metadata.name (RFC 1123 label) the apiserver enforces.
const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

// ---- skill-pack catalog (ISI-7856 W1) ----------------------------------------

// Pack mirrors one row of GET /v1/skillpacks .packs (booth/internal/skillpacks).
// title/description degrade to the name when the SKILL.md omits them.
interface Pack {
  name: string;
  title?: string;
  description?: string;
}

// SkillPacksData is the GET /v1/skillpacks read model (ISI-7856): the discovered
// catalog, the explicit per-content-format default composition (keyed by the
// format / agent handle — e.g. "content-writer", "tutorial" — NOT the skillKind
// enum, see writer/skills.go resolveSkillPacks), and whether an unlisted key
// falls back to a single pack named after the key.
interface SkillPacksData {
  packs: Pack[];
  defaults: Record<string, string[]>;
  dynamicFallback: boolean;
}

// useSkillPacks fetches the catalog + default composition once. Degrade-never:
// a load error (endpoint absent on an older Booth, or Admin-gate miss) leaves an
// empty catalog + failed:true so the dialog still works as free-text and the
// empty state can explain it — never a worse state than before W1 (spec §2).
function useSkillPacks(): { data: SkillPacksData; failed: boolean; loaded: boolean } {
  const [data, setData] = useState<SkillPacksData>({ packs: [], defaults: {}, dynamicFallback: false });
  const [failed, setFailed] = useState(false);
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    api
      .get<SkillPacksData | null>("/v1/skillpacks")
      .then((d) =>
        setData({
          packs: Array.isArray(d?.packs) ? d!.packs : [],
          defaults: d?.defaults ?? {},
          dynamicFallback: !!d?.dynamicFallback,
        }),
      )
      .catch(() => setFailed(true))
      .finally(() => setLoaded(true));
  }, []);
  return { data, failed, loaded };
}

// defaultPacksFor resolves the ordered default composition the platform runs for
// an agent before any override. The composition is keyed by the agent handle /
// content format (spec §2 example "content-writer → […]"), so we look the name up
// directly; an unlisted name degrades to the dynamic single-pack rule the server
// advertises. The seeded writer agent is named "content-writer", which hits the
// explicit entry; hand-created agents hit the single-pack fallback.
function defaultPacksFor(name: string, data: SkillPacksData): string[] {
  const explicit = data.defaults[name];
  if (explicit && explicit.length) return explicit;
  return data.dynamicFallback && name ? [name] : [];
}

// ---- skillPacks editor (your packs: picker + removable chips) ----------------

// SkillPacksField is the editable "Your skill packs" overlay: removable chips for
// the chosen packs + a searchable catalog picker (ISI-7856) + a free-text escape
// hatch for packs not yet in the catalog. An override, when non-empty, REPLACES
// the default composition at run time (writer/skills.go resolveSkillPacks); the
// replace is made visually unambiguous by DefaultPacksSection above. Dedup + trim
// + order preserved (the CRD is listType=atomic); unknown packs are hinted, never
// blocked (they degrade — warn — at run time, never gate).
export function SkillPacksField({
  packs,
  onChange,
  catalog = [],
}: {
  packs: string[];
  onChange: (next: string[]) => void;
  catalog?: Pack[];
}) {
  const [pickerOpen, setPickerOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [draft, setDraft] = useState("");

  const catalogNames = new Set(catalog.map((p) => p.name));
  function add(raw: string) {
    const v = raw.trim();
    if (!v || packs.includes(v)) {
      setDraft("");
      return;
    }
    onChange([...packs, v]);
    setDraft("");
  }
  function toggle(name: string) {
    onChange(packs.includes(name) ? packs.filter((p) => p !== name) : [...packs, name]);
  }
  function remove(name: string) {
    onChange(packs.filter((p) => p !== name));
  }

  const q = query.trim().toLowerCase();
  const filtered = catalog.filter(
    (p) =>
      !q ||
      p.name.toLowerCase().includes(q) ||
      (p.title ?? "").toLowerCase().includes(q) ||
      (p.description ?? "").toLowerCase().includes(q),
  );

  return (
    <div className="space-y-2">
      {/* chosen packs as removable chips */}
      <div className="flex flex-wrap gap-2">
        {packs.length === 0 ? (
          <span className="text-sm text-text-muted">No added packs — this agent uses its default packs.</span>
        ) : (
          packs.map((p) => {
            const unknown = catalog.length > 0 && !catalogNames.has(p);
            return (
              <span
                key={p}
                className="inline-flex items-center gap-1 rounded-md border border-border bg-surface px-2 py-0.5 text-xs text-text"
              >
                {p}
                {unknown && (
                  <span className="text-text-muted" title="Not in the skill-pack catalog — ignored at run time.">
                    (?)
                  </span>
                )}
                <button
                  type="button"
                  aria-label={`Remove skill pack ${p}`}
                  className="text-text-muted hover:text-text"
                  onClick={() => remove(p)}
                >
                  ×
                </button>
              </span>
            );
          })
        )}
      </div>

      {!pickerOpen ? (
        <Button type="button" variant="ghost" size="sm" onClick={() => setPickerOpen(true)}>
          + Add packs
        </Button>
      ) : (
        <div className="space-y-2 rounded-md border border-border p-2">
          <Input
            value={query}
            placeholder="Search packs…"
            aria-label="Search packs"
            onChange={(e) => setQuery(e.target.value)}
          />
          {catalog.length > 0 ? (
            <ul role="listbox" aria-label="Skill packs" className="max-h-48 space-y-1 overflow-y-auto">
              {filtered.length === 0 ? (
                <li className="px-1 py-1 text-xs text-text-muted">No packs match "{query}".</li>
              ) : (
                filtered.map((p) => {
                  const checked = packs.includes(p.name);
                  return (
                    <li key={p.name}>
                      {/* One click target per option: a focusable button toggles
                          the pack (Enter/Space parity); the Checkbox is a purely
                          visual indicator so clicking anywhere toggles once. */}
                      <button
                        type="button"
                        role="option"
                        aria-selected={checked}
                        className="flex w-full items-start gap-2 rounded-md px-1 py-1 text-left hover:bg-surface"
                        onClick={() => toggle(p.name)}
                      >
                        <Checkbox checked={checked} tabIndex={-1} aria-hidden className="pointer-events-none mt-0.5" />
                        <span>
                          <span className="block text-sm font-medium text-text">{p.title || p.name}</span>
                          {p.description && <span className="block text-xs text-text-muted">{p.description}</span>}
                        </span>
                      </button>
                    </li>
                  );
                })
              )}
            </ul>
          ) : (
            <p className="text-xs text-text-muted">The pack catalog is unavailable — add packs by name below.</p>
          )}
          {/* escape hatch: packs not (yet) in the catalog */}
          <div className="space-y-1 border-t border-border pt-2">
            <span className="text-xs text-text-muted">Can't find it? Add by name</span>
            <div className="flex gap-2">
              <Input
                value={draft}
                placeholder="e.g. concise"
                aria-label="Add pack by name"
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    add(draft);
                  }
                }}
              />
              <Button type="button" variant="ghost" size="sm" onClick={() => add(draft)} disabled={!draft.trim()}>
                Add
              </Button>
            </div>
            <span className="text-xs text-text-muted">Unknown packs are kept, but ignored at run time.</span>
          </div>
          <div className="flex justify-end">
            <Button type="button" variant="ghost" size="sm" onClick={() => setPickerOpen(false)}>
              Done
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

// ---- default composition (read-only) + replace banner ------------------------

// DefaultPacksSection renders the read-only default composition (spec §3 D1) so a
// default agent never looks unequipped. When the agent has override packs the
// defaults are struck-through + tagged "⊘ replaced" and a warn banner states the
// replace plainly (State C) — no silent swap. Removing the last override returns
// to State A with no save (live preview). The chips are not focusable and carry
// no ×: they are not editable here.
function DefaultPacksSection({
  defaultPacks,
  kind,
  overridden,
  failed,
}: {
  defaultPacks: string[];
  kind: string;
  overridden: boolean;
  failed: boolean;
}) {
  if (failed) {
    return <p className="text-sm text-text-muted">Using the default skill packs for this skill.</p>;
  }
  if (defaultPacks.length === 0) return null;
  return (
    <div className="space-y-1">
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium text-text">{overridden ? "Default skill packs (overridden)" : "Default skill packs"}</span>
      </div>
      <div
        className="flex flex-wrap gap-2"
        aria-label={`Default skill packs from the ${kind} skill (read-only)`}
      >
        {defaultPacks.map((p) => (
          <span
            key={p}
            className={
              "inline-flex items-center gap-1 rounded-md border border-dashed border-border px-2 py-0.5 text-xs " +
              (overridden ? "text-text-muted line-through" : "text-text")
            }
          >
            {p}
            {overridden && <span className="text-text-muted no-underline">⊘ replaced</span>}
          </span>
        ))}
      </div>
      {overridden ? (
        <p className="rounded-md border border-warn/40 bg-warn/10 px-2 py-1 text-xs text-warn">
          ⚠ Your packs replace the {kind} default. The {defaultPacks.length} default pack
          {defaultPacks.length === 1 ? "" : "s"} above will NOT run — only the packs you choose below run.
        </p>
      ) : (
        <p className="text-xs text-text-muted">These run as-is. The {kind} skill composes them for you.</p>
      )}
    </div>
  );
}

// ---- helpers -----------------------------------------------------------------

// toPacks reads spec.skillPacks[] off a raw Agent CR.
function toPacks(o: CrObject | null): string[] {
  const raw = (o?.spec?.skillPacks as unknown[]) ?? [];
  return raw.filter((p): p is string => typeof p === "string" && p.trim() !== "");
}

// useModelProfiles fetches the live litellmName options for the model Select
// (webhook rule A2 — only real profiles + "default" are offered). Best-effort:
// a load error leaves just "default" so the dialog still works.
function useModelProfiles(): string[] {
  const [names, setNames] = useState<string[]>([]);
  useEffect(() => {
    api
      .get<CrObject[] | null>("/v1/modelprofiles")
      .then((rows) => {
        const ns = (rows ?? [])
          .map((r) => (typeof r.spec?.litellmName === "string" ? r.spec.litellmName : ""))
          .filter((n) => n);
        setNames(ns);
      })
      .catch(() => setNames([]));
  }, []);
  return names;
}

// ---- create ------------------------------------------------------------------

interface CreateState {
  name: string;
  role: string;
  skillKind: string;
  modelProfile: string; // "default" sentinel → omitted in the body
  actorTemplate: string;
  description: string;
  skillPacks: string[];
}

function emptyCreate(): CreateState {
  return {
    name: "",
    role: "",
    skillKind: "coding",
    modelProfile: "default",
    actorTemplate: "content",
    description: "",
    skillPacks: [],
  };
}

// validateAgent mirrors the webhook/CRD for fast feedback; the server stays
// authoritative. Kind/model/actor are Selects, so only name + role are checkable
// here (A1/A2 are structurally impossible to violate from the UI).
function validateAgent(f: CreateState): string | null {
  if (!f.name.trim()) return "A name is required.";
  if (!NAME_PATTERN.test(f.name.trim())) {
    return "Name must be lowercase alphanumeric or '-', starting and ending alphanumeric.";
  }
  if (!f.role.trim()) return "A role is required.";
  return null;
}

export function NewAgentButton({ path, onSaved }: { path: string; onSaved: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        New
      </Button>
      {open && (
        <CreateAgentDialog
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

function CreateAgentDialog({
  path,
  onClose,
  onSaved,
}: {
  path: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { toast } = useToast();
  const [f, setF] = useState<CreateState>(emptyCreate);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const models = useModelProfiles();
  const { data: skillPacks } = useSkillPacks();

  const invalid = validateAgent(f);
  function set<K extends keyof CreateState>(key: K, value: CreateState[K]) {
    setF((prev) => ({ ...prev, [key]: value }));
  }

  async function submit() {
    const problem = validateAgent(f);
    if (problem) {
      setErr(problem);
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      await api.post(path, {
        metadata: { name: f.name.trim() },
        spec: {
          role: f.role.trim(),
          skillKind: f.skillKind,
          modelProfile: f.modelProfile === "default" ? undefined : f.modelProfile,
          actorTemplate: f.actorTemplate || undefined,
          skillPacks: f.skillPacks.length ? f.skillPacks : undefined,
          description: f.description.trim() || undefined,
          // spec.default is intentionally never set here: new admin agents are
          // always default:false — only the seeder sets default:true, and the
          // webhook allows at most one default per kind.
        },
      });
      toast({ title: "Agent created", tone: "success" });
      onSaved();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : e instanceof Error ? e.message : "request failed");
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title="New agent">
        <DialogBody className="space-y-4">
          <Field label="Name" required hint="Unique id, DNS-1123 lower-kebab, e.g. coding-fast.">
            {({ id }) => (
              <Input id={id} value={f.name} placeholder="coding-fast" onChange={(e) => set("name", e.target.value)} />
            )}
          </Field>
          <Field label="Role" required hint="Human label, e.g. Coding Specialist (fast).">
            {({ id }) => (
              <Input id={id} value={f.role} placeholder="Coding Specialist (fast)" onChange={(e) => set("role", e.target.value)} />
            )}
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Skill kind" required hint="The Go behaviour this agent runs.">
              {() => (
                <Select value={f.skillKind} onValueChange={(v) => set("skillKind", v)}>
                  <SelectTrigger aria-label="Skill kind">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {SKILL_KINDS.map((k) => (
                      <SelectItem key={k} value={k}>
                        {k}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </Field>
            <Field label="Actor template" hint="content (prose/code) or media (image/video).">
              {() => (
                <Select value={f.actorTemplate} onValueChange={(v) => set("actorTemplate", v)}>
                  <SelectTrigger aria-label="Actor template">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {ACTOR_TEMPLATES.map((a) => (
                      <SelectItem key={a} value={a}>
                        {a}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </Field>
          </div>
          <Field label="Model profile" hint="A ModelProfile litellmName, or default for the system default.">
            {() => (
              <Select value={f.modelProfile} onValueChange={(v) => set("modelProfile", v)}>
                <SelectTrigger aria-label="Model profile">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="default">default</SelectItem>
                  {models.map((m) => (
                    <SelectItem key={m} value={m}>
                      {m}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </Field>
          <Field
            label="Skill packs"
            hint="Optional prompt add-ons. Leave empty to use this skill's default packs; adding any replaces that default for this agent. Unknown packs are ignored at run time."
          >
            {() => (
              <SkillPacksField
                packs={f.skillPacks}
                onChange={(next) => set("skillPacks", next)}
                catalog={skillPacks.packs}
              />
            )}
          </Field>
          <Field label="Description" hint="Optional operator context.">
            {({ id }) => (
              <Textarea id={id} value={f.description} onChange={(e) => set("description", e.target.value)} />
            )}
          </Field>
          <p className="text-xs text-text-muted">
            New agents are always non-default — the seeded default agents are the canonical handle for each skill kind.
          </p>
          {err && <p className="text-sm text-warn">{err}</p>}
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} loading={busy} disabled={!!invalid}>
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---- scoped skillPacks edit ("Skills…") -------------------------------------

export function EditAgentSkillsButton({
  agent,
  path,
  onSaved,
}: {
  agent: CrObject;
  path: string;
  onSaved: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
        Skills…
      </Button>
      {open && (
        <EditSkillsDialog
          agent={agent}
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

function EditSkillsDialog({
  agent,
  path,
  onClose,
  onSaved,
}: {
  agent: CrObject;
  path: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { toast } = useToast();
  const [packs, setPacks] = useState<string[]>(() => toPacks(agent));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const { data: skillPacks, failed } = useSkillPacks();

  const kind = typeof agent.spec?.skillKind === "string" ? agent.spec.skillKind : "?";
  const defaultPacks = defaultPacksFor(agent.metadata.name, skillPacks);

  async function submit() {
    setBusy(true);
    setErr(null);
    try {
      // GET→swap one slice→PUT: re-fetch so the PUT carries a fresh
      // resourceVersion and every untouched field (role, skillKind, model,
      // owner, …) is preserved — only spec.skillPacks changes.
      const name = agent.metadata.name;
      const current = await api.get<CrObject>(`${path}/${encodeURIComponent(name)}`);
      await api.put(`${path}/${encodeURIComponent(name)}`, {
        ...current,
        spec: {
          ...(current.spec ?? {}),
          skillPacks: packs.length ? packs : undefined,
        },
      });
      toast({ title: "Skill packs saved", tone: "success" });
      onSaved();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : e instanceof Error ? e.message : "request failed");
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={`Skills — ${agent.metadata.name}`}>
        <DialogBody className="space-y-4">
          <p className="text-sm text-text-muted">
            A skill pack is an optional prompt add-on. This agent's skill is <Badge tone="info">{kind}</Badge>, which
            already runs a default set of packs (shown below). Any packs you add here replace that default for this
            agent's runs; unknown packs are ignored at run time.
          </p>
          <DefaultPacksSection
            defaultPacks={defaultPacks}
            kind={kind}
            overridden={packs.length > 0}
            failed={failed}
          />
          <div className="space-y-1">
            <span className="text-sm font-medium text-text">
              Your skill packs <span className="text-xs font-normal text-text-muted">(optional)</span>
            </span>
            <SkillPacksField packs={packs} onChange={setPacks} catalog={skillPacks.packs} />
          </div>
          {err && <p className="text-sm text-warn">{err}</p>}
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} loading={busy}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
