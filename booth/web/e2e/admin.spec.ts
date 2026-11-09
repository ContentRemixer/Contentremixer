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
});
