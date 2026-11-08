# Phase 4 — ContentRemixer Web UI Implementation Plan

> Status: **APPROVED by board (2026-11-08)** · Owner: BigBoss (CEO) · Issue: ISI-7205
> Source of truth for screens: **ISI-7198** (26 validated PNGs, light+dark × desktop+mobile)
> Design system: `branding/kit/tokens.json` · Backend base: `booth/` (Go)

## 0. Board decisions (resolved 2026-11-08)

1. **Frontend = React** (React + Vite + TS SPA, §2) — approved.
2. **Scraper runs as a triggered Job, not synchronously** — the scraper is a
   component kicked off as a Kubernetes **batch Job**; the API records/queues the
   request and the Job writes results back to the DB for review (§3.2).
3. **Content & scraped data live in a database, not CRDs** — approved (§3.3).
4. **Supersede ISI-7151 (P3-3 Booth UI)** into this track — approved (§5).

**Data-ownership model (board-stated):**
- **CRDs = configuration** — the GitHub repo definition (`ReferenceRepo`), the
  `DevRelPersona`, `ModelProfile`, and similar declarative platform config.
- **Database (Postgres)** — content, scraped sources, inline comments,
  review threads, illustration requests, review/queue state, users & sessions.
- **GitHub** — the published content artifacts themselves (via the existing
  GitHub App / PR path); the DB holds working/draft state + collaboration data.

This is the frontend + backend implementation plan to build the ContentRemixer
web UI from the validated mocks, plus the child-issue decomposition. It makes
the stack decisions, enumerates the backend/API gaps, fixes the authz model, and
sequences the work. **No code is written by this issue** — it is the planning
hand-off; implementation lands in the child issues below after approval.

---

## 1. Current state (grounded in the repo)

| Area | What exists today | Gap to the mocks |
|---|---|---|
| **Frontend** | None. Booth is API-only. | Everything: shell, auth, scraper-review, content-detail, admin CRUD. |
| **AuthN** | `booth/auth` — bcrypt users, HMAC-signed sessions, seed-Admin bootstrap, `POST /v1/auth/login`, `/logout`, `GET /v1/auth/whoami`, session middleware. | Client surfaces (A1 login, first-run bootstrap, A3 session-expired). No admin user/role CRUD API. |
| **AuthZ** | `booth/internal/authz` — frozen §6.3 predicate; `internal/store` is the **only** path handler→CR, gates every verb + writes audit. | Reused as-is. New kinds/entities must route through the same store seam. |
| **API surface** | `booth/internal/server` — stdlib `net/http` ServeMux; CRUD only over `ModelProfileKind` + `ReferenceRepoKind`. | Scraper-review, Content/Document, inline comments, review threads, illustration-request, Personas route, Users/Roles. |
| **Data plane** | DevRelPersona CRD (ISI-7143), reference scraper (ISI-7145), pgvector embed/store/retrieve (ISI-7146), Postgres live on k8squad-test (ISI-7148). | Scraper-review queue + content/comment/illustration entities not yet modeled. |
| **Design system** | `tokens.json` (palette, gradients, typography, effects); Satori + Poppins SVG/PNG pipeline. | No `tokens.json → CSS-var / TS-token` export. No component library. |

---

## 2. Frontend stack decision

**Decision: React + Vite + TypeScript SPA**, built to static assets and served
directly by the Booth Go binary, talking to Booth's JSON API over the existing
HMAC session cookie.

- **Component approach:** headless primitives (Radix UI) + a thin in-house
  component library (`booth/web/src/ui/`) generated from `tokens.json`. Tailwind
  CSS with its theme mapped onto the exported CSS variables, so the validated
  design system is the single source of truth — no hand-copied hex values.
- **Rendering:** client-rendered SPA, **no SSR**. This is an authenticated
  internal console (no SEO/first-paint-crawl need), so a static SPA avoids
  shipping a Node runtime into the cluster — Booth serves `index.html` + hashed
  assets from an embedded `embed.FS`, keeping one deployable artifact.
- **Why not HTMX/templ:** the richest screens (B3 Google-Docs inline comments,
  peer-review threads, assign-an-agent flow, B6 chunk-preview accept/discard) are
  stateful client interactions that a hypermedia approach makes awkward; a typed
  SPA scales cleanly across DevRel + Admin surfaces.
- **Main alternative considered:** SvelteKit/Remix SSR — rejected only on the
  "no Node in cluster / one Go artifact" operational constraint, not capability.

### 2.1 `tokens.json` → design-system pipeline

Add a build step (`branding/kit/build/build_css.py`, sibling to the existing
`build_kit.py`) that emits, from the **same** `tokens.json`:

1. `tokens.css` — `:root { --cr-violet: #7C3AED; … }` plus `[data-theme="dark"]`
   overrides, driving the validated light/dark toggle (A2).
2. `tokens.ts` — typed token module for component props and charts.

The SPA imports `tokens.css`; Tailwind's config reads the CSS vars via
`theme.extend.colors`. Gradients (`fade`, `tile-bg`), typography (Poppins
SemiBold/Medium, SIL OFL), radii, and glow are all exported so the build inherits
the brand 1:1. A CI check fails the build if `tokens.json` changes without a
regenerated `tokens.css` (keeps them in lockstep).

---

## 3. Backend / API gaps

All new surfaces route through the **existing `internal/store` seam** so the
§6.3 invariant and audit log apply structurally — adding a route cannot reach an
entity without the gate.

### 3.1 Reuse as-is
- **Auth/session** — login/logout/whoami already shipped. Client treats `401`
  from any call as "session expired" → A3 session-expired screen + redirect.
- **Personas / ReferenceRepos / ModelProfiles** — DevRelPersona CRD + the two
  existing kinds; wire `DevRelPersonaKind` into the server route table (the store
  + authz already generalize over `ResourceKind`).

### 3.2 New endpoints to build

| Screen | New API | Entity / backing |
|---|---|---|
| **B6 Scraper review** | `POST /v1/scrape-jobs` (enqueue a scrape — **creates a K8s batch Job**, returns job id), `GET /v1/scrape-jobs/{id}` (job status), `GET /v1/scraped-sources` (list + status), `GET /v1/scraped-sources/{id}/chunks` (chunk preview), `POST …/accept`, `POST …/rescrape` (re-enqueues a Job), `POST …/discard` → accepted chunks flow into the Reference Repo corpus (pgvector, ISI-7146). | **Scraper runs as a triggered Job** (board decision §0.2): the API creates a Kubernetes batch Job that runs the ISI-7145 scraper; the Job writes `ScrapedSource` rows (status `pending`) to Postgres. New `ScrapedSource` model + review-queue state machine (queued/running/pending-review/accepted/rescraping/discarded). Owner-scoped (DevRel sees own repos' sources). |
| **B3 Content detail** | `GET /v1/content/{id}` (document model), `GET/POST /v1/content/{id}/comments` (anchored inline comments), `GET/POST …/reviews` (peer-review threads), `POST /v1/illustration-requests` (+ `assignee=Graphic Designer`), `GET /v1/illustration-requests?assignee=…` (agent work queue). | New `Content`/`Document`, `Comment` (with text-anchor range), `ReviewThread`, `IllustrationRequest` (status + assigned agent) entities. |
| **A1 first-run + Admin** | `GET /v1/users`, `POST /v1/users`, `PATCH /v1/users/{id}` (role), `DELETE …` — Admin-only via §6.3 clause 1. First-run bootstrap endpoint surfacing the seed-Admin flow. | Extend `booth/auth` store with an admin-facing CRUD surface (today it only authenticates). |

### 3.3 Data ownership (board-decided, §0)
`ScrapedSource`, `Content`, `Comment`, `ReviewThread`, `IllustrationRequest`,
users & sessions are **app-domain data in a Booth-owned Postgres schema** (reuse
the live pgvector Postgres, ISI-7148) behind a `store`-style gating interface —
**not** CRDs. CRDs remain for declarative **configuration only**: the GitHub repo
definition (`ReferenceRepo`), `DevRelPersona`, `ModelProfile`. Published content
artifacts live in **GitHub** (existing GitHub App / PR path, ISI-7085); the DB
holds the working/draft + collaboration state. The backend child issues (W2/W4)
implement the Postgres schema + a DB-backed gating store that enforces §6.3 the
same way the CRD store does today.

---

## 4. Role-aware routing & authz

- **Server-side is authoritative** — every route stays behind the auth
  middleware and the §6.3 store gate. `List` filters; `Get/Update/Delete/Approve`
  gate the object; Admin-only classes (`ModelProfile`, `ReferenceRepo`, `Users`)
  return `403` to DevRel regardless of ownership.
- **Client routing is cosmetic** — `whoami` drives nav: Admin sees the admin
  sections (Users, Model Profiles, Reference Repos), DevRel sees content +
  scraper-review for owned repos. Client-side hiding is UX only; the server never
  trusts it. A DevRel hitting an admin route directly gets `403` → A3 403 screen.

---

## 5. Sequencing & fold decisions

- **Supersede ISI-7151 (P3-3 Booth role-aware UI):** this plan absorbs it. 7151's
  "server-side authz only" principle is carried forward; its deliverable is
  re-expressed as W0–W1 here. Recommend marking 7151 superseded-by ISI-7205.
- **ISI-7199 (DevRel) / ISI-7200 (Admin)** mock children of ISI-7198 are **design
  inputs** consumed by the frontend child issues, not parallel build tracks.
- **Reuse the Satori + Poppins pipeline** (captured on ISI-7198) if any
  additional mock states are needed mid-build.

### Dependency order
```
W0 foundation ─┬─ W1 auth+states ─┐
               ├─ W7 component lib ┤ (parallel, UX-reviewed)
               │                   │
W2 scraper API ─ W3 scraper UI ────┤
W4 content API ─ W5 content UI ────┼─ W8 web E2E + CI
W6 admin CRUD (API+UI) ────────────┘
```
W2/W4/W6 backend tracks can start in parallel with W0 (no frontend dependency);
their UI counterparts (W3/W5) depend on W0 + W7.

---

## 6. Proposed child issues (created after approval)

| # | Title | Dept | Depends on |
|---|---|---|---|
| **W0** | Frontend foundation: Vite+React+TS scaffold in `booth/web/`, `tokens.json`→CSS/TS pipeline, app shell (A2) + theme toggle, auth-gated SPA routing, served from Booth `embed.FS` | CTO | — |
| **W1** | Auth UI: A1 login + first-run Admin bootstrap; A3 system states (403/404/session-expired/empty/loading-skeleton) | CTO | W0 |
| **W2** | Backend: **Job-triggered scraper** + review API — `POST /v1/scrape-jobs` creates a K8s batch Job running the ISI-7145 scraper; Job writes `ScrapedSource` rows to Postgres; list/chunk-preview/accept/rescrape/discard → pgvector corpus | CTO | — |
| **W3** | Frontend: B6 Scraper-review screen | CTO | W0, W2, W7 |
| **W4** | Backend: Postgres schema + DB-backed §6.3 gating store for Content/Document + anchored inline comments + peer-review threads + `IllustrationRequest` + assign-to-agent queue; published content → GitHub PR path (ISI-7085) | CTO | — |
| **W5** | Frontend: B3 Content-detail reader + inline comments + request-illustration flow | CTO | W0, W4, W7 |
| **W6** | Admin CRUD (API + UI): Personas / Reference Repos / Model Profiles / Users & Roles, Admin-gated via §6.3 | CTO | W0, W7 |
| **W7** | Design system: component library from tokens + `tokens.json`→CSS/TS export spec + design-review sign-off | UXDesigner | W0 (scaffold) |
| **W8** | Web E2E + CI: build/typecheck/Playwright + serve-from-Booth integration, wired into the two-pipeline CI (ISI-7093) | CTO | W1, W3, W5, W6 |

Rough shape: ~8 build issues, mostly CTO-owned with UX owning the component
system (W7) and design review on every UI issue. No new owner-credential gates
anticipated (all runs on existing k8squad-test + Booth + pgvector).

---

## 7. Decisions — resolved (2026-11-08)

All three open decisions were approved by the board (see §0):
1. **Stack** — React+Vite SPA served from Booth. ✅
2. **Entity backing** — Postgres schema for content/comments/scraper-queue
   (not CRDs); CRDs for config only; published content in GitHub. ✅
3. **ISI-7151 fold** — superseded into this track. ✅

Added refinement: the **scraper runs as a triggered Kubernetes Job** (§0.2, §3.2).
