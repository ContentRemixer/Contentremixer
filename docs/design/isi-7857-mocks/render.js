// Generates the ISI-7857 "Skills" dialog redesign mock as SVG, then rasterizes
// to PNG via sharp. Issue attachments reject SVG (ISI-7801 footgun), so PNG is
// the deliverable; the SVG is committed as the editable source.
//
//   node docs/design/isi-7857-mocks/render.js
//
// sharp is resolved from /tmp/node_modules (same as branding/kit/build/*).

const sharp = require("/tmp/node_modules/sharp");
const fs = require("fs");
const path = require("path");

const OUT = __dirname;

// --- tokens (light theme, verbatim from booth/web/src/styles/tokens.css) -----
const C = {
  bg: "#F5F3FF",
  surface: "#FFFFFF",
  surface2: "#F0EDFB",
  border: "#E4DEF7",
  borderStrong: "#CFC6EE",
  text: "#1A1235",
  muted: "#6B6490",
  violet: "#7C3AED",
  magenta: "#EC4899",
  orange: "#F97316",
  white: "#FFFFFF",
};
const FONT = "'Poppins', 'Segoe UI', ui-sans-serif, system-ui, sans-serif";

// --- tiny SVG helpers --------------------------------------------------------
const esc = (s) =>
  String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
function text(x, y, s, o = {}) {
  const {
    size = 14,
    fill = C.text,
    weight = 400,
    anchor = "start",
    opacity = 1,
    deco = "none",
    family = FONT,
    italic = false,
  } = o;
  return `<text x="${x}" y="${y}" font-family="${family}" font-size="${size}" font-weight="${weight}" fill="${fill}" text-anchor="${anchor}" opacity="${opacity}" text-decoration="${deco}"${
    italic ? ' font-style="italic"' : ""
  }>${esc(s)}</text>`;
}
function rect(x, y, w, h, o = {}) {
  const {
    rx = 0,
    fill = "none",
    stroke = "none",
    sw = 1,
    opacity = 1,
    dash = null,
  } = o;
  return `<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="${rx}" fill="${fill}" stroke="${stroke}" stroke-width="${sw}" opacity="${opacity}"${
    dash ? ` stroke-dasharray="${dash}"` : ""
  }/>`;
}
function line(x1, y1, x2, y2, o = {}) {
  const { stroke = C.border, sw = 1, dash = null } = o;
  return `<line x1="${x1}" y1="${y1}" x2="${x2}" y2="${y2}" stroke="${stroke}" stroke-width="${sw}"${
    dash ? ` stroke-dasharray="${dash}"` : ""
  }/>`;
}

// measure-free chip: width estimated from char count (fine for a mock)
function chipW(label, pad = 24, cpx = 7.6) {
  return Math.round(label.length * cpx + pad);
}

// read-only default chip (outline, dashed, no ×)
function defChip(x, y, label, { replaced = false } = {}) {
  const w = chipW(label, replaced ? 86 : 20);
  const h = 28;
  let s = rect(x, y, w, h, {
    rx: 6,
    fill: replaced ? C.surface2 : C.surface,
    stroke: C.borderStrong,
    sw: 1,
    dash: "4 3",
    opacity: replaced ? 0.6 : 1,
  });
  s += text(x + 10, y + 19, label, {
    size: 13,
    fill: replaced ? C.muted : C.text,
    deco: replaced ? "line-through" : "none",
  });
  if (replaced) {
    s += text(x + w - 70, y + 19, "⊘ replaced", { size: 11, fill: C.orange, weight: 600 });
  }
  return { svg: s, w };
}

// editable selected chip (solid, violet tint, with ×)
function selChip(x, y, label, { unknown = false } = {}) {
  const w = chipW(label, unknown ? 58 : 38);
  const h = 28;
  let s = rect(x, y, w, h, { rx: 6, fill: "#F3ECFE", stroke: C.violet, sw: 1 });
  s += text(x + 10, y + 19, label, { size: 13, fill: C.violet, weight: 500 });
  let cx = x + w - 20;
  if (unknown) {
    s += text(x + 10 + label.length * 7.0 + 6, y + 19, "(?)", {
      size: 12,
      fill: C.orange,
      weight: 700,
    });
  }
  s += text(cx, y + 19, "×", { size: 16, fill: C.violet, opacity: 0.7 });
  return { svg: s, w };
}

// numbered annotation callout bubble
function callout(x, y, n, title, body, { tone = C.violet } = {}) {
  const w = 300;
  let s = "";
  // connector dot + number badge
  s += `<circle cx="${x + 14}" cy="${y + 14}" r="13" fill="${tone}"/>`;
  s += text(x + 14, y + 19, n, { size: 14, fill: C.white, weight: 700, anchor: "middle" });
  s += text(x + 36, y + 13, title, { size: 13.5, fill: C.text, weight: 700 });
  // wrap body
  const words = body.split(" ");
  let lineStr = "";
  let ly = y + 31;
  const maxw = 44;
  for (const wd of words) {
    if ((lineStr + " " + wd).trim().length > maxw) {
      s += text(x + 36, ly, lineStr.trim(), { size: 12, fill: C.muted });
      lineStr = wd;
      ly += 16;
    } else {
      lineStr += " " + wd;
    }
  }
  if (lineStr.trim()) s += text(x + 36, ly, lineStr.trim(), { size: 12, fill: C.muted });
  return s;
}

// --- dialog panel frame ------------------------------------------------------
function dialog(x, y, w, h, title) {
  let s = rect(x, y, w, h, { rx: 12, fill: C.surface, stroke: C.border, sw: 1 });
  // header
  s += text(x + 24, y + 34, title, { size: 17, fill: C.text, weight: 600 });
  s += text(x + w - 24, y + 34, "×", { size: 18, fill: C.muted, anchor: "end" });
  s += line(x, y + 52, x + w, y + 52, { stroke: C.border });
  return s;
}

// =============================================================================
// Layout
// =============================================================================
const W = 1480;
const DLG_X = 56;
const DLG_W = 620;
const ANN_X = 720; // annotation column
let y = 0;
let body = "";

// ---- page header ------------------------------------------------------------
body += rect(0, 0, W, 112, { fill: C.surface2 });
body += line(0, 112, W, 112, { stroke: C.border });
body += text(56, 48, "Agent Skills — redesign (ISI-7857 / W2 of ISI-7855)", {
  size: 24,
  fill: C.text,
  weight: 700,
});
body += text(56, 78, "Admin ▸ Agents ▸ Skills… dialog.  Three states, one screen. Grounded on GET /v1/skillpacks (W1) + the default-composition API.", {
  size: 14,
  fill: C.muted,
});
body += text(56, 98, "Design tokens: booth/web/src/styles/tokens.css (light). Target file: booth/web/src/pages/admin/AgentForm.tsx", {
  size: 12.5,
  fill: C.muted,
  italic: true,
});

// =============================================================================
// PANEL A — default / empty state (shows the default, never "no skills")
// =============================================================================
let py = 150;
const A_H = 300;
body += text(DLG_X, py - 12, "STATE A — opened, no packs configured yet", {
  size: 13,
  fill: C.violet,
  weight: 700,
});
body += dialog(DLG_X, py, DLG_W, A_H, "Skills — content-writer");
let iy = py + 84;
const ix = DLG_X + 24;
// Skill line
body += text(ix, iy, "Skill", { size: 12, fill: C.muted, weight: 600 });
// badge
body += rect(ix + 48, iy - 15, 70, 22, { rx: 999, fill: "#F3ECFE", stroke: C.violet, sw: 1 });
body += text(ix + 83, iy, "writer", { size: 12.5, fill: C.violet, weight: 600, anchor: "middle" });
body += text(ix + 130, iy, "chosen once, when the agent was created — not editable here.", {
  size: 12.5,
  fill: C.muted,
});
iy += 34;
// Default packs section
body += text(ix, iy, "Default skill packs", { size: 13.5, fill: C.text, weight: 600 });
body += text(ix + 158, iy, "from the writer skill", { size: 12, fill: C.muted, italic: true });
iy += 16;
body += text(ix, iy, "These run as-is. The writer skill composes them for you.", {
  size: 12.5,
  fill: C.muted,
});
iy += 18;
let cx = ix;
for (const lbl of ["blog-writing", "human-voice-review", "seo"]) {
  const c = defChip(cx, iy, lbl);
  body += c.svg;
  cx += c.w + 10;
}
iy += 52;
body += line(DLG_X + 24, iy - 14, DLG_X + DLG_W - 24, iy - 14, { stroke: C.border });
// Your packs section (empty → CTA)
body += text(ix, iy, "Your skill packs", { size: 13.5, fill: C.text, weight: 600 });
body += rect(ix + 132, iy - 14, 70, 18, { rx: 999, fill: C.surface2 });
body += text(ix + 167, iy - 1, "optional", { size: 11, fill: C.muted, anchor: "middle" });
iy += 18;
body += text(ix, iy, "Add packs to replace the default set above for this agent only.", {
  size: 12.5,
  fill: C.muted,
});
iy += 16;
// + Add packs button
body += rect(ix, iy, 128, 32, { rx: 8, fill: C.surface, stroke: C.violet, sw: 1.3 });
body += text(ix + 64, iy + 21, "+  Add packs", { size: 13.5, fill: C.violet, weight: 600, anchor: "middle" });
// footer
const aFooterY = py + A_H - 22;
body += line(DLG_X, aFooterY - 20, DLG_X + DLG_W, aFooterY - 20, { stroke: C.border });
body += text(DLG_X + DLG_W - 150, aFooterY, "Cancel", { size: 13.5, fill: C.muted, weight: 500 });
body += rect(DLG_X + DLG_W - 92, aFooterY - 18, 68, 30, { rx: 8, fill: C.violet });
body += text(DLG_X + DLG_W - 58, aFooterY, "Save", { size: 13.5, fill: C.white, weight: 600, anchor: "middle" });

// annotations for A
body += callout(ANN_X, py + 70, "1", "Show the default — never \"no skills\"",
  "Replaces the old empty state (\"No skill packs — the skill's catalog default applies\"). Read-only, dashed chips = not editable; the agent always looks equipped.");
body += callout(ANN_X + 360, py + 70, "3", "Plain copy: Skill vs. Skill packs",
  "Skill = the kind, picked once at create. Skill packs = optional prompt add-ons. The sentence explains a pack in one line.");

// =============================================================================
// PANEL B — picker open (pick, don't type)
// =============================================================================
py = 150 + A_H + 110;
const B_H = 360;
body += text(DLG_X, py - 12, "STATE B — \"Add packs\" picker open", {
  size: 13,
  fill: C.violet,
  weight: 700,
});
body += dialog(DLG_X, py, DLG_W, B_H, "Skills — content-writer");
iy = py + 80;
body += text(ix, iy, "Your skill packs", { size: 13.5, fill: C.text, weight: 600 });
iy += 14;
// search field
body += rect(ix, iy, DLG_W - 48, 34, { rx: 8, fill: C.surface, stroke: C.borderStrong, sw: 1 });
body += text(ix + 12, iy + 22, "⌕  Search packs…", { size: 13, fill: C.muted });
iy += 44;
// dropdown list
const listH = 176;
body += rect(ix, iy, DLG_W - 48, listH, { rx: 8, fill: C.surface, stroke: C.borderStrong, sw: 1 });
body += `<filter id="sh"><feDropShadow dx="0" dy="2" stdDeviation="4" flood-color="#1A1235" flood-opacity="0.08"/></filter>`;
const opts = [
  ["concise", "Tighten prose; cut filler and hedging.", true],
  ["human-voice-review", "Strip AI tells; enforce a natural voice.", false],
  ["seo", "Keyword, metadata & structure discipline.", true],
  ["visual-design", "Diagram & figure guidance for the piece.", false],
];
let oy = iy + 8;
for (const [name, desc, checked] of opts) {
  // checkbox
  body += rect(ix + 12, oy + 8, 16, 16, {
    rx: 4,
    fill: checked ? C.violet : C.surface,
    stroke: checked ? C.violet : C.borderStrong,
    sw: 1.2,
  });
  if (checked) body += text(ix + 20, oy + 21, "✓", { size: 12, fill: C.white, weight: 700, anchor: "middle" });
  body += text(ix + 40, oy + 16, name, { size: 13.5, fill: C.text, weight: 600 });
  body += text(ix + 40, oy + 33, desc, { size: 12, fill: C.muted });
  oy += 42;
}
iy += listH + 12;
// free-text escape hatch
body += text(ix, iy, "Can't find it? Add by name", { size: 12.5, fill: C.text, weight: 600 });
iy += 10;
body += rect(ix, iy, 300, 32, { rx: 8, fill: C.surface, stroke: C.borderStrong, sw: 1, dash: "4 3" });
body += text(ix + 12, iy + 21, "experimental-pack", { size: 13, fill: C.muted, italic: true });
body += rect(ix + 312, iy, 60, 32, { rx: 8, fill: C.surface, stroke: C.violet, sw: 1.2 });
body += text(ix + 342, iy + 21, "Add", { size: 13, fill: C.violet, weight: 600, anchor: "middle" });
body += text(ix + 384, iy + 21, "(?) unknown packs are kept, but ignored at run time.", {
  size: 11.5,
  fill: C.orange,
});

// annotations for B
body += callout(ANN_X, py + 70, "2", "Pick, don't type",
  "The blind free-text field becomes a searchable multiselect from GET /v1/skillpacks. Every option shows title + one-line description, so a pack reads as one choice, not a mystery string.");
body += callout(ANN_X, py + 200, "2b", "Free-text stays — as an escape hatch",
  "Packs not yet in the catalog can still be typed. The (?) marker is now real: it means \"unknown → ignored at run time,\" matching the degrade-never-gate CRD contract.");

// =============================================================================
// PANEL C — packs selected (replace made visually unambiguous)
// =============================================================================
py = py + B_H + 110;
const C_H = 356;
body += text(DLG_X, py - 12, "STATE C — packs chosen → default is visibly replaced", {
  size: 13,
  fill: C.orange,
  weight: 700,
});
body += dialog(DLG_X, py, DLG_W, C_H, "Skills — content-writer");
iy = py + 82;
// default packs, struck-through
body += text(ix, iy, "Default skill packs", { size: 13.5, fill: C.muted, weight: 600 });
body += text(ix + 158, iy, "overridden", { size: 12, fill: C.orange, italic: true, weight: 600 });
iy += 18;
cx = ix;
for (const lbl of ["blog-writing", "human-voice-review", "seo"]) {
  const c = defChip(cx, iy, lbl, { replaced: true });
  body += c.svg;
  cx += c.w + 10;
}
iy += 44;
// warn banner
body += rect(ix, iy, DLG_W - 48, 42, { rx: 8, fill: "#FEF1E6", stroke: C.orange, sw: 1.2 });
body += text(ix + 14, iy + 18, "⚠  Your packs replace the writer default.", {
  size: 12.8,
  fill: "#B4530C",
  weight: 700,
});
body += text(ix + 14, iy + 34, "The 3 default packs above will NOT run — only the packs you choose below run.", {
  size: 12,
  fill: "#B4530C",
});
iy += 56;
// your packs chips
body += text(ix, iy, "Your skill packs", { size: 13.5, fill: C.text, weight: 600 });
iy += 14;
cx = ix;
for (const [lbl, unk] of [["concise", false], ["seo", false], ["experimental-pack", true]]) {
  const c = selChip(cx, iy, lbl, { unknown: unk });
  body += c.svg;
  cx += c.w + 10;
}
// + add more
body += rect(cx, iy, 104, 28, { rx: 6, fill: C.surface, stroke: C.violet, sw: 1 });
body += text(cx + 52, iy + 19, "+ Add packs", { size: 12.5, fill: C.violet, weight: 600, anchor: "middle" });
iy += 44;
body += text(ix, iy, "(?) experimental-pack isn't in the catalog — it will be ignored at run time.", {
  size: 11.8,
  fill: C.orange,
});
// footer
const cFooterY = py + C_H - 22;
body += line(DLG_X, cFooterY - 20, DLG_X + DLG_W, cFooterY - 20, { stroke: C.border });
body += text(DLG_X + DLG_W - 150, cFooterY, "Cancel", { size: 13.5, fill: C.muted, weight: 500 });
body += rect(DLG_X + DLG_W - 92, cFooterY - 18, 68, 30, { rx: 8, fill: C.violet });
body += text(DLG_X + DLG_W - 58, cFooterY, "Save", { size: 13.5, fill: C.white, weight: 600, anchor: "middle" });

// annotations for C
body += callout(ANN_X, py + 70, "4", "Make REPLACE unambiguous",
  "Board kept replace-not-additive. So the default chips go struck-through + \"⊘ replaced\", and a warn banner spells out that the defaults will NOT run. No silent swap.");
body += callout(ANN_X, py + 210, "5", "Designer flag: additive may be safer",
  "The common intent is \"add concise on top of the blog stack,\" yet replace silently drops blog-writing + seo. Recommend revisiting additive-with-opt-out. See spec §6.");

// =============================================================================
const totalH = py + C_H + 70;
let svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${totalH}" viewBox="0 0 ${W} ${totalH}">`;
svg += rect(0, 0, W, totalH, { fill: C.bg });
svg += body;
svg += text(56, totalH - 26, "ContentRemixer · IsItObservable Labs — design mock, not final pixels. Copy strings are final (see isi-7857-skills-ux-spec.md §5).", {
  size: 12,
  fill: C.muted,
  italic: true,
});
svg += "</svg>";

const svgPath = path.join(OUT, "skills-dialog-states.svg");
fs.writeFileSync(svgPath, svg);

(async () => {
  await sharp(Buffer.from(svg), { density: 192 })
    .png()
    .toFile(path.join(OUT, "skills-dialog-states.png"));
  const meta = await sharp(path.join(OUT, "skills-dialog-states.png")).metadata();
  console.log(`PNG ${meta.width}x${meta.height} written`);
})().catch((e) => {
  console.error(e.message);
  process.exit(1);
});
