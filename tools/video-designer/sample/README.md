# Sample output (acceptance evidence, ISI-7396)

A real assembly produced by this tool — the `recycle:video` acceptance:
a demo clip assembled into a captioned, brand-titled MP4.

```sh
node assemble.mjs examples/otel-quickstart.mjs \
  --slug otel-quickstart --platform youtube-video \
  --content-root <out> --asset-root <demo-and-assets>
```

- **`video.mp4`** — 1920x1080 H.264, true CFR 30fps (450 frames / 15.0s), AAC,
  `+faststart`. Violet intro sting → trimmed demo clip with a violet title card,
  magenta lower-third, moving cursor box, and burned captions → orange outro
  sting. Music bed ducked under a voiceover, mastered to -14 LUFS.
- **`poster.jpg`** — poster frame.
- **`captions.srt`** — the re-timed sidecar (shifted +1.5s for the intro pre-roll
  so cues align with the assembled cut).
- **`meta.yaml`** — delivery metadata.

Inputs were synthesized (a UI-like `color`+`drawtext` clip standing in for a
demo-recorder capture, a sine music bed, and a tone voiceover) so the sample is
self-contained; in production the clip comes from the demo-recorder skill and the
audio from the acestep / elevenlabs skills.
