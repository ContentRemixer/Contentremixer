# video-designer runtime

ffmpeg pipeline for the [`video-designer` skill](../../skills/video-designer/SKILL.md)
(ISI-7396, parent ISI-7389 §7). Assembles a finished, captioned, brand-titled
video from demo-recorder clips in **one ffmpeg pass** (the only re-encode is the
final master — no generation loss).

## Run

```sh
# inside the media-actor image (ffmpeg on PATH):
node assemble.mjs examples/otel-quickstart.mjs \
  --slug otel-quickstart --platform youtube-video \
  --content-root /artifacts --asset-root /artifacts
```

Produces `content/videos/<year>/<slug>/{video.mp4, poster.jpg, captions.<ext>, meta.yaml}`.

- `--content-root` — where deliverables land (the content checkout).
- `--asset-root` — base for `file:` paths in the manifest (usually the same).
- `--platform` / `--slug` — override the manifest fields.

Env: `FFMPEG_BIN` (default `ffmpeg`), `FONT_FILE` (drawtext font; falls back to
DejaVu/Liberation baked into the image).

## Layout

| file | role |
|------|------|
| `assemble.mjs` | orchestrator + CLI + the pure `buildGraph` composer |
| `lib/presets.mjs` | platform presets, brand palette, loudness, font resolve |
| `lib/ffgraph.mjs` | pure filtergraph / ffmpeg-arg builders |
| `lib/captions.mjs` | pre-roll offset + SRT/VTT cue re-timing |
| `examples/` | example edit manifests |
| `assemble.test.mjs` | `node --test` — contract tests, no real ffmpeg |

## Test

```sh
node --test
```

The pipeline builders are pure (strings in, strings out), so the tests assert the
filtergraph shape and the encode footguns (CFR not `-vsync`, R128 loudnorm,
ducking wiring) without spawning ffmpeg. The committed `sample/` directory is a
real assembly produced by this tool (see `sample/README.md`).

## No runtime deps

Node 20+ and `ffmpeg` on PATH — both baked into the media-actor image. Nothing to
`npm install`; the Dockerfile copies the source as-is.
