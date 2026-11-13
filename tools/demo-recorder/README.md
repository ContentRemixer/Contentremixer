# demo-recorder

Runtime for the [`demo-recorder` skill](../../skills/demo-recorder/SKILL.md): an
agent writes a Playwright scenario, and this wrapper records it as a watchable,
human-paced MP4 with a visible cursor, then delivers it to the Crate.

## Pipeline

`record.mjs` orchestrates five stages with a hard ordering contract:

```
assert (headless, NO recording)  ->  record  ->  convert (ffmpeg)  ->  verify  ->  deliver + cleanup
```

- **assert** runs the scenario once headless with no video. A broken scenario
  throws here and is **never recorded**.
- **record** runs the *same* function recorded. `recordVideo.size` equals
  `viewport` (no letterbox); `slowMo` is set at launch; `context.close()` runs in
  `finally` so the WebM is finalized.
- **convert** normalizes the VFR WebM to CFR 30fps MP4
  (`-fps_mode cfr -r 30 … -movflags +faststart -an`; never `-vsync`).
- **verify** requires the MP4 to exist and be non-zero.
- **deliver** writes `content/demos/YYYY/<slug>/demo.mp4` + `meta.yaml`; the WebM
  is removed **only after** the MP4 is verified.

## Usage

```bash
npm ci
node record.mjs ./scenarios/demo-otelt.mjs --slug otelt-overview --orientation landscape
```

A scenario module:

```js
export const meta = { url: "https://demo.otelt.io", orientation: "landscape", slug: "otelt-overview" };
export default async function scenario(page, api) {
  await api.goto(meta.url);
  await api.click(page.getByRole("link", { name: "Dashboards" }));
  await api.type(page.getByPlaceholder("Search"), "latency");
  await api.read(1200);
}
```

`api` is the paced helper set from `lib/pacing.mjs` (`goto`, `moveTo`, `click`,
`type`, `read`) — stepped mouse travel, keystroke-by-keystroke typing, and
read-pauses. `lib/cursor.mjs` injects the visible cursor + click ripple via
`addInitScript`.

## Tests

```bash
node --test   # unit-asserts the pipeline contract (no browser / ffmpeg needed)
```

The tests mock each stage and verify: the dry-run gate (broken scenario never
records), the convert command shape (CFR / faststart / silent / no `-vsync`),
scenario reuse across dry run and take, `recordVideo.size == viewport`, and
cleanup-only-after-MP4-verified.

## Version pin coordination (DR-2)

`playwright` is pinned to `1.64.0` in `package.json`. The DR-2 runtime image
([ISI-7400](/ISI/issues/ISI-7400)) **must** use `mcr.microsoft.com/playwright:v1.64.0-noble`
so the npm client matches the baked-in browsers exactly. If DR-2 lands on a
different patch, bump this pin to match in the same change.

## demowright evaluation (R2)

The plan asked us to evaluate [`demowright`](https://www.npmjs.com/package/demowright)
(SVG cursor + click ripples + keystroke HUD + optional SRT, wrapping the
Playwright config in one line) against the dependency-free injected cursor.

Decision for DR-1: **ship the lean, auditable init-script cursor** (`lib/cursor.mjs`)
— zero added dependency, fully inspectable, and it already covers the realism
floor (visible cursor following `page.mouse`, click ripple, survives navigation
via `addInitScript`). `demowright` remains a documented upgrade path: adopt it in
a later iteration if we want the keystroke HUD or burned-in SRT captions, since
those are additive and not required for the acceptance bar (watchable clip, no
secrets on screen).
