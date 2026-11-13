// smoke.mjs — the DR-2 (ISI-7400) cold-start probe.
//
// A self-contained, trivial record that exercises the WHOLE runtime path the
// real recorder uses — launch Chromium → record a WebM → ffmpeg-transcode to a
// faststart MP4 — WITHOUT depending on DR-1's record.mjs / lib/* (ISI-7399).
// Used by the image workflow's cold-start job to prove: cold pull of the
// published image → completed trivial record < 2 min.
//
// It deliberately mirrors the DR recording gotchas so the probe stays honest:
//   - recordVideo.size MUST equal the viewport (else the WebM is letterboxed),
//   - context.close() runs in `finally` (else the WebM is truncated / never
//     finalized),
//   - the ffmpeg recipe is the DR R4 recipe (cfr 30, libx264, faststart, no
//     -vsync — removed in ffmpeg 9).
//
// Exit 0 on a non-empty MP4; non-zero otherwise. Prints the elapsed record time.

import { chromium } from 'playwright';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, existsSync, statSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const VIEWPORT = { width: 1280, height: 720 };
const start = Date.now();

const workDir = mkdtempSync(join(tmpdir(), 'demo-smoke-'));
const outMp4 = join(workDir, 'smoke.mp4');

let context;
let browser;
try {
  // --no-sandbox: the container runs as root (docker run in CI), where Chromium
  // refuses to start its sandbox. Safe for this throwaway local-page probe.
  browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] });
  // recordVideo.size === viewport — the #1 DR footgun.
  context = await browser.newContext({
    viewport: VIEWPORT,
    recordVideo: { dir: workDir, size: VIEWPORT },
  });
  const page = await context.newPage();

  // A trivial, fully-offline scenario: an inline page with a little motion so
  // the encoder has real frames to write.
  await page.setContent(`
    <!doctype html><html><body style="margin:0;background:#0b0b0f;
      color:#e6e6ea;font:600 48px system-ui;display:grid;place-items:center;
      height:100vh">
      <div id="t">demo-recorder cold-start smoke</div>
      <script>
        let n=0; const t=document.getElementById('t');
        setInterval(()=>{ t.style.opacity = (n++%2)?0.35:1; }, 150);
      </script>
    </body></html>`);
  await page.waitForTimeout(1500);
} finally {
  // Close context FIRST so Playwright finalizes the WebM, then the browser.
  if (context) await context.close();
  if (browser) await browser.close();
}

// Find the WebM Playwright just wrote.
const webm = readdirSync(workDir).find((f) => f.endsWith('.webm'));
if (!webm) {
  console.error('smoke FAILED: no .webm recorded');
  process.exit(1);
}
const webmPath = join(workDir, webm);

// DR R4 transcode: CFR 30, H.264, yuv420p, +faststart, drop audio. NEVER -vsync.
const ff = spawnSync('ffmpeg', [
  '-y', '-i', webmPath,
  '-fps_mode', 'cfr', '-r', '30',
  '-c:v', 'libx264', '-crf', '20',
  '-pix_fmt', 'yuv420p',
  '-movflags', '+faststart',
  '-an',
  outMp4,
], { stdio: ['ignore', 'ignore', 'inherit'] });

if (ff.status !== 0) {
  console.error('smoke FAILED: ffmpeg exited', ff.status);
  process.exit(1);
}
if (!existsSync(outMp4) || statSync(outMp4).size === 0) {
  console.error('smoke FAILED: MP4 missing or empty');
  process.exit(1);
}

const elapsed = ((Date.now() - start) / 1000).toFixed(1);
console.log(`smoke OK: ${outMp4} (${statSync(outMp4).size} bytes) in ${elapsed}s`);
