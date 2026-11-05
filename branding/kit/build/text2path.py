#!/usr/bin/env python3
"""Convert text to SVG path data using Poppins outlines (font-independent wordmarks)."""
import sys
from fontTools.ttLib import TTFont
from fontTools.pens.svgPathPen import SVGPathPen

def text_path(ttf_path, text, font_size, x, y, anchor="middle", tracking=0.0):
    """Return (path_d, total_width). y is the baseline. tracking in em units (e.g. 0.01)."""
    font = TTFont(ttf_path)
    upm = font["head"].unitsPerEm
    cmap = font.getBestCmap()
    glyphset = font.getGlyphSet()
    hmtx = font["hmtx"]
    scale = font_size / upm
    track_units = tracking * upm

    # measure
    total = 0.0
    for ch in text:
        gname = cmap.get(ord(ch))
        if gname is None:
            total += upm * 0.3  # fallback space
            continue
        adv = hmtx[gname][0]
        total += adv + track_units
    total -= track_units  # no trailing track
    total_w = total * scale

    if anchor == "middle":
        startx = x - total_w / 2
    elif anchor == "end":
        startx = x - total_w
    else:
        startx = x

    # build combined path: place each glyph with a transform baked into coords
    parts = []
    penx = 0.0
    for ch in text:
        gname = cmap.get(ord(ch))
        if gname is None:
            penx += upm * 0.3 + track_units
            continue
        pen = SVGPathPen(glyphset)
        glyphset[gname].draw(pen)
        d = pen.getCommands()
        if d:
            # transform: SVG path lives in font units; we wrap in a <g transform>
            tx = startx + penx * scale
            parts.append((d, tx, y, scale))
        adv = hmtx[gname][0]
        penx += adv + track_units
    return parts, total_w

def emit_group(parts, fill, extra=""):
    """Emit a <g> wrapping each glyph path with its own transform (flip Y)."""
    out = []
    for d, tx, ty, scale in parts:
        out.append(
            f'<path transform="translate({tx:.2f},{ty:.2f}) scale({scale:.5f},{-scale:.5f})" '
            f'd="{d}" fill="{fill}"{extra}/>'
        )
    return "\n  ".join(out)

if __name__ == "__main__":
    import json
    # args: ttf text size x y anchor tracking fill
    ttf, text, size, x, y, anchor, tracking, fill = (
        sys.argv[1], sys.argv[2], float(sys.argv[3]), float(sys.argv[4]),
        float(sys.argv[5]), sys.argv[6], float(sys.argv[7]), sys.argv[8])
    parts, w = text_path(ttf, text, size, x, y, anchor, tracking)
    sys.stderr.write(f"width={w:.2f}\n")
    print(emit_group(parts, fill))
