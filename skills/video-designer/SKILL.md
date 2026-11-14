---
name: video-designer
description: Assemble a finished, captioned, brand-titled video from demo-recorder clips - ffmpeg trims dead air, concatenates with intro/outro stings, burns titles and captions, mixes a ducked music bed and voiceover, and masters to a platform preset. Use when an agent must turn raw demo footage into a publishable video.
---
# Video designer skill

Turn raw demo-recorder clips into a publishable video: trim the dead air, stitch
the clips between brand stings, lay on titles and lower-thirds, burn captions for
accessibility, mix a music bed under the voiceover, and master to the platform the
persona targets. The runtime (`tools/video-designer/`) does the ffmpeg
orchestration in ONE pass - this skill is the authoring contract and the quality
bar. ffmpeg-first by design (ADR §7 / §10): no Remotion until a concrete
motion-graphics need appears.

## Input: the edit manifest

Author the cut as a plain data object (`.json` or a `.mjs` default export) - the
edit decision list, not a script. See `tools/video-designer/examples/`.

```
{ title, slug, platform,                       // platform -> encode preset
  segments: [                                  // ordered; stings + clips
    { kind: "sting-intro", text, duration },   // generated brand card (violet)
    { kind: "clip", file, in, out },           // a demo-recorder MP4, trimmed
    { kind: "sting-outro", text, duration } ], // generated brand card (orange)
  titleCard:  { text, start, end },            // violet title overlay
  lowerThird: { text, start, end },            // magenta lower-third
  captions:   { file, mode: "burn"|"sidecar" },// SRT/VTT, timed to the clip
  audio:      { music, voiceover, duck } }     // both optional; omit = silent
```

`file:` paths resolve against `--asset-root` (the content checkout). A sting
without a `file` is a generated solid brand card; give it a `file` to use real
sting footage instead.

## Pipeline (one ffmpeg pass - no generation loss)
1. **Normalize** - each segment is scaled+padded to the preset canvas, SAR 1,
   CFR fps, yuv420p, so `concat` accepts them. `in`/`out` trim dead air per clip.
2. **Concat** - segments join in order; generated stings are `color` sources.
3. **Titles** - `drawtext` title card (violet #7C3AED) and lower-third (magenta
   #EC4899), time-gated. Text is passed via `textfile=` so copy needs no
   escaping. Outro stings accent orange (#F97316).
4. **Captions (a11y)** - `mode:"burn"` burns the cues in via `subtitles`; a
   **re-timed sidecar always ships too** (toggle/translate/index). Cues are
   authored against the clip and auto-shifted by the intro pre-roll so they line
   up with the assembled cut.
5. **Audio** - music bed side-chain **ducked** under the voiceover, mixed, then
   **loudnorm to -14 LUFS** (EBU R128). Omit `audio` for a silent cut (`-an`).
6. **Master** - H.264 `-crf 20`, `-fps_mode cfr` (NEVER `-vsync`, removed in
   ffmpeg 9 - the ISI-7388 R4 footgun), `-pix_fmt yuv420p`, `+faststart`.
   `-shortest` clamps output to the video; the bed is padded to match.

## Platform presets (honor `contentFormatTargets`)
The persona's `spec.contentFormatTargets` (ISI-7370) names the content type; the
preset sets the canvas, fps, and duration ceiling. Pass it as `platform`:

| platform / alias            | canvas      | notes               |
|-----------------------------|-------------|---------------------|
| `youtube-video` (landscape) | 1920x1080   | the default         |
| `youtube-short` (shorts)    | 1080x1920   | <=3 min (Shorts cap)|
| `linkedin-post` (square)    | 1080x1080   | feed-native square  |

## Deliverables
Into `content/videos/YYYY/<slug>/`: `video.mp4` + `poster.jpg` + the caption
sidecar + `meta.yaml`. **Flush before suspend (ADR-4):** the clip lives in an
emptyDir that dies with the Job pod, so the media-actor entrypoint commits and
pushes the deliverables to the PR branch BEFORE the Job completes - Job success
must imply the video is durable.

## Review gate
Loop in brand/DevRel review (CMO) on **titles and voice** before merge - the
palette is enforced here but the wording and the synthetic voice are editorial.
Synthetic voiceover (elevenlabs) is the default; human VO is supported (drop the
file in `audio.voiceover`). Generate music with the acestep skill, VO with
elevenlabs - this skill consumes the audio files, it does not synthesize them.

## Runtime
Runs in the `media` template (same image as demo-recorder: Playwright base +
ffmpeg + git). The mediajob launcher invokes
`node video-designer/assemble.mjs <edit.mjs> --content-root /artifacts
--asset-root /artifacts`. No extra install at run - ffmpeg is baked.

## Checklist
- [ ] Manifest segments ordered: intro sting -> clip(s) -> outro sting
- [ ] Dead air trimmed (`in`/`out`) on every clip
- [ ] Title + lower-third use the brand palette (violet / magenta / orange)
- [ ] Captions present (burn and/or sidecar); cues aligned to the assembled cut
- [ ] Music ducked under VO; mastered to -14 LUFS (no clipping)
- [ ] Encoded `-fps_mode cfr ... +faststart` (never `-vsync`)
- [ ] Platform preset matches the persona's `contentFormatTargets`
- [ ] On-screen scrub: no secrets, real emails, or internal hostnames in frame
- [ ] Delivered to `content/videos/YYYY/<slug>/` with poster + sidecar + meta.yaml
- [ ] Brand/DevRel (CMO) signed off on titles + voice
