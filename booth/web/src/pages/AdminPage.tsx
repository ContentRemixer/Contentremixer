import { Badge, Tabs, TabsContent, TabsList, TabsTrigger } from "../ui";
import { UsersPanel } from "./admin/UsersPanel";
import { CrPanel, type CrObject } from "./admin/CrPanel";

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
          Platform configuration — personas, reference repos, model profiles, and users &amp; roles.
        </p>
      </header>

      <Tabs defaultValue="users">
        <TabsList>
          <TabsTrigger value="users">Users &amp; roles</TabsTrigger>
          <TabsTrigger value="personas">Personas</TabsTrigger>
          <TabsTrigger value="repos">Reference repos</TabsTrigger>
          <TabsTrigger value="models">Model profiles</TabsTrigger>
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
            // No inline create: a ReferenceRepo carries token/webhook Secret refs
            // and PR routing authored via GitOps/kubectl, not a web form. The
            // console lists and can remove them; authoring stays in the repo.
            createNote="Reference repos are provisioned via GitOps. None are registered yet."
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
      </Tabs>
    </section>
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
