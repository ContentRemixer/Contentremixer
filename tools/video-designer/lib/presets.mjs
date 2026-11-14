// Platform presets + brand palette for the video-designer skill (ISI-7396,
// parent ISI-7389 §7).
//
// A finished video targets a platform, not a resolution — the DevRel persona's
// `spec.contentFormatTargets` names content types (youtube-video, youtube-short,
// linkedin-post; ISI-7370). This module maps those names to the concrete encode
// shape (canvas, fps, loudness, max duration) so assemble.mjs never hardcodes a
// number and `recycle:video` honors the persona's targets.

// Brand palette (plan §7). ffmpeg drawtext/drawbox want 0xRRGGBB.
export const BRAND = {
  violet: "0x7C3AED", // #7C3AED — title cards
  magenta: "0xEC4899", // #EC4899 — lower-thirds
  orange: "0xF97316", // #F97316 — outro / accents
};

// EBU R128 loudness normalization target (loudnorm). -14 LUFS is the de-facto
// ceiling YouTube/LinkedIn normalize TO, so mastering there avoids a second
// platform-side gain change. TP/LRA are the R128 defaults.
export const LOUDNESS = { I: -14, TP: -1.5, LRA: 11 };

// Platform presets. `name` is the canonical contentFormatTarget content type;
// aliases (the demo-recorder orientation vocabulary) resolve to the same shape.
//   maxSec: hard cap the assembled duration advertised by the target (null = no
//           cap). youtube-short is the ≤3min Shorts ceiling (plan §7).
export const PRESETS = {
  "youtube-video": { width: 1920, height: 1080, fps: 30, maxSec: null },
  "youtube-short": { width: 1080, height: 1920, fps: 30, maxSec: 180 },
  "linkedin-post": { width: 1080, height: 1080, fps: 30, maxSec: 600 },
};

const ALIASES = {
  landscape: "youtube-video",
  shorts: "youtube-short",
  square: "linkedin-post",
};

export function resolvePreset(platform = "youtube-video") {
  const key = ALIASES[platform] ?? platform;
  const preset = PRESETS[key];
  if (!preset) {
    throw new Error(
      `unknown platform '${platform}' (expected: ${Object.keys(PRESETS).join(", ")}, or alias ${Object.keys(ALIASES).join("/")})`,
    );
  }
  return { name: key, ...preset };
}

// drawtext needs a real TrueType file. The media image (Playwright noble base)
// ships DejaVu; FONT_FILE overrides for brand fonts mounted at runtime. This is
// a pure resolver (candidate list); the orchestrator verifies the path exists.
export const FONT_CANDIDATES = [
  "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
  "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
  "/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf",
  "/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
];

export function fontCandidates(env = process.env) {
  return env.FONT_FILE ? [env.FONT_FILE, ...FONT_CANDIDATES] : FONT_CANDIDATES;
}
