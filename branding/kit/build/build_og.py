#!/usr/bin/env python3
import os, sys
sys.path.insert(0, "/tmp")
from text2path import text_path, emit_group
SVG = "/mnt/nas/project/contentremixer/branding/kit/svg"
SB = "/tmp/Poppins-SemiBold.ttf"; MED = "/tmp/Poppins-Medium.ttf"
VIOLET, MAGENTA, ORANGE = "#7C3AED", "#EC4899", "#F97316"
GLYPH_LAV, GLYPH_ORG = "#A78BFA", "#FB923C"
BG1, BG2, WHITE, TRACK_BASE = "#1A1235", "#0D0A1A", "#F5F3FF", "#312E5A"

def wm(text, size, cx, base, fill, tr=0.0, ttf=SB, anchor="middle"):
    parts, w = text_path(ttf, text, size, cx, base, anchor, tr)
    return emit_group(parts, fill), w

# icon block on the left: reuse base mark geometry inside a 420-box centered at x=300,y=315
# the mark native coords are 0..512; place a rounded tile then the mark scaled
TILE = 300  # tile size
tx, ty = 110, 165  # tile top-left
s = TILE/512
# mark glyph paths scaled into tile
def scaled_mark():
    return f'''  <g transform="translate({tx},{ty}) scale({s})">
    <g stroke="{GLYPH_LAV}" stroke-width="18" stroke-linecap="round">
      <line x1="92" y1="156" x2="208" y2="156"/>
      <line x1="92" y1="204" x2="172" y2="204"/>
      <line x1="92" y1="252" x2="200" y2="252"/>
    </g>
    <circle cx="398" cy="204" r="60" fill="none" stroke="{GLYPH_ORG}" stroke-width="18"/>
    <path d="M380 174 L426 204 L380 234 Z" fill="{GLYPH_ORG}"/>
    <rect x="80" y="332" width="352" height="16" rx="8" fill="{TRACK_BASE}"/>
    <rect x="80" y="332" width="352" height="16" rx="8" fill="url(#fade)" filter="url(#glow)"/>
    <rect x="238" y="304" width="44" height="72" rx="13" fill="{WHITE}" filter="url(#glow)"/>
  </g>'''

# wordmark + tagline, left-aligned starting at x=470
wmk, wmw = wm("ContentRemixer", 80, 470, 322, WHITE, tr=-0.005, anchor="start")
tag, tgw = wm("Create once. Publish everywhere.", 36, 472, 380, GLYPH_LAV, tr=0.01, ttf=MED, anchor="start")
# a short accent fader bar under the tagline
body = f'''  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="{BG1}"/><stop offset="1" stop-color="{BG2}"/></linearGradient>
    <linearGradient id="fade" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="{VIOLET}"/><stop offset="0.5" stop-color="{MAGENTA}"/><stop offset="1" stop-color="{ORANGE}"/></linearGradient>
    <filter id="glow" x="-20%" y="-20%" width="140%" height="140%"><feGaussianBlur stdDeviation="6" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>
  </defs>
  <rect width="1200" height="630" fill="url(#bg)"/>
  <rect x="110" y="165" width="300" height="300" rx="58" fill="#00000022"/>
{scaled_mark()}
  {wmk}
  <rect x="472" y="408" width="300" height="10" rx="5" fill="url(#fade)" filter="url(#glow)"/>
  {tag}'''
with open(os.path.join(SVG, "og_image.svg"), "w") as f:
    f.write(f'<svg viewBox="0 0 1200 630" width="1200" height="630" xmlns="http://www.w3.org/2000/svg">\n{body}\n</svg>\n')
sys.stderr.write(f"og wordmark w={wmw:.1f} tagline w={tgw:.1f}\n")
print("wrote og_image.svg")
