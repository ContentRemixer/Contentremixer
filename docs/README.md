# ContentRemixer Documentation 🎛️

**Create once. Publish everywhere.**

ContentRemixer is an agentic content-remixing platform for DevRel teams. One
source piece — a talk, livestream, or blog post — is recycled into blogs, shorts,
teleprompter scripts, and tutorials in your team's voice, each PR shipping with
source attribution. This is the documentation home. For the product pitch, see
the [repo README](../README.md).

> **Docs live in the main repo under `/docs`** (board-accepted default). The
> marketing website links into these pages rather than duplicating them, so this
> directory is the single source of truth for how to run and extend the
> platform.

---

## Start here

| If you want to… | Read |
|---|---|
| Understand the platform and run your first remix | **[Getting Started](getting-started.md)** |
| Deploy to a cluster (full runbook) | [`deploy/README.md`](../deploy/README.md) |
| Understand the architecture | [Phase-1 operator design](design/phase1-operator-design.md) |
| Administer models, personas, and publishing | [The Booth](../booth/README.md) |

---

## Documentation map

### Concepts & getting started
- **[Getting Started](getting-started.md)** — prerequisites, install, configure,
  and a full issue → PR walkthrough.
- [Brand language & the music-production metaphor](../README.md#brand-language) —
  The Crate, Sample, Mix, Mastering, The Booth, The Drop.

### Architecture & design
- [Phase-1 operator design](design/phase1-operator-design.md) — CRDs, the
  label → format routing contract, the analyze → write → check pipeline, the PR
  contract, AuthN/AuthZ and tenancy, Kubernetes shape, secrets policy.
- [DevRel model contract (ISI-7356)](design/isi-7356-devrel-model-contract.md) —
  reference kinds, primary content kind, style-profile + validation, identity
  link.
- [Style synthesis (ISI-7358)](design/isi-7358-style-synthesis.md) — corpus →
  named style traits + sample.
- [Phase-2 personas & RAG design](design/phase2-personas-rag-design.md) —
  `DevRelPersona`, retrieval-augmented writer conditioning.
- [DevRelPersona CRD contract](design/phase2-devrelpersona-crd-contract.md) —
  the Phase-2 persona API.
- [Phase-4 web UI implementation plan](design/phase4-web-ui-implementation-plan.md)
  — The Booth web UI (React SPA served from the Booth binary).

### Operations
- [Deploy runbook](../deploy/README.md) — Kustomize bases/overlays, out-of-band
  secrets, image & CI lanes.
- [Observability](observability/) — OpenTelemetry semantic conventions (Weaver
  registry), persona/RAG instrumentation, and the collector config.
  - [Phase-2 personas & RAG observability](observability/phase2-personas-rag-observability.md)
  - [Weaver semconv registry](observability/weaver/README.md)

### Administration — The Booth
- [The Booth](../booth/README.md) — the single front door to the CRs: AuthN
  (bcrypt users, signed sessions), the frozen §6.3 authorization predicate, the
  CRUD API, and DevRel self-service onboarding (sources → style synthesis →
  content-test → validate → promote).

### Reference
- [`.env.example`](../.env.example) — local-development keys (placeholders only;
  never commit real secrets).
- [Recycle request issue form](../.github/ISSUE_TEMPLATE/recycle.yml) — the
  structured form that starts a remix.
- CRDs: `ModelProfile`, `ReferenceRepo`, `DevRelPersona`
  (`deploy/base/crds/`).

---

## The flow at a glance

```
GitHub issue (recycle:* labels)
        │  GitHub App webhook → ack comment (resolved routes)
        ▼
Operator pipeline:  analyze ──▶ write (styles/<format>.md) ──▶ check (Mastering: brand voice + scrub)
        │
        ▼
Pull request (recycled/<slug>/ + Sample clearance)  ──▶  human review  ──▶  merge  ──▶  The Drop
```

See [Getting Started §1](getting-started.md#1-how-contentremixer-works) for the
narrative version.

---

## Conventions used in these docs

- **Declarative config.** You create Custom Resources (`ModelProfile`,
  `ReferenceRepo`, `DevRelPersona`), not config files. A validating webhook
  enforces invariants (e.g. exactly one default `ModelProfile`).
- **Secrets are never committed.** Every credential arrives via a Kubernetes
  Secret referenced by `SecretKeyRef`; `*.example.yaml` files document the
  *shape* only.
- **Phase labels.** Phase 1 = operator + pipeline + CRDs; Phase 2 = personas +
  RAG; Phase 3 = The Booth auth; Phase 4 = the web UI. Pages note the phase a
  feature belongs to.

---

## Contributing to the docs

These pages are plain Markdown in `/docs`. Keep the Getting Started guide as the
canonical "first run" path — the repo README (S-B) and the marketing website
(S-C) both link to it rather than restating install steps, so changes here
propagate to both surfaces.
