import { test, expect, installApi, defaults, DEVREL, doc } from "./fixtures";

// B3 — content detail (W5): the document index and the reader with inline
// comments, peer-review threads, and the request-illustration flow (W4 API).
test.describe("B3 content", () => {
  test("empty library shows the empty state", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL })); // no content
    await page.goto("/content");
    await expect(page.getByRole("heading", { name: "Content", exact: true })).toBeVisible();
    await expect(page.getByText("No documents yet")).toBeVisible();
  });

  test("the library lists documents with status", async ({ page }) => {
    await installApi(page, defaults({ session: DEVREL, content: [doc()] }));
    await page.goto("/content");
    await expect(page.getByRole("heading", { name: "Kubernetes show notes" })).toBeVisible();
    await expect(page.getByText("Draft", { exact: true })).toBeVisible();
  });

  test("opening a document shows the reader, body, and review affordances", async ({ page }) => {
    const d = doc();
    await installApi(
      page,
      defaults({
        session: DEVREL,
        content: [d],
        contentDetail: { "doc-1": { doc: d, comments: [], reviews: [], illustrations: [] } },
      }),
    );
    await page.goto("/content");
    await page.getByRole("heading", { name: "Kubernetes show notes" }).click();
    await expect(page).toHaveURL(/\/content\/doc-1$/);
    await expect(page.getByRole("heading", { name: "Kubernetes show notes" })).toBeVisible();
    await expect(page.getByText("This is the draft body for review.")).toBeVisible();
    // Reader affordances: the status control and the three collaboration rails.
    // (Request-illustration lives in the text-selection toolbar, not a static button.)
    await expect(page.getByLabel("Document status")).toBeVisible();
    await expect(page.getByRole("tab", { name: /Comments/ })).toBeVisible();
    await expect(page.getByRole("tab", { name: /Review/ })).toBeVisible();
    await expect(page.getByRole("tab", { name: /Art/ })).toBeVisible();
  });
});
