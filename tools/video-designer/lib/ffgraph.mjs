// ffmpeg filtergraph + argument builders for the video-designer skill.
//
// The whole assembly is ONE ffmpeg pass (one `-filter_complex`), so the only
// re-encode is the final master — no generation loss from intermediate files.
// Every function here is PURE (strings in, strings out): no spawn, no fs. That
// is what makes the pipeline unit-testable without a real ffmpeg (assemble.test
// .mjs), the same contract demo-recorder's buildConvertArgs holds.
//
// Graph shape:
//   per segment:  [k:v] trim? , scale+pad to WxH , setsar , fps , format  [vK]
//   concat:       [v0][v1]... concat=n=N:v=1:a=0  [vcat]
//   titles:       [vcat] drawtext(title) , drawtext(lowerThird)           [vtxt]
//   captions:     [vtxt] subtitles=FILE  (burn mode only)                 [vout]
//   audio:        music (+ ducked voiceover) -> loudnorm                  [aout]
//
// Text is passed via `textfile=` (never `text=`) so arbitrary title/caption
// copy needs no drawtext escaping — the orchestrator writes each string to a
// temp file and hands us the path.

import { BRAND, LOUDNESS } from "./presets.mjs";

// filtergraph option VALUES (paths, enable exprs) must escape the characters
// that delimit the graph: \ : ' and the , that separates chained filters.
export function escapeGraphValue(s) {
  return String(s).replace(/\\/g, "\\\\").replace(/:/g, "\\:").replace(/'/g, "\\'");
}

// Normalize ONE input to the canvas so concat (which demands identical w/h/
// sar/fps/pixfmt across inputs) accepts it. `trim` cuts dead air per segment.
export function segmentFilter({ inLabel, outLabel, width, height, fps, trim }) {
  const steps = [];
  if (trim && (trim.in != null || trim.out != null)) {
    const start = trim.in != null ? `start=${trim.in}` : "";
    const end = trim.out != null ? `end=${trim.out}` : "";
    steps.push(`trim=${[start, end].filter(Boolean).join(":")}`, "setpts=PTS-STARTPTS");
  }
  steps.push(
    `scale=${width}:${height}:force_original_aspect_ratio=decrease`,
    `pad=${width}:${height}:(ow-iw)/2:(oh-ih)/2:color=black`,
    "setsar=1",
    `fps=${fps}`,
    "format=yuv420p",
  );
  return `[${inLabel}]${steps.join(",")}[${outLabel}]`;
}

// Concatenate the N normalized segment labels into one video stream.
export function concatFilter(labels, outLabel = "vcat") {
  const ins = labels.map((l) => `[${l}]`).join("");
  return `${ins}concat=n=${labels.length}:v=1:a=0[${outLabel}]`;
}

// A brand-palette drawtext overlay time-gated to [start,end]. `kind` picks the
// style: a centered title card (violet box) or a lower-third (magenta box,
// bottom-left). textfilePath is a file the orchestrator wrote the copy into.
export function drawtextFilter({ kind, fontFile, textfilePath, start, end, width, height }) {
  const box = kind === "title" ? BRAND.violet : BRAND.magenta;
  const fontsize = kind === "title" ? Math.round(height / 15) : Math.round(height / 25);
  const pos =
    kind === "title"
      ? "x=(w-text_w)/2:y=(h-text_h)/2"
      : `x=${Math.round(width / 24)}:y=h-text_h-${Math.round(height / 6)}`;
  const opts = [
    `fontfile=${escapeGraphValue(fontFile)}`,
    `textfile=${escapeGraphValue(textfilePath)}`,
    "fontcolor=white",
    `fontsize=${fontsize}`,
    "box=1",
    `boxcolor=${box}@0.85`,
    `boxborderw=${Math.round(fontsize / 2)}`,
    pos,
  ];
  if (start != null && end != null) {
    opts.push(`enable='${escapeGraphValue(`between(t,${start},${end})`)}'`);
  }
  return `drawtext=${opts.join(":")}`;
}

// Chain the title/lower-third drawtext filters onto [inLabel]; burn captions in
// when mode==="burn". Returns "" (passthrough) when nothing overlays, so the
// caller can alias labels.
export function overlayChain({ inLabel, outLabel, draws = [], captionsFile, captionsMode, width, height }) {
  const chain = [...draws];
  if (captionsFile && captionsMode === "burn") {
    // force_style keeps burned subs legible on any footage; MarginV lifts them
    // clear of a lower-third. Path is escaped for the graph.
    const style = `FontSize=${Math.round(height / 45)},Outline=2,Shadow=0,MarginV=${Math.round(height / 12)}`;
    chain.push(`subtitles=${escapeGraphValue(captionsFile)}:force_style='${escapeGraphValue(style)}'`);
  }
  void width;
  if (chain.length === 0) return "";
  return `[${inLabel}]${chain.join(",")}[${outLabel}]`;
}

// Build the audio stream from a music bed and/or a voiceover. When both are
// present and duck=true, the music is side-chain compressed by the VO so speech
// sits on top, then the two are mixed. Everything ends at loudnorm (R128).
// `musicLabel`/`voLabel` are input stream specifiers like "2:a" (null = absent).
// Returns { filter, outLabel } or null when there is no audio at all. The chain
// ends in `apad` so the audio is padded with silence to the (finite) video
// master; the caller passes `-shortest` so output length == video length — a
// bed shorter than the video no longer cuts the picture, a longer bed is
// trimmed. loudnorm runs BEFORE apad so trailing silence is never normalized.
export function audioFilter({ musicLabel, voLabel, duck = true }) {
  const L = `loudnorm=I=${LOUDNESS.I}:TP=${LOUDNESS.TP}:LRA=${LOUDNESS.LRA},apad`;
  if (musicLabel && voLabel) {
    if (duck) {
      return {
        outLabel: "aout",
        filter:
          `[${voLabel}]asplit=2[vok][vom];` +
          `[${musicLabel}][vok]sidechaincompress=threshold=0.03:ratio=8:attack=20:release=300[duck];` +
          `[duck][vom]amix=inputs=2:duration=longest:dropout_transition=0[amix];` +
          `[amix]${L}[aout]`,
      };
    }
    return {
      outLabel: "aout",
      filter: `[${musicLabel}][${voLabel}]amix=inputs=2:duration=longest:dropout_transition=0[amix];[amix]${L}[aout]`,
    };
  }
  if (voLabel) return { outLabel: "aout", filter: `[${voLabel}]${L}[aout]` };
  if (musicLabel) return { outLabel: "aout", filter: `[${musicLabel}]${L}[aout]` };
  return null;
}

// Final H.264 master encode args (plan §7): CFR via -fps_mode (NEVER -vsync,
// removed in ffmpeg 9 — the ISI-7388 R4 footgun), faststart for web streaming,
// yuv420p for universal playback. AAC audio when an audio map is present.
export function encodeArgs({ fps, hasAudio }) {
  const a = [
    "-c:v", "libx264",
    "-crf", "20",
    "-preset", "medium",
    "-pix_fmt", "yuv420p",
    "-fps_mode", "cfr",
    "-r", String(fps),
    "-movflags", "+faststart",
  ];
  if (hasAudio) a.push("-c:a", "aac", "-b:a", "192k", "-ar", "48000");
  else a.push("-an");
  return a;
}

// Poster thumbnail: one JPEG frame at `atSec` from the finished MP4.
export function thumbnailArgs(inputMp4, outJpg, atSec = 1) {
  // -update 1: write a single still (not an image2 sequence) to a fixed name.
  return ["-y", "-ss", String(atSec), "-i", inputMp4, "-frames:v", "1", "-update", "1", "-q:v", "3", outJpg];
}
