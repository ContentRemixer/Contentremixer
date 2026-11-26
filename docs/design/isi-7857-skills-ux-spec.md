# ISI-7857 — Agent Skills section: UX redesign spec

**W2 of [ISI-7855](../../) (plan approved by board 2026-11-26).**
Role: Graphic Designer (UX). Status: ready for **W3 ([ISI-7858](../../)) frontend** to implement.

- **Target file:** `booth/web/src/pages/admin/AgentForm.tsx` (lives on `main`).
- **Data source:** `GET /v1/skillpacks` + per-skill-kind default composition — delivered by **W1 ([ISI-7856](../../))**.
- **Mock:** [`isi-7857-mocks/skills-dialog-states.png`](isi-7857-mocks/skills-dialog-states.png) (source: `skills-dialog-states.svg`, regen via `node isi-7857-mocks/render.js`).
- **Tokens:** `booth/web/src/styles/tokens.css` (light theme). Font `Poppins`, violet `#7C3AED`, warn `#F97316`.

---

## 1. Problem (board feedback)

Clicking an agent → "Skills…" shows a reference to *skill packs* but **no default skills**. The
empty state reads `No skill packs — the skill's catalog default applies`, which looks like the
agent has **no** skills. Adding a pack is a single **blind free-text field**, so "a skill" feels
far bigger than one text box. The copy never separates **Skill** (the kind) from **Skill pack**
(an optional overlay).

Three concrete defects (verified against `main`):

| # | Defect | Where |
|---|--------|-------|
| D1 | Default composition is invisible — empty state names no skills | `AgentForm.tsx:88` |
| D2 | Add-pack is a blind free-text field (`GET /v1/skillpacks` never built → `suggestions` always `[]`) | `AgentForm.tsx:53-56`, `:112-137` |
| D3 | Jargon copy; Skill vs. Skill pack never distinguished | `:88`, `:119`, `:344`, `:447-449` |

---

## 2. Data contract this design consumes (from W1 / ISI-7856)

Design assumes W1 exposes, additively and Admin-gated:

```jsonc
// GET /v1/skillpacks  → the catalog (discovered from skills/*/SKILL.md)
[
  { "name": "concise",            "title": "Concise",            "description": "Tighten prose; cut filler and hedging." },
  { "name": "human-voice-review", "title": "Human-voice review", "description": "Strip AI tells; enforce a natural voice." },
  { "name": "seo",                "title": "SEO",                "description": "Keyword, metadata & structure discipline." }
  // …one row per skills/<name>/SKILL.md
]
```

Plus a **per-skill-kind default composition** (same or sibling endpoint), surfacing the Go
`skillComposition` map incl. the dynamic single-pack fallback. For the agent being edited the UI
needs the ordered default pack list for its skill, e.g.

```jsonc
// content-writer → ["blog-writing", "human-voice-review", "seo"]   (catalog entry)
// <any kind not in the map> → [ "<that-name>" ]                     (dynamic single-pack fallback)
```

If a `description`/`title` is absent for a row, fall back to `name` (degrade, never blank).
If the defaults endpoint fails, fall back to the current blank — but **log** it; never show a
worse state than today.

---

## 3. Redesigned dialog (three states — see mock)

Opened from **Admin ▸ Agents ▸ Skills…**. Same `Dialog` shell as today; the **body** changes.

### State A — opened, no packs configured (the common case)
1. **Skill** line: label + `writer` badge + *"chosen once, when the agent was created — not
   editable here."* Distinguishes the kind from packs up front.
2. **Default skill packs** section (**D1**): read-only chips of the default composition for this
   skill (`blog-writing`, `human-voice-review`, `seo`), rendered as **dashed-outline, no ×**
   (= not editable). Sub-label *"These run as-is. The writer skill composes them for you."*
   The agent now always looks equipped.
3. **Your skill packs** (`optional` pill): empty → a **`+ Add packs`** button and one line,
   *"Add packs to replace the default set above for this agent only."*

### State B — "Add packs" picker open (**D2**)
- A **searchable multiselect** sourced from `GET /v1/skillpacks`. Each row = **checkbox +
  title (bold) + one-line description**. Selecting toggles a pack into *Your skill packs*.
- **Escape hatch** retained: *"Can't find it? Add by name"* free-text + **Add**, for packs not
  yet in the catalog. Inline note: *"(?) unknown packs are kept, but ignored at run time."*
  Pattern mirrors the existing `datalist`, upgraded to a real listbox with descriptions.

### State C — packs chosen → **replace made unambiguous** (product decision, §6)
- The **Default skill packs** become **struck-through + dimmed**, each tagged **`⊘ replaced`**,
  section label flips to *overridden*.
- A **warn-tone banner**: *"⚠ Your packs replace the writer default. The 3 default packs above
  will NOT run — only the packs you choose below run."*
- **Your skill packs** = removable solid chips (`concise ×`, `seo ×`). An unknown pack shows the
  working **`(?)`** marker (`experimental-pack (?) ×`) with the inline *ignored at run time* note.

---

## 4. Interaction & accessibility notes for W3

- **Read-only default chips** are not focusable and have no `×`; give the group an
  `aria-label="Default skill packs from the <kind> skill (read-only)"`.
- **Picker** = `role="listbox"` with `role="option"` rows, `aria-selected` on each; typing filters
  by `title`/`name`/`description`. Keep keyboard parity (↑/↓/Enter/Esc).
- **Unknown marker**: keep today's logic — `unknown = catalog.length > 0 && !catalog.includes(p)`.
  Now that the catalog is real, `(?)` becomes meaningful; keep the `title`/`aria` tooltip.
- **Replace banner** only renders when `packs.length > 0`. When the last pack is removed, the UI
  returns to State A (defaults un-struck) — no save needed to preview the effect.
- Dedup + trim + order preserved (CRD `listType=atomic`) — unchanged from today.
- No backend write changes: still GET→swap `spec.skillPacks`→PUT. `GET /v1/skillpacks` is a
  pure read the picker calls on open.

---

## 5. Final copy strings (ready to paste)

Replace the four strings. "Skill" = the kind (create-time enum); "Skill pack" = optional overlay.

| Location | Current | **Final** |
|----------|---------|-----------|
| `AgentForm.tsx:88` (empty state) | `No skill packs — the skill's catalog default applies.` | **(removed — replaced by the "Default skill packs" read-only section).** If packs ever can't load: `Using the default skill packs for this skill.` |
| `:344` (create Field hint) | `Prompt packs layered on the skill. Override the catalog default; unknown packs are ignored at run time.` | **`Optional prompt add-ons. Leave empty to use this skill's default packs; adding any replaces that default for this agent. Unknown packs are ignored at run time.`** |
| `:447-449` (edit dialog intro) | `Skill packs layered on top of the <kind> skill. They override the catalog composition for this agent's runs. Unknown packs are ignored at run time.` | **`A skill pack is an optional prompt add-on. This agent's skill is <kind>, which already runs a default set of packs (shown below). Any packs you add here replace that default for this agent's runs; unknown packs are ignored at run time.`** |
| New — default section label | — | **`Default skill packs`** + sub-label **`These run as-is. The <kind> skill composes them for you.`** |
| New — picker CTA / search | — | **`+ Add packs`** / placeholder **`Search packs…`** / escape-hatch label **`Can't find it? Add by name`** |
| New — replace banner (State C) | — | **`Your packs replace the <kind> default. The N default packs above will NOT run — only the packs you choose below run.`** |
| Unknown-pack marker tooltip | `not found in reference repo — will be ignored at run time` | **`Not in the skill-pack catalog — ignored at run time.`** |

> **Term lock:** **Skill** = the kind, chosen once at create (Select, closed set).
> **Skill pack** = an optional prompt add-on, chosen here. Never call a pack "a skill".

---

## 6. Product decision surfaced: replace vs. additive

**Today & board-accepted default: added packs REPLACE the skill's default composition**
(`resolveSkillPacks`, `writer/skills.go` — a non-empty override replaces, not appends). The board
accepted keeping replace. This spec's job was to make it **visually unambiguous**, which State C
does (struck-through defaults + `⊘ replaced` + warn banner). **No silent swap.**

### Designer flag (as the card requested) — additive is probably the safer default

- **The common intent is additive.** The realistic admin edit is *"run the normal blog stack
  **plus** `concise`."* Under replace, typing/picking `concise` **silently drops** `blog-writing`
  **and** `seo` — the exact footgun this whole ticket exists to kill. The warn banner mitigates but
  doesn't remove the trap; a user who wanted "+concise" must now re-pick all three defaults by hand.
- **Recommendation (non-blocking, for a follow-up):** make the field **additive with an explicit
  opt-out** — default packs stay active and are shown as locked chips; added packs append; a single
  **"Replace defaults instead"** toggle switches to today's replace semantics for the rare
  full-override case. This keeps the one-line mental model ("packs add on") and makes destructive
  replace a deliberate choice.
- **Scope:** additive changes `resolveSkillPacks` behaviour (backend) + the CRD note, so it is
  **out of scope for W3** and should be its own card if the board agrees. If the board stays with
  replace, **State C ships as-is** and is correct.

---

## 7. Done / handoff

- [x] Spec doc (this file) + final copy strings (§5).
- [x] Annotated mock PNG — [`isi-7857-mocks/skills-dialog-states.png`](isi-7857-mocks/skills-dialog-states.png).
- [x] Grounded on the real W1 contract (§2) and real code line numbers (§1).
- → **W3 (ISI-7858)** implements against this once W1 lands. Review: Code Reviewer.
