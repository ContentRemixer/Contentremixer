// Example scenario documenting the demo-recorder contract.
//
// A scenario default-exports one async `scenario(page, api)` and may export
// `meta` (url / orientation / slug). The SAME function is run headless to assert
// (dry run, no recording) and then recorded. Keep everything on screen free of
// real tenant IDs, tokens, emails, or internal hostnames.
//
// This file is intentionally illustrative. The real E2E scenario against
// demo.otelt.io is delivered in DR-3 (ISI-7401).

export const meta = {
  url: "https://example.com",
  orientation: "landscape", // or "shorts"
  slug: "example",
};

export default async function scenario(page, api) {
  // Navigate and let the viewer settle.
  await api.goto(meta.url);

  // Travel to a link with a visible stepped move, then click (ripple fires).
  await api.click(page.getByRole("link", { name: "More information..." }));

  // Read-pause so the result is legible.
  await api.read(1200);
}
