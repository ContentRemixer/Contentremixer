import { test, expect, installApi, defaults, DEVREL, source } from "./fixtures";

// B6 — scraper review (W3): list the sources a scrape Job produced, preview their
// chunks, and accept / rescrape / discard each pending one. Consumes the W2 API.
test.describe("B6 scraper review", () => {
  test("empty pending queue shows the empty state", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL })); // no sources
    await page.goto("/scraper-review");
    await expect(page.getByRole("heading", { name: "Scraper review" })).toBeVisible();
    await expect(page.getByText("No sources are waiting for review.")).toBeVisible();
  });

  test("pending sources render with review actions", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, scrapedSources: [source()] }));
    await page.goto("/scraper-review");
    await expect(page.getByRole("heading", { name: "Scaling Postgres on Kubernetes" })).toBeVisible();
    await expect(page.getByText("Pending review")).toBeVisible();
    await expect(page.getByRole("button", { name: "Accept" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Rescrape" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Discard" })).toBeVisible();
  });

  test("accepting a source toasts success and drops it from the pending view", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, scrapedSources: [source()] }));
    await page.goto("/scraper-review");
    await page.getByRole("button", { name: "Accept" }).click();
    await expect(page.getByText("accepted — chunks queued for the corpus")).toBeVisible();
    // The source moved out of `pending`; the reloaded pending view is now empty.
    await expect(page.getByText("No sources are waiting for review.")).toBeVisible();
  });
});
