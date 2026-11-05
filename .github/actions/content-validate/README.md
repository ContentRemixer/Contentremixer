# `content-validate` composite action

Runs the ContentRemixer **Content CI** checks (ISI-7118, plan §3) over the
changed content folders of a reference repository and reports findings as inline
PR annotations plus a job-summary table.

It is a thin wrapper around the published validator image
(`ghcr.io/contentremixer/content-validate`), which runs the extensible rule
registry in [`app/internal/contentvalidate`](../../../app/internal/contentvalidate).
The CLI, the image, and this action are the **single source of truth** for the
checks — the ReferenceRepo install (C8, ISI-7120) `uses:` this same action, so
there is zero duplication of validation logic.

## Checks

| Rule | Severity | What it enforces |
| --- | --- | --- |
| `required-assets` | error | every asset declared by `content/<type>/meta.template.yaml` (`files.*`) exists |
| `asset-structure` | error / warning | `meta.yaml` parses; `title` is set; template-declared fields are populated |
| `content-quality` | error / warning | reuses the ISI-7082 fail-closed scrub (secrets / IP / internal hostnames block; brand-voice warns) |
| `seo` | warning | sane title length, a single H1, non-empty tags |
| `pr-template` | error | the PR body carries the C9 (ISI-7119) content-template fields + Sample-clearance section |

Fail-closed: any **error** finding fails the step. `fail-on: warning` also fails
on warnings.

## Usage

```yaml
# In a ReferenceRepo (or this repo):
jobs:
  content-ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 } # needed for changed-file diffing
      - uses: ContentRemixer/contentremixer/.github/actions/content-validate@v1
        with:
          # optional — all inputs have defaults:
          # image: ghcr.io/contentremixer/content-validate:v1
          # changed-files: "content/blog/foo/index.md content/blog/foo/meta.yaml"
          # all: "true"                 # validate every content folder
          # pr-body-file: pr-body.md    # enables the pr-template check
          # pr-content-type: blog       # force the PR template type
          # fail-on: error              # or "warning"
```

With no `changed-files` and `all: false`, the action diffs `content/**` against
the PR base ref (`GITHUB_BASE_REF`) and validates only what changed.

## Inputs

See [`action.yml`](./action.yml). Key ones: `image`, `repo-root`,
`changed-files`, `all`, `base-ref`, `pr-body-file`, `pr-content-type`,
`fail-on`.

## Versioning

Pin to a released major (`@v1`) or an exact tag (`@v1.2.0`). Each `v*` tag of
this repo ships a matching `ghcr.io/contentremixer/content-validate:v*` image;
set `image:` to the same tag you reference the action at when you want an exact
pin.

## Local use

The same checks run outside CI via the CLI:

```sh
contentremixer content validate --all                 # whole content/ tree
contentremixer content validate --changed content/blog/foo/index.md
contentremixer content validate --list-rules
```
