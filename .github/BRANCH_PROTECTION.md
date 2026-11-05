# Branch protection — CI-required merge gate (ISI-7123 / C10)

Makes CI a **required merge gate** so agent-authored PRs **cannot merge red**
(ISI-7093 plan §4, board requirement).

## The pinned required check: `ci-required`

`.github/workflows/ci.yml` defines a single aggregate job, `ci-required`, that
`needs: [build, test, coverage, quality]` and fails unless all four succeed. We
pin **only** `ci-required` as the required status check so the gate stays stable
as individual jobs are added/renamed (C2/C3 security, image, SBOM, Trivy, etc.)
— those feed `ci-required` via its `needs:` list, not branch protection.

The job runs on **every** PR (no path filter) on purpose: a required check that
never triggers leaves PRs stuck "Expected — waiting for status".

## Protection settings applied to `main`

| Setting | Value | Why |
|---|---|---|
| Require status checks | `ci-required` | the board §4 gate |
| Require branches up to date (`strict`) | ✅ | no merging stale-against-main |
| Require a pull request | ✅ (0 approvals) | blocks direct pushes to `main`; agents self-merge on green, no human review mandated |
| **Include administrators** (`enforce_admins`) | ✅ **ON** | the automation PATs hold repo `admin`, so **agents are admins** — without this the gate would not apply to them and a red PR could still be merged |
| Allow force pushes / deletions | ❌ | protect history |
| Require conversation resolution | ✅ | |

> ⚠️ `enforce_admins=true` is load-bearing here. It is the only thing that makes
> the gate bind the agents, since they push with an admin-level token.

## Apply / verify (owner runbook)

Requires a token with repository **Administration: Read and write** (fine-grained
PAT) or classic `repo` scope. The project push/PR PAT only has `metadata=read`
and **403s** on these endpoints — ask the repo owner (Alfred/board) for an
Administration-scoped token, or run this as a repo admin.

```bash
# preview the exact payload (no token needed)
DRY_RUN=1 .github/scripts/apply-branch-protection.sh ContentRemixer/Contentremixer

# apply to the code repo
GH_TOKEN=<admin-scoped-pat> .github/scripts/apply-branch-protection.sh ContentRemixer/Contentremixer main ci-required

# verify what is live
GH_TOKEN=<admin-scoped-pat> .github/scripts/apply-branch-protection.sh ContentRemixer/Contentremixer main --verify
```

## Done = (ISI-7123 acceptance)

1. ✅ `ci-required` aggregate job exists and is the pinned required check (C1).
2. ⬜ Branch protection active on `main` — run the script with an admin token.
3. ⬜ Prove a red CI blocks merge: open a throwaway PR that fails one job
   (e.g. break `gofmt`), confirm the **Merge** button is blocked on
   `ci-required`, then close it.

## Content side (ReferenceRepo)

Same gate, same script. Blocked until the board names the canonical ReferenceRepo
and grants access (ISI-7093 §5 #1), and C7/C8 land a `ci-required` aggregate in
that repo's content-CI workflow. Then:

```bash
GH_TOKEN=<admin-scoped-pat> .github/scripts/apply-branch-protection.sh <owner>/<ReferenceRepo> main ci-required
```
