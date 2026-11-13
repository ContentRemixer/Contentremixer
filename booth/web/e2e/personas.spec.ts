import {
  test,
  expect,
  installApi,
  defaults,
  DEVREL,
  persona,
  styleProfile,
  promoteResult,
} from "./fixtures";

// D7 — persona-management dashboard (ISI-7362, plan ISI-7350 §6). The returning
// DevRel's surfaces: P1 overview (library + synthesized style + badges), P2
// sources / re-ingest, P3 extra-context / bio, P4 standalone content-test →
// validate → promote (D4/D9). Hermetic via fixtures.ts; a default DevRel session
// reports "already onboarded" (needsOnboarding:false) so the gate renders the
// shell rather than bouncing into the wizard.

// An unvalidated persona (only the Ready condition) so P4 offers "Validate".
function unvalidated() {
  return persona({
    status: { phase: "Ready", exemplarCount: 7, conditions: [{ type: "Ready", status: "True" }] },
  });
}

test.describe("D7 P1 — persona overview", () => {
  test("empty library shows the no-persona state", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL })); // no personas
    await page.goto("/personas");
    await expect(page.getByRole("heading", { name: "My Personas" })).toBeVisible();
    await expect(page.getByText("No persona yet")).toBeVisible();
  });

  test("lists the persona with its synthesized style and badges", async ({ page }) => {
    await installApi(
      page,
      defaults({
        session: DEVREL,
        personas: [persona()],
        styleProfiles: { robin: styleProfile({ validated: true }) },
      }),
    );
    await page.goto("/personas");
    await expect(page.getByRole("heading", { name: "My Personas" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "robin", exact: true })).toBeVisible();
    // The synthesized style profile (D3) renders inline in the detail.
    await expect(page.getByText(/Direct and concrete/)).toBeVisible();
    await expect(page.getByRole("heading", { name: "Style profile" })).toBeVisible();
    // Direct links into the P2/P4 editors.
    await expect(page.getByRole("button", { name: "Edit / re-ingest" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Run a content test" })).toBeVisible();
  });
});

test.describe("D7 P2 — sources / re-ingest", () => {
  test("renders the persona's sources and clears dirty after a save", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, personas: [persona()] }));
    // Reach P2 the way a DevRel does — from the P1 overview (base:"./" makes a
    // hard deep-link reload fail to resolve assets, so navigate client-side).
    await page.goto("/personas");
    await page.getByRole("button", { name: "Edit / re-ingest" }).click();
    await expect(page.getByRole("heading", { name: "Sources — robin" })).toBeVisible();

    const save = page.getByRole("button", { name: "Save changes" });
    await expect(save).toBeDisabled(); // pristine

    // Editing a source URL dirties the form, enabling Save; a successful PUT
    // re-syncs the rows so the form is pristine (Save disabled) again.
    await page.getByLabel("URL").fill("https://example.com/posts/updated");
    await expect(save).toBeEnabled();
    await save.click();
    await expect(save).toBeDisabled();
  });

  test("re-ingest enqueues a scrape job and lists it", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, personas: [persona()] }));
    await page.goto("/personas");
    await page.getByRole("button", { name: "Edit / re-ingest" }).click();
    await expect(page.getByRole("heading", { name: "Sources — robin" })).toBeVisible();
    await page.getByRole("button", { name: "Save & re-ingest all" }).click();

    await expect(page.getByRole("heading", { name: "Ingestion jobs" })).toBeVisible();
    await expect(page.getByText("https://example.com/posts/scaling")).toBeVisible();
    // `exact` targets the job status badge, not the card subtitle which also
    // contains the word ("Queued → scraping → succeeded.").
    await expect(page.getByText("succeeded", { exact: true })).toBeVisible();
  });
});

test.describe("D7 P3 — extra context / bio", () => {
  test("edits the bio and clears dirty after a save", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, personas: [persona()] }));
    await page.goto("/personas");
    // The "Extra context" card's Edit action (exact — "Edit / re-ingest" also
    // contains "Edit") routes to P3.
    await page.getByRole("button", { name: "Edit", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Extra context — robin" })).toBeVisible();

    const save = page.getByRole("button", { name: "Save changes" });
    await expect(save).toBeDisabled();

    await page.getByLabel("Bio").fill("Warm, concrete, and example-first — for working developers.");
    await expect(save).toBeEnabled();
    await save.click();
    await expect(save).toBeDisabled();
  });
});

test.describe("D7 P4 — content test → validate → promote", () => {
  test("generates a sample, validates the style, and degrades promote on 404", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, personas: [unvalidated()] }));
    await page.goto("/personas");
    await page.getByRole("button", { name: "Run a content test" }).click();
    await expect(page.getByRole("heading", { name: "Content test — robin" })).toBeVisible();
    await expect(page.getByText("Style not validated")).toBeVisible();

    await page.getByLabel("Topic").fill("Observability for serverless workloads");
    await page.getByRole("button", { name: "Generate sample" }).click();
    await expect(page.getByRole("heading", { name: "Generated sample" })).toBeVisible();
    await expect(page.getByText(/short, concrete take in your voice/)).toBeVisible();

    // Validate flips the persona's Validated condition.
    await page.getByRole("button", { name: "Validate my style" }).click();
    await expect(
      page.getByText("Your persona can now be used for content generation.", { exact: true }),
    ).toBeVisible();
    await expect(page.getByText("Style not validated")).toHaveCount(0);

    // Promote is an optional surface: a Booth without the route 404s → a hint,
    // not an error (default fixtures mount no promote route).
    await page.getByRole("button", { name: "Suggest LinkedIn post" }).click();
    await expect(
      page.getByText("Promotion suggestions are not available on this deployment", { exact: true }),
    ).toBeVisible();
  });

  test("suggests an editable LinkedIn post when the promote route is mounted", async ({ page }) => {
    await installApi(
      page,
      defaults({ session: DEVREL, personas: [persona()], promote: promoteResult() }),
    );
    await page.goto("/personas");
    await page.getByRole("button", { name: "Run a content test" }).click();
    await expect(page.getByRole("heading", { name: "Content test — robin" })).toBeVisible();
    await page.getByLabel("Topic").fill("Why idempotent deploys matter");
    await page.getByRole("button", { name: "Generate sample" }).click();
    await expect(page.getByRole("heading", { name: "Generated sample" })).toBeVisible();

    await page.getByRole("button", { name: "Suggest LinkedIn post" }).click();
    await expect(page.getByRole("heading", { name: "Suggested LinkedIn post" })).toBeVisible();
    await expect(page.getByLabel(/Draft/)).toHaveValue(/Most deploys fail for boring reasons/);
  });
});
