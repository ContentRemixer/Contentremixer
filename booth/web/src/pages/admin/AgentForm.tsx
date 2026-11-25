import { useEffect, useState } from "react";
import {
  Badge,
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

// ---- skillPacks chips --------------------------------------------------------

// SkillPacksField is the "modify skills" multiselect: current packs as removable
// chips + a free-text add. Discovery (GET /v1/skillpacks from the reference repo)
// is a flagged follow-up (UX spec §1.4); until it lands this degrades to
// free-text-with-suggestions, which is contract-correct anyway — the CRD says
// unknown packs degrade (warn) at run time, they never gate. So a typed-but-
// unknown pack is allowed and only hinted, never blocked. Dedup + trim + order
// preserved (the CRD is listType=atomic).
export function SkillPacksField({
  packs,
  onChange,
  suggestions = [],
}: {
  packs: string[];
  onChange: (next: string[]) => void;
  suggestions?: string[];
}) {
  const [draft, setDraft] = useState("");

  function add(raw: string) {
    const v = raw.trim();
    if (!v) return;
    if (packs.includes(v)) {
      setDraft("");
      return;
    }
    onChange([...packs, v]);
    setDraft("");
  }
  function remove(i: number) {
    onChange(packs.filter((_, j) => j !== i));
  }

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
        {packs.length === 0 ? (
          <span className="text-sm text-text-muted">No skill packs — the skill's catalog default applies.</span>
        ) : (
          packs.map((p, i) => {
            const unknown = suggestions.length > 0 && !suggestions.includes(p);
            return (
              <span
                key={p}
                className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-0.5 text-xs text-text"
              >
                {p}
                {unknown && (
                  <span className="text-text-muted" title="not found in reference repo — will be ignored at run time">
                    (?)
                  </span>
                )}
                <button
                  type="button"
                  aria-label={`Remove skill pack ${p}`}
                  className="text-text-muted hover:text-text"
                  onClick={() => remove(i)}
                >
                  ×
                </button>
              </span>
            );
          })
        )}
      </div>
      <div className="flex gap-2">
        <Input
          value={draft}
          placeholder="add a pack, e.g. concise"
          list={suggestions.length ? "skillpack-suggestions" : undefined}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add(draft);
            }
          }}
        />
        {suggestions.length > 0 && (
          <datalist id="skillpack-suggestions">
            {suggestions.map((s) => (
              <option key={s} value={s} />
            ))}
          </datalist>
        )}
        <Button type="button" variant="ghost" size="sm" onClick={() => add(draft)} disabled={!draft.trim()}>
          Add
        </Button>
      </div>
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
          <Field label="Skill packs" hint="Prompt packs layered on the skill. Override the catalog default; unknown packs are ignored at run time.">
            {() => <SkillPacksField packs={f.skillPacks} onChange={(next) => set("skillPacks", next)} />}
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
            Skill packs layered on top of the{" "}
            <Badge tone="info">{typeof agent.spec?.skillKind === "string" ? agent.spec.skillKind : "?"}</Badge> skill.
            They override the catalog composition for this agent's runs. Unknown packs are ignored at run time.
          </p>
          <SkillPacksField packs={packs} onChange={setPacks} />
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
