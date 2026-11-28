# ISI-7969 — Recycle → ContentDraft deposit (Part 2 / C2)

Builds on **C1** ([ISI-7968](/ISI/issues/ISI-7968), the ingest seam + `Content`
model). Changes the recycle pipeline **terminus**: instead of opening a GitHub
PR directly, a `recycle:*` run now **deposits a `ContentDraft` into `/content`**
for human review. The commit/PR logic is preserved for **C3** (approval → PR,
[ISI-7970](/ISI/issues/ISI-7970)).

## What changed

`app/internal/pipeline/pr/runner.go` — the `Runner` terminus. After
`analyze → write → check`, when a `Deposit` seam is wired:

1. **Deposit** the generated Mix as a `Content{Source: recycle}` with
   `Origin{repo, issue}` provenance and `TargetRepo/TargetPath` = the reference
   repo + `recycled/<slug>/index.md` (where C3's PR will publish it — the same
   slug the Assembler uses, so C3 and C2 agree on the destination).
2. **Annotate, don't block.** The Mastering scrubber still runs, but its
   `Violation`s (block *and* warn) are mapped to `ScrubFinding` annotations and
   back-filled onto the draft — they no longer fail the run closed. This is the
   fix for the **issue-#7 disappearance**: nothing is silently dropped; a human
   reviewer sees everything the scrubber caught, with the evidence already
   redacted (`Violation.Match`).
3. The draft lands `status=draft` (the seam default) and is reported back on the
   recycle issue with a link + findings summary. **No PR is opened.**

The pre-C2 assemble-a-PR path stays live as the **nil-`Deposit` fallback** (an
operator without a content DSN is byte-for-byte unchanged) and is the exact
commit/PR logic **C3 reuses on approval** — `Assembler` is preserved, not
deleted.

### Idempotency per origin

GitHub redelivers a recycle issue's webhook and fires both `opened` and
`labeled`, so the same origin (repo + issue) can drive the pipeline more than
once; a `recycle:*` re-label is also a deliberate retry. The pre-C2 path was
idempotent because the `Assembler` converged on one branch/PR per issue. The
deposit path preserves that: `Store.Deposit` is **idempotent per origin** for
recycled drafts — a repeat deposit for an origin that already has a draft
**refreshes** that draft's title/body/target in place (same row id) instead of
spawning a duplicate, and leaves `Status` untouched so a re-delivery can never
regress a draft a human already advanced to `in_review`/`approved`. Manual
drafts (nil origin) are never deduplicated.

## Key decision — deposit directly via the shared DSN, not a new HTTP route

The recycle pipeline runs in the **operator** process; the C1 Go seam
(`booth/internal/content.Store.Deposit`) lives in **Booth**, a separate module
the operator cannot import. The C1 doc anticipated "wrap Deposit in a thin
authenticated route" for out-of-process callers.

We took the **boring, lower-surface** path instead: the operator already holds
the Postgres credential (`PGVECTOR_DSN`) for the **same database** Booth serves
`booth.contents` from (`BOOTH_CONTENT_DSN`). So the operator opens its own
`app/content.Store` against that DSN and deposits directly — **no new HTTP
endpoint, no new auth channel, no new Secret**.

- `app/content/ingest.go` adds `Deposit`/`Backfill` to the **shared**
  `app/content.Store` (the raw, authz-less persistence). It is the app-module
  twin of Booth's seam: Booth wraps the same validate-and-insert with §6.3 +
  audit for the *session* path; the operator (a trusted first-party workstream)
  runs it directly for the *recycle* path. The input invariants (owner/title/
  source required) are kept in lockstep across the two.
- **Trade-off (ADR):** operator deposits do not pass through Booth's authz
  **audit** log. Acceptable — the writer is first-party and trusted, the run is
  observable via operator logs, and the row itself carries `source=recycle` +
  `origin` provenance. If a future out-of-process (non-first-party) depositor
  appears, the C1 HTTP-wrapper path is still the right answer for it.

## Owner / §6.3 visibility

Recycle runs are platform-owned (Phase-1 empty subject), but an empty owner is
invisible to everyone but an Admin **and** is rejected by the seam. So deposits
are stamped with a configured owner subject — `--recycle-draft-owner` /
`RECYCLE_DRAFT_OWNER`, default `system:recycle` (Admin-visible). Point it at a
DevRel subject to make recycle drafts show up in that DevRel's `/content`.

## Config (operator)

| Flag / env | Default | Effect |
|---|---|---|
| `--content-dsn` / `CONTENT_DSN` | `PGVECTOR_DSN` | DSN for `booth.contents`. Empty ⇒ terminus stays on the open-a-PR path. |
| `--recycle-draft-owner` / `RECYCLE_DRAFT_OWNER` | `system:recycle` | §6.3 owner stamped on deposited drafts. |

## Tests

- `app/internal/pipeline/pr/runner_deposit_test.go` — deposits a draft
  (source=recycle, origin, target) and opens no PR; scrub findings are recorded
  as annotations (not a block) and never re-leak the caught secret.
- `app/content/ingest_test.go` — the seam's input invariants (no-DB path).
