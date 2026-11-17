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

  test("Admin edits labelRoutes and the PUT preserves the Secret refs", async ({ page }) => {
    await installApi(page, defaults({ session: ADMIN, repos: [repoFixture()] }));
    await page.goto("/admin");
    await page.getByRole("tab", { name: "Reference repos" }).click();
    await expect(page.getByRole("cell", { name: "acme-docs", exact: true })).toBeVisible();

    await page.getByRole("button", { name: "Edit routes" }).click();
    await expect(page.getByText("Label routes — acme-docs")).toBeVisible();
    await expect(page.getByPlaceholder("recycle:blog").first()).toHaveValue("recycle:blog");

    // Add a second route and fill its label.
    await page.getByRole("button", { name: "Add route" }).click();
    await page.getByPlaceholder("recycle:blog").nth(1).fill("recycle:tutorial");

    const putReq = page.waitForRequest(
      (r) => r.method() === "PUT" && /\/v1\/referencerepos\/acme-docs$/.test(r.url()),
    );
    await page.getByRole("button", { name: "Save routes" }).click();
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
    await page.getByRole("button", { name: "Edit routes" }).click();

    await page.getByRole("button", { name: "Add route" }).click();
    await page.getByPlaceholder("recycle:blog").nth(1).fill("recycle:blog"); // duplicate

    await expect(page.getByRole("button", { name: "Save routes" })).toBeDisabled();
  });
});
