# ContentRemixer Web (`booth/web`)

React + Vite + TypeScript SPA for the ContentRemixer console. Built to static
assets and **served from the Booth Go binary** via `embed.FS` — one deployable
artifact, no Node runtime in the cluster (plan §2, ISI-7206 / W0).

## Design system is generated, not hand-written

The single source of truth is `branding/kit/tokens.json`. The build step
`branding/kit/build/build_css.py` emits:

- `src/styles/tokens.css` — `:root` brand primitives + a semantic layer mapped
  for light (`:root`) and dark (`[data-theme="dark"]`), driving the A2 toggle.
- `src/styles/tokens.ts` — the same tokens as a typed module.

Tailwind (`tailwind.config.js`) maps its theme onto those CSS variables, so no hex
is copied by hand. CI (`web-ci.yml`) fails if these drift from `tokens.json`
(tokens lockstep).

```
npm run tokens         # regenerate tokens.css / tokens.ts
npm run tokens:check   # CI lockstep check
```

## Develop

```
npm install            # note: this repo's CI/env defaults npm to omit=dev;
                       # use `npm install --include=dev` locally if devDeps are skipped
npm run dev            # Vite dev server on :5173, proxies /v1 -> Booth (BOOTH_DEV_TARGET)
npm run typecheck      # tsc --noEmit
npm run build          # tsc && vite build -> dist/
```

Point the dev proxy at a running Booth with `BOOTH_DEV_TARGET=http://host:8080`.

## How it's served

`embed.go` embeds `dist/` and exposes `web.Handler()`, mounted on the Booth mux at
`/` **after** the API/health routes (`internal/server`). Unknown paths fall back to
`index.html` so client-side routes (`/content`, `/admin`, …) deep-link. Auth is
enforced per API route server-side (§6.3) and mirrored cosmetically in the client
router (`RequireAuth` + `GET /v1/auth/whoami`); static assets are public.

`dist/` is **committed** so the Go build needs no JS toolchain. `web-ci.yml`
rebuilds it and fails on drift — regenerate with `npm run build` and commit.

## Layout

| Path | Role |
|---|---|
| `src/app/` | theme + auth contexts, API client |
| `src/shell/` | A2 app shell: top bar, sidebar, user menu, notifications, theme toggle |
| `src/routes/` | `RequireAuth` client gate (cosmetic; server is authoritative) |
| `src/pages/` | route screens (feature screens are W3/W5/W6 placeholders) |
| `src/components/` | shared primitives (logo, icons, loader) |
| `src/styles/` | generated tokens + global CSS |

Scope of W0: scaffold, token pipeline, app shell, auth-gated routing, embed+serve.
Feature screens (B3/B6/admin) and the full A1/A3 auth surfaces land in W1/W3/W5/W6.
