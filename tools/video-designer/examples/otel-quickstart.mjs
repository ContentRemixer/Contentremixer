// Example edit manifest for the video-designer skill.
//
// Run (inside the media image, ffmpeg on PATH):
//   node assemble.mjs examples/otel-quickstart.mjs \
//     --content-root /artifacts --asset-root /artifacts
//
// Produces content/videos/<year>/otel-quickstart/{video.mp4,poster.jpg,captions.vtt,meta.yaml}.
// Paths in `file:` resolve against --asset-root (the content checkout).

export default {
  title: "OpenTelemetry in 5 Minutes",
  slug: "otel-quickstart",
  platform: "youtube-video", // 1920x1080 @30 (honors persona contentFormatTargets)

  segments: [
    // Auto-generated brand sting card (no file) — violet, 1.5s, centered title.
    { kind: "sting-intro", text: "ContentRemixer", duration: 1.5 },
    // The demo-recorder clip; trim the first 0.5s of dead air and the tail.
    { kind: "clip", file: "content/demos/2026/otel-quickstart/demo.mp4", in: 0.5, out: 22.0 },
    // Outro sting — orange, 2s.
    { kind: "sting-outro", text: "Subscribe for more", duration: 2.0 },
  ],

  // Main title card over the opening; magenta lower-third credits the speaker.
  titleCard: { text: "OpenTelemetry in 5 Minutes", start: 1.5, end: 4.5 },
  lowerThird: { text: "Henrik - DevRel", start: 5.0, end: 10.0 },

  // a11y: burn captions into the frame (sidecar also delivered when mode:sidecar).
  captions: { file: "content/demos/2026/otel-quickstart/captions.srt", mode: "burn" },

  // Music bed ducked under the voiceover, mastered to -14 LUFS. Both optional;
  // generate them with the acestep (music) / elevenlabs (VO) skills, or drop a
  // human VO in. Omit the whole `audio` block for a silent cut.
  audio: {
    music: "assets/bed.mp3",
    voiceover: "assets/vo.mp3",
    duck: true,
  },
};
