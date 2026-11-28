# ISI-7978 (ISI-7965 W4) — Admin "Content styles" tab UX spec

CTO-owned UX spec (no dedicated UXDesigner agent; per the approved ISI-7965 plan rev 2).
Backend contract: the `ContentStyle` CRD from W1 (PR #142, merged): `spec.outputFormat`
(enum `blog;short;tutorial;teleprompter`, exactly one CR per format per namespace —
webhook-enforced), `spec.guide` (non-empty markdown), `spec.description` (optional).
GLOBAL scope: one set of guides shared across all reference repos.

## Jobs to be done

1. **See what styles exist** — at a glance, per output format ("is `short` configured?").
2. **Edit a guide** — the body is markdown the writer renders against; editing a
   multi-hundred-word doc in a one-line input is a non-starter.
3. **Create a style** for a format that has none (post-migration: a brand-new format).

## Decisions

- **List is grouped by output format, not by CR name.** The format IS the identity
  users care about; the CR name is plumbing. Columns: Format (badge), Description,
  Guide size (lines · words). No Name column.
- **Format is chosen once, at create.** Changing a guide's format afterwards is a
  delete + re-create, not an edit: the webhook owns one-per-format, and formats are
  the writer's fail-closed lookup key. The edit dialog states this.
- **Reuse CrPanel** for list/create/delete + a scoped row editor for the guide — the
  exact pattern ReferenceRepo (ISI-7514/7782) and Agent skills (ISI-7858) use: the
  generic panel, one bespoke dialog for the field that needs real room.
- **Create dialog**: output format as a select (the CRD enum — free text here is an
  avoidable webhook 400), description single-line, guide as a tall textarea
  (16 rows). Duplicate formats are *not* hidden from the select: the server's
  webhook is the authority; its error surfaces inline in the dialog (same contract
  as every other panel).
- **Guide editor**: a 18-row monospace textarea, live char count, Save disabled while
  the body is empty (the writer is fail-closed — an empty guide must never be
  writable). Description editable; format fixed. **GET→PUT round-trip** so a fresh
  resourceVersion and every untouched field ride along (AgentForm contract).
- **Delete stays** (generic panel). Rationale: one-per-format mistakes must be
  recoverable, and the writer's transitional repo fallback (W2) keeps generation
  safe until W5 cutover. The server/seeder stay authoritative regardless.

## Mocks

### List — Admin → Content styles

```
┌──────────────────────────────────────────────────────────────┐
│ Content styles                                    [ New ]    │
│ Per-format markdown style guides the writer renders against. │
├──────────┬──────────────────────┬─────────────────────────────┤
│ FORMAT   │ DESCRIPTION          │ GUIDE                       │
│ blog     │ Blog voice           │ 42 lines · 610 words       │
│ short    │ Shorts — punchy      │ 18 lines · 190 words  [Edit guide] [Delete] │
│ tutorial │ Tutorial depth       │ 55 lines · 780 words       │
└──────────┴──────────────────────┴─────────────────────────────┘
```

### Create — "New Content style"

```
┌ New Content style ─────────────────────────────┐
│ Output format *   [ short            ▾ ]        │  ← enum select
│                   Exactly one style per format. │
│ Description        [ Shorts — punchy    ]       │
│ Guide (markdown) * ┌─────────────────────────┐  │
│                    │ ## Voice                │  │  ← 16-row textarea
│                    │ Hook in the first …     │  │
│                    └─────────────────────────┘  │
│                            [ Cancel ] [ Create ] │
└─────────────────────────────────────────────────┘
```

### Edit — "Style guide — short"

```
┌ Style guide — short ──────────────────────────────────┐
│ Markdown the writer renders against for [short]. The   │
│ output format is fixed after create; delete + re-create│
│ to change it.                                          │
│ Description  [ Shorts — punchy ]                       │
│ Guide (markdown) *  ┌────────────────────────────┐     │
│ (1840 chars)         │ # Shorts                 │     │  ← 18-row mono
│                      │ Hook fast, land the …    │     │
│                      └────────────────────────────┘     │
│                             [ Cancel ] [ Save ]         │
└────────────────────────────────────────────────────────┘
```

## Errors & edges

- Non-Admin → server 403 → PanelForbidden (§6.3; unchanged, server-authoritative).
- Duplicate format on create → webhook message inline in the dialog.
- Empty guide → Save/Create disabled client-side; server MinLength is the backstop.
- Empty list → PanelEmpty: "No style guides yet. Use "New" to create one."
  (post-W3 migration the four formats arrive seeded).

## Acceptance

- Admin sees the tab; list renders per-format rows; create round-trips
  `{metadata.name = format, spec.{outputFormat, guide, description}}`.
- Edit guide → PUT `/v1/contentstyles/{name}` with full-object round-trip; only
  description + guide change.
- e2e: admin.spec.ts "Admin edits a content style guide…" covers list + editor PUT.
