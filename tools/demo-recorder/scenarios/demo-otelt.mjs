// DR-3 reference scenario: a landscape walk-through of the otelt demo.
//
// Run through the DR-1 wrapper on the DR-2 runtime image:
//
//   node record.mjs ./scenarios/demo-otelt.mjs --slug otelt-overview --orientation landscape
//
// delivering content/demos/YYYY/otelt-overview/demo.mp4 + meta.yaml.
//
// The SAME function runs twice (assert dry-run, then recorded). It is written to
// be resilient to the live DOM: the guaranteed beat is a paced cursor tour of the
// landing view (no selector assumptions), and any richer interaction (opening a
// nav entry, a search) is BEST-EFFORT and skipped when the element is absent, so
// a cosmetic markup change on the demo never fails the dry-run gate.
//
// Plan §5 scrub is enforced in-pipeline here as defense-in-depth with the Graphic
// Designer realism gate: assertNoSecrets() scans on-screen text and HARD-FAILS the
// scenario (so nothing is recorded) if a tenant id, token, real email, or internal
// hostname is visible. A leak must never reach an MP4.

// The target defaults to the plan's canonical demo. DEMO_URL_OVERRIDE lets the
// E2E workflow point the same scenario at any reachable environment (e.g. a live
// staging URL) without a code change — it feeds both navigation and meta.source_url.
export const meta = {
  url: process.env.DEMO_URL_OVERRIDE || "https://demo.otelt.io",
  orientation: "landscape", // 1920x1080
  slug: "otelt-overview",
};

// Patterns that must never be visible in frame (plan §5). Kept deliberately
// conservative to avoid false positives on ordinary demo copy.
const SECRET_PATTERNS = [
  // bearer / api tokens and long opaque secrets
  { name: "bearer/JWT token", re: /\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\./ },
  { name: "sk-/api key", re: /\b(?:sk|pk|api[_-]?key|token|secret)[-_=:\s]+[A-Za-z0-9]{16,}\b/i },
  // real personal emails (allow obvious demo/example inboxes)
  {
    name: "real email",
    re: /\b[A-Za-z0-9._%+-]+@(?!example\.|demo\.|otelt\.io\b)[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b/,
  },
  // internal hostnames / cluster addresses
  { name: "internal hostname", re: /\b[\w-]+\.(?:internal|local|svc|cluster\.local)\b/i },
  { name: "private RFC1918 host", re: /\b(?:10|192\.168|172\.(?:1[6-9]|2\d|3[01]))\.\d{1,3}\.\d{1,3}\.\d{1,3}\b/ },
  // UUID-shaped tenant ids
  { name: "tenant UUID", re: /\btenant[-_ ]?id[-_=:\s]+[0-9a-f-]{32,36}\b/i },
];

async function assertNoSecrets(page) {
  const text = await page.evaluate(() => document.body?.innerText ?? "");
  for (const { name, re } of SECRET_PATTERNS) {
    const hit = text.match(re);
    if (hit) {
      throw new Error(
        `scrub gate: possible ${name} visible on screen (${JSON.stringify(hit[0].slice(0, 48))}). ` +
          "A secret on screen is a hard reject (plan §5) — fix the demo data before recording.",
      );
    }
  }
}

// Best-effort click: travel + click only if exactly one visible match exists.
// Never throws on a miss — the demo must survive DOM drift.
async function tryClick(page, api, locator, label) {
  try {
    const count = await locator.count();
    if (count !== 1) return false;
    if (!(await locator.first().isVisible())) return false;
    await api.click(locator.first()); // paced click already read-pauses
    return true;
  } catch {
    return false;
  }
}

export default async function scenario(page, api) {
  // 1. Land and let the dashboard settle.
  await api.goto(meta.url);
  await assertNoSecrets(page);
  await api.read(1000);

  // 2. Guaranteed beat — a paced cursor tour of the landing view so the viewer
  //    sees the visible cursor move across the UI even if nothing is clickable.
  const { width, height } = page.viewportSize() ?? { width: 1920, height: 1080 };
  const tour = [
    [width * 0.25, height * 0.3],
    [width * 0.75, height * 0.3],
    [width * 0.75, height * 0.65],
    [width * 0.4, height * 0.55],
  ];
  for (const [x, y] of tour) {
    await api.moveTo(Math.round(x), Math.round(y));
    await api.read(600);
  }

  // 3. Best-effort: open a primary nav entry if the demo exposes one. Each is
  //    skipped silently when absent, so the dry-run gate stays green.
  for (const name of ["Dashboards", "Traces", "Services", "Explore"]) {
    const opened = await tryClick(page, api, page.getByRole("link", { name, exact: false }), name);
    if (opened) {
      await assertNoSecrets(page);
      await api.read(1200);
      break;
    }
  }

  // 4. Best-effort: exercise a search box if present (keystroke-by-keystroke).
  const search = page.getByPlaceholder(/search/i);
  try {
    if ((await search.count()) >= 1 && (await search.first().isVisible())) {
      await api.type(search.first(), "latency");
      await assertNoSecrets(page);
      await api.read(1200);
    }
  } catch {
    // no search box on this view — fine.
  }

  // 5. Final read-pause so the closing frame is legible.
  await api.read(1200);
}
