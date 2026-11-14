#!/usr/bin/env node
// video-designer: assemble a finished, captioned, brand-titled video from
// demo-recorder clips in ONE ffmpeg pass (ISI-7396, parent ISI-7389 §7).
//
// Usage:
//   node assemble.mjs <edit.json|edit.mjs> [--slug s] [--platform youtube-video]
//                     [--content-root .] [--asset-root .]
//
// The edit manifest is the authoring contract (see README / examples). It is a
// plain data object loaded from JSON or a .mjs default export:
//
//   { title, slug, platform, segments:[{kind,file?,in?,out?,text?,duration?}],
//     titleCard:{text,start,end}, lowerThird:{text,start,end},
//     captions:{file,mode:"burn"|"sidecar"}, audio:{music?,voiceover?,duck?} }
//
// Pipeline (mirrors demo-recorder's assert->...->deliver ordering contract):
//   build graph -> ffmpeg (assemble) -> verify MP4 -> thumbnail -> deliver
//                  -> (git flush happens in the Job entrypoint, ADR-4).
//
// Pure graph building lives in lib/ffgraph.mjs; this file is the IO shell.

import { spawn } from "node:child_process";
import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { pathToFileURL } from "node:url";

import { resolvePreset, fontCandidates, BRAND } from "./lib/presets.mjs";
import { preRollSec, shiftCaptions } from "./lib/captions.mjs";
import {
  segmentFilter,
  concatFilter,
  drawtextFilter,
  overlayChain,
  audioFilter,
  encodeArgs,
  thumbnailArgs,
} from "./lib/ffgraph.mjs";

// ffmpeg binary: PATH `ffmpeg` in the media image; FFMPEG_BIN overrides (local
// Sample generation points it at an ffmpeg-static binary).
const FFMPEG = process.env.FFMPEG_BIN || "ffmpeg";

// Sting background by role: intro violet, outro orange (brand accents, §7).
const STING_BG = { "sting-intro": BRAND.violet, "sting-outro": BRAND.orange };

// --- Pure graph builder ----------------------------------------------------

// buildGraph turns a manifest + preset into the exact ffmpeg invocation: the
// ordered input args, the single -filter_complex string, the stream maps, and
// the list of small text files the drawtext filters read (textfile= avoids all
// drawtext escaping). No IO — the orchestrator writes the textfiles and spawns.
export function buildGraph(manifest, preset, { fontFile, workDir, assetRoot = ".", captionsFile: captionsOverride } = {}) {
  const { width, height, fps } = preset;
  const segments = manifest.segments ?? [];
  if (segments.length === 0) throw new Error("manifest.segments is empty");

  const inputArgs = [];
  const segFilters = [];
  const vLabels = [];
  const textfiles = [];
  let idx = 0;

  segments.forEach((seg, i) => {
    const isSting = seg.kind === "sting-intro" || seg.kind === "sting-outro";
    if (isSting && !seg.file) {
      // Generated brand card: a solid palette color of the given duration.
      const dur = seg.duration ?? 1.5;
      inputArgs.push("-f", "lavfi", "-i", `color=c=${STING_BG[seg.kind]}:s=${width}x${height}:d=${dur}:r=${fps}`);
    } else {
      if (!seg.file) throw new Error(`segment ${i} (${seg.kind}) needs a file`);
      inputArgs.push("-i", path.resolve(assetRoot, seg.file));
    }
    const raw = `s${i}`;
    segFilters.push(
      segmentFilter({
        inLabel: `${idx}:v`,
        outLabel: raw,
        width,
        height,
        fps,
        trim: { in: seg.in, out: seg.out },
      }),
    );
    // Sting text (optional) overlays as a centered brand title on its card.
    if (isSting && seg.text) {
      const tf = path.join(workDir, `sting-${i}.txt`);
      textfiles.push({ path: tf, content: seg.text });
      segFilters.push(
        `[${raw}]${drawtextFilter({ kind: "title", fontFile, textfilePath: tf, width, height })}[v${i}]`,
      );
    } else {
      // No overlay: rename raw label to the canonical vN via a nullsink-free
      // copy filter so concat sees a stable label set.
      segFilters.push(`[${raw}]null[v${i}]`);
    }
    vLabels.push(`v${i}`);
    idx += 1;
  });

  const parts = [...segFilters, concatFilter(vLabels, "vcat")];

  // Title card + lower-third drawtext (brand palette), then optional burn-in.
  const draws = [];
  if (manifest.titleCard?.text) {
    const tf = path.join(workDir, "title.txt");
    textfiles.push({ path: tf, content: manifest.titleCard.text });
    draws.push(
      drawtextFilter({
        kind: "title",
        fontFile,
        textfilePath: tf,
        start: manifest.titleCard.start,
        end: manifest.titleCard.end,
        width,
        height,
      }),
    );
  }
  if (manifest.lowerThird?.text) {
    const tf = path.join(workDir, "lowerthird.txt");
    textfiles.push({ path: tf, content: manifest.lowerThird.text });
    draws.push(
      drawtextFilter({
        kind: "lowerThird",
        fontFile,
        textfilePath: tf,
        start: manifest.lowerThird.start,
        end: manifest.lowerThird.end,
        width,
        height,
      }),
    );
  }
  // captionsOverride is the re-timed copy the orchestrator wrote; fall back to
  // the manifest path (pure-function/test use with no pre-roll shift).
  const captionsFile =
    captionsOverride ?? (manifest.captions?.file ? path.resolve(assetRoot, manifest.captions.file) : null);
  const captionsMode = manifest.captions?.mode ?? "sidecar";
  const overlay = overlayChain({
    inLabel: "vcat",
    outLabel: "vout",
    draws,
    captionsFile,
    captionsMode,
    width,
    height,
  });
  let videoLabel = "vcat";
  if (overlay) {
    parts.push(overlay);
    videoLabel = "vout";
  }

  // Audio: music bed at the input index after the segments, voiceover next.
  const audio = manifest.audio ?? {};
  let musicLabel = null;
  let voLabel = null;
  if (audio.music) {
    inputArgs.push("-i", path.resolve(assetRoot, audio.music));
    musicLabel = `${idx}:a`;
    idx += 1;
  }
  if (audio.voiceover) {
    inputArgs.push("-i", path.resolve(assetRoot, audio.voiceover));
    voLabel = `${idx}:a`;
    idx += 1;
  }
  const a = audioFilter({ musicLabel, voLabel, duck: audio.duck ?? true });
  if (a) parts.push(a.filter);

  const mapArgs = ["-map", `[${videoLabel}]`];
  if (a) mapArgs.push("-map", `[${a.outLabel}]`);

  return { inputArgs, filterComplex: parts.join(";"), mapArgs, hasAudio: Boolean(a), textfiles };
}

// --- Stage implementations (default, real) ---------------------------------

function runFfmpeg(args, spawnFn = spawn) {
  return new Promise((resolve, reject) => {
    const proc = spawnFn(FFMPEG, args, { stdio: ["ignore", "inherit", "inherit"] });
    proc.on("error", reject);
    proc.on("close", (code) => (code === 0 ? resolve() : reject(new Error(`ffmpeg exited ${code}`))));
  });
}

async function verifyMp4(mp4Path, statFn = fs.stat) {
  try {
    return (await statFn(mp4Path)).size > 0;
  } catch {
    return false;
  }
}

async function resolveFont(candidates = fontCandidates(), statFn = fs.stat) {
  for (const c of candidates) {
    try {
      await statFn(c);
      return c;
    } catch {
      /* next */
    }
  }
  // Fallback: scan the system font tree for any .ttf/.otf. The media image ships
  // fonts-liberation (a candidate above), but layouts drift across base-image
  // bumps — a scan keeps drawtext working without pinning an exact path.
  const found = await scanForFont("/usr/share/fonts");
  if (found) return found;
  throw new Error(`no usable font found (set FONT_FILE); tried: ${candidates.join(", ")}`);
}

async function scanForFont(root) {
  let entries;
  try {
    entries = await fs.readdir(root, { withFileTypes: true });
  } catch {
    return null;
  }
  const dirs = [];
  for (const e of entries) {
    const p = path.join(root, e.name);
    if (e.isDirectory()) dirs.push(p);
    else if (/\.(ttf|otf)$/i.test(e.name)) return p;
  }
  for (const d of dirs) {
    const hit = await scanForFont(d);
    if (hit) return hit;
  }
  return null;
}

export const defaultDeps = {
  assemble: ({ args }) => runFfmpeg(args),
  thumbnail: ({ args }) => runFfmpeg(args),
  verifyMp4,
  resolveFont,
  mkdir: (p) => fs.mkdir(p, { recursive: true }),
  copyFile: (src, dst) => fs.copyFile(src, dst),
  readFile: (p) => fs.readFile(p, "utf8"),
  writeFile: (p, data) => fs.writeFile(p, data),
  log: (...a) => console.log("[video-designer]", ...a),
};

// --- Pure helpers ----------------------------------------------------------

export function targetDir(contentRoot, date, slug) {
  const year = String(new Date(date).getUTCFullYear());
  return path.join(contentRoot, "content", "videos", year, slug);
}

export function buildMeta({ title, platform, slug, date, hasAudio, captionsMode, captionsName, poster }) {
  return (
    [
      `# content/videos/${new Date(date).getUTCFullYear()}/${slug}/`,
      `title: ${JSON.stringify(title ?? "")}`,
      `platform: ${platform}`,
      `assembled_date: ${JSON.stringify(date)}`,
      `has_audio: ${hasAudio}`,
      `captions: ${captionsMode}`, // burn | sidecar (a sidecar file ships either way)
      `files:`,
      `  video: video.mp4`,
      `  poster: ${poster}`,
      captionsName ? `  captions: ${captionsName}` : null,
    ]
      .filter(Boolean)
      .join("\n") + "\n"
  );
}

// --- Orchestrator ----------------------------------------------------------

export async function runPipeline(config, deps = defaultDeps) {
  const {
    manifest,
    slug = manifest.slug,
    platform = manifest.platform ?? "youtube-video",
    contentRoot = ".",
    assetRoot = ".",
    workDir,
    date = new Date().toISOString().slice(0, 10),
  } = config;

  if (!slug) throw new Error("slug is required (manifest.slug or --slug)");
  if (!workDir) throw new Error("workDir is required");
  const preset = resolvePreset(platform);

  const fontFile = await deps.resolveFont();
  deps.log("platform", preset.name, `${preset.width}x${preset.height}@${preset.fps}`, "font", fontFile);

  await deps.mkdir(workDir);

  // 0. CAPTIONS — re-time the cue file by the intro-sting pre-roll so burned
  //    and sidecar captions line up with the assembled cut (lib/captions.mjs).
  let captionsFile = null;
  let captionExt = ".vtt";
  if (manifest.captions?.file) {
    const srcCaptions = path.resolve(assetRoot, manifest.captions.file);
    captionExt = path.extname(srcCaptions) || ".vtt";
    const offset = preRollSec(manifest.segments);
    const shifted = shiftCaptions(await deps.readFile(srcCaptions), offset);
    captionsFile = path.join(workDir, `captions${captionExt}`);
    await deps.writeFile(captionsFile, shifted);
    if (offset) deps.log("captions: shifted", offset, "s for intro pre-roll");
  }

  // 1. BUILD GRAPH — pure; then write the small text files it references.
  const graph = buildGraph(manifest, preset, { fontFile, workDir, assetRoot, captionsFile });
  for (const tf of graph.textfiles) await deps.writeFile(tf.path, tf.content);

  // 2. ASSEMBLE — one ffmpeg pass. -shortest clamps output to the video master.
  const outMp4 = path.join(workDir, "video.mp4");
  const args = [
    "-y",
    ...graph.inputArgs,
    "-filter_complex", graph.filterComplex,
    ...graph.mapArgs,
    ...encodeArgs({ fps: preset.fps, hasAudio: graph.hasAudio }),
    ...(graph.hasAudio ? ["-shortest"] : []),
    outMp4,
  ];
  deps.log("assemble: ffmpeg", graph.inputArgs.length / 2, "inputs");
  await deps.assemble({ args });

  // 3. VERIFY before anything downstream reads the file.
  if (!(await deps.verifyMp4(outMp4))) throw new Error(`assemble produced no valid MP4: ${outMp4}`);

  // 4. THUMBNAIL — poster frame.
  const posterName = "poster.jpg";
  const outPoster = path.join(workDir, posterName);
  await deps.thumbnail({ args: thumbnailArgs(outMp4, outPoster, 1) });

  // 5. DELIVER into the Crate: video + poster + caption sidecar + meta.
  const captionsMode = manifest.captions?.mode ?? "sidecar";
  const dir = targetDir(contentRoot, date, slug);
  await deps.mkdir(dir);
  const deliveredMp4 = path.join(dir, "video.mp4");
  await deps.copyFile(outMp4, deliveredMp4);
  const deliveredPoster = path.join(dir, posterName);
  await deps.copyFile(outPoster, deliveredPoster);
  // Always deliver the (re-timed) caption sidecar for a11y, even in burn mode —
  // a sidecar track lets viewers toggle/translate and search engines index it.
  let captionsName = null;
  if (captionsFile) {
    captionsName = `captions${captionExt}`;
    await deps.copyFile(captionsFile, path.join(dir, captionsName));
  }
  const metaPath = path.join(dir, "meta.yaml");
  await deps.writeFile(
    metaPath,
    buildMeta({
      title: manifest.title,
      platform: preset.name,
      slug,
      date,
      hasAudio: graph.hasAudio,
      captionsMode,
      captionsName,
      poster: posterName,
    }),
  );
  deps.log("deliver:", deliveredMp4);

  return { mp4: deliveredMp4, poster: deliveredPoster, meta: metaPath, dir };
}

// --- manifest loading + CLI ------------------------------------------------

export async function loadManifest(manifestPath) {
  const abs = path.resolve(manifestPath);
  if (abs.endsWith(".json")) return JSON.parse(await fs.readFile(abs, "utf8"));
  const mod = await import(pathToFileURL(abs).href);
  return mod.default ?? mod.manifest;
}

function parseArgs(argv) {
  const [manifestPath, ...rest] = argv;
  const opts = {};
  for (let i = 0; i < rest.length; i += 2) {
    const key = rest[i]?.replace(/^--/, "");
    if (key) opts[key] = rest[i + 1];
  }
  return { manifestPath, opts };
}

async function main() {
  const { manifestPath, opts } = parseArgs(process.argv.slice(2));
  if (!manifestPath) {
    console.error(
      "usage: node assemble.mjs <edit.json|edit.mjs> [--slug s] [--platform youtube-video] [--content-root .] [--asset-root .]",
    );
    process.exit(2);
  }
  const manifest = await loadManifest(manifestPath);
  const workDir = await fs.mkdtemp(path.join(process.env.TMPDIR ?? "/tmp", "video-asm-"));
  try {
    const res = await runPipeline({
      manifest,
      slug: opts.slug ?? manifest.slug,
      platform: opts.platform ?? manifest.platform,
      contentRoot: opts["content-root"] ?? ".",
      assetRoot: opts["asset-root"] ?? ".",
      workDir,
    });
    console.log(JSON.stringify(res, null, 2));
  } catch (err) {
    console.error("[video-designer] FAILED:", err.message);
    process.exit(1);
  }
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  main();
}
