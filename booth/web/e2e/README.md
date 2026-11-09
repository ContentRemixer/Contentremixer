# Web E2E (ISI-7214, W8)

Playwright end-to-end tests for the ContentRemixer SPA. **Hermetic**: every Booth
`/v1/*` API call is stubbed in-browser via `page.route` (see `fixtures.ts`), so the
suite runs with no Postgres, no Kubernetes, and no Go binary — just the built SPA
served by `vite preview`.

## Coverage (plan §6: A1 / A3 / B6 / B3 / admin)

| Spec | Screen | What it proves |
|------|--------|----------------|
| `auth.spec.ts` | A1 login / first-run | anonymous→login redirect, first-run welcome + seed prefill, invalid-credential error, successful sign-in→home |
| `states.spec.ts` | A3 states | states catalogue, 401-mid-session→session-expired teardown, 404, DevRel→403 on admin route |
| `scraper-review.spec.ts` | B6 | empty queue, pending rows + actions, accept→toast + drops from pending |
| `content.spec.ts` | B3 | empty library, document list, detail reader (body + status + comments/review/art rails) |
| `admin.spec.ts` | admin C1–C4 | Admin sees users table, persona (CR) panel loads |

The **serve-from-Booth** integration (the embedded SPA served by the Go binary
alongside `/v1/*` and `/healthz`) is covered at the Go layer in
`booth/internal/server/serve_web_test.go` and `booth/web/embed_test.go`.

## Run locally

```bash
cd booth/web
npm ci
npx playwright install chromium   # one-time browser download
npm run e2e                       # builds dist, previews it, runs the suite
npm run e2e:ui                    # interactive watch mode
```

`npm run e2e` starts the preview server itself (`playwright.config.ts` → `webServer`),
building a fresh `dist` first so a stale build never greens.

## CI

`.github/workflows/web-ci.yml` runs this as the `e2e` job (chromium + OS deps via
`playwright install --with-deps`), parallel to the `build` job (typecheck + build +
dist lockstep). A failed run uploads the HTML report as the `playwright-report` artifact.

## Adding a test

Call `installApi(page, defaults({ … }))` first to stub the backend, then drive the
UI with semantic locators (`getByRole`, `getByLabel`, `getByText`). Per-test route
overrides registered *after* `installApi` take precedence (Playwright runs the most
recently added handler first) — see the session-expiry test in `states.spec.ts`.
