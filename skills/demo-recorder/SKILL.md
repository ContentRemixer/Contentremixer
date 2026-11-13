---
name: demo-recorder
description: Record reproducible demo videos of web UIs - a Playwright script navigates, captured as video and converted to MP4, delivered to the Crate. Use when an agent must produce a watchable demo clip of a tool or dashboard.
---
# Demo recorder skill

Turn a web UI into a watchable demo clip: write the walk-through as a Playwright
scenario, record it with human pacing and a visible cursor, convert to MP4, and
deliver it to the Crate as a reusable Sample. The runtime (`tools/demo-recorder/`)
does the orchestration - this skill is the authoring contract and the realism bar.

## Pipeline
1. **Scenario** - write the demo as a Playwright module exporting one async
   `scenario(page, api)`. `api` is the paced helper set (move, click, type, read).
   The same function is run **twice**: once headless with **no recording** to assert
   it works, then once recorded. A broken scenario fails the dry run and is **never
   recorded**.
2. **Record** - headless chromium, `slowMo` 400-600ms (a **launch** option), context
   with `recordVideo` (WebM). `recordVideo.size` MUST equal `viewport`
   (1920x1080 landscape, or 1080x1920 Shorts - the scenario declares `orientation`).
   The fake cursor is injected via `addInitScript`. `context.close()` runs in a
   `finally` or the WebM is truncated.
3. **Convert** - `ffmpeg -i demo.webm -fps_mode cfr -r 30 -c:v libx264 -crf 20
   -preset slow -pix_fmt yuv420p -movflags +faststart -an demo.mp4`. Playwright WebM
   is VFR; normalize to CFR. Use `-fps_mode` (never `-vsync` - removed in ffmpeg 9).
   `-an` because demos are silent by design.
4. **Deliver** - MP4 into `content/demos/YYYY/<slug>/` with a `meta.yaml` (source URL,
   date, duration, orientation). The WebM is removed **only after** the MP4 is
   verified (exists and non-zero). Music / voiceover are added later (see
   video skills) - the delivered clip is silent.

## Realism rules
- **Visible cursor.** Playwright renders no cursor; inject a fake one that follows
  `page.mouse` moves, with a click highlight (ripple / outline flash on targets).
- **Human pace.** Stepped mouse travel (`page.mouse.move(x,y,{steps:20-40})`) before
  each click, keystroke-by-keystroke typing (`pressSequentially(text,{delay:60})`),
  and `waitForTimeout(600-1200)` read-pauses. No rushed transitions.
- **Scrub on screen.** Fake data only - **no tenant IDs, tokens, real emails, or
  internal hostnames** visible in frame. A leak is a hard reject; check before
  delivery.

## Tooling package (provided by the runtime)
- node 20+, `playwright` (pinned == the runtime image tag) with chromium, `ffmpeg` 6+.
- Shipped as the `demo-recorder` image / devcontainer feature (official Playwright
  base + ffmpeg; browsers baked in → cold start < 2 min). Agents run
  `tools/demo-recorder/record.mjs <scenario>`; they do not install browsers at run.

## Checklist
- [ ] Scenario asserted headless (dry run) before any recording
- [ ] `recordVideo.size` == `viewport`; `slowMo` set as a launch option
- [ ] Cursor visible + clicks highlighted; pacing reads as human
- [ ] On-screen scrub: no secrets, real emails, or internal hostnames
- [ ] Convert via `-fps_mode cfr -r 30 … -movflags +faststart -an` (no `-vsync`)
- [ ] MP4 verified non-zero before the WebM is deleted
- [ ] Delivered to `content/demos/YYYY/<slug>/` with `meta.yaml`
