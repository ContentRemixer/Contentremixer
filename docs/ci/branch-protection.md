# Branch protection + `ci-required` gate (C10 / ISI-7123)

> Parent: ISI-7093 (CI umbrella), plan §4 board requirement — **agent-authored
> PRs cannot merge red.**

Both ContentRemixer repos make CI a required gate on `main` by pinning a single
**`ci-required`** aggregate status check. The aggregate depends on the
individual CI jobs, so the required check stays stable as jobs are added or
renamed — branch protection never needs re-pointing.

| Repo | Workflow | Aggregate job (pinned required check) | Subtask |
| --- | --- | --- | --- |
| `ContentRemixer/contentremixer` (code) | `.github/workflows/ci.yml` | `ci-required` (needs build, test, coverage, quality) | C1 / ISI-7115 |
| ReferenceRepo (content) | `.github/workflows/content-ci.yml` (from `deploy/reference-repo-bundle/`) | `ci-required` (needs validate-content) | C7 / C8 / ISI-7118 / ISI-7122 |

Both aggregates use `if: always()` + a per-dependency `result == success` check,
so a skipped or failed job fails the gate instead of silently passing. Both
workflows run on **every** PR (no path filter on the required path) so the check
is never left `pending` on an unrelated PR.

## Applying protection — Ruleset UI (what the board used on the code repo)

**Settings → Rules → Rulesets → New ruleset → New branch ruleset**

- **Name:** `main` · **Enforcement:** **Active** · **Bypass list:** empty (so
  admins can't bypass either).
- **Target branches:** Include default branch (`main`).
- **Rules:**
  - ☑ **Restrict deletions**
  - ☑ **Block force pushes**
  - ☑ **Require a pull request before merging** → **Required approvals: `0`**
  - ☑ **Require status checks to pass**
    - ☑ **Require branches to be up to date before merging** *(the `strict`
      flag — required by this ticket; make sure it is ON)*
    - **Add check:** **`ci-required`** (choose source: GitHub Actions).
      **Pin only this** — do not add the individual `build`/`test`/`coverage`/
      `quality` jobs, nor anything from the legacy `operator-ci` workflow.

## Applying protection — classic Branch protection rule (equivalent)

**Settings → Branches → Add branch protection rule**, pattern `main`:
- ☑ Require a pull request before merging — Required approvals `0`.
- ☑ Require status checks to pass + ☑ Require branches to be up to date →
  add **`ci-required`**.
- ☑ Do not allow bypassing the above settings (enforce for admins).
- Force pushes: OFF · Deletions: OFF.

## REST / `gh api` equivalent

Requires a token or GitHub App with **Administration: write** on the repo:

```sh
gh api -X PUT repos/ContentRemixer/Contentremixer/branches/main/protection \
  -H 'Accept: application/vnd.github+json' \
  --input - <<'JSON'
{
  "required_status_checks": { "strict": true, "checks": [ { "context": "ci-required" } ] },
  "enforce_admins": true,
  "required_pull_request_reviews": { "required_approving_review_count": 0, "dismiss_stale_reviews": false, "require_code_owner_reviews": false },
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
JSON
```

### What each setting buys

- `strict: true` — PR branch must be up to date with `main` before merge.
- `enforce_admins: true` / empty ruleset bypass list — **no one** can merge a
  red PR; this is what makes "cannot merge red" hold.
- `required_approving_review_count: 0` — forces the PR flow (no direct pushes to
  `main`) without demanding a human approval on every agent PR; CI is the gate.
- `ci-required` — the one pinned required check.

## Applying protection on the ReferenceRepo

Same shape, same required-check name `ci-required` (from the bundle's
`content-ci.yml`), once that repo is named + an admin credential is granted —
see `deploy/reference-repo-bundle/README.md` → *Branch protection*. Blocked on
plan §5 open-Q #1 (ReferenceRepo not yet provisioned; owner: board).

## Done-check (both repos)

1. Protection active on `main`; `ci-required` is the pinned required check.
2. Open a test PR with a deliberately-failing change; confirm `ci-required` goes
   red and the **Merge** button is blocked.
3. Fix the change; confirm green `ci-required` unblocks merge.

> Code repo status: ruleset applied (id `26717878`) — active, no bypass, PR
> required, force-push/deletion blocked, `ci-required` pinned. PR #8 is the
> standing proof (`ci-required` failure→success on the same PR). Remaining:
> enable the *up to date* (`strict`) toggle and trim the required list to only
> `ci-required`.
