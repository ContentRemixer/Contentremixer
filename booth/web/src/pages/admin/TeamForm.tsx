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
  useToast,
} from "../../ui";
import { api, ApiError } from "../../app/api";
import type { CrObject } from "./CrPanel";

// Bespoke create + scoped team editor for AgentTeam (ISI-7804 P3, "define the
// handoff, the role, the orchestration"). Mirrors the UX spec §2.2 table/list
// editor and the AgentForm / LabelRoutesEditor discipline: no new backend
// route — create POSTs /v1/agentteams, edit GETs the one team and PUTs the
// round-tripped object with ONLY spec.entry/nodes/edges swapped (default and
// owner preserved untouched). The server stays authoritative: the §6.3 store
// gates writes to Admins and the AgentTeam admission webhook re-validates
// (entry declared, unique node ids, edge endpoints, acyclic, reachable, agentRef
// resolves, writer rule, one default team); its aggregated error surfaces
// inline below.

export interface TeamNode {
  id: string;
  agentRef: string;
}

export interface TeamEdge {
  from: string;
  to: string;
}

// NAME_PATTERN is the metadata.name (RFC 1123 label) the apiserver enforces.
const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

// ---- CR (de)serialization ----------------------------------------------------

function toNodes(o: CrObject | null): TeamNode[] {
  const raw = (o?.spec?.nodes as unknown[]) ?? [];
  return raw.map((r) => {
    const n = (r ?? {}) as Record<string, unknown>;
    return {
      id: typeof n.id === "string" ? n.id : "",
      agentRef: typeof n.agentRef === "string" ? n.agentRef : "",
    };
  });
}

function toEdges(o: CrObject | null): TeamEdge[] {
  const raw = (o?.spec?.edges as unknown[]) ?? [];
  return raw.map((r) => {
    const e = (r ?? {}) as Record<string, unknown>;
    return {
      from: typeof e.from === "string" ? e.from : "",
      to: typeof e.to === "string" ? e.to : "",
    };
  });
}

function toEntry(o: CrObject | null): string {
  const v = o?.spec?.entry;
  return typeof v === "string" ? v : "";
}

// ---- graph helpers (shared with a future DAG-canvas follow-up) ---------------

// findCycle returns one cycle as a node-id path (["a", "b", "a"]) or null.
export function findCycle(nodes: TeamNode[], edges: TeamEdge[]): string[] | null {
  const adj = new Map<string, string[]>();
  for (const e of edges) {
    if (!e.from || !e.to || e.from === e.to) continue;
    adj.set(e.from, [...(adj.get(e.from) ?? []), e.to]);
  }
  const state = new Map<string, number>(); // 0 = new, 1 = on stack, 2 = done
  const stack: string[] = [];
  function dfs(n: string): string[] | null {
    state.set(n, 1);
    stack.push(n);
    for (const m of adj.get(n) ?? []) {
      if (state.get(m) === 1) {
        const start = stack.indexOf(m);
        return [...stack.slice(start), m];
      }
      if ((state.get(m) ?? 0) === 0) {
        const cycle = dfs(m);
        if (cycle) return cycle;
      }
    }
    stack.pop();
    state.set(n, 2);
    return null;
  }
  for (const n of nodes) {
    if ((state.get(n.id) ?? 0) === 0) {
      const c = dfs(n.id);
      if (c) return c;
    }
  }
  return null;
}

// unreachableNodes returns the node ids not reachable from entry.
export function unreachableNodes(entry: string, nodes: TeamNode[], edges: TeamEdge[]): Set<string> {
  const adj = new Map<string, string[]>();
  for (const e of edges) {
    if (e.from && e.to) adj.set(e.from, [...(adj.get(e.from) ?? []), e.to]);
  }
  const seen = new Set<string>([entry]);
  const queue = [entry];
  while (queue.length) {
    const n = queue.shift() as string;
    for (const m of adj.get(n) ?? []) {
      if (!seen.has(m)) {
        seen.add(m);
        queue.push(m);
      }
    }
  }
  return new Set(nodes.map((n) => n.id).filter((id) => !seen.has(id)));
}

// validateTeam mirrors the AgentTeam webhook (UX spec §3.2): returns the first
// blocking message or null when savable. T6 (cycle) and T7 (orphan) are the two
// affordances the spec calls out — the banner + disabled Save below surface them.
export function validateTeam(
  entry: string,
  nodes: TeamNode[],
  edges: TeamEdge[],
  knownAgents: string[],
): string | null {
  if (nodes.length === 0) return "A team needs at least one node.";
  const ids = new Set<string>();
  for (const n of nodes) {
    if (!n.id.trim()) return "Every node needs an id.";
    if (ids.has(n.id)) return 'Node id "' + n.id + '" is used more than once.';
    ids.add(n.id);
    if (!n.agentRef) return 'Node "' + n.id + '" needs an agent.';
    if (knownAgents.length > 0 && !knownAgents.includes(n.agentRef)) {
      return 'Node "' + n.id + '" references an unknown agent.';
    }
  }
  if (!ids.has(entry)) return "Entry must be one of the team's nodes.";
  for (const e of edges) {
    if (!ids.has(e.from) || !ids.has(e.to)) return "Every handoff must connect two declared nodes.";
    if (e.from === e.to) return 'A node cannot hand off to itself ("' + e.from + '").';
  }
  const cycle = findCycle(nodes, edges);
  if (cycle) return "This creates a cycle: " + cycle.join(" → ") + ". Hand-offs must flow one way.";
  const orphans = unreachableNodes(entry, nodes, edges);
  if (orphans.size > 0) {
    return 'Node "' + [...orphans][0] + '" is never reached from the entry node.';
  }
  return null;
}

// ---- shared editor fields -----------------------------------------------------

// useAgents loads the agentRef Select options: live /v1/agents rows rendered
// "role — name" (UX spec §2.2: pick "Coding Specialist — coding", not a bare
// id). Best-effort like useModelProfiles — a load error leaves the options
// empty so validateTeam skips the agent-known check (server still authoritative).
function useAgents(): Array<{ name: string; role: string }> {
  const [rows, setRows] = useState<Array<{ name: string; role: string }>>([]);
  useEffect(() => {
    api
      .get<CrObject[] | null>("/v1/agents")
      .then((rs) => {
        setRows(
          (rs ?? []).map((r) => ({
            name: r.metadata.name,
            role: typeof r.spec?.role === "string" ? r.spec.role : "",
          })),
        );
      })
      .catch(() => setRows([]));
  }, []);
  return rows;
}

// TeamEditorFields is the §2.2 table/list editor: entry Select, node rows
// (id Input + agentRef Select), handoff rows (from → to Selects over declared
// node ids), with the cycle banner and unreachable pills from §3.2.
export function TeamEditorFields({
  entry,
  nodes,
  edges,
  agents,
  onEntry,
  onNodes,
  onEdges,
}: {
  entry: string;
  nodes: TeamNode[];
  edges: TeamEdge[];
  agents: Array<{ name: string; role: string }>;
  onEntry: (v: string) => void;
  onNodes: (next: TeamNode[]) => void;
  onEdges: (next: TeamEdge[]) => void;
}) {
  const nodeIds = nodes.map((n) => n.id).filter(Boolean);
  const orphans = unreachableNodes(entry, nodes, edges);
  const cycle = findCycle(nodes, edges);
  const cycleSet = new Set(cycle ?? []);

  function patchNode(i: number, next: Partial<TeamNode>) {
    onNodes(nodes.map((n, j) => (j === i ? { ...n, ...next } : n)));
  }
  function patchEdge(i: number, next: Partial<TeamEdge>) {
    onEdges(edges.map((e, j) => (j === i ? { ...e, ...next } : e)));
  }

  return (
    <div className="space-y-4">
      <Field label="Entry" hint="The node the run starts from — its outline is adopted downstream.">
        {() => (
          <Select value={entry} onValueChange={onEntry}>
            <SelectTrigger aria-label="Entry node">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {nodeIds.map((id) => (
                <SelectItem key={id} value={id}>
                  {id}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </Field>

      <div className="space-y-2">
        <p className="text-sm font-medium text-text">Nodes</p>
        {nodes.map((n, i) => (
          <div key={i} className="flex items-end gap-2">
            <Field label={i === 0 ? "Node id" : ""}>
              {({ id }) => (
                <Input
                  id={id}
                  value={n.id}
                  placeholder="plan"
                  aria-label={"Node " + (i + 1) + " id"}
                  onChange={(e) => patchNode(i, { id: e.target.value })}
                />
              )}
            </Field>
            <Field label={i === 0 ? "Agent" : ""}>
              {() => (
                <Select value={n.agentRef} onValueChange={(v) => patchNode(i, { agentRef: v })}>
                  <SelectTrigger className="w-64" aria-label={"Agent for node " + (i + 1)}>
                    <SelectValue placeholder="agent…" />
                  </SelectTrigger>
                  <SelectContent>
                    {agents.map((a) => (
                      <SelectItem key={a.name} value={a.name}>
                        {(a.role ? a.role + " — " : "") + a.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </Field>
            {orphans.has(n.id) && (
              <Badge tone="warning" className="mb-2">
                unreachable
              </Badge>
            )}
            <Button
              variant="ghost"
              size="sm"
              onClick={() => onNodes(nodes.filter((_, j) => j !== i))}
              aria-label={"Remove node " + (i + 1)}
            >
              Remove
            </Button>
          </div>
        ))}
        <Button variant="ghost" size="sm" onClick={() => onNodes([...nodes, { id: "", agentRef: "" }])}>
          Add node
        </Button>
      </div>

      <div className="space-y-2">
        <p className="text-sm font-medium text-text">Handoffs</p>
        {cycle && (
          <div className="rounded border-l-4 border-warn bg-warn/10 px-3 py-2 text-sm text-warn" role="alert">
            This creates a cycle: {cycle.join(" → ")}. Hand-offs must flow one way.
          </div>
        )}
        {edges.length === 0 && (
          <p className="text-sm text-text-muted">
            No handoffs — a single-node team runs the linear path (the content-default shape).
          </p>
        )}
        {edges.map((e, i) => {
          const flagged = cycleSet.has(e.from) && cycleSet.has(e.to);
          return (
            <div
              key={i}
              className={"flex items-end gap-2 " + (flagged ? "rounded border-l-4 border-warn pl-2" : "")}
            >
              <Field label={i === 0 ? "From" : ""}>
                {() => (
                  <Select value={e.from} onValueChange={(v) => patchEdge(i, { from: v })}>
                    <SelectTrigger className="w-40" aria-label={"Handoff " + (i + 1) + " from"}>
                      <SelectValue placeholder="node…" />
                    </SelectTrigger>
                    <SelectContent>
                      {nodeIds.map((id) => (
                        <SelectItem key={id} value={id}>
                          {id}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                )}
              </Field>
              <span className="pb-2 text-text-muted">→</span>
              <Field label={i === 0 ? "To" : ""}>
                {() => (
                  <Select value={e.to} onValueChange={(v) => patchEdge(i, { to: v })}>
                    <SelectTrigger className="w-40" aria-label={"Handoff " + (i + 1) + " to"}>
                      <SelectValue placeholder="node…" />
                    </SelectTrigger>
                    <SelectContent>
                      {nodeIds.map((id) => (
                        <SelectItem key={id} value={id}>
                          {id}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                )}
              </Field>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => onEdges(edges.filter((_, j) => j !== i))}
                aria-label={"Remove handoff " + (i + 1)}
              >
                Remove
              </Button>
            </div>
          );
        })}
        <Button
          variant="ghost"
          size="sm"
          onClick={() => onEdges([...edges, { from: "", to: "" }])}
        >
          Add handoff
        </Button>
      </div>
    </div>
  );
}

// ---- create -------------------------------------------------------------------

export function NewTeamButton({ path, onSaved }: { path: string; onSaved: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        New team
      </Button>
      {open && (
        <CreateTeamDialog
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

function CreateTeamDialog({
  path,
  onClose,
  onSaved,
}: {
  path: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { toast } = useToast();
  const [name, setName] = useState("");
  const [entry, setEntry] = useState("");
  const [nodes, setNodes] = useState<TeamNode[]>([{ id: "", agentRef: "" }]);
  const [edges, setEdges] = useState<TeamEdge[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const agents = useAgents();

  const invalid =
    (name.trim() ? (NAME_PATTERN.test(name.trim()) ? null : "invalid name") : "name required") ??
    validateTeam(entry, nodes, edges, agents.map((a) => a.name));

  async function submit() {
    setBusy(true);
    setErr(null);
    try {
      await api.post(path, {
        metadata: { name: name.trim() },
        spec: {
          entry: entry,
          nodes: nodes.map((n) => ({ id: n.id.trim(), agentRef: n.agentRef })),
          edges: edges.filter((e) => e.from && e.to).map((e) => ({ from: e.from, to: e.to })),
          // spec.default is intentionally never set here: only one team may be
          // the default and the seeded default stays until an admin flips it.
        },
      });
      toast({ title: "Team created", tone: "success" });
      onSaved();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : e instanceof Error ? e.message : "request failed");
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title="New team">
        <DialogBody className="space-y-4">
          <Field label="Name" required hint="Unique id, DNS-1123 lower-kebab, e.g. tutorial-fast.">
            {({ id }) => (
              <Input
                id={id}
                value={name}
                placeholder="tutorial-fast"
                onChange={(e) => setName(e.target.value)}
              />
            )}
          </Field>
          <TeamEditorFields
            entry={entry}
            nodes={nodes}
            edges={edges}
            agents={agents}
            onEntry={setEntry}
            onNodes={setNodes}
            onEdges={setEdges}
          />
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

// ---- scoped edit ("Team…") -----------------------------------------------------

export function EditTeamButton({
  team,
  path,
  onSaved,
}: {
  team: CrObject;
  path: string;
  onSaved: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
        Team…
      </Button>
      {open && (
        <EditTeamDialog
          team={team}
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

function EditTeamDialog({
  team,
  path,
  onClose,
  onSaved,
}: {
  team: CrObject;
  path: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { toast } = useToast();
  const [entry, setEntry] = useState(() => toEntry(team));
  const [nodes, setNodes] = useState<TeamNode[]>(() => toNodes(team));
  const [edges, setEdges] = useState<TeamEdge[]>(() => toEdges(team));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const agents = useAgents();

  const invalid = validateTeam(entry, nodes, edges, agents.map((a) => a.name));

  async function submit() {
    setBusy(true);
    setErr(null);
    try {
      // GET→swap the orchestration slice→PUT: re-fetch so the PUT carries a
      // fresh resourceVersion and every untouched field (spec.default, owner,
      // status) is preserved — only entry/nodes/edges change.
      const name = team.metadata.name;
      const current = await api.get<CrObject>(path + "/" + encodeURIComponent(name));
      await api.put(path + "/" + encodeURIComponent(name), {
        ...current,
        spec: {
          ...(current.spec ?? {}),
          entry: entry,
          nodes: nodes.map((n) => ({ id: n.id.trim(), agentRef: n.agentRef })),
          edges: edges.filter((e) => e.from && e.to).map((e) => ({ from: e.from, to: e.to })),
        },
      });
      toast({ title: "Team saved", tone: "success" });
      onSaved();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : e instanceof Error ? e.message : "request failed");
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={"Team — " + team.metadata.name}>
        <DialogBody className="space-y-4">
          <p className="text-sm text-text-muted">
            Define the hand-off graph: nodes are agents, edges are hand-offs, and the entry node starts the run.
            Edits are picked up live — the next run uses them without a restart.
          </p>
          <TeamEditorFields
            entry={entry}
            nodes={nodes}
            edges={edges}
            agents={agents}
            onEntry={setEntry}
            onNodes={setNodes}
            onEdges={setEdges}
          />
          {err && <p className="text-sm text-warn">{err}</p>}
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} loading={busy} disabled={!!invalid}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
