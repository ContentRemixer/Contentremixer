# ContentRemixer UI kit (`booth/web/src/ui/`)

The in-house component library for the web console — **headless [Radix UI]
primitives + the brand design tokens**. This is W7 (ISI-7210) under the approved
web-UI plan (`docs/design/phase4-web-ui-implementation-plan.md`, §2 / §2.1).

Downstream UI issues — **W3** (scraper review), **W5** (content detail), **W6**
(admin CRUD) — compose exclusively from this module. They should not reach for
raw Radix or hand-roll styled controls.

## Principles

1. **Tokens are the single source of truth.** Every colour, radius, shadow,
   gradient, and font comes from `branding/kit/tokens.json`, exported to
   `src/styles/tokens.css` (+ `tokens.ts`) and surfaced to components through
   Tailwind's theme (`tailwind.config.js` → `theme.extend`). **No hex is
   hand-copied in a component.** Grep proves it: `src/ui/**` contains no `#`
   colour literals.
2. **Theme-aware by construction.** Components style against the *semantic*
   layer (`bg`, `surface`, `text`, `primary`, `border`, …). The A2 light/dark
   toggle flips `data-theme` on `<html>`, which re-points those vars — no
   component re-render logic needed.
3. **Accessibility from the primitive.** Radix gives us focus management, ARIA
   wiring, keyboard interaction, and portalling. The wrapper only adds brand
   styling and ergonomic props.
4. **Presentational, data-agnostic.** Components take plain props + callbacks.
   The collaboration components model the B3/B4 mocks but hold no fetching or
   anchor-range logic — that lands in W5 against the Booth API.

## Token export spec (§2.1)

A single build step, `branding/kit/build/build_css.py` (sibling of
`build_kit.py`), reads the **same** `tokens.json` and emits:

| Output | Contents |
|---|---|
| `src/styles/tokens.css` | `:root { --cr-* }` brand primitives (verbatim hex) + a curated **semantic** layer for light (`:root`) and dark (`[data-theme="dark"]`). |
| `src/styles/tokens.ts` | The same tokens as a typed module (`primitives`, `gradients`, `semantic`, `scalars`) for component props and chart/canvas contexts. |

- **Primitives** (palette, gradients) are copied verbatim — the brand 1:1.
- **Semantic tokens** (surface/text/border/primary/…) are a deterministic design
  mapping expressed against the primitives, so they regenerate byte-stable.
- **Lockstep CI gate:** `npm run tokens:check` (`build_css.py --check`) exits
  non-zero if `tokens.json` drifts from the committed `tokens.css`/`tokens.ts`.
  Regenerate with `npm run tokens`.

Tailwind maps the CSS vars onto utility classes (`bg-surface`, `text-text-muted`,
`rounded-md`, `shadow-card`, `bg-fade`, …), so components use ordinary Tailwind
and still inherit the brand + theme.

## Catalog

Import everything from the barrel `src/ui/index.ts`, e.g.
`import { Button, CommentThread } from "../ui"`.

### Primitives
- **Button** — `primary | secondary | ghost | danger` × `sm | md | lg`,
  `loading`, `iconLeft/iconRight`.
- **Field / Label / Input / Textarea** — labelled control with a11y-wired
  error/hint; `invalid` flips the error state.
- **Badge** — status pill, tones `neutral | primary | accent | success |
  warning | info`, optional status `dot`.
- **Card** (+ `CardHeader/Body/Footer`) — surface panel, `raised`,
  `interactive`.
- **Separator** — themed rule (Radix Separator).

### Overlays & disclosure (Radix)
- **Dialog** (+ `DialogContent/Body/Footer`, trigger, close) — modal with focus
  trap, ESC, scroll-lock.
- **DropdownMenu** (+ items, checkbox-item, label, separator) — keyboard menu.
- **Select** — themed single-select.
- **Popover** — anchored floating panel (used by inline comments).
- **Tooltip** (+ `TooltipProvider`) — hover/focus hint.
- **Tabs** (+ list/trigger/content).
- **Toast** (`ToastProvider` + `useToast()`) — transient notifications.

### Identity & input toggles (Radix)
- **Avatar** — initials fallback; `agent` adds the brand-gradient ring for
  bot/agent identities.
- **Switch**, **Checkbox**.

### Collaboration (the W7 signature components)
- **Inline comments (Google-Docs style)** — `CommentMark` (anchored,
  highlighted, resolvable text span), `CommentThread` (quote + comments + reply
  composer + resolve/reopen), `CommentComposer` (reusable textarea + submit,
  ⌘/Ctrl-Enter to send). Models B3.
- **Peer review** — `ReviewPanel` (reviewer verdicts + approve / request-changes
  footer), `ReviewStatusBadge`. Models B4.
- **Assign an agent** — `AssignAgent` (searchable people/agents menu, gradient
  ring on agents). Maps to reviewer assignment and
  `POST /v1/illustration-requests?assignee=…`.

## Design review

A dev-only gallery renders every component in both themes:

```bash
cd booth/web && npm install && npm run dev   # then open /gallery.html
```

`gallery.html` is **not** in the production rollup input, so it never ships in
the Booth binary — it exists for design review and as a future visual-regression
target (W8).

[Radix UI]: https://www.radix-ui.com/primitives
