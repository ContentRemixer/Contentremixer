// Human pacing for demo recordings.
//
// Playwright, left to its own devices, moves instantly and types at machine
// speed - the result looks like a bot, not a demo. These helpers slow every
// meaningful action to a watchable cadence: stepped mouse travel, click
// highlights (via the injected cursor), keystroke-by-keystroke typing, and
// read-pauses so a viewer can follow along.
//
// `slowMo` is a LAUNCH option (chromium.launch), NOT a context option - it is
// exported here so record.mjs sets it on launch.

export const PACING = {
  // slowMo delay between every Playwright action, applied at launch.
  // 400-600ms range (plan §4); 500 sits in the middle.
  slowMoMs: 500,
  // page.mouse.move steps: 20-40 so travel is visible, not a teleport.
  moveSteps: 30,
  // pressSequentially per-keystroke delay (ms).
  typeDelayMs: 60,
  // waitForTimeout read-pause after a meaningful action (600-1200ms).
  readPauseMs: 800,
};

// Launch options carrying the human-pace slowMo. Spread into chromium.launch().
// Chromium refuses to run as root without --no-sandbox, and CI job containers
// (GitHub Actions) run as root. PW_NO_SANDBOX=1 opts in — the E2E workflow sets
// it. A demo recorder is not a security boundary, so this is safe where enabled;
// it is OFF by default so non-root/desktop runs keep the sandbox.
export function launchOptions(overrides = {}) {
  const base = { slowMo: PACING.slowMoMs };
  if (process.env.PW_NO_SANDBOX === "1") base.args = ["--no-sandbox"];
  return { ...base, ...overrides };
}

// Clamp slowMo into the researched 400-600ms band.
export function normalizeSlowMo(ms) {
  if (typeof ms !== "number" || Number.isNaN(ms)) return PACING.slowMoMs;
  return Math.max(400, Math.min(600, ms));
}

// Build the paced action API handed to a scenario as `api`.
// Every method moves/acts at human speed and awaits completion.
export function pacedApi(page, pacing = PACING) {
  const steps = pacing.moveSteps;
  const typeDelay = pacing.typeDelayMs;

  async function centerOf(locator) {
    const box = await locator.boundingBox();
    if (!box) throw new Error("pacing: target has no bounding box (not visible?)");
    return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
  }

  const api = {
    // Move the (visible) cursor to absolute coords with visible steps.
    async moveTo(x, y) {
      await page.mouse.move(x, y, { steps });
    },

    // Travel to an element's center, then click it (so the cursor is over the
    // target when the click highlight fires).
    async click(locator) {
      const { x, y } = await centerOf(locator);
      await page.mouse.move(x, y, { steps });
      await locator.click();
      await api.read();
    },

    // Type into a field one keystroke at a time.
    async type(locator, text) {
      await locator.click();
      await locator.pressSequentially(text, { delay: typeDelay });
      await api.read();
    },

    // Read-pause so the viewer can absorb the current state.
    async read(ms = pacing.readPauseMs) {
      await page.waitForTimeout(ms);
    },

    // Navigate and settle.
    async goto(url) {
      await page.goto(url, { waitUntil: "networkidle" });
      await api.read();
    },
  };

  return api;
}
