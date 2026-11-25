# ISI-7801 — UX spec: Agents management + Orchestration/handoff editor

Status: **READY FOR CTO** · Author: Winston (Architect, acting Graphic Designer) ·
Parent: [ISI-7778](/ISI/issues/ISI-7778) (plan approved) ·
Data contract: [ISI-7800 CRD contract](isi-7800-agent-crd-contract.md) (signed off — field shapes LOCKED).

This spec is consumed **before** coding by the frontend build children:

- **ISI-7802** — P1 read-only Admin tabs (lists only; no mutation).
- **ISI-7803** — P2 CRUD (create Agent, edit skill-packs, team editor, delete-guard).

It is **mocks + interaction spec, not code.** Mocks live in [`isi-7801-mocks/`](isi-7801-mocks/).
Every surface here is an **additive tab** on the existing `AdminPage.tsx`, built from
components already on `main`. **Invent nothing new that an existing pattern already covers.**

---

## 0. Reuse ledger (what each deliverable is built from)

| New surface | Reuses (on `main`) | New glue only |
|---|---|---|
| Agents tab — list | `CrPanel.tsx` (`columns`, `create`, delete) + `useResource.ts` + `AdminPage` `<TabsTrigger>` | column cells, create field set |
| Agents tab — skill-packs multiselect | the `LabelRoutesEditor.tsx` **controlled-rows** pattern (add/remove chips, parent owns array, `validate*` mirrors webhook) | `SkillPacksField` (chips) + packs discovery fetch |
| Orchestration tab — team list | `CrPanel.tsx` (list + delete) | column cells |
| Orchestration tab — team editor | the `LabelRoutesEditor.tsx` **GET → swap one spec slice → PUT** pattern (round-trip full object, preserve untouched fields) | `TeamEditor` (nodes rows + edges rows) |
| All validation | `validateRoutes()` precedent: client mirror of the webhook, **server stays authoritative** | `validateTeam()`, `validateAgent()` |
| Forbidden/empty/loading | `panelStates.tsx` (`PanelForbidden`/`PanelError`/`PanelLoading`/`PanelEmpty`) | — |
| Auth gate | `RequireAuth role="Admin"` already wrapping `AdminPage` | — |

The two new tabs sit **after** the four existing ones:

```
Users & roles │ Personas │ Reference repos │ Model profiles │ Agents │ Orchestration
                                                              └── new ──┘
```

Both render the server's 403 via `PanelForbidden` for a non-Admin — the tabs are
cosmetic, the §6.3 store is authoritative (same stance as the other four tabs).

---

## 1. Agents tab

**Route data:** `GET/POST/DELETE /v1/agents` (the `AgentKind` registered in
ISI-7800 §7.4). One `Agent` CR per row. See mock
[`mock-agents-list.svg`](isi-7801-mocks/mock-agents-list.svg).

### 1.1 List — columns

Columns map 1:1 to `CrColumn.cell` readers off the raw CR (same `str()` dotted-path
helper `AdminPage` already uses). Order and source:

| Column | Source (`spec.*` unless noted) | Cell render |
|---|---|---|
| **Role** | `spec.role` | plain text, medium weight |
| **Skill kind** | `spec.skillKind` | `<Badge tone="info">` (closed enum, so a fixed palette reads well) |
| **Skill packs** | `spec.skillPacks[]` | up to 3 chips, then `+N`; `—` when empty |
| **Model** | `spec.modelProfile` | text; render `default` in muted text when empty or literally `"default"` |
| **Actor** | `spec.actorTemplate` | `content` (muted) / `media` (`<Badge tone="warning">`, the image path) |
| **Default** | `spec.default` | `<Badge tone="success">Default</Badge>` when true, else blank |

**"See default agents"** = the seeded `default:true` rows (ISI-7800 §6.1: the six
`architect / research / coding / observability / graphic / content-writer`). They
are ordinary rows carrying the **Default** badge — no separate screen. A short
helper line above the table reads: *"Agents marked **Default** are seeded and
reproduce the platform's built-in team. You can add extra agents below."*

### 1.2 Delete-guard (seeded rows)

A `default:true` Agent is the stable by-kind handle the seeder and team nodes rely
on (ISI-7800 §2 `Default`, Q2). The row's **Delete** button is therefore
**disabled** with a tooltip: *"Default agents are seeded and cannot be deleted.
Add an extra agent of the same skill kind instead."*

- This is a **client affordance only.** If a delete slips through (API call by
  hand), the server/seeder re-creates the seed on next boot — the guard is UX, not
  security. State that in the `CrPanel` config comment.
- Non-default (admin-added) rows delete normally through the existing `CrPanel`
  delete path (confirm-via-toast, no extra dialog — matches Personas/Models today).

`CrPanel` currently renders an always-enabled Delete. P2 adds **one** optional
hook to `CrPanelConfig`: `deleteGuard?: (o: CrObject) => string | null` returning a
reason string to disable+tooltip, or `null` to allow. This is the minimal,
reusable extension (Personas/Models pass nothing and behave exactly as today).

### 1.3 Create form — "add extra agents"

Reuses `CrPanel`'s `create` dialog (`CrCreateSpec`). Fields, in order:

| Field | Control | Required | Notes / hint |
|---|---|---|---|
| **Name** | `Input` | ✓ | `metadata.name`; DNS-1123, lower-kebab. Hint: *"Unique id, e.g. `coding-fast`."* |
| **Role** | `Input` | ✓ | `spec.role`, the human label. Hint: *"Shown in the team editor, e.g. `Coding Specialist (fast)`."* |
| **Skill kind** | `Select` | ✓ | closed enum — the six `KnownSkillKinds`. **Not** free text. |
| **Model profile** | `Select` | — | options = live `GET /v1/modelprofiles` `litellmName`s + a leading `default`. Mirrors webhook rule A2. |
| **Actor template** | `Select` | — | `content` (default) / `media`. |
| **Skill packs** | `SkillPacksField` (chips) | — | see §1.4. |
| **Description** | `Textarea` | — | `spec.description`. |

`spec.default` is **not** in the create form — new admin agents are always
`default:false` (only the seeder sets `default:true`, and the webhook A3 allows at
most one per kind). Note this in the dialog as muted helper text.

The two `Select` fields (Skill kind, Actor) need enum sources. Because `CrPanel`'s
current `CrCreateField` only renders `Input`/`Textarea`, P2 extends it with a
`kind?: "text" | "textarea" | "select"` + `options?: {value,label}[]` so the same
dialog renders a `Select`. This is the **only** structural change to `CrPanel` and
it is additive (existing callers keep their `multiline` boolean behaviour).

`build(values)` assembles:

```jsonc
{
  "metadata": { "name": v.name },
  "spec": {
    "role": v.role,
    "skillKind": v.skillKind,
    "modelProfile": v.modelProfile === "default" ? undefined : v.modelProfile || undefined,
    "actorTemplate": v.actorTemplate || undefined,   // omit → server default "content"
    "skillPacks": packs.length ? packs : undefined,
    "description": v.description || undefined
  }
}
```

### 1.4 "Modify skills" = inline `skillPacks[]` multiselect

This is the explicit deliverable-1 requirement. Two entry points, one component:

1. In the **create** dialog (above).
2. On an **existing row**: a `rowAction` **"Skills…"** opens a small dialog that
   GETs the one Agent, lets the admin edit `spec.skillPacks`, and PUTs the
   round-tripped object back (the LabelRoutesEditor GET→swap-one-slice→PUT pattern —
   every other spec field preserved untouched). See
   [`mock-agent-skills.svg`](isi-7801-mocks/mock-agent-skills.svg).

**`SkillPacksField`** (the chips control):

- Renders current packs as removable chips; a combobox/`Select` adds one more.
- **Discovery:** options come from the reference repo's available packs via
  `GET /v1/skillpacks` (packs discovered from the reference repo — deliverable-1
  wording). **Until that endpoint exists it degrades gracefully to a free-text add**
  (type a pack name, Enter to add a chip) — because the CRD contract says unknown
  packs **degrade (warn), they do not fail admission** (ISI-7800 §2.1 `skillPacks`
  note). So the UI must **never block** on an unknown pack: show known packs as
  suggestions, allow arbitrary strings, mark a typed-but-unknown pack with a muted
  *"not found in reference repo — will be ignored at run time"* hint, not an error.
- Dedup on add; trim; preserve order (the CRD is `listType=atomic`).

> **Open UX note for CTO (non-blocking):** `GET /v1/skillpacks` is not in the
> ISI-7800 contract. P2 can ship the free-text-with-suggestions form now and wire
> real discovery when a packs-list endpoint lands. Filed as a follow-up thought,
> not a blocker — the degrade path is the contract-correct behaviour anyway.

---

## 2. Orchestration tab

**Route data:** `GET/POST/PUT/DELETE /v1/agentteams` (`AgentTeamKind`, ISI-7800 §7.4).
One `AgentTeam` CR = one orchestration DAG. See
[`mock-orchestration.svg`](isi-7801-mocks/mock-orchestration.svg).

Layout: a **team list** (left/top) + a **scoped editor** for the selected team
(right/below). The editor is a **table/list editor** (deliverable-2: *start with a
table/list editor; a visual DAG canvas is an explicit nice-to-have follow-up, NOT a
blocker*).

### 2.1 Team list — columns

| Column | Source | Cell |
|---|---|---|
| **Name** | `metadata.name` | text; click selects it into the editor |
| **Entry** | `spec.entry` | the entry node id |
| **Nodes** | `spec.nodes.length` | count |
| **Handoffs** | `spec.edges.length` | count (`0` = single-node/linear team) |
| **Default** | `spec.default` | `<Badge tone="success">Default</Badge>` when true |

The seeded `tutorial-default` (5 nodes, 4 edges, `default:true`) and
`content-default` (1 node, 0 edges) rows appear here with the same **Default**
delete-guard as agents. A helper line: *"The **Default** team runs when a tutorial
names no team. Exactly one team may be the default."*

### 2.2 Team editor — the table/list editor (P2)

Opened by selecting a team (or **New team**). Mirrors `LabelRoutesEditor`'s
controlled-rows + GET→swap→PUT discipline. Three stacked sections:

**(a) Entry** — a single `Select`, options = this team's declared node ids. Changing
it re-validates reachability (T7) live.

**(b) Nodes** — one row per node, `[ node id (Input) ] [ agentRef (Select) ] [Remove]`:
- `agentRef` `Select` options = live `GET /v1/agents` by `metadata.name`, each
  rendered as **`role` — `name`** (so the admin picks "Coding Specialist — coding",
  not a bare id). Mirrors webhook T3 (`agentRef` must resolve).
- **Add node** appends `{ id: "", agentRef: "" }` (same shape as `emptyRoute()`).
- Node id is free text but validated unique (T1) and non-empty (MinLength=1).

**(c) Handoffs (edges)** — one row per edge, `[ from (Select) ] → [ to (Select) ] [Remove]`:
- Both `Select`s list the **declared node ids** only (so T4 "endpoint is a declared
  node" is structurally impossible to violate from the UI — you cannot pick an
  undeclared id).
- **Add handoff** appends `{ from: "", to: "" }`.
- A self-edge (`from === to`, T5) is flagged inline, not blocked at pick time.

Footer: **Save** (PUT the round-tripped object, `spec.nodes`/`spec.edges`/`spec.entry`
swapped, `default`/`owner` preserved) and **Cancel**. Save is disabled while
`validateTeam()` returns a message (§3).

The same editor backs **New team** (POST instead of PUT) and a single-node/linear
team (0 edges is valid — the `content-default` case).

### 2.3 Visual DAG canvas — explicit follow-up, NOT in P2/P3

The table editor is the shipping deliverable. A read-only **preview** is a cheap,
high-value middle step and is specced as optional polish, not a blocker:

- A **Mermaid** `flowchart LR` string generated from `nodes`+`edges`
  (`entry` highlighted) rendered beneath the editor — read-only, no drag.
  Example generated source for `tutorial-default`:

  ```mermaid
  flowchart LR
    architect([architect · entry]) --> research[research]
    architect --> coding[coding]
    architect --> observability[observability]
    architect --> graphic[graphic]
  ```

- A full drag-to-connect canvas (react-flow or similar) is a **separate follow-up
  issue** the CTO can cut after P3 if there's appetite. Flag it; do not build it.

---

## 3. Validation affordances (mirror the webhook; server authoritative)

Every client check below **mirrors** an ISI-7800 §4 webhook rule for fast feedback.
The server stays the source of truth — exactly the `validateRoutes()` precedent.
On a `422/Invalid` from the API, surface the server's aggregated `field.ErrorList`
message verbatim in the dialog's error slot (the webhook already aggregates all
problems into one message, so one API round-trip shows everything).

### 3.1 `validateAgent()` — client mirror

| Webhook rule | Client mirror | Surfaced as |
|---|---|---|
| A1 `skillKind` ∈ registry | `Select` only offers the six known kinds | impossible to violate from UI |
| A2 `modelProfile` resolves | `Select` offers only live `litellmName`s + `default` | impossible to violate from UI |
| A3 ≤1 default per kind | create form never sets `default` | n/a (admin cannot set default) |
| name uniqueness / DNS-1123 | regex check on Name field | inline field error |

### 3.2 `validateTeam()` — client mirror (the important one)

Returns the first blocking message or `null` (same contract as `validateRoutes`):

| Webhook | Client check | Inline message |
|---|---|---|
| nodes ≥ 1 (MinItems) | `nodes.length >= 1` | *"A team needs at least one node."* |
| T1 unique ids | `Set` over node ids | *"Node id `X` is used more than once."* |
| T2 entry declared | `entry ∈ nodeIds` | *"Entry must be one of the team's nodes."* |
| T3 agentRef resolves | each `agentRef ∈` fetched agent names | *"Node `X` references an unknown agent."* (also: `Select` only offers real agents) |
| T4 edge endpoints declared | structurally enforced by edge `Select`s | n/a |
| T5 no self-edge | `from !== to` per edge | *"A node cannot hand off to itself (`X`)."* |
| **T6 acyclic** | Kahn topo-sort over (nodes, edges); residual in-degree ⇒ cycle | *"This creates a cycle: `A → B → A`. Hand-offs must flow one way."* |
| **T7 reachable from entry** | BFS/DFS from `entry`; unreached node ⇒ orphan | *"Node `X` is never reached from the entry node."* |

**Cycle (T6) and orphan (T7)** are the two affordances deliverable-3 calls out
explicitly. Presentation:

- **Cycle:** when detected, the offending edges render with a red left-border and a
  banner names the cycle path (`A → B → A`). Save disabled until broken.
- **Orphan:** an unreached node row renders a muted warning pill *"unreachable"* and
  the banner lists them. Save disabled (T7 is a hard error per ISI-7800 Q3).

Both are **client-side pre-submit** — the admin fixes before the request is sent.
If the server still rejects (race, direct edit, webcat drift), its message wins and
is shown verbatim. See [`mock-orchestration-validation.svg`](isi-7801-mocks/mock-orchestration-validation.svg).

The topo-sort + reachability helpers live next to `validateTeam()` so P2 and a
later DAG-canvas follow-up share one implementation (the `validateRoutes` file is
the placement precedent).

---

## 4. Interaction flows (step-by-step for the build)

**F1 — Admin adds a cheaper coding agent**
1. Agents tab → **New** → dialog.
2. Name `coding-fast`, Role `Coding Specialist (fast)`, Skill kind `coding`,
   Model profile `glm-cheap`, Actor `content`, Skill packs `+ concise`, Create.
3. `POST /v1/agents`; A2 passes (model exists) → row appears, no Default badge,
   Delete enabled.

**F2 — Admin edits an existing agent's skill-packs**
1. Agents tab → row → **Skills…** → dialog GETs the Agent.
2. Remove chip `verbose`, add chip `concise` (typed; shows *"not found — ignored at
   run time"* hint, still allowed).
3. Save → PUT round-trips the full object with only `spec.skillPacks` changed.

**F3 — Admin builds a new 3-node team**
1. Orchestration tab → **New team** → editor.
2. Add nodes `plan`(→ architect), `write`(→ coding), `art`(→ graphic). Entry `plan`.
3. Add handoffs `plan → write`, `plan → art`.
4. `validateTeam()` green → **Save** → `POST /v1/agentteams`.

**F4 — Admin accidentally creates a cycle**
1. In F3's editor add handoff `write → plan`.
2. Live check: banner *"Cycle: plan → write → plan"*, offending edges red, **Save**
   disabled. Admin removes the edge; banner clears; Save re-enabled.

**F5 — Non-Admin opens either tab** → `useResource` gets 403 → `PanelForbidden`.

---

## 5. Hand-off to CTO (what P2/P3 build against this)

- **ISI-7802 (P1 read-only):** both tabs as **lists only** — `CrPanel` with the
  §1.1 / §2.1 columns, no create, delete-guard shows seeded rows as read-only.
  Ships the Default-badge + helper lines. No editor, no create dialog.
- **ISI-7803 (P2 CRUD):** the create dialog (§1.3), `SkillPacksField` (§1.4),
  `TeamEditor` (§2.2), `validateAgent`/`validateTeam` (§3), the `deleteGuard` +
  `select`-field `CrPanel` extensions (§1.2/§1.3), and the F1–F4 flows.
- **Minimal `CrPanel` extensions** this spec introduces (all additive, existing
  callers unaffected): `deleteGuard?` hook; `CrCreateField.kind:"select"` +
  `options`. Nothing else in `CrPanel`/`useResource`/`AdminPage` changes.
- **Deferred, flagged, not blockers:** `GET /v1/skillpacks` discovery endpoint
  (§1.4 degrade path ships without it); read-only Mermaid preview (§2.3);
  full drag DAG canvas (separate post-P3 issue).

---

## 6. Mock index

| File | Screen |
|---|---|
| [`mock-agents-list.svg`](isi-7801-mocks/mock-agents-list.svg) | Agents tab — list with Default badges, delete-guard, Skills…/Delete row actions |
| [`mock-agent-skills.svg`](isi-7801-mocks/mock-agent-skills.svg) | Create/Skills dialog — form fields + `SkillPacksField` chips |
| [`mock-orchestration.svg`](isi-7801-mocks/mock-orchestration.svg) | Orchestration tab — team list + table/list team editor (nodes + handoffs) |
| [`mock-orchestration-validation.svg`](isi-7801-mocks/mock-orchestration-validation.svg) | Team editor validation — cycle banner + orphan pill + disabled Save |

All mocks use the booth design tokens (neutral slate base, one accent, the
`Badge` tones already in `ui/`). They are wireframe-fidelity — spacing/typography
follow the existing admin tabs, not a new visual language.
