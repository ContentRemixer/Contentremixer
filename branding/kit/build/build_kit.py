#!/usr/bin/env python3
"""Build the full ContentRemixer brand kit from the locked crossfader base.
Wordmarks are embedded as Poppins vector outlines (no textPath, no font dependency)."""
import os, sys
sys.path.insert(0, "/tmp")
from text2path import text_path, emit_group

KIT = "/mnt/nas/project/contentremixer/branding/kit"
SVG = os.path.join(KIT, "svg")
SB = "/tmp/Poppins-SemiBold.ttf"
MED = "/tmp/Poppins-Medium.ttf"

# ---- palette ----
VIOLET, MAGENTA, ORANGE = "#7C3AED", "#EC4899", "#F97316"
GLYPH_LAV, GLYPH_ORG = "#A78BFA", "#FB923C"
BG1, BG2, WHITE = "#1A1235", "#0D0A1A", "#F5F3FF"
TRACK_BASE = "#312E5A"

def defs(glow_std=6, bgvertical=False):
    return f'''  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="{BG1}"/>
      <stop offset="1" stop-color="{BG2}"/>
    </linearGradient>
    <linearGradient id="fade" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0" stop-color="{VIOLET}"/>
      <stop offset="0.5" stop-color="{MAGENTA}"/>
      <stop offset="1" stop-color="{ORANGE}"/>
    </linearGradient>
    <filter id="glow" x="-20%" y="-20%" width="140%" height="140%"><feGaussianBlur stdDeviation="{glow_std}" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>
  </defs>'''

def mark(scale=1.0, dx=0, dy=0, lines_color=GLYPH_LAV, play_color=GLYPH_ORG,
         knob_color=WHITE, track_glow=True, mono=None):
    """The crossfader mark (glyphs + track + knob), 512-canvas coords from base v1a."""
    lc = mono or lines_color
    pc = mono or play_color
    kc = mono or knob_color
    g = ' filter="url(#glow)"' if track_glow else ''
    track_fill = mono if mono else "url(#fade)"
    return f'''  <g transform="translate({dx},{dy}) scale({scale})">
    <g stroke="{lc}" stroke-width="18" stroke-linecap="round">
      <line x1="92" y1="156" x2="208" y2="156"/>
      <line x1="92" y1="204" x2="172" y2="204"/>
      <line x1="92" y1="252" x2="200" y2="252"/>
    </g>
    <circle cx="398" cy="204" r="60" fill="none" stroke="{pc}" stroke-width="18"/>
    <path d="M380 174 L426 204 L380 234 Z" fill="{pc}"/>
    <rect x="80" y="332" width="352" height="16" rx="8" fill="{TRACK_BASE if not mono else 'none'}"/>
    <rect x="80" y="332" width="352" height="16" rx="8" fill="{track_fill}"{g}/>
    <rect x="238" y="304" width="44" height="72" rx="13" fill="{kc}"{g}/>
  </g>'''

def wordmark(text, size, cx, baseline, fill, tracking=0.0, ttf=SB):
    parts, w = text_path(ttf, text, size, cx, baseline, "middle", tracking)
    return emit_group(parts, fill), w

def write(name, body, w=512, h=512):
    path = os.path.join(SVG, name)
    with open(path, "w") as f:
        f.write(f'<svg viewBox="0 0 {w} {h}" width="{w}" height="{h}" xmlns="http://www.w3.org/2000/svg">\n{body}\n</svg>\n')
    print(f"wrote {name}")

# ---------------------------------------------------------------------------
# 1. MASTER LOCKUP (v1a refined) -- wordmark locked to Poppins SemiBold
# Fit wordmark within the 80px side margins (max width 352). Tune size.
wm_size = 46
wm, wmw = wordmark("ContentRemixer", wm_size, 256, 442, WHITE, tracking=-0.005)
# scale down if exceeds safe width 356
if wmw > 356:
    wm_size = wm_size * 356 / wmw
    wm, wmw = wordmark("ContentRemixer", wm_size, 256, 442, WHITE, tracking=-0.005)
sys.stderr.write(f"master wordmark size={wm_size:.2f} width={wmw:.2f}\n")
master = f'''{defs()}
  <rect width="512" height="512" rx="100" fill="url(#bg)"/>
{mark()}
  {wm}'''
write("logo_master.svg", master)

# ---------------------------------------------------------------------------
# 1b. TAGLINE LOCKUP (v1c refined)
wm2_size = 42
wm2, wm2w = wordmark("ContentRemixer", wm2_size, 256, 420, WHITE, tracking=-0.005)
if wm2w > 356:
    wm2_size = wm2_size * 356 / wm2w
    wm2, wm2w = wordmark("ContentRemixer", wm2_size, 256, 420, WHITE, tracking=-0.005)
tg, tgw = wordmark("Create once. Publish everywhere.", 19, 256, 456, GLYPH_LAV, tracking=0.01, ttf=MED)
tagline = f'''{defs()}
  <rect width="512" height="512" rx="100" fill="url(#bg)"/>
{mark(dy=-14)}
  {wm2}
  {tg}'''
write("logo_tagline.svg", tagline)

# ---------------------------------------------------------------------------
# 2. ICON (v1b refined) -- icon-only, no wordmark. Keep base geometry, centered.
icon = f'''{defs(glow_std=7)}
  <rect width="512" height="512" rx="110" fill="url(#bg)"/>
  <g stroke="{GLYPH_LAV}" stroke-width="22" stroke-linecap="round">
    <line x1="76" y1="150" x2="216" y2="150"/>
    <line x1="76" y1="212" x2="168" y2="212"/>
    <line x1="76" y1="274" x2="204" y2="274"/>
  </g>
  <circle cx="400" cy="212" r="78" fill="none" stroke="{GLYPH_ORG}" stroke-width="22"/>
  <path d="M376 172 L436 212 L376 252 Z" fill="{GLYPH_ORG}"/>
  <rect x="64" y="368" width="384" height="20" rx="10" fill="{TRACK_BASE}"/>
  <rect x="64" y="368" width="384" height="20" rx="10" fill="url(#fade)" filter="url(#glow)"/>
  <rect x="230" y="332" width="52" height="92" rx="15" fill="{WHITE}" filter="url(#glow)"/>'''
write("icon.svg", icon)

# ---------------------------------------------------------------------------
# 3. MONO (white on transparent) -- full lockup, single color, no glow/gradient
wm_m, _ = wordmark("ContentRemixer", wm_size, 256, 442, WHITE, tracking=-0.005)
mono = f'''  <g>
    <g stroke="{WHITE}" stroke-width="18" stroke-linecap="round">
      <line x1="92" y1="156" x2="208" y2="156"/>
      <line x1="92" y1="204" x2="172" y2="204"/>
      <line x1="92" y1="252" x2="200" y2="252"/>
    </g>
    <circle cx="398" cy="204" r="60" fill="none" stroke="{WHITE}" stroke-width="18"/>
    <path d="M380 174 L426 204 L380 234 Z" fill="{WHITE}"/>
    <rect x="80" y="332" width="352" height="16" rx="8" fill="{WHITE}" opacity="0.35"/>
    <rect x="238" y="304" width="44" height="72" rx="13" fill="{WHITE}"/>
  </g>
  {wm_m}'''
write("logo_mono_white.svg", mono)

# 3b. REVERSE (dark ink on transparent, for light backgrounds)
INK = "#1A1235"
wm_r, _ = wordmark("ContentRemixer", wm_size, 256, 442, INK, tracking=-0.005)
rev = f'''  <g>
    <g stroke="{INK}" stroke-width="18" stroke-linecap="round">
      <line x1="92" y1="156" x2="208" y2="156"/>
      <line x1="92" y1="204" x2="172" y2="204"/>
      <line x1="92" y1="252" x2="200" y2="252"/>
    </g>
    <circle cx="398" cy="204" r="60" fill="none" stroke="{INK}" stroke-width="18"/>
    <path d="M380 174 L426 204 L380 234 Z" fill="{INK}"/>
    <rect x="80" y="332" width="352" height="16" rx="8" fill="{INK}" opacity="0.35"/>
    <rect x="238" y="304" width="44" height="72" rx="13" fill="{INK}"/>
  </g>
  {wm_r}'''
write("logo_mono_dark.svg", rev)

# ---------------------------------------------------------------------------
# 4. FAVICON LADDER
# full-glyph favicon source (reuse icon geometry)
def favicon_full():
    return icon
# 16px: drop glyphs, keep fader + knob only, centered vertically
def favicon_min():
    return f'''{defs(glow_std=2)}
  <rect width="512" height="512" rx="110" fill="url(#bg)"/>
  <rect x="64" y="236" width="384" height="40" rx="20" fill="{TRACK_BASE}"/>
  <rect x="64" y="236" width="384" height="40" rx="20" fill="url(#fade)"/>
  <rect x="214" y="196" width="84" height="120" rx="26" fill="{WHITE}"/>'''
write("favicon_full.svg", favicon_full())
write("favicon_min.svg", favicon_min())

print("ALL SVGs BUILT")
