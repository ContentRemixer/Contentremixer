import { defineConfig, devices } from "@playwright/test";

// Web E2E (ISI-7214, W8). Drives the built SPA through `vite preview` serving the
// committed dist (the same bytes Booth embeds, booth/web/embed.go), with every
// Booth JSON API call stubbed via page.route (see e2e/fixtures.ts) so the suite
// is hermetic — no Postgres, no k8s, no Go binary. Covers A1 (login/first-run),
// A3 (states: session-expired/403/404), B6 (scraper review), B3 (content), and
// the admin console. Wired into the two-pipeline CI by .github/workflows/web-ci.yml.
const PORT = 4173;
const BASE_URL = `http://localhost:${PORT}`;

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI
    ? [["github"], ["list"], ["html", { open: "never" }]]
    : "list",
  use: {
    baseURL: BASE_URL,
    trace: "on-first-retry",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  // Build fresh and preview so E2E never greens against a stale dist. Preview
  // serves static files only; all /v1/* traffic is intercepted in-test.
  webServer: {
    command: `npm run build && npm run preview -- --port ${PORT} --strictPort`,
    url: BASE_URL,
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
  },
});
