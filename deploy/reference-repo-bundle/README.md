# ContentRemixer — ReferenceRepo Content CI bundle (C8 / ISI-7122)

A ready-to-install drop-in that wires **Content CI** into the ContentRemixer
**ReferenceRepo** (the content-hub / "The Crate"). It is the deliverable of
subtask **C8** ([ISI-7122](https://.../ISI-7122)), plan §3/§5, parent
[ISI-7093](https://.../ISI-7093).

> **Where this runs (board decision, ISI-7122):** the `ContentRemixer/contentremixer`
> repo hosts **only the ContentMixer code**, not DevRel content — so Content CI
> does **not** run there. It is *installed on the separate content repo* (the
> future DevRel content hub) from this bundle. The code repo's job is just to
> build, self-test, and publish the reusable tool (the composite action + the
> `ghcr.io/contentremixer/content-validate` image) via its
> `content-validate-tool` workflow.

> **Two repos, don't conflate them (ISI-7137):** the `@v1` composite-action ref
> below (`ContentRemixer/contentremixer/.github/actions/content-validate@v1`)
> always points at the operator/action **source** repo — it is the published
> tool, never a delivery target, so it is **not** parameterized. The repo this
> bundle is *installed into* is the configurable content repo, named once as the
> `$TARGET_REPO` install parameter (see `deploy/README.md`); it is wherever you
> run the copy-and-PR install below.

Everything here calls the **central** composite action published from
`ContentRemixer/contentremixer` — **zero duplication** of validation logic. The
content repo carries this thin workflow + the PR templates + the per-type
contract templates (shipped below).

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

Copy this bundle's `.github/` **and** `content/` (the per-type contract
templates the validator enforces) into the content repo root and open a PR:

```sh
# from a checkout of the content repo:
cp -r <this-repo>/deploy/reference-repo-bundle/.github/ .github/
cp -r <this-repo>/deploy/reference-repo-bundle/content/ content/   # type contracts
git checkout -b chore/install-content-ci
git add .github/workflows/content-ci.yml .github/PULL_REQUEST_TEMPLATE/ content/
git commit -m "ci: install ContentRemixer Content CI (C8 / ISI-7122)"
git push -u origin chore/install-content-ci
gh pr create --fill
```

The `content/<type>/meta.template.yaml` files declare each type's required
assets + fields; the validator reads them from the content repo at runtime.
Keep them if the content repo already defines its own — this bundle ships the
canonical set so a fresh content repo validates out of the box.

That is the whole install — the workflow just:

```yaml
- uses: ContentRemixer/contentremixer/.github/actions/content-validate@v1
```

### Versioning

Pin the action **and** the image to the same released major (`@v1` /
`:v1`) for a reproducible gate. Publishing a `vX` tag of
`ContentRemixer/contentremixer` ships a matching
`ghcr.io/contentremixer/content-validate:vX` image (see the source repo's
`content-validate-tool` workflow's image job). Until the first `v1` tag is cut,
change both pins to `@main` / `:latest`.

## ⚠️ Prerequisites (owner-gated install)

The board confirmed (ISI-7122) that the **content repo is a separate repo** from
the code repo (`ContentRemixer/contentremixer` is code-only). This bundle is
**install-ready**; the live install waits on:

1. **The DevRel content repo exists + is named.** The separate repo that hosts
   `content/` + `recycled/` and receives recycle PRs. **Owner:** board / CTO to
   create + name it.
2. **Push/PR access to it.** A GitHub App install or PAT that lets an agent (or
   a human) add `.github/` + `content/` and open the install PR. Tracked
   alongside the edge/token work in [ISI-7112](https://.../ISI-7112).
   **Owner:** board to grant; then CTO-side install.
3. **First `v1` tag cut** on `ContentRemixer/contentremixer` so `@v1` / `:v1`
   resolve. Until then use `@main` / `:latest` as noted above.

Once (1) + (2) land, installing is the copy-and-PR above — no further code work
on this bundle. The install itself is tracked as a follow-up issue.

## Files

- `.github/workflows/content-ci.yml` — the drop-in Content CI workflow (runs in
  the content repo, not the code repo).
- `.github/PULL_REQUEST_TEMPLATE/{blog,social,newsletter,default}.md` — the C9
  ([ISI-7119](https://.../ISI-7119)) type-aware content PR templates, kept in
  lock-step with the `pr-template` rule. Authors pick one via the GitHub
  `?template=blog.md` (etc.) query param; ContentRemixer-opened PRs pre-fill the
  matching type automatically.
- `content/<type>/meta.template.yaml` — the canonical per-type contract
  templates (blog, videos, social, podcasts, livestreams, newsletter) the
  validator enforces (`required-assets`, `asset-structure`, `pr-template`).
