import { test, expect, installApi, defaults, ADMIN } from "./fixtures";

// A1 — login + first-run Admin bootstrap (W1). The SPA boots with a whoami probe;
// anonymous users are routed to the login surface, which has normal / first-run /
// session-expired variants.
test.describe("A1 login", () => {
  test("anonymous boot redirects to the sign-in surface", async ({ page }) => {
    await installApi(page, defaults()); // session null → whoami 401, bootstrap 404
    await page.goto("/");
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
    await expect(page.getByLabel("Username")).toBeVisible();
    await expect(page.getByLabel("Password")).toBeVisible();
  });

  test("first-run shows the welcome variant and prefills the seed username", async ({ page }) => {
    await installApi(page, defaults({ bootstrap: { firstRun: true, adminUsername: "admin" } }));
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: "Welcome to ContentRemixer" })).toBeVisible();
    await expect(page.getByLabel("Username")).toHaveValue("admin");
    await expect(page.getByRole("button", { name: "Sign in & set up" })).toBeVisible();
  });

  test("invalid credentials surface an inline error", async ({ page }) => {
    await installApi(page, defaults({ loginAs: null })); // login POST → 401
    await page.goto("/login");
    await page.getByLabel("Username").fill("admin");
    await page.getByLabel("Password").fill("wrong");
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page.getByRole("alert")).toHaveText("Invalid username or password.");
  });

  test("successful sign-in lands on the console home", async ({ page }) => {
    await installApi(page, defaults({ loginAs: ADMIN }));
    await page.goto("/login");
    await page.getByLabel("Username").fill("admin");
    await page.getByLabel("Password").fill("admin-pw");
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole("heading", { name: /Welcome, admin/ })).toBeVisible();
  });
});
