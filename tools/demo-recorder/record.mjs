#!/usr/bin/env node
// demo-recorder wrapper: assert -> record -> convert -> cleanup -> deliver.
//
// Usage:  node record.mjs <scenario.mjs> [--slug my-demo] [--orientation landscape|shorts]
//
// A scenario module default-exports one async function:
//
//     export default async function scenario(page, api) { ... }
//     export const meta = { url: 'https://…', orientation: 'landscape', slug: 'my-demo' };
//
// The SAME scenario function is run twice: first headless with NO recording to
// assert it works (a broken demo must never be recorded), then once recorded.
// The WebM is converted to MP4 and removed only after the MP4 is verified.
//
// Pure helpers and the orchestrator take injectable `deps` so each stage can be
// unit-tested without a real browser or ffmpeg (see record.test.mjs).

import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { pathToFileURL } from "node:url";

import { installCursor } from "./lib/cursor.mjs";
import { PACING, launchOptions, normalizeSlowMo, pacedApi } from "./lib/pacing.mjs";

// --- Pure helpers ----------------------------------------------------------

// recordVideo.size MUST equal viewport or the frame is letterboxed (plan §8).
export const VIEWPORTS = {
  landscape: { width: 1920, height: 1080 },
  shorts: { width: 1080, height: 1920 },
};

export function resolveViewport(orientation = "landscape") {
  const size = VIEWPORTS[orientation];
  if (!size) {
    throw new Error(
      `unknown orientation '${orientation}' (expected: ${Object.keys(VIEWPORTS).join(", ")})`,
    );
  }
  // size and viewport are the SAME object by contract.
  return { size, viewport: size };
}

// ffmpeg args: VFR WebM -> CFR 30fps MP4, faststart, silent. Never `-vsync`
// (removed in ffmpeg 9); `-fps_mode` works on ffmpeg 6/7/9 (plan §4/R4).
export function buildConvertArgs(inputWebm, outputMp4) {
  return [
    "-y",
    "-i", inputWebm,
    "-fps_mode", "cfr",
    "-r", "30",
    "-c:v", "libx264",
    "-crf", "20",
    "-preset", "slow",
    "-pix_fmt", "yuv420p",
    "-movflags", "+faststart",
    "-an",
    outputMp4,
  ];
}

export function buildMeta({ url, orientation, durationSec, date, slug }) {
  const lines = [
    `# content/demos/${new Date(date).getUTCFullYear()}/${slug}/`,
    `title: ""`,
    `source_url: ${JSON.stringify(url ?? "")}`,
    `recorded_date: ${JSON.stringify(date)}`,
    `orientation: ${orientation}`,
    `duration_sec: ${durationSec ?? 0}`,
    `silent: true   # music/voiceover added at edit`,
    `tags: []`,
    `files:`,
    `  video: demo.mp4`,
  ];
  return lines.join("\n") + "\n";
}

export function targetDir(contentRoot, date, slug) {
  const year = String(new Date(date).getUTCFullYear());
  return path.join(contentRoot, "content", "demos", year, slug);
}

// --- Stage implementations (default, real) ---------------------------------

const require = createRequire(import.meta.url);

function resolvePlaywright() {
  // Resolved lazily so pure helpers and unit tests never need playwright.
  return require("playwright").chromium;
}

// Run the scenario headless with NO recording to assert it works.
async function assertStage({ scenario, orientation, pacing }) {
  const chromium = resolvePlaywright();
  const { viewport } = resolveViewport(orientation);
  const browser = await chromium.launch(launchOptions({ slowMo: normalizeSlowMo(pacing.slowMoMs) }));
  try {
    const context = await browser.newContext({ viewport });
    await installCursor(context);
    const page = await context.newPage();
    try {
      await scenario(page, pacedApi(page, pacing));
    } finally {
      await context.close();
    }
  } finally {
    await browser.close();
  }
}

// Run the SAME scenario recorded; returns the finalized WebM path.
async function recordStage({ scenario, orientation, pacing, workDir }) {
  const chromium = resolvePlaywright();
  const { size, viewport } = resolveViewport(orientation);
  const browser = await chromium.launch(launchOptions({ slowMo: normalizeSlowMo(pacing.slowMoMs) }));
  let videoPath;
  try {
    const context = await browser.newContext({
      viewport,
      recordVideo: { dir: workDir, size }, // size === viewport
    });
    await installCursor(context);
    const page = await context.newPage();
    try {
      await scenario(page, pacedApi(page, pacing));
      videoPath = await page.video().path();
    } finally {
      // context.close() finalizes the WebM; MUST be in finally or it truncates.
      await context.close();
    }
  } finally {
    await browser.close();
  }
  return videoPath;
}

// Spawn ffmpeg to convert WebM -> MP4.
function convertStage({ inputWebm, outputMp4, spawnFn = spawn }) {
  return new Promise((resolve, reject) => {
    const args = buildConvertArgs(inputWebm, outputMp4);
    const proc = spawnFn("ffmpeg", args, { stdio: ["ignore", "inherit", "inherit"] });
    proc.on("error", reject);
    proc.on("close", (code) =>
      code === 0 ? resolve(outputMp4) : reject(new Error(`ffmpeg exited ${code}`)),
    );
  });
}

// MP4 is valid only if it exists and is non-zero.
async function verifyMp4(mp4Path, statFn = fs.stat) {
  try {
    const st = await statFn(mp4Path);
    return st.size > 0;
  } catch {
    return false;
  }
}

export const defaultDeps = {
  assert: assertStage,
  record: recordStage,
  convert: convertStage,
  verifyMp4,
  removeFile: (p) => fs.rm(p, { force: true }),
  mkdir: (p) => fs.mkdir(p, { recursive: true }),
  copyFile: (src, dst) => fs.copyFile(src, dst),
  writeFile: (p, data) => fs.writeFile(p, data),
  log: (...a) => console.log("[demo-recorder]", ...a),
};

// --- Orchestrator ----------------------------------------------------------

// runPipeline enforces the ordering contract:
//   assert (gate) -> record -> convert -> verify -> deliver -> cleanup.
// If assert throws, record/convert/cleanup NEVER run. The WebM is removed only
// after the MP4 is verified non-zero.
export async function runPipeline(config, deps = defaultDeps) {
  const {
    scenario,
    slug,
    url = null,
    orientation = "landscape",
    pacing = PACING,
    workDir,
    contentRoot = ".",
    date = new Date().toISOString().slice(0, 10),
  } = config;

  if (typeof scenario !== "function") throw new Error("config.scenario must be a function");
  if (!slug) throw new Error("config.slug is required");
  resolveViewport(orientation); // validate early

  // 1. ASSERT — dry run, no recording. The gate: a throw here aborts before any
  //    browser video context is ever opened.
  deps.log("assert: dry run (headless, no recording)");
  await deps.assert({ scenario, orientation, pacing });

  // 2. RECORD — same scenario, recorded.
  deps.log("record: capturing WebM");
  const webm = await deps.record({ scenario, orientation, pacing, workDir });
  if (!webm) throw new Error("record stage produced no video path");

  // 3. CONVERT — WebM -> MP4.
  const outMp4 = path.join(workDir, "demo.mp4");
  deps.log("convert: WebM -> MP4");
  await deps.convert({ inputWebm: webm, outputMp4: outMp4 });

  // 4. VERIFY — MP4 must exist and be non-zero before we touch the WebM.
  const ok = await deps.verifyMp4(outMp4);
  if (!ok) throw new Error(`convert produced no valid MP4: ${outMp4}`);

  // 5. DELIVER — place MP4 + meta.yaml into the Crate.
  const dir = targetDir(contentRoot, date, slug);
  await deps.mkdir(dir);
  const deliveredMp4 = path.join(dir, "demo.mp4");
  await deps.copyFile(outMp4, deliveredMp4);
  const metaPath = path.join(dir, "meta.yaml");
  await deps.writeFile(metaPath, buildMeta({ url, orientation, date, slug }));
  deps.log("deliver:", deliveredMp4);

  // 6. CLEANUP — only now that the MP4 is verified and delivered.
  await deps.removeFile(webm);
  deps.log("cleanup: removed", webm);

  return { mp4: deliveredMp4, meta: metaPath, dir };
}

// --- CLI -------------------------------------------------------------------

function parseArgs(argv) {
  const [scenarioPath, ...rest] = argv;
  const opts = {};
  for (let i = 0; i < rest.length; i += 2) {
    const key = rest[i]?.replace(/^--/, "");
    if (key) opts[key] = rest[i + 1];
  }
  return { scenarioPath, opts };
}

async function main() {
  const { scenarioPath, opts } = parseArgs(process.argv.slice(2));
  if (!scenarioPath) {
    console.error("usage: node record.mjs <scenario.mjs> [--slug s] [--orientation landscape|shorts]");
    process.exit(2);
  }
  const mod = await import(pathToFileURL(path.resolve(scenarioPath)).href);
  const scenario = mod.default;
  const sceneMeta = mod.meta ?? {};
  const slug = opts.slug ?? sceneMeta.slug;
  const orientation = opts.orientation ?? sceneMeta.orientation ?? "landscape";
  const workDir = await fs.mkdtemp(path.join(process.env.TMPDIR ?? "/tmp", "demo-rec-"));

  try {
    const res = await runPipeline({
      scenario,
      slug,
      url: sceneMeta.url ?? null,
      orientation,
      workDir,
      contentRoot: opts["content-root"] ?? ".",
    });
    console.log(JSON.stringify(res, null, 2));
  } catch (err) {
    console.error("[demo-recorder] FAILED:", err.message);
    process.exit(1);
  }
}

// Only run the CLI when executed directly, not when imported by tests.
if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  main();
}
