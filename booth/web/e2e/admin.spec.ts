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
    await expect(page.getByText("New agent")).toBeVisible();
    await page.getByPlaceholder("coding-fast").fill("coding-fast");
    await page.getByPlaceholder("Coding Specialist (fast)").fill("Coding Specialist (fast)");
    // Add a skill pack chip via free text.
    await page.getByPlaceholder("add a pack, e.g. concise").fill("concise");
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await expect(page.getByText("concise", { exact: true })).toBeVisible();

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

  // ISI-7803: "modify skills" — the per-row Skills… editor GETs the one Agent and
  // PUTs the round-tripped object with ONLY spec.skillPacks changed; every other
  // field (role, skillKind, resourceVersion) is preserved untouched.
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
    await installApi(page, defaults({ session: ADMIN, agents: [agent] }));
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Agents" }).click();

    await page.getByRole("button", { name: "Skills…", exact: true }).click();
    await expect(page.getByText("Skills — content-writer")).toBeVisible();
    // Existing pack chip is shown; add another.
    await expect(page.getByText("blog-writing", { exact: true })).toBeVisible();
    await page.getByPlaceholder("add a pack, e.g. concise").fill("seo");
    await page.getByRole("button", { name: "Add", exact: true }).click();

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
