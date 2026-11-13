# Getting Started with ContentRemixer 🎛️

**Create once. Publish everywhere.**

This guide takes you from an empty cluster to your first remix: you file a
GitHub issue, the agent opens a pull request with the content recycled into a
new format, and you review and merge it. Budget ~30 minutes for a first run.

> New to the project? Read [the concepts](#1-how-contentremixer-works) first.
> Already have a cluster and just want the commands? Jump to
> [Install](#3-install) → [Configure](#4-configure) → [Your first remix](#5-your-first-remix-issue--pr).

---

## 1. How ContentRemixer works

ContentRemixer is an **agentic content-remixing platform for DevRel teams**.
One source piece — a conference talk, a livestream, a blog post — becomes a
blog, a YouTube short script, a teleprompter script, or a tutorial, each in your
team's voice, with source attribution baked in.

The vocabulary follows a music-production metaphor:

| Term | Meaning |
|---|---|
| **The Crate** | This repo — the reference content library (`content/`). Crate digging = the scraper finding gems. |
| **Sample** | A source extract the agent pulls from a piece in the Crate. |
| **Mix / Remix** | The recycled output (lands in `recycled/`). |
| **Mastering** | The checker step — brand-voice pass + hard scrub rules. |
| **Sample clearance** | Source attribution attached to every PR. |
| **The Booth** | The admin web UI + API that administers models, style profiles, and personas. |
| **The Drop** | Publication of an approved mix. |

### The flow

```
GitHub issue                 Operator pipeline                     Result
(labels: recycle:blog,  ──▶  analyze ─▶ write ─▶ check (Master)  ──▶  Pull request
 recycle:short, ...)         │          │         │                   (recycled/<slug>/,
                             │          │         │                    Sample clearance)
   GitHub App webhook ───────┘          │         └── scrub rules            │
   posts an ack comment                 └── styles/<format>.md               ▼
                                                                      human review ─▶ merge ─▶ The Drop
```

1. You open an issue (or use the **Recycle request** issue form) and apply one
   or more `recycle:*` labels.
2. A **GitHub App** receives the webhook, resolves each `recycle:*` label to an
   `(inputFormat → outputFormat)` route, and posts an **ack comment** echoing
   what it will do (and naming any labels it didn't recognize).
3. The **operator** runs the pipeline **once per matched route**:
   **analyze** the source → **write** the Mix in the target style →
   **check** (Mastering: brand voice + hard scrub rules).
4. The agent opens a **pull request** containing the new content under
   `recycled/<slug>/`, traceable to its source, with **Sample clearance**
   (attribution).
5. A human reviews and merges. Merged content is ready for The Drop.

**Hard scrub rules (always enforced in Mastering):** no tenant IDs, no tokens,
no IP addresses, no internal hostnames in published content.

### Where things live in the repo (The Crate)

| Path | Contents |
|---|---|
| `content/` | Reference content by type (videos, blog, livestreams, podcasts), one folder per piece with a `meta.yaml`. |
| `styles/` | Per-format style guides the writer agent follows, plus `styles/authors/` voice cards. |
| `recycled/` | Generated mixes, one folder per request, traceable to its source. |
| `.github/ISSUE_TEMPLATE/recycle.yml` | The **Recycle request** issue form. |
| `app/` | The Kubernetes operator (Go, kubebuilder). |
| `booth/` | The Booth — admin web UI + API (separate Go module). |
| `deploy/` | Kustomize bases + overlays and the deploy runbook. |
| `docs/` | This documentation. |

---

## 2. Prerequisites

You need a Kubernetes cluster and a handful of credentials. The platform runs as
a Kubernetes operator; there is no single-binary mode.

**Cluster & tooling**

- A **Kubernetes cluster** — [`kind`](https://kind.sigs.k8s.io/) for local
  evaluation, or any real cluster for a shared environment.
- [`kubectl`](https://kubernetes.io/docs/tasks/tools/) (with
  [`kustomize`](https://kustomize.io/) support — bundled in modern `kubectl`).
- [cert-manager](https://cert-manager.io/) installed in the cluster (the
  operator's admission webhook uses it for serving certs).
- An **ingress mechanism** so GitHub can reach the webhook endpoint. On the
  reference `k8Squad test` tier this is kgateway + MetalLB; any Ingress
  controller works.

**Credentials (none committed — see [Secrets](#secrets))**

- A **GitHub App** with webhook access to your reference repo, providing:
  - `GITHUB_APP_ID`
  - `GITHUB_APP_PRIVATE_KEY`
  - `GITHUB_WEBHOOK_SECRET`
- A **GitHub PAT** (`GITHUB_PAT`) used to clone the reference repo.
- At least one **model-provider API key** (`OPENAI_API_KEY` and/or
  `ANTHROPIC_API_KEY`). Models are fronted by an in-cluster
  **LiteLLM** gateway.

**For local development only**

- **Go 1.26** (the operator in `app/`; The Booth in `booth/` is a separate
  module that references `app/` via a `replace` directive).
- See [`.env.example`](../.env.example) for the full set of local keys.

---

## 3. Install

The platform installs with Kustomize. The `deploy/base` kustomization installs
the whole component set (CRDs, operator, webhook Service, LiteLLM gateway,
Postgres/pgvector, and The Booth); an **overlay** pins the image tag and
cluster-specific exposure.

> Full runbook: [`deploy/README.md`](../deploy/README.md). The summary below is
> the happy path.

### 3.1 Create the namespace and secrets

Secrets are **created out-of-band** and never committed (design §8). Populate
the values from your own secret manager:

```sh
kubectl create namespace contentremixer

# GitHub App + PAT
kubectl -n contentremixer create secret generic github-pat \
  --from-literal=token="$GITHUB_PAT"
kubectl -n contentremixer create secret generic github-webhook \
  --from-literal=hmac="$GITHUB_WEBHOOK_SECRET"

# One Secret per model provider referenced by a ModelProfile (see §4.1)
kubectl -n contentremixer create secret generic openai-apikey \
  --from-literal=key="$OPENAI_API_KEY"
```

The Booth and Postgres also need their out-of-band Secrets
(`booth-auth`, `pgvector-credentials`). The shapes are documented in
`deploy/secrets/*.example.yaml` — those `.example.yaml` files are **shape
documentation, never applied as-is**.

### 3.2 Apply an overlay

```sh
# Reference test overlay (k8Squad test):
kubectl apply -k deploy/overlays/k8squad-test

# Or the kind overlay for local E2E:
kubectl apply -k deploy/overlays/kind-e2e
```

### 3.3 Verify

```sh
kubectl -n contentremixer get pods
kubectl get crds | grep contentremixer.io
# contentremixer.io_modelprofiles / _referencerepos / _devrelpersonas
```

All operator, webhook, LiteLLM, Postgres, and Booth pods should reach
`Running`. If the webhook pod is crash-looping on certs, confirm cert-manager is
installed and healthy.

---

## 4. Configure

Configuration is **declarative** — you create Custom Resources, not config
files. Two CRs get you to a working pipeline; a third (`DevRelPersona`) adds
per-author voice in Phase 2.

### 4.1 `ModelProfile` — which model the pipeline uses

Each `ModelProfile` registers a model alias with the LiteLLM gateway and
references its API key via `spec.apiKeyRef` (a `SecretKeyRef` into the Secret
you created in §3.1). **Exactly one** profile must be `spec.default: true` — a
validating webhook enforces this.

```yaml
apiVersion: contentremixer.io/v1alpha1
kind: ModelProfile
metadata:
  name: fast-openai
  namespace: contentremixer
spec:
  default: true
  litellmName: default          # must match CONTENTREMIXER_DEFAULT_MODEL
  provider: openai
  model: gpt-4o-mini
  apiKeyRef:
    name: openai-apikey         # the Secret from §3.1
    key: key
```

`CONTENTREMIXER_DEFAULT_MODEL` (default `default`) selects which alias the
pipeline uses when a job doesn't pin a model.

### 4.2 `ReferenceRepo` — the Crate and its label routes

The `ReferenceRepo` CR points at your content repo and declares the
**label → format routing** — the single source of truth for how a `recycle:*`
label maps to `(inputFormat → outputFormat)`.

```yaml
apiVersion: contentremixer.io/v1alpha1
kind: ReferenceRepo
metadata:
  name: the-crate
  namespace: contentremixer
spec:
  url: https://github.com/<org>/<content-repo>
  tokenRef:
    name: github-pat
    key: token
  labelRoutes:
    - { label: "recycle:blog",         inputFormat: auto, outputFormat: blog }
    - { label: "recycle:short",        inputFormat: auto, outputFormat: short }
    - { label: "recycle:tutorial",     inputFormat: auto, outputFormat: tutorial }
    - { label: "recycle:teleprompter", inputFormat: auto, outputFormat: teleprompter }
```

**Routing rules that matter:**

- Only labels with the `recycle:` prefix are considered.
- A label routes **by exact string match** against `labelRoutes` — the format is
  *not* parsed out of the label name. `recycle:blog` produces a blog **only
  because a route says so.**
- `inputFormat: auto` detects the source format from the piece's `meta.yaml`
  (video → teleprompter, livestream → livestream, blog → blog, podcast →
  podcast), so Admins usually only specify the **output** intent.
- Several `recycle:*` labels on one issue → several routes → the pipeline runs
  once per route (default: **one PR per route**).
- A `recycle:*` label with no matching route is echoed in the ack as
  "ignored"; it does not start a run.

### 4.3 Styles — how each output reads

The writer agent loads `styles/<outputFormat>.md` for the target format (e.g.
`styles/blog.md`). Edit these to shape tone, length, and structure. Phase 2 adds
per-author voice via `styles/authors/` and `DevRelPersona`.

### 4.4 The Booth (admin UI) — optional but recommended

The Booth is the single front door to the CRs above. It serves a role-aware web
UI and a gated API:

- **Admins** manage `ModelProfile` and `ReferenceRepo` (Admin-only resource
  classes) and review mixes.
- **DevRel** users self-service onboard: supply reference sources + a short bio,
  the platform scrapes and **synthesizes a style profile**, you run a
  content-test, validate, and promote. (See the onboarding wizard in The Booth.)

Authorization is enforced **server-side** by the frozen predicate:
`authorized == (role == "Admin") OR (resource.owner.subject == session.subject)`.
See [`booth/README.md`](../booth/README.md).

---

## 5. Your first remix (issue → PR)

With a `ModelProfile` (default) and a `ReferenceRepo` applied, and the webhook
reachable from GitHub, you're ready to recycle a piece.

1. **Pick a source.** Choose a folder under `content/` (e.g.
   `content/videos/2026/2026-11-04-otel-k8s/`) that has a `meta.yaml`, or have
   an external URL ready.

2. **Open a Recycle request.** In your content repo, open a new issue using the
   **Recycle request** form (`.github/ISSUE_TEMPLATE/recycle.yml`):
   - **Source content path** — e.g. `content/videos/2026/2026-11-04-otel-k8s/`
   - **Target formats** — tick *blog*, *YouTube short*, *teleprompter script*,
     or *GitHub tutorial*
   - **Notes for the agent** — angle, audience, length

   The form applies the `recycle` label; add the specific `recycle:*` label(s)
   (e.g. `recycle:blog`) that match your `labelRoutes`.

3. **Read the ack.** Within moments the GitHub App posts a comment confirming
   the resolved `(inputFormat → outputFormat)` route(s) and flagging any
   unmapped `recycle:*` labels. No ack usually means the webhook isn't reaching
   the cluster — check ingress and the `github-webhook` HMAC secret.

4. **Let the pipeline run.** For each matched route the operator runs
   **analyze → write → check**. The checker (Mastering) applies brand voice and
   the hard scrub rules.

5. **Review the PR.** The agent opens a pull request with:
   - the Mix under `recycled/<slug>/`, traceable to its source,
   - **Sample clearance** (source attribution),
   - one PR per route by default.

   Review as you would any PR. The scrub guarantees no tenant IDs, tokens, IPs,
   or internal hostnames leaked into the output.

6. **Merge → The Drop.** Merge the PR. The approved mix is ready to publish; The
   Booth's Publishing surface suggests next steps.

---

## 6. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| No ack comment on the issue | Webhook unreachable or bad HMAC | Check the overlay's Ingress/exposure and the `github-webhook` Secret. |
| Ack says a label was "ignored" | No `labelRoutes` entry matches that exact label | Add the route to the `ReferenceRepo` CR (§4.2). |
| Operator webhook pod crash-loops | cert-manager missing/unhealthy | Install cert-manager; re-apply the overlay. |
| Pipeline can't reach a model | Wrong `apiKeyRef` or no default `ModelProfile` | Exactly one profile must be `default: true`; verify the referenced Secret. |
| PR not opened after a run | PAT lacks repo write, or clone failed | Check the `github-pat` Secret and the App's repo permissions. |

---

## 7. Next steps

- **Deploy runbook** — [`deploy/README.md`](../deploy/README.md)
- **Architecture** — [`docs/design/phase1-operator-design.md`](design/phase1-operator-design.md)
- **Personas & RAG (Phase 2)** — [`docs/design/phase2-personas-rag-design.md`](design/phase2-personas-rag-design.md)
- **The Booth (auth, API, onboarding)** — [`booth/README.md`](../booth/README.md)
- **Observability** — [`docs/observability/`](observability/)
- **Full docs index** — [`docs/README.md`](README.md)
