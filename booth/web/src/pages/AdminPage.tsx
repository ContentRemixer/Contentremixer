import { Badge, Tabs, TabsContent, TabsList, TabsTrigger } from "../ui";
import { UsersPanel } from "./admin/UsersPanel";
import { CrPanel, type CrObject } from "./admin/CrPanel";
import { NewReferenceRepoButton, EditReferenceRepoButton } from "./admin/ReferenceRepoForm";
import { NewAgentButton, EditAgentSkillsButton } from "./admin/AgentForm";
import { NewTeamButton, EditTeamButton } from "./admin/TeamForm";

// AdminPage is the C1–C4 admin console (W6 / ISI-7213). Four Admin-only surfaces:
// Users & roles (brand-new CRUD API), plus Personas / Reference repos / Model
// profiles backed by the existing CR CRUD routes. Server-side §6.3 is
// authoritative — the tabs are cosmetic; every panel renders the server's 403 if
// a non-Admin reaches it (plan §4).
export function AdminPage() {
  return (
    <section>
      <header className="mb-6">
        <h1 className="text-xl font-semibold text-text">Admin</h1>
        <p className="mt-1 text-sm text-text-muted">
          Platform configuration — personas, reference repos, model profiles, agents, and users &amp; roles.
        </p>
      </header>

      <Tabs defaultValue="users">
        <TabsList>
          <TabsTrigger value="users">Users &amp; roles</TabsTrigger>
          <TabsTrigger value="personas">Personas</TabsTrigger>
          <TabsTrigger value="repos">Reference repos</TabsTrigger>
          <TabsTrigger value="models">Model profiles</TabsTrigger>
          <TabsTrigger value="agents">Agents</TabsTrigger>
          <TabsTrigger value="orchestration">Orchestration</TabsTrigger>
        </TabsList>

        <TabsContent value="users">
          <UsersPanel />
        </TabsContent>

        <TabsContent value="personas">
          <CrPanel
            path="/v1/devrelpersonas"
            title="Personas"
            subtitle="Owner-scoped DevRel voice profiles. Admins see all; a DevRel sees their own."
            columns={[
              { header: "Name", cell: (o) => o.metadata.name },
              { header: "Owner", cell: (o) => str(o, "spec.owner.subject") || "—" },
              { header: "Phase", cell: (o) => <Badge tone="info">{str(o, "status.phase") || "Idle"}</Badge> },
              { header: "Exemplars", cell: (o) => str(o, "status.exemplarCount") || "0" },
            ]}
            create={{
              fields: [
                { key: "name", label: "Name", required: true },
                { key: "owner", label: "Owner subject", required: true, hint: "The DevRel that owns this persona." },
                { key: "selfDescription", label: "Self-description", multiline: true, hint: "Seed voice/style text." },
              ],
              build: (v) => ({
                metadata: { name: v.name },
                spec: { owner: { subject: v.owner }, selfDescription: v.selfDescription || undefined },
              }),
            }}
          />
        </TabsContent>

        <TabsContent value="repos">
          <CrPanel
            path="/v1/referencerepos"
            title="Reference repos"
            subtitle="GitHub repositories the platform mirrors. Admin-only."
            columns={[
              { header: "Name", cell: (o) => o.metadata.name },
              { header: "Repo", cell: (o) => str(o, "spec.repo") || "—" },
              { header: "Routes", cell: (o) => str(o, "status.routeCount") || "0" },
            ]}
            // Bespoke create/edit form (ISI-7782): a ReferenceRepo's nested
            // tokenRef / webhookSecretRef / prTarget / labelRoutes cannot be
            // expressed by CrPanel's flat CreateDialog. The form references
            // existing Secrets by name+key (values stay GitOps/W2-managed) and
            // round-trips the object on edit so Secret refs stay untouched/unshown
            // (builds on ISI-7514's labelRoutes editor). Server stays
            // authoritative (§6.3 + admission webhook); its errors surface inline.
            createNote="No reference repos registered yet. Use “New” to create one."
            customCreate={(refresh) => (
              <NewReferenceRepoButton path="/v1/referencerepos" onSaved={refresh} />
            )}
            rowActions={(o, refresh) => (
              <EditReferenceRepoButton repo={o} path="/v1/referencerepos" onSaved={refresh} />
            )}
          />
        </TabsContent>

        <TabsContent value="models">
          <CrPanel
            path="/v1/modelprofiles"
            title="Model profiles"
            subtitle="LiteLLM model bindings used by the platform. Admin-only."
            columns={[
              { header: "Name", cell: (o) => o.metadata.name },
              { header: "Provider", cell: (o) => str(o, "spec.provider") || "—" },
              { header: "Model", cell: (o) => str(o, "spec.model") || "—" },
              { header: "LiteLLM name", cell: (o) => str(o, "spec.litellmName") || "—" },
            ]}
            create={{
              fields: [
                { key: "name", label: "Name", required: true },
                { key: "provider", label: "Provider", required: true, hint: "e.g. openai, anthropic" },
                { key: "model", label: "Model", required: true, hint: "e.g. gpt-4o" },
                { key: "litellmName", label: "LiteLLM name", required: true },
              ],
              build: (v) => ({
                metadata: { name: v.name },
                spec: { provider: v.provider, model: v.model, litellmName: v.litellmName },
              }),
            }}
          />
        </TabsContent>

        <TabsContent value="agents">
          <p className="mb-3 text-sm text-text-muted">
            Agents marked <Badge tone="success">Default</Badge> are seeded and reproduce the platform's built-in
            team. You can add extra agents and edit any agent's skill packs below.
          </p>
          <CrPanel
            path="/v1/agents"
            title="Agents"
            subtitle="The specialists the content pipeline runs. Admin-only."
            columns={[
              { header: "Name", cell: (o) => o.metadata.name },
              { header: "Role", cell: (o) => str(o, "spec.role") || "—" },
              { header: "Skill kind", cell: (o) => <Badge tone="info">{str(o, "spec.skillKind") || "—"}</Badge> },
              { header: "Skill packs", cell: (o) => <PacksCell o={o} /> },
              { header: "Model", cell: (o) => str(o, "spec.modelProfile") || <span className="text-text-muted">default</span> },
              {
                header: "Actor",
                cell: (o) =>
                  str(o, "spec.actorTemplate") === "media" ? (
                    <Badge tone="warning">media</Badge>
                  ) : (
                    <span className="text-text-muted">content</span>
                  ),
              },
              {
                header: "Default",
                cell: (o) =>
                  str(o, "spec.default") === "true" ? <Badge tone="success">Default</Badge> : "",
              },
            ]}
            // Create (ISI-7803 "add extra agents") + per-row "Skills…" scoped editor
            // ("modify skills"). No new backend route — both use the generic
            // /v1/agents CRUD. The seeded default:true rows are delete-guarded (UX
            // only; the seeder re-creates a missing default on next boot).
            customCreate={(refresh) => <NewAgentButton path="/v1/agents" onSaved={refresh} />}
            rowActions={(o, refresh) => (
              <EditAgentSkillsButton agent={o} path="/v1/agents" onSaved={refresh} />
            )}
            deleteGuard={(o) =>
              str(o, "spec.default") === "true"
                ? "Default agents are seeded and cannot be deleted. Add an extra agent of the same skill kind instead."
                : null
            }
          />
        </TabsContent>
        <TabsContent value="orchestration">
          <p className="mb-3 text-sm text-text-muted">
            The <Badge tone="success">Default</Badge> team runs when a tutorial names no team. Exactly one team may
            be the default. Edit a team to define its nodes and hand-offs; edits are picked up live.
          </p>
          <CrPanel
            path="/v1/agentteams"
            title="Orchestration"
            subtitle="The hand-off graphs the content pipeline runs. Admin-only."
            columns={[
              { header: "Name", cell: (o) => o.metadata.name },
              { header: "Entry", cell: (o) => str(o, "spec.entry") || "—" },
              { header: "Nodes", cell: (o) => String(((o.spec?.nodes as unknown[]) ?? []).length) },
              { header: "Handoffs", cell: (o) => String(((o.spec?.edges as unknown[]) ?? []).length) },
              {
                header: "Default",
                cell: (o) =>
                  str(o, "spec.default") === "true" ? <Badge tone="success">Default</Badge> : "",
              },
            ]}
            customCreate={(refresh) => <NewTeamButton path="/v1/agentteams" onSaved={refresh} />}
            rowActions={(o, refresh) => (
              <EditTeamButton team={o} path="/v1/agentteams" onSaved={refresh} />
            )}
            deleteGuard={(o) =>
              str(o, "spec.default") === "true"
                ? "Default teams are seeded and cannot be deleted. Create another team and make it the default instead."
                : null
            }
          />
        </TabsContent>
      </Tabs>
    </section>
  );
}

// PacksCell renders spec.skillPacks[] as up to three chips, then "+N", or "—".
function PacksCell({ o }: { o: CrObject }) {
  const raw = (o.spec?.skillPacks as unknown[]) ?? [];
  const packs = raw.filter((p): p is string => typeof p === "string" && p.trim() !== "");
  if (packs.length === 0) return <span className="text-text-muted">—</span>;
  const shown = packs.slice(0, 3);
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      {shown.map((p) => (
        <span key={p} className="rounded border border-border px-1.5 py-0.5 text-xs">
          {p}
        </span>
      ))}
      {packs.length > shown.length && (
        <span className="text-xs text-text-muted">+{packs.length - shown.length}</span>
      )}
    </span>
  );
}

// str reads a dotted path off a raw CR and coerces it to a display string.
function str(o: CrObject, path: string): string {
  const v = path.split(".").reduce<unknown>((acc, k) => {
    if (acc && typeof acc === "object" && k in (acc as Record<string, unknown>)) {
      return (acc as Record<string, unknown>)[k];
    }
    return undefined;
  }, o);
  return v == null ? "" : String(v);
}
