import { test, expect, installApi, defaults, ADMIN } from "./fixtures";

// Admin console (W6 / C1-C4): Users & roles plus Personas / Reference repos /
// Model profiles, Admin-gated server-side (§6.3). DevRel 403 is covered in
// states.spec.ts; here an Admin session reaches and reads the surfaces.
test.describe("Admin console", () => {
  test("Admin sees the console with the users tab populated", async ({ page }) => {
    await installApi(
      page,
      defaults({
        session: ADMIN,
        users: [
          { username: "admin", role: "Admin" },
          { username: "robin", role: "DevRel" },
        ],
      }),
    );
    await page.goto("/admin");
    await expect(page.getByRole("heading", { name: "Admin", exact: true })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Users & roles" })).toBeVisible();
    await expect(page.getByRole("cell", { name: "admin", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "robin", exact: true })).toBeVisible();
  });

  test("switching to the Personas tab loads the CR-backed panel", async ({ page }) => {
    await installApi(
      page,
      defaults({
        session: ADMIN,
        personas: [
          {
            metadata: { name: "robin-voice" },
            spec: { owner: { subject: "robin" } },
            status: { phase: "Ready", exemplarCount: 3 },
          },
        ],
      }),
    );
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Personas" }).click();
    await expect(page.getByText("Owner-scoped DevRel voice profiles.", { exact: false })).toBeVisible();
    await expect(page.getByText("robin-voice")).toBeVisible();
  });
  // ISI-7965 W4: the Content styles tab lists per-format guides and the scoped
  // guide editor PUTs the markdown body with a full-object round-trip (the
  // AgentForm contract) — description + guide change, the format never does.
  test("Admin edits a content style guide and the PUT carries the body", async ({ page }) => {
    await installApi(
      page,
      defaults({
        session: ADMIN,
        contentStyles: [
          {
            metadata: { name: "blog", resourceVersion: "7" },
            spec: {
              outputFormat: "blog",
              guide: "# Blog style\n\nBe sharp.",
              description: "Blog voice",
            },
          },
        ],
      }),
    );
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Content styles" }).click();
    await expect(page.getByText("Per-format markdown style guides", { exact: false })).toBeVisible();
    await expect(page.getByRole("cell", { name: "blog", exact: true })).toBeVisible();

    await page.getByRole("button", { name: "Edit guide" }).click();
    await expect(page.getByText("Style guide — blog")).toBeVisible();
    const guide = page.locator("textarea");
    await expect(guide).toHaveValue("# Blog style\n\nBe sharp.");
    await guide.fill("# Blog style\n\nBe sharp. Land the hook fast.");

    const putReq = page.waitForRequest(
      (r) => r.method() === "PUT" && /\/v1\/contentstyles\/blog$/.test(r.url()),
    );
    await page.getByRole("button", { name: "Save", exact: true }).click();
    const body = (await putReq).postDataJSON();
    // Round-trip: the format is untouched, the guide body is sent, and the
    // resourceVersion from the fresh GET rides along.
    expect(body.spec.outputFormat).toBe("blog");
    expect(body.spec.guide).toContain("Land the hook fast.");
    expect(body.metadata.resourceVersion).toBe("7");
    await expect(page.getByRole("cell", { name: "blog", exact: true })).toBeVisible();
  });


  // ISI-7514: in-UI editor for ReferenceRepo.spec.labelRoutes. The editor must
  // round-trip the full object so the token/webhook Secret refs are preserved
  // and never surfaced, while spec.labelRoutes is the only thing it changes.
  const repoFixture = () => ({
    metadata: { name: "acme-docs", resourceVersion: "42" },
    spec: {
      repo: "acme/docs",
      tokenRef: { name: "gh-token", key: "pat" },
      webhookSecretRef: { name: "gh-hook", key: "secret" },
      labelRoutes: [{ label: "recycle:blog", inputFormat: "auto", outputFormat: "blog" }],
    },
    status: { routeCount: 1 },
  });

  // ISI-7782: the bespoke edit form edits repo/prTarget/labelRoutes while the
  // token/webhook Secret refs are preserved untouched (never surfaced) via the
  // full-object round-trip — same contract ISI-7514 established for routes.
  test("Admin edits a reference repo and the PUT preserves the Secret refs", async ({ page }) => {
    await installApi(page, defaults({ session: ADMIN, repos: [repoFixture()] }));
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Reference repos" }).click();
    await expect(page.getByRole("cell", { name: "acme-docs", exact: true })).toBeVisible();

    await page.getByRole("button", { name: "Edit", exact: true }).click();
    await expect(page.getByText("Edit reference repo — acme-docs")).toBeVisible();
    await expect(page.getByPlaceholder("recycle:blog").first()).toHaveValue("recycle:blog");
    // Secret refs are not surfaced in the edit form (W2 boundary).
    await expect(page.getByPlaceholder("gh-token")).toHaveCount(0);

    // Add a second route and fill its label.
    await page.getByRole("button", { name: "Add route" }).click();
    await page.getByPlaceholder("recycle:blog").nth(1).fill("recycle:tutorial");

    const putReq = page.waitForRequest(
      (r) => r.method() === "PUT" && /\/v1\/referencerepos\/acme-docs$/.test(r.url()),
    );
    await page.getByRole("button", { name: "Save", exact: true }).click();
    const body = (await putReq).postDataJSON();

    // Secret refs round-tripped untouched…
    expect(body.spec.tokenRef).toEqual({ name: "gh-token", key: "pat" });
    expect(body.spec.webhookSecretRef).toEqual({ name: "gh-hook", key: "secret" });
    // …and the edited routes are sent.
    expect(body.spec.labelRoutes).toHaveLength(2);
    expect(body.spec.labelRoutes.map((r: { label: string }) => r.label)).toContain("recycle:tutorial");
  });

  test("the routes editor blocks a duplicate label (mirrors the webhook)", async ({ page }) => {
    await installApi(page, defaults({ session: ADMIN, repos: [repoFixture()] }));
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Reference repos" }).click();
    await page.getByRole("button", { name: "Edit", exact: true }).click();

    await page.getByRole("button", { name: "Add route" }).click();
    await page.getByPlaceholder("recycle:blog").nth(1).fill("recycle:blog"); // duplicate

    await expect(page.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  });

  // ISI-7782: the bespoke create form POSTs the full nested spec (repo, Secret
  // refs by name+key, prTarget, ≥1 labelRoute) the generic CrPanel cannot build.
  test("Admin creates a reference repo with the bespoke form", async ({ page }) => {
    await installApi(page, defaults({ session: ADMIN, repos: [] }));
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Reference repos" }).click();

    await page.getByRole("button", { name: "New", exact: true }).click();
    await expect(page.getByText("New reference repo")).toBeVisible();

    await page.getByPlaceholder("contentremixer-docs").fill("acme-docs");
    await page.getByPlaceholder("owner/name").fill("acme/docs");
    await page.getByPlaceholder("gh-token").fill("gh-token");
    await page.getByPlaceholder("pat").fill("pat");
    await page.getByPlaceholder("gh-hook").fill("gh-hook");
    await page.getByPlaceholder("secret").fill("secret");
    await page.getByPlaceholder("recycle:blog").fill("recycle:blog");

    const postReq = page.waitForRequest(
      (r) => r.method() === "POST" && /\/v1\/referencerepos$/.test(r.url()),
    );
    await page.getByRole("button", { name: "Create", exact: true }).click();
    const body = (await postReq).postDataJSON();

    expect(body.metadata.name).toBe("acme-docs");
    expect(body.spec.repo).toBe("acme/docs");
    expect(body.spec.tokenRef).toEqual({ name: "gh-token", key: "pat" });
    expect(body.spec.webhookSecretRef).toEqual({ name: "gh-hook", key: "secret" });
    expect(body.spec.labelRoutes).toHaveLength(1);
    expect(body.spec.labelRoutes[0].label).toBe("recycle:blog");
  });

  // ISI-7897 (W4): the guided onboarding wizard sequences the existing steps —
  // create (W1) → install (W2) → seed (W3) → verify — carrying the created repo
  // forward so the later steps address it. Done-when: the four steps advance,
  // the create still POSTs the bespoke spec, and the verify step deep-links the
  // repo's PRs derived from the just-created spec.repo.
  test("Admin onboards a repo via the guided wizard", async ({ page }) => {
    await installApi(page, defaults({ session: ADMIN, repos: [] }));
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Reference repos" }).click();

    await page.getByRole("button", { name: "Onboard a repo" }).click();
    await expect(page.getByText("Onboard a reference repo")).toBeVisible();

    // Step ① — create via the reused bespoke form.
    await page.getByRole("button", { name: "Create reference repo" }).click();
    await page.getByPlaceholder("contentremixer-docs").fill("acme-docs");
    await page.getByPlaceholder("owner/name").fill("acme/docs");
    await page.getByPlaceholder("gh-token").fill("gh-token");
    await page.getByPlaceholder("pat").fill("pat");
    await page.getByPlaceholder("gh-hook").fill("gh-hook");
    await page.getByPlaceholder("secret").fill("secret");
    await page.getByPlaceholder("recycle:blog").fill("recycle:blog");

    const postReq = page.waitForRequest(
      (r) => r.method() === "POST" && /\/v1\/referencerepos$/.test(r.url()),
    );
    await page.getByRole("button", { name: "Create", exact: true }).click();
    expect((await postReq).postDataJSON().metadata.name).toBe("acme-docs");

    // Step ② — install (auto-advanced on create); the reused W2 control appears.
    await expect(page.getByRole("heading", { name: "Install the GitHub App" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Install GitHub App" })).toBeVisible();
    await page.getByRole("button", { name: "Next" }).click();

    // Step ③ — seed (reused W3 control).
    await expect(page.getByRole("heading", { name: "Seed template issues" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Seed template issues" })).toBeVisible();
    await page.getByRole("button", { name: "Next" }).click();

    // Step ④ — verify: the PR deep link is derived from the created spec.repo.
    await expect(page.getByRole("heading", { name: "Verify the first recycle PR" })).toBeVisible();
    await expect(page.getByRole("link", { name: /Open pull requests on GitHub/ })).toHaveAttribute(
      "href",
      "https://github.com/acme/docs/pulls",
    );
    await page.getByRole("button", { name: "Finish" }).click();

    // Finishing closes the wizard back to the panel.
    await expect(page.getByText("Onboard a reference repo")).toBeHidden();
  });

  // Done-when: admission-webhook validation errors surface in the form.
  test("a webhook rejection surfaces in the create form", async ({ page }) => {
    await installApi(page, defaults({ session: ADMIN, repos: [] }));
    // Per-test override AFTER installApi so it runs first (most-recently-added).
    await page.route("**/v1/referencerepos", async (route, req) => {
      if (req.method() === "POST") {
        await route.fulfill({
          status: 400,
          contentType: "application/json",
          body: JSON.stringify({ error: "admission webhook denied: labelRoutes[0].label is required" }),
        });
        return;
      }
      await route.fallback();
    });
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Reference repos" }).click();

    await page.getByRole("button", { name: "New", exact: true }).click();
    await page.getByPlaceholder("contentremixer-docs").fill("acme-docs");
    await page.getByPlaceholder("owner/name").fill("acme/docs");
    await page.getByPlaceholder("gh-token").fill("gh-token");
    await page.getByPlaceholder("pat").fill("pat");
    await page.getByPlaceholder("gh-hook").fill("gh-hook");
    await page.getByPlaceholder("secret").fill("secret");
    await page.getByPlaceholder("recycle:blog").fill("recycle:blog");
    await page.getByRole("button", { name: "Create", exact: true }).click();

    await expect(page.getByText(/admission webhook denied/)).toBeVisible();
  });

  // ISI-7802: the read-only Agents tab lists the seeded default specialists over
  // /v1/agents (CrPanel, no create). Delivers the "see the default agents" ask.
  test("switching to the Agents tab lists the seeded default agents", async ({ page }) => {
    await installApi(
      page,
      defaults({
        session: ADMIN,
        agents: [
          {
            metadata: { name: "architect" },
            spec: { role: "System Architect", skillKind: "architect", modelProfile: "default", actorTemplate: "content", default: true },
          },
          {
            metadata: { name: "graphic" },
            spec: { role: "Graphic Designer", skillKind: "graphic", modelProfile: "default", actorTemplate: "media", default: true },
          },
        ],
      }),
    );
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Agents" }).click();
    await expect(page.getByText("The specialists the content pipeline runs.", { exact: false })).toBeVisible();
    // "architect"/"graphic" appear in BOTH the Name and Skill-kind columns, so
    // assert on the unique Role and Actor cells to avoid a strict-mode match.
    await expect(page.getByRole("cell", { name: "architect", exact: true }).first()).toBeVisible();
    await expect(page.getByRole("cell", { name: "System Architect", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "Graphic Designer", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "media", exact: true })).toBeVisible();
  });

  // ISI-7803: "add extra agents" — the bespoke create form POSTs /v1/agents with
  // the skill-kind/actor Selects and the skillPacks chips. spec.default is never
  // set (new agents are non-default).
  test("Admin adds an extra agent via the create form", async ({ page }) => {
    await installApi(
      page,
      defaults({
        session: ADMIN,
        agents: [],
        models: [{ metadata: { name: "glm-cheap" }, spec: { litellmName: "glm-cheap" } }],
      }),
    );
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Agents" }).click();

    await page.getByRole("button", { name: "New", exact: true }).click();
    // Match the dialog heading specifically — the helper text "New agents are
    // always non-default…" also contains the substring "New agent".
    await expect(page.getByRole("heading", { name: "New agent" })).toBeVisible();
    await page.getByPlaceholder("coding-fast").fill("coding-fast");
    await page.getByPlaceholder("Coding Specialist (fast)").fill("Coding Specialist (fast)");
    // Add a skill pack chip via the redesigned picker (ISI-7858). This fixture
    // ships no catalog, so the picker falls back to the add-by-name escape hatch.
    // The chip exposes an accessible "Remove skill pack <name>" button we can
    // assert on without the × noise.
    await page.getByRole("button", { name: "+ Add packs" }).click();
    await page.getByLabel("Add pack by name").fill("concise");
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await expect(page.getByRole("button", { name: "Remove skill pack concise" })).toBeVisible();

    const postReq = page.waitForRequest(
      (r) => r.method() === "POST" && /\/v1\/agents$/.test(r.url()),
    );
    await page.getByRole("button", { name: "Create", exact: true }).click();
    const body = (await postReq).postDataJSON();

    expect(body.metadata.name).toBe("coding-fast");
    expect(body.spec.role).toBe("Coding Specialist (fast)");
    expect(body.spec.skillKind).toBe("coding"); // the default Select value
    expect(body.spec.skillPacks).toEqual(["concise"]);
    expect(body.spec.default).toBeUndefined();
  });

  // ISI-7856/7858: the catalog + default composition the Skills… editor reads.
  const skillCatalog = {
    packs: [
      { name: "blog-writing", title: "Blog writing", description: "Long-form blog structure & flow." },
      { name: "human-voice-review", title: "Human-voice review", description: "Strip AI tells; natural voice." },
      { name: "seo", title: "SEO", description: "Keyword, metadata & structure discipline." },
      { name: "concise", title: "Concise", description: "Tighten prose; cut filler." },
    ],
    defaults: { "content-writer": ["blog-writing", "human-voice-review", "seo"] },
    dynamicFallback: true,
  };

  // ISI-7803/7858: "modify skills" — the per-row Skills… editor GETs the one Agent
  // and PUTs the round-tripped object with ONLY spec.skillPacks changed; every
  // other field (role, skillKind, resourceVersion) is preserved untouched. The
  // added pack is picked from the real W1 catalog (ISI-7856), not a blind field.
  test("Admin edits an agent's skill packs and the PUT preserves other fields", async ({ page }) => {
    const agent = {
      metadata: { name: "content-writer", resourceVersion: "7" },
      spec: {
        role: "Content Writer",
        skillKind: "writer",
        modelProfile: "default",
        actorTemplate: "content",
        skillPacks: ["blog-writing"],
        default: true,
      },
    };
    await installApi(page, defaults({ session: ADMIN, agents: [agent], skillpacks: skillCatalog }));
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Agents" }).click();

    await page.getByRole("button", { name: "Skills…", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Skills — content-writer" })).toBeVisible();
    // An existing override pack means State C: defaults show struck-through +
    // "overridden", and the replace warn banner is present.
    await expect(page.getByText("Default skill packs (overridden)")).toBeVisible();
    await expect(page.getByText(/Your packs replace the writer default/)).toBeVisible();
    // Existing override chip is shown (assert via its remove button); add another
    // from the catalog picker.
    await expect(page.getByRole("button", { name: "Remove skill pack blog-writing" })).toBeVisible();
    await page.getByRole("button", { name: "+ Add packs" }).click();
    await page.getByRole("option", { name: /SEO/ }).click();

    const putReq = page.waitForRequest(
      (r) => r.method() === "PUT" && /\/v1\/agents\/content-writer$/.test(r.url()),
    );
    await page.getByRole("button", { name: "Save", exact: true }).click();
    const body = (await putReq).postDataJSON();

    expect(body.spec.skillPacks).toEqual(["blog-writing", "seo"]);
    // Untouched fields round-tripped.
    expect(body.spec.role).toBe("Content Writer");
    expect(body.spec.skillKind).toBe("writer");
    expect(body.metadata.resourceVersion).toBe("7");
  });

  // ISI-7858 W3: a default agent with no override packs still shows its default
  // composition (D1 — never "No skill packs"), and the picker offers the real
  // catalog with descriptions. Selecting a pack flips to the replace (State C).
  test("Skills… shows the default composition and the catalog picker", async ({ page }) => {
    const agent = {
      metadata: { name: "content-writer", resourceVersion: "3" },
      spec: { role: "Content Writer", skillKind: "writer", default: true },
    };
    await installApi(page, defaults({ session: ADMIN, agents: [agent], skillpacks: skillCatalog }));
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Agents" }).click();
    await page.getByRole("button", { name: "Skills…", exact: true }).click();

    // D1: default composition visible, not "No skill packs".
    await expect(page.getByText("Default skill packs", { exact: true })).toBeVisible();
    const defaultGroup = page.getByLabel("Default skill packs from the writer skill (read-only)");
    await expect(defaultGroup.getByText("blog-writing")).toBeVisible();
    await expect(defaultGroup.getByText("human-voice-review")).toBeVisible();
    await expect(page.getByText("No added packs — this agent uses its default packs.")).toBeVisible();
    // No replace banner while there is no override.
    await expect(page.getByText(/Your packs replace the writer default/)).toHaveCount(0);

    // Picker offers the catalog with descriptions.
    await page.getByRole("button", { name: "+ Add packs" }).click();
    await expect(page.getByRole("listbox", { name: "Skill packs" })).toBeVisible();
    await expect(page.getByText("Tighten prose; cut filler.")).toBeVisible();
    // Selecting a pack flips to State C (replace banner appears).
    await page.getByRole("option", { name: /Concise/ }).click();
    await expect(page.getByText(/Your packs replace the writer default/)).toBeVisible();
  });

  // ISI-7803: default rows are delete-guarded (UX affordance). A default agent's
  // Delete is disabled; a non-default one is enabled.
  test("default agents are delete-guarded, extra agents are deletable", async ({ page }) => {
    await installApi(
      page,
      defaults({
        session: ADMIN,
        agents: [
          { metadata: { name: "architect" }, spec: { role: "System Architect", skillKind: "architect", default: true } },
          { metadata: { name: "coding-fast" }, spec: { role: "Coding Specialist (fast)", skillKind: "coding" } },
        ],
      }),
    );
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Agents" }).click();

    const rows = page.getByRole("row");
    const defaultRow = rows.filter({ hasText: "System Architect" });
    const extraRow = rows.filter({ hasText: "Coding Specialist (fast)" });
    await expect(defaultRow.getByRole("button", { name: "Delete" })).toBeDisabled();
    await expect(extraRow.getByRole("button", { name: "Delete" })).toBeEnabled();
  });
});

// ISI-7804: the Orchestration tab — the data-driven handoff/orchestration
// editor over /v1/agentteams (team list, per-row "Team…" scoped editor with the
// GET→swap→PUT discipline, live cycle/orphan validation, seeded default row).
const teamFixture = () => ({
  metadata: { name: "tutorial-default", resourceVersion: "9" },
  spec: {
    default: true,
    entry: "architect",
    nodes: [
      { id: "architect", agentRef: "architect" },
      { id: "research", agentRef: "research" },
      { id: "coding", agentRef: "coding" },
      { id: "observability", agentRef: "observability" },
      { id: "graphic", agentRef: "graphic" },
    ],
    edges: [
      { from: "architect", to: "research" },
      { from: "architect", to: "coding" },
      { from: "architect", to: "observability" },
      { from: "architect", to: "graphic" },
    ],
  },
});

const linearTeamFixture = () => ({
  metadata: { name: "content-default", resourceVersion: "3" },
  spec: { entry: "writer", nodes: [{ id: "writer", agentRef: "content-writer" }] },
});

const seededAgents = () => [
  { metadata: { name: "architect" }, spec: { role: "System Architect", skillKind: "architect" } },
  { metadata: { name: "research" }, spec: { role: "Researcher", skillKind: "research" } },
  { metadata: { name: "coding" }, spec: { role: "Coding Specialist", skillKind: "coding" } },
  { metadata: { name: "observability" }, spec: { role: "Observability Specialist", skillKind: "observability" } },
  { metadata: { name: "graphic" }, spec: { role: "Graphic Designer", skillKind: "graphic" } },
  { metadata: { name: "content-writer" }, spec: { role: "Content Writer", skillKind: "writer" } },
];

test.describe("Orchestration tab (ISI-7804)", () => {
  test("switching to the Orchestration tab lists the seeded teams", async ({ page }) => {
    await installApi(
      page,
      defaults({ session: ADMIN, agentteams: [teamFixture(), linearTeamFixture()] }),
    );
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Orchestration" }).click();
    await expect(page.getByText("The hand-off graphs the content pipeline runs.", { exact: false })).toBeVisible();
    await expect(page.getByRole("cell", { name: "tutorial-default", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "content-default", exact: true })).toBeVisible();
    // Entry column + the Default badge on the seeded default row.
    await expect(page.getByRole("cell", { name: "architect", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "Default", exact: true })).toBeVisible();
  });

  // The scoped editor round-trips the full object: only spec.entry/nodes/edges
  // change; spec.default and every other field are preserved untouched.
  test("Admin edits a team's handoffs and the PUT preserves other fields", async ({ page }) => {
    await installApi(
      page,
      defaults({ session: ADMIN, agents: seededAgents(), agentteams: [teamFixture()] }),
    );
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Orchestration" }).click();

    await page.getByRole("button", { name: "Team…", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Team — tutorial-default" })).toBeVisible();

    // Add handoff research → coding on the appended (5th) row.
    await page.getByRole("button", { name: "Add handoff" }).click();
    await page.getByRole("combobox", { name: "Handoff 5 from" }).click();
    await page.getByRole("option", { name: "research", exact: true }).click();
    await page.getByRole("combobox", { name: "Handoff 5 to" }).click();
    await page.getByRole("option", { name: "coding", exact: true }).click();

    const putReq = page.waitForRequest(
      (r) => r.method() === "PUT" && r.url().endsWith("/v1/agentteams/tutorial-default"),
    );
    await page.getByRole("button", { name: "Save", exact: true }).click();
    const body = (await putReq).postDataJSON();

    expect(body.spec.entry).toBe("architect"); // preserved
    expect(body.spec.default).toBe(true); // preserved — the editor never touches it
    expect(body.spec.nodes).toHaveLength(5); // preserved
    expect(body.spec.edges).toContainEqual({ from: "research", to: "coding" }); // the edit
  });

  // UX spec §3.2 T6: a cycle is flagged live (banner + disabled Save) BEFORE
  // any request is sent — the server webhook is the authoritative backstop.
  test("the team editor blocks a cycle before submit", async ({ page }) => {
    await installApi(
      page,
      defaults({ session: ADMIN, agents: seededAgents(), agentteams: [teamFixture()] }),
    );
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Orchestration" }).click();

    await page.getByRole("button", { name: "Team…", exact: true }).click();
    await page.getByRole("button", { name: "Add handoff" }).click();
    await page.getByRole("combobox", { name: "Handoff 5 from" }).click();
    await page.getByRole("option", { name: "coding", exact: true }).click();
    await page.getByRole("combobox", { name: "Handoff 5 to" }).click();
    await page.getByRole("option", { name: "architect", exact: true }).click();

    await expect(page.getByRole("alert")).toContainText("This creates a cycle");
    await expect(page.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  });

  // UX spec §4 F3: New team → editor → POST /v1/agentteams (never sets default).
  test("Admin creates a new team via the editor", async ({ page }) => {
    await installApi(
      page,
      defaults({ session: ADMIN, agents: seededAgents(), agentteams: [] }),
    );
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Orchestration" }).click();

    await page.getByRole("button", { name: "New team", exact: true }).click();
    await expect(page.getByRole("heading", { name: "New team" })).toBeVisible();
    await page.getByPlaceholder("tutorial-fast").fill("tutorial-fast");
    await page.getByRole("textbox", { name: "Node 1 id" }).fill("plan");
    await page.getByRole("combobox", { name: "Agent for node 1" }).click();
    await page.getByRole("option", { name: "System Architect — architect" }).click();
    await page.getByRole("combobox", { name: "Entry node" }).click();
    await page.getByRole("option", { name: "plan", exact: true }).click();

    const postReq = page.waitForRequest(
      (r) => r.method() === "POST" && r.url().endsWith("/v1/agentteams"),
    );
    await page.getByRole("button", { name: "Create", exact: true }).click();
    const body = (await postReq).postDataJSON();

    expect(body.metadata.name).toBe("tutorial-fast");
    expect(body.spec.entry).toBe("plan");
    expect(body.spec.nodes).toEqual([{ id: "plan", agentRef: "architect" }]);
    expect(body.spec.default).toBeUndefined();
  });
});
