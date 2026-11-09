import { test, expect, installApi, defaults, ADMIN, DEVREL } from "./fixtures";

// A3 — system states (W1): the shared loading/empty/error catalogue plus the
// terminal routes (session-expired, 403, 404) reached through real app flows.
test.describe("A3 states", () => {
  test("states catalogue renders every shared primitive", async ({ page }) => {
    await installApi(page, defaults({ session: ADMIN }));
    await page.goto("/states");
    await expect(page.getByRole("heading", { name: "System states (A3)" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Loading skeleton" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Empty" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Terminal states" })).toBeVisible();
    await expect(page.getByText("No scraped sources yet")).toBeVisible();
  });

  test("a 401 on a live data call tears down to the session-expired surface", async ({ page }) => {
    await installApi(page, defaults({ session: ADMIN }));
    // whoami succeeds (authenticated), then the content list 401s mid-session →
    // the central handler flips the whole app to session-expired and redirects.
    await page.route("**/v1/content", (route) =>
      route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ error: "session expired" }) }),
    );
    await page.goto("/content");
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByText("Your session expired.")).toBeVisible();
  });

  test("unknown route shows the 404 screen", async ({ page }) => {
    await installApi(page, defaults({ session: ADMIN }));
    await page.goto("/no-such-screen");
    await expect(page.getByText("404")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
  });

  test("a DevRel hitting the admin route is 403'd", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL }));
    await page.goto("/admin");
    await expect(page).toHaveURL(/\/403$/);
    await expect(page.getByText("403")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Access denied" })).toBeVisible();
  });
});
