import { test, expect, installApi, defaults, DEVREL, ADMIN } from "./fixtures";

// O1–O9 DevRel onboarding wizard (ISI-7361 D6). The first-run gate (D5 probe)
// routes a DevRel without a validated persona into the wizard; the synchronous
// steps (welcome → primary content → sources → bio → review) gate their Continue
// actions. The async ingestion/synthesis/validate path is backend-driven and is
// exercised in the D8 integration suite, not here.

const FIRST_RUN = {
  subject: "robin",
  role: "DevRel",
  hasPersona: false,
  hasValidatedPersona: false,
  needsOnboarding: true,
};

test.describe("DevRel onboarding wizard", () => {
  test("a first-run DevRel is routed into the wizard", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, onboarding: FIRST_RUN }));
    await page.goto("/");
    await expect(page).toHaveURL(/\/onboarding$/);
    await expect(page.getByRole("heading", { name: /capture your voice/ })).toBeVisible();
  });

  test("welcome advances to the primary-content picker and gates Continue", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, onboarding: FIRST_RUN }));
    await page.goto("/onboarding");
    await page.getByRole("button", { name: "Get started" }).click();

    await expect(page.getByRole("heading", { name: /What do you mostly create/ })).toBeVisible();
    const cont = page.getByRole("button", { name: "Continue" });
    await expect(cont).toBeDisabled();
    await page.getByRole("button", { name: /Blog posts/ }).click();
    await expect(cont).toBeEnabled();
  });

  test("the sources step requires at least one source URL", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, onboarding: FIRST_RUN }));
    await page.goto("/onboarding");
    await page.getByRole("button", { name: "Get started" }).click();
    await page.getByRole("button", { name: /Blog posts/ }).click();
    await page.getByRole("button", { name: "Continue" }).click();

    await expect(page.getByRole("heading", { name: /Add a few writing samples/ })).toBeVisible();
    // One empty article row is seeded; Continue is disabled until a URL is typed.
    const cont = page.getByRole("button", { name: /Continue/ });
    await expect(cont).toBeDisabled();
    await page.getByLabel("Source URL").first().fill("https://example.com/post");
    await expect(cont).toBeEnabled();
  });

  test("an already-onboarded DevRel visiting the wizard is sent home", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL })); // default: needsOnboarding false
    await page.goto("/onboarding");
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByText("Welcome", { exact: false })).toBeVisible();
  });

  test("an Admin is never routed into onboarding", async ({ page }) => {
    await installApi(page, defaults({ session: ADMIN }));
    await page.goto("/");
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByText("Welcome", { exact: false })).toBeVisible();
  });
});
