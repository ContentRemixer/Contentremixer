# ContentRemixer — README badge & banner style

How the README (and the website) present the top-of-page banner and the status badge row,
so every repo in the org reads as one brand.

![badge strip](png/badge_strip.png)

## Banner

Put the brand banner at the very top of the README, full-width, linking to the site/docs.

```md
<p align="center">
  <a href="https://contentremixer.dev">
    <img src="branding/kit/png/readme_banner.png" alt="ContentRemixer — Create once. Publish everywhere." width="100%">
  </a>
</p>
```

- Asset: `branding/kit/png/readme_banner.png` (1280×320) · retina `readme_banner@2x.png` (2560×640).
- Source vector: `branding/kit/svg/readme_banner.svg` (wordmark embedded as outlines — renders with no fonts installed).
- On the website, use `og_image.png` (1200×630) for social cards; the banner for the hero masthead.

## Badge row

One centered row, directly under the banner. **flat-square only** — it matches the geometric,
flat brand marks. Never mix `flat-square` with the default rounded shields.

```md
<p align="center">
  <img src="https://img.shields.io/badge/build-passing-7C3AED?style=flat-square" alt="build">
  <img src="https://img.shields.io/badge/license-MIT-EC4899?style=flat-square" alt="license">
  <img src="https://img.shields.io/badge/docs-read_the_guide-7C3AED?style=flat-square" alt="docs">
  <img src="https://img.shields.io/badge/status-active-F97316?style=flat-square" alt="status">
</p>
```

Live CI/version badges (GitHub Actions, release, etc.) use the same palette via `&color=`:

```md
![CI](https://img.shields.io/github/actions/workflow/status/ContentRemixer/Contentremixer/ci.yml?style=flat-square&color=7C3AED&label=ci)
```

### Badge color rules
| Use | Token | Hex (shields) |
|-----|-------|------|
| Primary / brand / default value side | violet | `7C3AED` |
| Accent (license, highlights) | magenta | `EC4899` |
| Call-to-action / "new" / active status | orange | `F97316` *(use sparingly — one per row)* |
| Label (left) side | ink | shields `labelColor=1A1235` |

- **Style:** `flat-square`, every badge, no exceptions.
- **Label side** is always ink (`labelColor=1A1235`); value side carries the token color.
- **Order:** build/CI → version/license → docs → status. Keep the row to ≤ 5 badges.
- **Logos:** add `&logo=github&logoColor=F5F3FF` where a glyph helps; keep logoColor near-white.
- **Orange is the accent, not the base** — reserve it for one CTA/active badge so it stays loud.

### Full example (top of README)
```md
<p align="center">
  <a href="https://contentremixer.dev"><img src="branding/kit/png/readme_banner.png" alt="ContentRemixer — Create once. Publish everywhere." width="100%"></a>
</p>
<p align="center">
  <img src="https://img.shields.io/badge/build-passing-7C3AED?style=flat-square&labelColor=1A1235" alt="build">
  <img src="https://img.shields.io/badge/license-MIT-EC4899?style=flat-square&labelColor=1A1235" alt="license">
  <img src="https://img.shields.io/badge/docs-read_the_guide-7C3AED?style=flat-square&labelColor=1A1235" alt="docs">
  <img src="https://img.shields.io/badge/status-active-F97316?style=flat-square&labelColor=1A1235" alt="status">
</p>
```
