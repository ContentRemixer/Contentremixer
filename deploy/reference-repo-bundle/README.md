# ContentRemixer — ReferenceRepo Content CI bundle (C8 / ISI-7122)

A ready-to-install drop-in that wires **Content CI** into the ContentRemixer
**ReferenceRepo** (the content-hub / "The Crate"). It is the deliverable of
subtask **C8** ([ISI-7122](https://.../ISI-7122)), plan §3/§5, parent
[ISI-7093](https://.../ISI-7093).

Everything here calls the **central** composite action published from
`ContentRemixer/contentremixer` — **zero duplication** of validation logic. The
ReferenceRepo only carries this thin workflow + the PR templates.

## What it does

On every PR that touches `content/**` or `recycled/**`, the workflow runs the
`contentremixer content validate` checks (via the published container image) on
the changed item folders and reports findings as inline PR annotations + a job
summary:

| Rule | Severity | Enforces |
| --- | --- | --- |
| `required-assets` | error | every asset a type's `meta.template.yaml` declares exists |
| `asset-structure` | error / warning | `meta.yaml` parses; `title` set; declared fields populated |
| `content-quality` | error / warning | the ISI-7082 **Mastering** scrub — secrets / IPs / internal hostnames fail closed |
| `seo` | warning | sane title length, single H1, non-empty tags |
| `pr-template` | error | the PR body carries the C9 content-template fields + Sample-clearance section |

Both content locations are gated:

- `content/**` — The Crate: reference/published source content.
- `recycled/**` — The Mixes: the `recycled/<slug>/{index.md,meta.yaml}` folder a
  ContentRemixer remix PR adds (ISI-7076 §4). A recycled draft resolves its type
  from `meta.output_format` (`blog`/`social`/`newsletter`; other formats still
  get the quality/SEO/PR-template checks).

## Install

Copy this bundle's `.github/` into the ReferenceRepo root and open a PR:

```sh
# from a checkout of the ReferenceRepo:
cp -r <this-repo>/deploy/reference-repo-bundle/.github/ .github/
git checkout -b chore/install-content-ci
git add .github/workflows/content-ci.yml .github/PULL_REQUEST_TEMPLATE/
git commit -m "ci: install ContentRemixer Content CI (C8 / ISI-7122)"
git push -u origin chore/install-content-ci
gh pr create --fill
```

That is the whole install — the workflow just:

```yaml
- uses: ContentRemixer/contentremixer/.github/actions/content-validate@v1
```

### Versioning

Pin the action **and** the image to the same released major (`@v1` /
`:v1`) for a reproducible gate. Publishing a `vX` tag of
`ContentRemixer/contentremixer` ships a matching
`ghcr.io/contentremixer/content-validate:vX` image (see the source repo's
`content-ci.yml` image job). Until the first `v1` tag is cut, change both pins
to `@main` / `:latest`.

## ⚠️ Prerequisites / open decision (plan §5 open-Q #1)

This bundle is **install-ready**, but two owner-gated items must land before the
workflow is live in the ReferenceRepo:

1. **Canonical ReferenceRepo named.** Which GitHub repo is the ReferenceRepo
   (the content-hub the recycle PRs target). **Owner:** board.
2. **Push/PR access.** A GitHub App install or PAT that lets an agent (or a
   human) add `.github/` to that repo and open the install PR. Tracked
   alongside the edge/token work in
   [ISI-7112](https://.../ISI-7112). **Owner:** board to grant; then CTO-side
   install.
3. **First `v1` tag cut** on `ContentRemixer/contentremixer` so `@v1` / `:v1`
   resolve (the source `content-ci.yml` publishes the image on tag). Until then
   use `@main` / `:latest` as noted above.

Once (1) + (2) are confirmed, installing is the copy-and-PR above — no further
code work on this bundle.

## Files

- `.github/workflows/content-ci.yml` — the drop-in Content CI workflow.
- `.github/PULL_REQUEST_TEMPLATE/{blog,social,newsletter,default}.md` — the C9
  ([ISI-7119](https://.../ISI-7119)) type-aware content PR templates, kept in
  lock-step with the `pr-template` rule. Authors pick one via the GitHub
  `?template=blog.md` (etc.) query param; ContentRemixer-opened PRs pre-fill the
  matching type automatically.
