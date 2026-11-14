#!/usr/bin/env node
// Functional smoke for the baked video-designer: proves the image's ffmpeg can
// run the real assembly pipeline (concat + drawtext title + CFR H.264 master +
// poster). No external assets — two generated brand stings and a title card, so
// it needs nothing but the baked ffmpeg + a drawtext font. Exits non-zero on any
// failure; used by media-actor-image-publish.yml.
//
// Run: node video-designer/smoke.mjs

import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import process from "node:process";

import { runPipeline } from "./assemble.mjs";

const manifest = {
  title: "smoke",
  slug: "smoke",
  platform: "youtube-video",
  segments: [
    { kind: "sting-intro", text: "ContentRemixer", duration: 1.0 },
    { kind: "sting-outro", text: "smoke ok", duration: 1.0 },
  ],
  titleCard: { text: "video-designer smoke", start: 0.2, end: 1.8 },
  // no clip file, no captions, no audio: a self-contained generated cut.
};

async function nonzero(p) {
  const st = await fs.stat(p);
  if (st.size <= 0) throw new Error(`empty: ${p}`);
  return st.size;
}

async function main() {
  const workDir = await fs.mkdtemp(path.join(os.tmpdir(), "vd-smoke-"));
  const contentRoot = await fs.mkdtemp(path.join(os.tmpdir(), "vd-smoke-out-"));
  const t0 = Date.now();
  const res = await runPipeline({ manifest, workDir, contentRoot, date: "2026-01-01" });
  const mp4 = await nonzero(res.mp4);
  const poster = await nonzero(res.poster);
  await nonzero(res.meta);
  console.log(`[video-designer smoke] OK in ${Date.now() - t0}ms — mp4=${mp4}B poster=${poster}B → ${res.dir}`);
}

main().catch((err) => {
  console.error("[video-designer smoke] FAILED:", err.message);
  process.exit(1);
});
