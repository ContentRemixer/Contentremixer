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
different patch, bump this pin to match in the same change (Dockerfile `FROM`,
`package.json`, `package-lock.json`, and `.devcontainer/demo-recorder/devcontainer.json`).

## Runtime image & cold start (DR-2, ISI-7400)

`Dockerfile.demo-recorder` packages this wrapper so an agent (or the DR-3 E2E)
runs `node record.mjs` with **cold start < 2 min** — nothing is installed at
container start:

- base `mcr.microsoft.com/playwright:v1.64.0-noble` bakes Node, the browsers, and
  every system dep; we add only `ffmpeg` (apt). `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1`
  makes `npm ci` trust the baked browsers (no re-download, no `playwright install`).
- `COPY package*.json` + `npm ci` land before source so the dep layer stays warm.

Published multi-arch (amd64, arm64) as `ghcr.io/contentremixer/demo-recorder` by
`.github/workflows/demo-recorder-image-publish.yml` — cosign keyless-signed with
SBOM + max provenance, the scrape-job lane ([ISI-7343]) shape. The `sha-<12>` tag
is the hand-off contract.

Cold start is **cold image pull + a trivial record**. The publish workflow's
`cold-start` job proves it on a fresh runner: timed `docker pull`, an arch guard
(pulled arch == runner arch — guards the "tag briefly publishes arm64-only"
footgun), then `docker run <img> node smoke.mjs` (timed), asserting
`pull + record < 120s`; the measured split is written to the job summary.
`smoke.mjs` is a self-contained probe (launch → record → ffmpeg MP4) that does
not depend on a scenario. Locally: `npm ci && npm run smoke` (needs ffmpeg +
browsers on PATH).

[ISI-7388]: https://github.com/ContentRemixer/Contentremixer
[ISI-7343]: https://github.com/ContentRemixer/Contentremixer
