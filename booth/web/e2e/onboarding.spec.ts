import { test, expect, installApi, defaults, DEVREL, ADMIN, source } from "./fixtures";

// O1–O9 DevRel onboarding wizard (ISI-7361 D6). The first-run gate (D5 probe)
// routes a DevRel without a validated persona into the wizard; the synchronous
// steps (welcome → primary content → sources → bio → review) gate their Continue
// actions. The async ingestion/synthesis/validate path is backend-driven and is
// exercised in the D8 integration suite below (ISI-7363).

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

  // O2 multi-select (ISI-7424): more than one kind can be picked. The primary
  // badge + "Set primary" affordance only appear once >= 2 are selected, and the
  // first pick is primary until re-designated.
  test("O2 is a multi-select that auto-derives and re-designates the primary", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, onboarding: FIRST_RUN }));
    await page.goto("/onboarding");
    await page.getByRole("button", { name: "Get started" }).click();
    await expect(page.getByRole("heading", { name: /What do you mostly create/ })).toBeVisible();

    const blog = page.getByRole("button", { name: /Blog posts/ });
    const video = page.getByRole("button", { name: /^Video/ });

    // First pick selects it; with one kind there is no Primary badge yet.
    await blog.click();
    await expect(blog).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByText("Primary", { exact: true })).toHaveCount(0);

    // Second pick surfaces the Primary badge on the first (blog) and a
    // "Set primary" control on the second (video).
    await video.click();
    await expect(video).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByText("Primary", { exact: true })).toHaveCount(1);
    const setPrimary = page.getByRole("button", { name: /Set primary/ });
    await expect(setPrimary).toHaveCount(1);

    // Re-designate video as primary; blog keeps being selected but loses primary.
    await setPrimary.click();
    await expect(page.getByText("Primary", { exact: true })).toHaveCount(1);
    // Now "Set primary" sits on blog (the non-primary selected card).
    await expect(page.getByRole("button", { name: /Set primary/ })).toHaveCount(1);

    await page.getByRole("button", { name: "Continue" }).click();
    await expect(page.getByRole("heading", { name: /Add a few writing samples/ })).toBeVisible();
  });

  // O2 → O5: the full set + derived primary (= first selected) ride through to
  // the persona upsert. Captures the PUT/POST body to assert the wire contract
  // (ISI-7424: send BOTH contentKinds and primaryContentKind; CRD drops order).
  test("O5 persists contentKinds + derived primaryContentKind on the persona", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, onboarding: FIRST_RUN }));
    let upsertBody: Record<string, unknown> | null = null;
    // Capture the create (POST) body; let it fall through to the mock handler.
    await page.route("**/v1/devrelpersonas", async (route) => {
      if (route.request().method() === "POST") {
        upsertBody = route.request().postDataJSON();
      }
      await route.fallback();
    });

    await page.goto("/onboarding");
    await page.getByRole("button", { name: "Get started" }).click();
    // Pick video then blog; video is primary (first selected).
    await page.getByRole("button", { name: /^Video/ }).click();
    await page.getByRole("button", { name: /Blog posts/ }).click();
    await page.getByRole("button", { name: "Continue" }).click();

    // O3 sources.
    await page.getByLabel("Source URL").fill("https://example.com/posts/scaling");
    await page.getByRole("button", { name: /^Continue/ }).click();
    // O4 bio.
    await page.getByRole("button", { name: "Continue" }).click();

    // O5 review lists both kinds and marks the primary.
    await expect(page.getByRole("heading", { name: /review & start ingestion/i })).toBeVisible();
    await expect(page.getByText("primary", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Start ingestion" }).click();

    // Ingestion runs (succeeded jobs by default); assert the captured upsert body.
    await expect(page.getByRole("heading", { name: /your style/i })).toBeVisible();
    expect(upsertBody).not.toBeNull();
    const spec = (upsertBody as { spec: Record<string, unknown> }).spec;
    expect(spec.contentKinds).toEqual(["video", "blog"]);
    expect(spec.primaryContentKind).toBe("video");
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

// D8 — the async ingestion → synthesis → content-test → validate path the D6
// suite above deliberately left to this integration layer (ISI-7363, plan §9).
// Scrape jobs default to "succeeded" in the fixture so O6 never polls (fast +
// deterministic); the progress and error specs override that to pin the
// intermediate UI.
test.describe("D8 onboarding ingestion + content-test flow", () => {
  // O3: the typed source cards — adding per kind yields a row whose input carries
  // that kind's placeholder (proves the kind drove the new row).
  test("O3 adds a source row per kind", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, onboarding: FIRST_RUN }));
    await page.goto("/onboarding");
    await page.getByRole("button", { name: "Get started" }).click();
    await page.getByRole("button", { name: /Blog posts/ }).click();
    await page.getByRole("button", { name: "Continue" }).click();

    await expect(page.getByRole("heading", { name: /writing samples/i })).toBeVisible();
    await expect(page.getByLabel("Source URL")).toHaveCount(1); // one default article row

    await page.getByRole("button", { name: "YouTube video", exact: true }).click();
    await expect(page.getByPlaceholder("https://www.youtube.com/watch?v=…")).toBeVisible();

    await page.getByRole("button", { name: "GitHub tutorial repo", exact: true }).click();
    await expect(page.getByPlaceholder("https://github.com/you/your-tutorial")).toBeVisible();

    await expect(page.getByLabel("Source URL")).toHaveCount(3);
  });

  // The full happy path O1 → O9, including the O6 ingestion run, the O7 style
  // review, and the O8 content-test + validate that completes onboarding.
  test("completes O1→O9: sources, ingestion, style review, content-test, validate", async ({ page }) => {
    await installApi(
      page,
      defaults({
        session: DEVREL,
        onboarding: FIRST_RUN,
        // One pending scraped source for the O6 accept phase to ingest.
        scrapedSources: [source({ status: "pending", personaName: "robin" })],
      }),
    );
    await page.goto("/onboarding");

    // O1 Welcome.
    await expect(page.getByRole("heading", { name: /capture your voice/i })).toBeVisible();
    await page.getByRole("button", { name: "Get started" }).click();

    // O2 Primary content kind.
    await expect(page.getByRole("heading", { name: /what do you mostly create/i })).toBeVisible();
    await page.getByRole("button", { name: /Blog posts/ }).click();
    await page.getByRole("button", { name: "Continue" }).click();

    // O3 Sources — fill the default article row.
    await expect(page.getByRole("heading", { name: /writing samples/i })).toBeVisible();
    await page.getByLabel("Source URL").fill("https://example.com/posts/scaling");
    await page.getByRole("button", { name: /^Continue/ }).click();

    // O4 Bio (optional).
    await expect(page.getByRole("heading", { name: /describe your voice/i })).toBeVisible();
    await page.getByRole("button", { name: "Continue" }).click();

    // O5 Review & start.
    await expect(page.getByRole("heading", { name: /review & start ingestion/i })).toBeVisible();
    await expect(page.getByText("https://example.com/posts/scaling")).toBeVisible();
    await page.getByRole("button", { name: "Start ingestion" }).click();

    // O6 → O7: with succeeded jobs, ingestion runs through to the synthesized
    // style profile. The style traits + sample render verbatim.
    await expect(page.getByRole("heading", { name: /your style/i })).toBeVisible();
    await expect(page.getByText(/Direct and concrete/)).toBeVisible();
    await expect(page.getByText(/Idempotent deploys mean/)).toBeVisible();
    await page.getByRole("button", { name: /Looks right/ }).click();

    // O8 content-test + validate.
    await expect(page.getByRole("heading", { name: /try it on a real topic/i })).toBeVisible();
    await page.getByLabel("Topic").fill("Why idempotent deploys matter");
    await page.getByRole("button", { name: /Generate sample/ }).click();
    await expect(page.getByText(/short, concrete take in your voice/)).toBeVisible();

    const validate = page.getByRole("button", { name: "Validate & finish" });
    await expect(validate).toBeEnabled();
    await validate.click();

    // O9 complete.
    await expect(page.getByRole("heading", { name: /all set/i })).toBeVisible();
    await page.getByRole("button", { name: "Go to dashboard" }).click();
    await expect(page).toHaveURL(/\/$/);
  });

  // O6 ingestion progress: jobs that stay "running" pin the wizard on the
  // progress screen, so the phase checklist + per-source running badge are
  // observable without racing the completion.
  test("O6 surfaces the ingestion phase checklist and per-source progress", async ({ page }) => {
    await installApi(
      page,
      defaults({
        session: DEVREL,
        onboarding: FIRST_RUN,
        scrapeJobCreateStatus: "running",
        scrapeJobPollStatus: "running",
      }),
    );
    await page.goto("/onboarding");
    await page.getByRole("button", { name: "Get started" }).click();
    await page.getByRole("button", { name: /Blog posts/ }).click();
    await page.getByRole("button", { name: "Continue" }).click();
    await page.getByLabel("Source URL").fill("https://example.com/posts/scaling");
    await page.getByRole("button", { name: /^Continue/ }).click();
    await page.getByRole("button", { name: "Continue" }).click(); // O4 bio
    await page.getByRole("button", { name: "Start ingestion" }).click();

    await expect(page.getByRole("heading", { name: /building your style profile/i })).toBeVisible();
    await expect(page.getByText("Scraping & chunking sources")).toBeVisible();
    await expect(page.getByText("Embedding into your corpus")).toBeVisible();
    await expect(page.getByText("Synthesizing your style")).toBeVisible();
    // The per-source progress row carries the live job status.
    await expect(page.getByText("running")).toBeVisible();
  });

  // O6 failure: a synthesis 409 (no corpus accepted) surfaces the inline error
  // with the Edit-sources / Retry affordances.
  test("O6 surfaces an ingestion error with retry", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, onboarding: FIRST_RUN }));
    // Succeeded scrape jobs (default), but synthesis fails → the O6 error state.
    await page.route("**/v1/devrelpersonas/*/style-profile/synthesize", (route) =>
      route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({ error: "no accepted sources to synthesize from" }),
      }),
    );
    await page.goto("/onboarding");
    await page.getByRole("button", { name: "Get started" }).click();
    await page.getByRole("button", { name: /Blog posts/ }).click();
    await page.getByRole("button", { name: "Continue" }).click();
    await page.getByLabel("Source URL").fill("https://example.com/posts/scaling");
    await page.getByRole("button", { name: /^Continue/ }).click();
    await page.getByRole("button", { name: "Continue" }).click(); // O4 bio
    await page.getByRole("button", { name: "Start ingestion" }).click();

    await expect(page.getByRole("alert")).toBeVisible();
    await expect(page.getByRole("button", { name: "Retry" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Edit sources" })).toBeVisible();
  });
});
