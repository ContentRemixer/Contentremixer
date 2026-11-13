# ContentRemixer — Brand Kit

> **Start here:** [`BRAND-KIT.md`](BRAND-KIT.md) is the single entry point (logo · palette ·
> type · [voice & tone](VOICE-AND-TONE.md) · [README badges & banner](README-BADGES.md)).
> This file documents the asset/build details.

Production brand kit, iterated from the locked crossfader direction (`branding/logo/`,
locked by Henrik 2026-11-04). **Concept:** the crossfader *is* the product — blog lines
(left channel) and a play button (right channel), knob mid-mix. *Create once. Publish everywhere.*

## Production font — LOCKED

**Poppins** (SIL OFL 1.1) — geometric sans, Poppins/Montserrat class as specified.
- Wordmark: **Poppins SemiBold**, tracking −0.5%
- Tagline: **Poppins Medium**, tracking +1%

All wordmarks are **embedded as vector outlines** in the SVGs — there is no runtime font
dependency, no `<text>`, and no `textPath` anywhere. SVGs render identically on any host,
including those with no fonts installed (this build host had none).

## Files

### `svg/` — source vectors
| File | Use |
|------|-----|
| `logo_master.svg` | Primary lockup (mark + wordmark) |
| `logo_tagline.svg` | Lockup + tagline |
| `icon.svg` | Icon-only (avatar / app icon) |
| `logo_mono_white.svg` | Mono — white on transparent (dark backgrounds) |
| `logo_mono_dark.svg` | Reverse — ink (#1A1235) on transparent (light backgrounds) |
| `favicon_full.svg` | Favicon source (full glyphs), for ≥32px |
| `favicon_min.svg` | 16px favicon — fader + knob only, glyphs dropped |
| `og_image.svg` | Social / OG card, 1200×630 |

### `png/` — rendered exports (sharp / libvips)
`logo_master.png`, `logo_tagline.png`, `icon.png`, `logo_mono_white.png`,
`logo_mono_dark.png` (512×512) and `og_image.png` (1200×630).

### `favicon/` — favicon ladder
`favicon_512.png`, `favicon_192.png`, `favicon_64.png`, `favicon_32.png` (full glyphs),
`favicon_16.png` (fader + knob only).

## Brand tokens

See `tokens.json`. Quick reference:

| Token | Hex |
|-------|-----|
| violet | `#7C3AED` |
| magenta | `#EC4899` |
| orange | `#F97316` |
| glyph-lavender | `#A78BFA` |
| glyph-orange | `#FB923C` |
| bg-1 | `#1A1235` |
| bg-2 | `#0D0A1A` |
| white | `#F5F3FF` |
| track-base | `#312E5A` |

- **Clear space:** ≥ 1 knob-width (≈8.6% of mark width) on all sides.
- **Min size:** full lockup 120px · icon 32px · favicon_min 16px. Below 32px, use `favicon_min`.
- **Corner radius:** one scale — 100 (master) / 110 (icon). Don't mix radii.

## Rebuilding

Rendered with **sharp** (libvips). `cairosvg` is not used (broken on this host).

```bash
cd /tmp
npm install sharp
# wordmark outlines were generated with Poppins via fontTools (see build scripts)
node render.js
```

## Acceptance

- ✅ Rendered via sharp / libvips (not cairosvg)
- ✅ No emoji glyphs, no `textPath`, no `<text>` in any SVG
- ✅ Iterated from the locked `branding/logo/` base (not prism/channels; V3 finger direction rejected)
- ✅ Favicon ladder 512/192/64/32/16, glyphs dropped at 16px
- ✅ Mono + reverse variants, OG 1200×630, brand tokens
