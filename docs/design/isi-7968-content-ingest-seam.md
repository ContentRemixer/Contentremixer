# ISI-7968 — ContentDraft model + ingest seam (Part 2 / C1)

Foundation for the content-flow redesign (parent **ISI-7887**, Part 2). Makes
`/content` the hub for **recycled + manual** content. C2/C3/C4/C5 build on this.

## The model: one table, extended — not a parallel one

There is **one** draft entity: `app/content.Content`, backed by the existing
`booth.contents` table. The redesign *extended* it rather than forking a new
`ContentDraft` table, because the original B3 document model was already
owner-scoped with the exact `draft → in_review → approved` lifecycle. This is
what keeps existing web-UI-authored docs working: every pre-redesign row is read
back as a `manual` draft with empty provenance/target/findings (column defaults).

`ContentDraft` is the *concept name*; the Go type is `Content`.

### Fields added by ISI-7968 (`app/content/types.go`)

| Field | JSON | Meaning | Set by |
|-------|------|---------|--------|
| `Source` | `source` | `manual` \| `recycle` (discriminator, immutable) | ingest seam |
| `Origin` | `origin` | `{repo, issue}` provenance (nil for manual) | C2 at deposit |
| `TargetRepo` / `TargetPath` | `targetRepo`/`targetPath` | where the draft publishes | ingest / PUT |
| `ScrubFindings` | `scrubFindings` | secret/PII annotations | **C2** back-fill |
| `PRURL` | `prUrl` | publishing PR | **C3** back-fill |

Status gains a terminal **`published`** state (`ContentPublished`), back-filled
by C3 once the PR is open/merged.

Migration: additive `ALTER TABLE … ADD COLUMN IF NOT EXISTS` in
`app/content/schema.go`, applied on every Booth startup (same idempotent
discipline as the rest of the schema). No data migration — defaults cover legacy
rows.

## The ingest seam — `booth/internal/content.Store`

**One internal entry point** for depositing + annotating drafts. It takes an
explicit owner/actor (not an `auth.Session`) because the callers run server-side
with no interactive session. Every call is still audited, under the system actor
`system:ingest`, and routes through the same validate-and-insert path the
interactive `CreateContent` uses.

### Deposit a new draft (C2 recycle, and manual authoring)

```go
draft, err := contentStore.Deposit(ctx, content.DepositRequest{
    Owner:      devrelSubject,            // required: the §6.3 tenant the draft belongs to
    Source:     appcontent.SourceRecycle, // or SourceManual
    Title:      "…",                      // required
    BodyMD:     generatedOrAuthoredBody,
    Origin:     &appcontent.Origin{Repo: "Org/Repo", Issue: 42}, // recycle provenance
    TargetRepo: "Org/Content",
    TargetPath: "posts/my-post.md",
})
```

- `Owner` must be non-empty (an empty owner is invisible to everyone but an
  Admin under §6.3) → `ErrBadRequest`.
- Manual web authoring does **not** call `Deposit` directly: it goes through
  `CreateContent(sess, …)`, which stamps `Owner = sess.Subject`,
  `Source = manual`, and delegates to the same path.

### Back-fill after creation (C2 scrub, C3 publish)

```go
// C2 — attach scrub findings:
contentStore.Backfill(ctx, draft.ID, content.BackfillRequest{
    ScrubFindings: findings, // non-nil replaces the stored slice
})

// C3 — record the PR and move to published:
pub := appcontent.ContentPublished
contentStore.Backfill(ctx, draft.ID, content.BackfillRequest{
    PRURL:  &prURL,
    Status: &pub,
})
```

`Backfill` fields are all optional; a nil field keeps the stored value. `Source`
and `Origin` are **not** patchable — provenance is stamped once at deposit.

## HTTP surface (`/v1/content`)

Unchanged routes (CRUD), now carrying the new fields in responses automatically
(they are struct fields with JSON tags). `PUT /v1/content/{id}` additionally
accepts `targetRepo`/`targetPath` for interactive editing. `scrubFindings` and
`prUrl` are **not** settable over HTTP — they are machine-populated via the
back-fill seam only.

If a future out-of-process workstream needs to deposit over HTTP, wrap `Deposit`
in a thin authenticated route; the Go seam is the single funnel either way.
