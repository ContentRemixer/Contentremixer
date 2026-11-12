# Style-recognition synthesis (D3, ISI-7358)

Status: **implemented** (backend stage + storage surface + guard). Implements
[ISI-7350](/ISI/issues/ISI-7350#document-plan) §5 *Style-recognition synthesis*
on the [D1 contract](isi-7356-devrel-model-contract.md) §4 (StyleProfile
Postgres row; `Validated` mirrors in the CRD as a condition). UI wiring is D6/D7;
the content-test + persona-validate endpoints are D4 (ISI-7359).

## 1. What D3 ships

| Piece | Where | Role |
|---|---|---|
| `style_profiles` DDL | `app/review/schema.go` | One row per (owner, persona): `traits`, `sample`, `validated`, `validated_at`, `source_count`, `synthesized_at`. Applied idempotently by `review.Open`, like the other review tables. |
| Store surface | `app/review/styleprofile.go` | `GetStyleProfile`, `UpsertStyleProfile` (re-synthesis resets `validated`), `ValidateStyleProfile` (the Postgres half of D4's validate action). |
| Synthesis input | `app/review/store.go` | `ListAcceptedExemplars` — accepted only, deterministic order, SQL-capped; `CountAcceptedSources` → `SourceCount`; owner+persona keys mandatory. |
| LLM stage | `app/internal/pipeline/style` | Prompt (4 named dimensions: tone, vocabulary tier, sentence rhythm, structure), one bounded LiteLLM completion, strict two-section parse, per-run truncation. Fail-closed: no exemplars → `ErrNoExemplars`; bad output → `ErrMalformed`. |
| Booth-facing factory | `app/review/style_synthesizer.go` | `NewSynthesizer(store, litellmBaseURL, apiKey, model, guard)` + `Synthesize(ctx, req)`. Same pattern as `review.NewCorpus`: Booth can't import `app/internal`, so the public glue lives in `app/review`. |

## 2. Flow (O6 → O7)

```
sources accepted (B6) → embeddings in persona corpus (ISI-7146)
        ↓
Booth route (D5/D7 wires) calls review.NewSynthesizer(...).Synthesize
        ↓ (§6.3 gate first: owner from session; persona authorized)
rate guard: last profile's synthesized_at + Guard.MinInterval
        ↓
ListAcceptedExemplars(owner, persona, cap)  — SQL, accepted status only
        ↓
style.Synthesize — ONE LiteLLM chat call, guard-capped payload
        ↓
Parse (## Style traits / ## Sample paragraph) → UpsertStyleProfile
        ↓
StyleProfile row (validated=false) → O7 review screen (D7) reads it;
operator recomputes CRD Validated condition → reason ProfileSynthesized
```

Owner-scoping: `app/review` performs no authorization (unchanged package
discipline); every key lookup takes the owner the Booth route stamped from the
verified session, and the exemplar/queries scope on (owner, persona).

## 3. Rate/cost guard

`style.Guard{MinInterval, MaxExemplars, MaxChars}` (zero value = defaults):

- **Rate**: a re-synthesis within `MinInterval` of the previous one returns
  `style.ErrRateLimited{RetryAfter}`. The stored profile's `synthesized_at` IS
  the guard state — no side table, no lock.
- **Cost**: `ListAcceptedExemplars` caps the fetch in SQL
  (`DefaultExemplarLimit` = 48 chunks); `style` truncates the prompt payload to
  `MaxChars` (default 24 000 ≈ 6k tokens) at rune boundaries. Defaults mirror
  `review.DefaultExemplarLimit`, so store fetch and prompt cap share one budget.

## 4. Error contract (for the Booth route, D5/D7)

| Error | Meaning | HTTP |
|---|---|---|
| `style.ErrNoExemplars` | persona has no accepted sources yet | 409 |
| `style.ErrRateLimited` | re-synthesis inside the guard interval | 429 (body carries RetryAfter) |
| `style.ErrMalformed` | gateway completion broke the section contract | 502 (Booth retries/re-runs) |
| `review.ErrNotFound` | no profile yet on read (O7 pre-synthesis) | 404 |

## 5. Decisions worth keeping

- **No embedding call at synthesis time.** The exemplars' TEXT is read straight
  from the review store; embeddings would cost tokens and add nothing — style
  traits want corpus-wide coverage, not seed-nearest chunks (which is what the
  write-time conditioner in `pipeline/persona` already does with retrieval).
  D3 therefore reuses the *corpus* + LiteLLM, not the embed/retrieve call path.
- **Fail-closed, not best-effort.** Unlike writer conditioning, a wrong profile
  a DevRel validates is a correctness bug, so empty corpora and malformed
  completions are errors, never degraded output.
- **Re-synthesis resets `validated`.** New output is unreviewed output; the
  DevRel re-validates (which also re-mirrors the CRD condition, D4's action).
- **`source_count` = distinct accepted sources** (not chunks): the reviewer
  confidence number in O7 ("derived from 3 videos and 2 articles"), and the
  staleness signal after re-ingestion.
- **Booth wiring left to D5/D7 deliberately.** Concurrent D-issues share one
  workspace; D3 ships the public seam (`review.NewSynthesizer`, store methods)
  so the route owner wires `POST …/style-profile/synthesize` + O7's read
  without touching `app/`.
