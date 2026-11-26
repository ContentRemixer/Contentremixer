# Onboarding a reference repo 🎛️

**The one authoritative path** for pointing ContentRemixer at a new GitHub repo —
from registering the `ReferenceRepo` to seeing your first `recycle:*` pull
request. It consolidates what used to be scattered across the repo README,
`deploy/secrets/github-app.example.yaml`, the deploy runbook, and the PR #131
onboarding notes.

> **Who this is for.** Platform Admins onboarding a *new* GitHub repo as a
> ContentRemixer **Crate** (reference content library) and **drop target** (where
> recycled PRs land). For the day-one "run your first remix on the demo repo"
> walkthrough, read [Getting Started](../getting-started.md) first — this page
> assumes the operator and Booth are already deployed (see the
> [deploy runbook](../../deploy/README.md)).

---

## What you are setting up

A **`ReferenceRepo`** is the Custom Resource that binds one GitHub repo to the
pipeline. Once it exists and the repo can deliver webhooks, applying a `recycle:*`
label to an issue triggers a remix:

```
GitHub issue (recycle:* label)
        │  GitHub App webhook → ack comment (resolved routes)
        ▼
Operator pipeline:  analyze ──▶ write (styles/<format>.md) ──▶ check (Mastering)
        │
        ▼
Pull request on branchPrefix/<slug>  ──▶  human review  ──▶  merge (The Drop)
```

Onboarding is four steps:

| # | Step | Who | One-time? |
|---|------|-----|-----------|
| 1 | [Register the `ReferenceRepo`](#step-1--register-the-referencerepo) | Admin | per repo |
| 2 | [Grant GitHub access](#step-2--grant-github-access) (install the App, or supply a PAT) | owner / Admin | per repo (App is one-time org-wide) |
| 3 | [Seed template issues](#step-3--seed-template-issues) | Admin | per repo |
| 4 | [Verify the first `recycle:*` PR](#step-4--verify-the-first-recycle-pr) | Admin | per repo |

Two things are **org-wide and one-time** — you do them once, not per repo:
registering the GitHub App (Step 2) and standing up the public webhook edge (see
the [deploy runbook](../../deploy/README.md#real-githubcom-deliveries-owner-gated)).
Everything else repeats per repo.

---

## Prerequisites

- The operator and Booth are deployed and reachable (deploy runbook). Live
  k8squad-test Booth: `http://10.0.0.226/`.
- You are signed in to Booth as an **Admin** (ReferenceRepos are platform config
  with no per-row owner — the Admin surface gates writes).
- A **public webhook edge** terminating TLS and routing `…/github/webhook` to the
  operator's delivery service (`operator-webhook.contentremixer.svc:8443`). This
  is the org-wide, owner-gated step in the deploy runbook. Without it, GitHub
  deliveries never reach the cluster and Step 4 cannot pass.
- The target repo on GitHub, with an issue label namespace of `recycle:*` (you
  create the labels — the webhook only acts on labels your `labelRoutes` declare).

---

## Step 1 — Register the `ReferenceRepo`

### Preferred: the Booth UI

Admin → **Reference repos** → **New reference repo**. The form
(`booth/web/src/pages/admin/ReferenceRepoForm.tsx`) owns the nested shape the
generic CR panel can't express:

| Field | Meaning | Example |
|-------|---------|---------|
| **Name** | `metadata.name` (RFC-1123 label) | `contentmixer-data` |
| **Repo** | `owner/name` on GitHub | `ContentRemixer/ContentMixer-Data` |
| **Base branch** | PR target base | `main` |
| **Branch prefix** | prefix for recycle branches | `recycle/` |
| **Draft PRs** | open PRs as draft | ✅ |
| **Label routes** | `recycle:<label>` → input/output format | see below |

**Label routes** are the single source of truth for routing — the label string
is never parsed for its format. Each row maps a `recycle:*` label to an
`inputFormat` (use `auto` — the analyzer detects source kind) and an
`outputFormat` (`blog` / `short` / `tutorial` / `teleprompter`, i.e.
`styles/<format>.md`):

| Label | inputFormat | outputFormat |
|-------|-------------|--------------|
| `recycle:blog` | `auto` | `blog` |
| `recycle:short` | `auto` | `short` |
| `recycle:tutorial` | `auto` | `tutorial` |
| `recycle:teleprompter` | `auto` | `teleprompter` |

> **Secret values are never typed into this form.** On create, the form captures
> the Secret *references* (name + key); the values are written separately (Step 2).
> On edit, the Secret refs are preserved untouched.

### Fallback: `kubectl apply`

If you aren't using the UI, apply the CR directly. The shape is documented in
[`deploy/secrets/referencerepo.example.yaml`](../../deploy/secrets/referencerepo.example.yaml):

```yaml
apiVersion: contentremixer.io/v1alpha1
kind: ReferenceRepo
metadata:
  name: contentmixer-data
  namespace: contentremixer
spec:
  repo: ContentRemixer/ContentMixer-Data
  # tokenRef is OPTIONAL — omit it entirely to use the GitHub App (Step 2a).
  # Include it only for the PAT fallback (Step 2b).
  webhookSecretRef:
    name: github-webhook
    key: hmac
  prTarget:
    baseBranch: main
    branchPrefix: recycle/
    draft: true
  labelRoutes:
    - { label: "recycle:blog",         inputFormat: auto, outputFormat: blog }
    - { label: "recycle:short",        inputFormat: auto, outputFormat: short }
    - { label: "recycle:tutorial",     inputFormat: auto, outputFormat: tutorial }
    - { label: "recycle:teleprompter", inputFormat: auto, outputFormat: teleprompter }
```

An admission webhook re-validates on create/update, so a malformed spec is
rejected regardless of which path you use.

---

## Step 2 — Grant GitHub access

The operator needs to (a) **receive** issue webhooks and (b) **write** back (ack
comment + open PR). There are two ways to authorize the write path. **Prefer the
GitHub App** — it removes the per-repo PAT and the manual-webhook dance, and it
avoids the `admin:repo_hook` / `repository_hooks` permission a PAT needs to
self-register a webhook.

### Step 2a (recommended) — install the GitHub App

The App is registered **once per org** (owner / org-admin only — a shared PAT
cannot create it). The authoritative, copy-pasteable version of these steps lives
in [`deploy/secrets/github-app.example.yaml`](../../deploy/secrets/github-app.example.yaml);
the summary below is the same flow. On k8squad-test the App is already registered
(**App ID `5934686`**) — if yours is too, skip to *Install on the repo*.

**One-time org registration (owner):**

1. **Register the App** via the pre-filled link (org-owned, so the key/webhook
   config lives with the org, not a person):
   ```
   https://github.com/organizations/ContentRemixer/settings/apps/new?name=contentremixer-recycler&webhook_active=true&webhook_url=<operator /github/webhook edge>&events[]=issues&issues=write&contents=write&pull_requests=write&metadata=read&public=false
   ```
   Set the App's **Webhook secret** to the **same value** as the `github-webhook`
   Secret's `hmac` key — existing HMAC verification then works unchanged (the App
   secret and `github-webhook/hmac` **must** match). Click **Create GitHub App**.

2. **Read the App ID.** On the App's **General** tab ("About" section):
   `App ID: <number>` — a plain, non-secret integer. This is **not** the
   "Client ID" (`Iv23…`) next to it. The App ID does not exist until the App is
   created.

3. ⚠️ **Download the private key — the footgun (ISI-7836).** In GitHub's current
   (2026) UI the path is **Credentials** tab → **Key pairs** → **New key**. The
   `.pem` downloads **automatically and only once**, the instant you click
   "New key" — there is **no re-download button** afterward (the list shows only a
   fingerprint + delete). If the download is missed or blocked, just click
   **New key** again (an App may hold several keys; delete unused ones). The
   "Client secret" on that same tab is for OAuth user-auth and is **not used
   here** — ignore it; only the key pair matters.

4. **Store the key as a Secret** (imperatively, so the PEM never touches git):
   ```bash
   kubectl -n contentremixer create secret generic github-app \
     --from-literal=app-id="$APP_ID" \
     --from-file=private-key=/path/to/contentremixer-recycler.YYYY-MM-DD.private-key.pem
   kubectl -n contentremixer rollout restart deployment/contentremixer-operator
   ```
   The minter accepts either PEM header (PKCS#1 `BEGIN RSA PRIVATE KEY` or PKCS#8
   `BEGIN PRIVATE KEY`) — no conversion needed.

   > ⚠️ **The operator Deployment must carry `GITHUB_APP_ID` /
   > `GITHUB_APP_PRIVATE_KEY` env** (optional `secretKeyRef` on the `github-app`
   > Secret). If a cluster was applied *before* that manifest landed, re-apply the
   > operator Deployment — creating the Secret alone is not enough, and a restart
   > without the env leaves the minter disabled.

5. **Confirm the minter is live:**
   ```bash
   kubectl -n contentremixer logs deploy/contentremixer-operator | grep "GitHub App auth enabled"
   ```

**Per-repo install (owner or org-admin):** App → **Install App** →
ContentRemixer → **Only select repositories** → pick the repo (e.g.
`ContentMixer-Data`). Per-repo scope — no org-wide repo access required.

With the App installed, **omit `spec.tokenRef`** on the `ReferenceRepo`. The
webhook mints a short-lived installation token per delivery, keyed on the
delivery's `installation.id`; it is a drop-in Bearer credential for the ack and
PR-assembly calls. `status.tokenResolved` is vacuously true on the App path.

### Step 2b (fallback) — per-repo PAT

If no GitHub App is available, use a Personal Access Token. Set `spec.tokenRef`
to the Secret holding it. The token needs `repo` scope (read, issue comments,
open PRs). With a PAT you must also **register the webhook manually** on the repo
(GitHub → Settings → Webhooks): payload URL = your public `…/github/webhook` edge,
content type `application/json`, secret = the same value as `github-webhook/hmac`,
events = *Issues*.

**Writing the PAT + HMAC through Booth** (preferred over raw `kubectl`): the UI
create flow posts to `POST /v1/referencerepos/{name}/secrets` (Admin-gated,
write-only — values are never echoed back). Booth stores them in a per-repo
Secret `refrepo-<name>` with keys `token` and `webhook-secret`, and returns only
the refs. Omitting the webhook secret makes Booth auto-generate a 32-byte HMAC.

> **Two Secret layouts exist, by design.** The Booth-written path uses Secret
> `refrepo-<name>` with keys `token` / `webhook-secret`. The hand-applied
> `kubectl` path can use any Secret name/key you reference (the
> `referencerepo.example.yaml` shows `github-pat`/`token` and
> `github-webhook`/`hmac`). Match your `spec.tokenRef` / `spec.webhookSecretRef`
> to whichever you used.

---

## Step 3 — Seed template issues

A freshly onboarded repo has no work in it. Seed a starter set of `recycle:*`
template issues so the DevRel team has ready-made remix requests to label.

> **Status (2026-11-26):** automated seeding is **in flight** as
> [ISI-7896 (W3)](../../). It adds an idempotent
> `POST /v1/referencerepos/{name}/seed-issues` backed by a versioned template
> pack shipped in-repo (Option A), plus a `CreateIssue` helper in
> `app/internal/githubapp/github.go` (today that package has `PostComment` only).
> Until it ships, seed manually using the structured
> [Recycle request issue form](../../.github/ISSUE_TEMPLATE/recycle.yml):

1. In the target repo, create a few issues from the **Recycle request** form (or
   plain issues) describing a source piece to remix.
2. Apply the matching `recycle:*` label(s) you declared in `labelRoutes`.
3. Create the labels first if they don't exist — the webhook only acts on labels
   your routes name.

Keep the seed set small (one per format is plenty) — its only job is to give the
team a working example and to drive Step 4's verification.

---

## Step 4 — Verify the first `recycle:*` PR

This is the **done-when** for onboarding. On the target repo:

1. **Label an issue.** Open (or reuse a seeded) issue and apply a `recycle:*`
   label — e.g. `recycle:blog`.

2. **Confirm the delivery.** GitHub → repo **Settings → Webhooks** (or the App's
   **Advanced** tab for the App path) → **Recent Deliveries**: the `issues`
   /`labeled` delivery should show a **`200`**. A non-200 means the edge, HMAC, or
   `ReferenceRepo` registration is off — see Troubleshooting.

3. **Confirm the ack comment.** The operator posts a comment on the issue echoing
   the **resolved routes** (label → input/output format). This proves routing
   matched your `labelRoutes`.

4. **Confirm the PR.** Within a short while a pull request opens on
   `<branchPrefix><slug>` (e.g. `recycle/<slug>`) against your base branch,
   containing the recycled output under the repo's output path with **Sample
   clearance** (source attribution). Draft if `prTarget.draft: true`.

5. **Review & merge** — the human gate (The Drop). Merging is intentionally never
   automated.

If all four succeed, the repo is onboarded.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Delivery shows no attempt at all | No public webhook edge, or App/repo webhook not pointed at it | Stand up the edge (deploy runbook); set the App/repo webhook URL to `…/github/webhook` |
| Delivery `401` / signature failure | App webhook secret ≠ `github-webhook/hmac` | Make them identical; re-check the `webhookSecretRef` |
| Delivery `200` but no ack comment | Label not in `labelRoutes`, or no write credential | Add the route; verify App install (2a) or `tokenRef` PAT scope (2b) |
| Ack comment but no PR | Pipeline not enabled, or base branch/perms | Check operator logs; confirm `contents:write` + `pull_requests:write` and `RECYCLE_*` wiring (deploy runbook) |
| Operator log lacks `GitHub App auth enabled` | `GITHUB_APP_*` env missing on the Deployment | Re-apply the operator Deployment, then `rollout restart` (see Step 2a note) |
| `.pem` never downloaded / lost | GitHub emits the key once, no re-download | Click **Credentials → Key pairs → New key** again; delete the stale key |

Useful checks:

```bash
# Is the minter live?
kubectl -n contentremixer logs deploy/contentremixer-operator | grep "GitHub App auth enabled"

# Did the ReferenceRepo register and resolve its token?
kubectl -n contentremixer get referencerepo <name> -o jsonpath='{.status}{"\n"}'

# LAN TLS smoke test of the delivery path (expects a signed body in real use):
curl -sk -X POST https://<edge>/github/webhook \
  -H 'Content-Type: application/json' \
  -d '{"action":"opened","repository":{"full_name":"owner/name"}}'
```

---

## See also

- [`deploy/secrets/github-app.example.yaml`](../../deploy/secrets/github-app.example.yaml) — authoritative GitHub App provisioning (the copy-paste source for Step 2a).
- [`deploy/secrets/referencerepo.example.yaml`](../../deploy/secrets/referencerepo.example.yaml) — the `ReferenceRepo` CR shape.
- [Deploy runbook](../../deploy/README.md) — the public webhook edge, TLS, and recycle-pipeline enablement (the org-wide, owner-gated steps).
- [Getting Started](../getting-started.md) — first-run narrative on the demo repo.
- [Phase-1 operator design](../design/phase1-operator-design.md) — the routing contract and PR contract this page operationalizes.
- [Recycle request issue form](../../.github/ISSUE_TEMPLATE/recycle.yml) — the structured form that starts a remix.
