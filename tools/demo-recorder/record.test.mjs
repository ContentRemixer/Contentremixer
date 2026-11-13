// Unit tests for the demo-recorder pipeline. Run: node --test
//
// These assert the pipeline CONTRACT with mocked stages - no real browser or
// ffmpeg - covering: the dry-run gate, the convert command shape, scenario
// reuse, viewport==size, and cleanup-only-after-MP4-verified.

import assert from "node:assert/strict";
import { test } from "node:test";

import {
  buildConvertArgs,
  resolveViewport,
  targetDir,
  buildMeta,
  runPipeline,
} from "./record.mjs";

// Minimal recording deps: every stage is a spy that records call order.
function makeDeps(overrides = {}) {
  const calls = [];
  const deps = {
    assert: async (ctx) => {
      calls.push("assert");
      deps._assertArgs = ctx;
    },
    record: async (ctx) => {
      calls.push("record");
      deps._recordArgs = ctx;
      return "/work/demo.webm";
    },
    convert: async (ctx) => {
      calls.push("convert");
      deps._convertArgs = ctx;
      return ctx.outputMp4;
    },
    verifyMp4: async () => {
      calls.push("verify");
      return true;
    },
    removeFile: async () => {
      calls.push("cleanup");
    },
    mkdir: async () => {},
    copyFile: async () => {
      calls.push("deliver");
    },
    writeFile: async () => {},
    log: () => {},
    _calls: calls,
    ...overrides,
  };
  return deps;
}

const baseConfig = (scenario) => ({
  scenario,
  slug: "my-demo",
  url: "https://demo.otelt.io",
  orientation: "landscape",
  workDir: "/work",
  contentRoot: "/repo",
  date: "2026-11-13",
});

test("convert command shape: CFR 30fps, faststart, silent, NO -vsync", () => {
  const args = buildConvertArgs("in.webm", "out.mp4");
  const joined = args.join(" ");
  assert.match(joined, /-fps_mode cfr/);
  assert.match(joined, /-r 30/);
  assert.match(joined, /-c:v libx264/);
  assert.match(joined, /-pix_fmt yuv420p/);
  assert.match(joined, /-movflags \+faststart/);
  assert.ok(args.includes("-an"), "must mux no audio");
  assert.ok(!args.includes("-vsync"), "-vsync removed in ffmpeg 9; must use -fps_mode");
  assert.equal(args[args.length - 1], "out.mp4", "output must be last positional");
});

test("recordVideo.size equals viewport (no letterbox)", () => {
  const { size, viewport } = resolveViewport("landscape");
  assert.deepEqual(size, viewport);
  assert.deepEqual(size, { width: 1920, height: 1080 });
  assert.deepEqual(resolveViewport("shorts").size, { width: 1080, height: 1920 });
  assert.throws(() => resolveViewport("portrait"), /unknown orientation/);
});

test("happy path runs stages in order and cleans up AFTER deliver", async () => {
  const deps = makeDeps();
  const scenario = async () => {};
  const res = await runPipeline(baseConfig(scenario), deps);
  assert.deepEqual(deps._calls, [
    "assert",
    "record",
    "convert",
    "verify",
    "deliver",
    "cleanup",
  ]);
  assert.equal(res.mp4, "/repo/content/demos/2026/my-demo/demo.mp4");
  assert.equal(res.meta, "/repo/content/demos/2026/my-demo/meta.yaml");
});

test("dry-run gate: a broken scenario fails assert and is NEVER recorded", async () => {
  const deps = makeDeps({
    assert: async () => {
      deps._calls.push("assert");
      throw new Error("selector not found");
    },
  });
  await assert.rejects(
    () => runPipeline(baseConfig(async () => {}), deps),
    /selector not found/,
  );
  assert.deepEqual(deps._calls, ["assert"], "record/convert/cleanup must not run");
  assert.ok(!deps._calls.includes("record"));
});

test("same scenario function is reused for dry run and recorded take", async () => {
  const deps = makeDeps();
  const scenario = async () => {};
  await runPipeline(baseConfig(scenario), deps);
  assert.equal(deps._assertArgs.scenario, scenario);
  assert.equal(deps._recordArgs.scenario, scenario);
  assert.equal(deps._assertArgs.scenario, deps._recordArgs.scenario);
});

test("cleanup does NOT run when MP4 fails verification", async () => {
  const deps = makeDeps({
    verifyMp4: async () => {
      deps._calls.push("verify");
      return false; // zero-byte / missing MP4
    },
  });
  await assert.rejects(
    () => runPipeline(baseConfig(async () => {}), deps),
    /no valid MP4/,
  );
  assert.ok(!deps._calls.includes("cleanup"), "WebM must be kept if MP4 invalid");
  assert.ok(!deps._calls.includes("deliver"), "nothing delivered on invalid MP4");
});

test("record stage receiving no path aborts before convert", async () => {
  const deps = makeDeps({
    record: async () => {
      deps._calls.push("record");
      return undefined; // truncated / missing WebM
    },
  });
  await assert.rejects(
    () => runPipeline(baseConfig(async () => {}), deps),
    /no video path/,
  );
  assert.ok(!deps._calls.includes("convert"));
  assert.ok(!deps._calls.includes("cleanup"));
});

test("config validation: scenario must be a function, slug required", async () => {
  await assert.rejects(() => runPipeline({ slug: "x", scenario: null }, makeDeps()), /scenario must be a function/);
  await assert.rejects(() => runPipeline({ scenario: async () => {} }, makeDeps()), /slug is required/);
});

test("targetDir and meta render the Crate layout", () => {
  assert.equal(targetDir("/repo", "2026-11-13", "my-demo"), "/repo/content/demos/2026/my-demo");
  const meta = buildMeta({
    url: "https://demo.otelt.io",
    orientation: "landscape",
    date: "2026-11-13",
    slug: "my-demo",
    durationSec: 42,
  });
  assert.match(meta, /source_url: "https:\/\/demo\.otelt\.io"/);
  assert.match(meta, /orientation: landscape/);
  assert.match(meta, /silent: true/);
  assert.match(meta, /video: demo\.mp4/);
});
