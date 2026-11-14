// Unit tests for the video-designer pipeline. Run: node --test
//
// These assert the CONTRACT with no real ffmpeg: the filtergraph shape, the
// encode footguns (CFR not -vsync; R128 loudnorm), ducking wiring, platform
// presets, and the deliver ordering (verify-before-thumbnail-before-deliver).

import assert from "node:assert/strict";
import { test } from "node:test";

import { resolvePreset, BRAND, LOUDNESS } from "./lib/presets.mjs";
import {
  segmentFilter,
  concatFilter,
  drawtextFilter,
  overlayChain,
  audioFilter,
  encodeArgs,
  thumbnailArgs,
  escapeGraphValue,
} from "./lib/ffgraph.mjs";
import { preRollSec, shiftCaptions } from "./lib/captions.mjs";
import { buildGraph, buildMeta, targetDir, runPipeline } from "./assemble.mjs";

// --- presets ---------------------------------------------------------------

test("resolvePreset maps content types and aliases; rejects unknown", () => {
  assert.equal(resolvePreset("youtube-video").width, 1920);
  assert.equal(resolvePreset("youtube-short").height, 1920);
  assert.equal(resolvePreset("youtube-short").maxSec, 180); // Shorts <=3min
  assert.equal(resolvePreset("linkedin-post").width, 1080);
  assert.equal(resolvePreset("landscape").name, "youtube-video"); // alias
  assert.equal(resolvePreset("shorts").name, "youtube-short");
  assert.throws(() => resolvePreset("tiktok"), /unknown platform/);
});

// --- filtergraph builders --------------------------------------------------

test("segmentFilter normalizes to canvas and only trims when asked", () => {
  const f = segmentFilter({ inLabel: "0:v", outLabel: "s0", width: 1920, height: 1080, fps: 30, trim: {} });
  assert.ok(!f.includes("trim="), "no trim when in/out absent");
  assert.ok(f.includes("scale=1920:1080:force_original_aspect_ratio=decrease"));
  assert.ok(f.includes("pad=1920:1080"));
  assert.ok(f.includes("setsar=1") && f.includes("fps=30") && f.includes("format=yuv420p"));

  const t = segmentFilter({ inLabel: "1:v", outLabel: "s1", width: 1080, height: 1920, fps: 30, trim: { in: 0.5, out: 22 } });
  assert.ok(t.includes("trim=start=0.5:end=22"), "trims dead air");
  assert.ok(t.includes("setpts=PTS-STARTPTS"), "resets PTS after trim");
});

test("concatFilter wires N labels into one video stream", () => {
  assert.equal(concatFilter(["v0", "v1", "v2"], "vcat"), "[v0][v1][v2]concat=n=3:v=1:a=0[vcat]");
});

test("drawtextFilter uses brand palette per kind and textfile (no escaping)", () => {
  const title = drawtextFilter({ kind: "title", fontFile: "/f.ttf", textfilePath: "/w/title.txt", start: 1, end: 4, width: 1920, height: 1080 });
  assert.ok(title.includes(`boxcolor=${BRAND.violet}@0.85`), "title = violet");
  assert.ok(title.includes("textfile=/w/title.txt") && !title.includes(":text="), "textfile, never the text= option");
  assert.ok(title.includes("enable='between(t,1,4)'"), "time-gated; quotes protect the commas");

  const lt = drawtextFilter({ kind: "lowerThird", fontFile: "/f.ttf", textfilePath: "/w/l.txt", width: 1920, height: 1080 });
  assert.ok(lt.includes(`boxcolor=${BRAND.magenta}@0.85`), "lower-third = magenta");
  assert.ok(!lt.includes("enable="), "no enable when untimed");
});

test("overlayChain adds burn-in subtitles only in burn mode; passthrough when empty", () => {
  const burn = overlayChain({ inLabel: "vcat", outLabel: "vout", draws: ["drawtext=x"], captionsFile: "/c.srt", captionsMode: "burn", width: 1920, height: 1080 });
  assert.ok(burn.startsWith("[vcat]drawtext=x,subtitles=/c.srt"), "chains caption burn after draws");
  assert.ok(burn.endsWith("[vout]"));

  const sidecar = overlayChain({ inLabel: "vcat", outLabel: "vout", draws: ["drawtext=x"], captionsFile: "/c.srt", captionsMode: "sidecar", width: 1920, height: 1080 });
  assert.ok(!sidecar.includes("subtitles="), "sidecar mode does not burn");

  assert.equal(overlayChain({ inLabel: "vcat", outLabel: "vout", draws: [], width: 1920, height: 1080 }), "", "empty => passthrough");
});

test("audioFilter: music ducked under VO, mastered to R128; degrades to single/none", () => {
  const both = audioFilter({ musicLabel: "3:a", voLabel: "4:a", duck: true });
  assert.ok(both.filter.includes("sidechaincompress"), "music ducked by VO");
  assert.ok(both.filter.includes("[4:a]asplit=2"), "VO split: one key, one mix (not reused)");
  assert.ok(both.filter.includes(`loudnorm=I=${LOUDNESS.I}:TP=${LOUDNESS.TP}:LRA=${LOUDNESS.LRA}`), "R128 master");
  assert.ok(both.filter.endsWith(",apad[aout]"), "padded to video master for -shortest");

  const noDuck = audioFilter({ musicLabel: "3:a", voLabel: "4:a", duck: false });
  assert.ok(!noDuck.filter.includes("sidechaincompress") && noDuck.filter.includes("amix=inputs=2"));

  assert.ok(audioFilter({ musicLabel: "3:a", voLabel: null }).filter.startsWith("[3:a]loudnorm"));
  assert.ok(audioFilter({ musicLabel: null, voLabel: "4:a" }).filter.startsWith("[4:a]loudnorm"));
  assert.equal(audioFilter({ musicLabel: null, voLabel: null }), null, "silent => no audio graph");
});

test("encodeArgs: CFR via -fps_mode (never -vsync), faststart; -an only when silent", () => {
  const withA = encodeArgs({ fps: 30, hasAudio: true });
  assert.ok(withA.includes("-fps_mode") && withA[withA.indexOf("-fps_mode") + 1] === "cfr");
  assert.ok(!withA.includes("-vsync"), "never -vsync (removed in ffmpeg 9)");
  assert.ok(withA.includes("+faststart") && withA.includes("yuv420p"));
  assert.ok(withA.includes("-c:a") && withA.includes("aac"));
  assert.ok(!withA.includes("-an"));

  const silent = encodeArgs({ fps: 30, hasAudio: false });
  assert.ok(silent.includes("-an") && !silent.includes("aac"));
});

test("thumbnailArgs seeks and grabs exactly one frame", () => {
  const a = thumbnailArgs("/w/video.mp4", "/w/poster.jpg", 1);
  assert.deepEqual(a, ["-y", "-ss", "1", "-i", "/w/video.mp4", "-frames:v", "1", "-update", "1", "-q:v", "3", "/w/poster.jpg"]);
});

test("escapeGraphValue escapes graph delimiters", () => {
  assert.equal(escapeGraphValue("a:b'c"), "a\\:b\\'c");
});

// --- buildGraph (composition) ---------------------------------------------

const MANIFEST = {
  title: "T",
  slug: "demo",
  platform: "youtube-video",
  segments: [
    { kind: "sting-intro", text: "Brand", duration: 1.5 },
    { kind: "clip", file: "content/demos/2026/x/demo.mp4", in: 0.5, out: 10 },
    { kind: "sting-outro", text: "Bye", duration: 2 },
  ],
  titleCard: { text: "Title", start: 1.5, end: 4 },
  lowerThird: { text: "Name", start: 5, end: 9 },
  captions: { file: "content/demos/2026/x/captions.srt", mode: "burn" },
  audio: { music: "assets/bed.mp3", voiceover: "assets/vo.mp3", duck: true },
};

test("buildGraph wires lavfi stings, a real clip input, and audio input indices", () => {
  const preset = resolvePreset("youtube-video");
  const g = buildGraph(MANIFEST, preset, { fontFile: "/f.ttf", workDir: "/w", assetRoot: "/root" });

  // 3 video inputs (intro lavfi, clip, outro lavfi) + music + vo = 5 inputs.
  const lavfi = g.inputArgs.filter((a) => a === "lavfi").length;
  assert.equal(lavfi, 2, "both stings are generated lavfi color cards");
  assert.ok(g.inputArgs.includes("/root/content/demos/2026/x/demo.mp4"), "clip resolved against assetRoot");
  assert.ok(g.inputArgs.includes("/root/assets/bed.mp3") && g.inputArgs.includes("/root/assets/vo.mp3"));

  // Audio inputs come AFTER the 3 video inputs => music=3:a, vo=4:a.
  assert.ok(g.filterComplex.includes("[3:a]") && g.filterComplex.includes("[4:a]asplit"), "audio indices follow segments");
  assert.deepEqual(g.mapArgs, ["-map", "[vout]", "-map", "[aout]"]);
  assert.equal(g.hasAudio, true);

  // Three text files (intro sting, outro sting, title, lower-third) = 4.
  assert.equal(g.textfiles.length, 4);
  assert.ok(g.textfiles.some((t) => t.content === "Brand" && t.path === "/w/sting-0.txt"));

  // concat sees exactly the three canonical labels.
  assert.ok(g.filterComplex.includes("[v0][v1][v2]concat=n=3"));
});

test("buildGraph: no audio block => silent, video-only map", () => {
  const preset = resolvePreset("youtube-video");
  const { audio, ...silent } = MANIFEST; // drop audio
  void audio;
  const g = buildGraph(silent, preset, { fontFile: "/f.ttf", workDir: "/w", assetRoot: "." });
  assert.equal(g.hasAudio, false);
  assert.deepEqual(g.mapArgs, ["-map", "[vout]"]);
});

test("buildGraph throws on empty segments / fileless clip", () => {
  const preset = resolvePreset("youtube-video");
  assert.throws(() => buildGraph({ segments: [] }, preset, { fontFile: "/f", workDir: "/w" }), /segments is empty/);
  assert.throws(
    () => buildGraph({ segments: [{ kind: "clip" }] }, preset, { fontFile: "/f", workDir: "/w" }),
    /needs a file/,
  );
});

// --- deliver helpers + orchestrator ordering -------------------------------

test("targetDir / buildMeta land under content/videos/<year>/<slug>", () => {
  assert.equal(targetDir("/root", "2026-11-14", "demo"), "/root/content/videos/2026/demo");
  const meta = buildMeta({ title: "T", platform: "youtube-video", slug: "demo", date: "2026-11-14", hasAudio: true, captionsMode: "burn", captionsName: "captions.srt", poster: "poster.jpg" });
  assert.ok(meta.includes("platform: youtube-video") && meta.includes("video: video.mp4") && meta.includes("captions: captions.srt"));
});

test("preRollSec sums leading intro stings; stops at the first clip", () => {
  assert.equal(preRollSec(MANIFEST.segments), 1.5, "only the intro sting precedes the clip");
  assert.equal(preRollSec([{ kind: "clip", file: "a" }]), 0, "clip first => no pre-roll");
  assert.equal(preRollSec([{ kind: "sting-intro", duration: 1.5 }, { kind: "sting-intro", duration: 1 }, { kind: "clip" }]), 2.5);
});

test("shiftCaptions re-times SRT and VTT cues, preserving the separator", () => {
  const srt = "1\n00:00:00,200 --> 00:00:03,000\nhi\n";
  const out = shiftCaptions(srt, 1.5);
  assert.ok(out.includes("00:00:01,700 --> 00:00:04,500"), "SRT comma, +1.5s");
  const vtt = "WEBVTT\n\n00:00:00.200 --> 00:00:03.000\nhi\n";
  assert.ok(shiftCaptions(vtt, 1.5).includes("00:00:01.700 --> 00:00:04.500"), "VTT dot, +1.5s");
  assert.equal(shiftCaptions(srt, 0), srt, "zero offset is a no-op");
});

test("runPipeline order: build -> assemble -> verify -> thumbnail -> deliver", async () => {
  const calls = [];
  const deps = {
    resolveFont: async () => "/f.ttf",
    assemble: async () => calls.push("assemble"),
    thumbnail: async () => calls.push("thumbnail"),
    verifyMp4: async () => (calls.push("verify"), true),
    mkdir: async () => {},
    copyFile: async () => calls.push("deliver"),
    readFile: async () => "1\n00:00:00,200 --> 00:00:03,000\nhi\n",
    writeFile: async () => {},
    log: () => {},
  };
  const res = await runPipeline(
    { manifest: MANIFEST, slug: "demo", contentRoot: "/root", assetRoot: "/root", workDir: "/w", date: "2026-11-14" },
    deps,
  );
  assert.deepEqual(
    calls.filter((c, i) => calls.indexOf(c) === i),
    ["assemble", "verify", "thumbnail", "deliver"],
    "verify precedes thumbnail+deliver",
  );
  assert.ok(res.mp4.endsWith("content/videos/2026/demo/video.mp4"));
});

test("runPipeline aborts if the MP4 fails verification (never delivers)", async () => {
  const calls = [];
  const deps = {
    resolveFont: async () => "/f.ttf",
    assemble: async () => calls.push("assemble"),
    thumbnail: async () => calls.push("thumbnail"),
    verifyMp4: async () => false,
    mkdir: async () => {},
    copyFile: async () => calls.push("deliver"),
    readFile: async () => "1\n00:00:00,200 --> 00:00:03,000\nhi\n",
    writeFile: async () => {},
    log: () => {},
  };
  await assert.rejects(
    runPipeline({ manifest: MANIFEST, slug: "demo", workDir: "/w", date: "2026-11-14" }, deps),
    /no valid MP4/,
  );
  assert.ok(!calls.includes("thumbnail") && !calls.includes("deliver"), "no downstream work on bad MP4");
});
