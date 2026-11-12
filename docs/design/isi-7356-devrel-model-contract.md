# DevRel self-service data-model contract (D1, ISI-7356)

Status: **implemented** (types + enum + manifests + lock-step tests). This is the
frozen data-model foundation the DevRel self-service onboarding children D2–D7
build on in parallel ([ISI-7350 plan](/ISI/issues/ISI-7350#document-plan) §4).
It extends the shipped `DevRelPersona` CRD contract
(`phase2-devrelpersona-crd-contract.md`) rather than replacing it; where the two
overlap, the field/condition names there remain authoritative for the Phase-2
ingestion state machine.

Scope of D1: **type/enum/manifest changes + this note**. The identity-link
*implementation* is handed to D5 (ISI-7360); the new reference-kind *fetchers*
to D2 (ISI-7357); style *synthesis* to D3 (ISI-7358). D1 freezes the contracts
those build against so they can proceed concurrently.

## 1. New reference kinds (lock-step)

`DevRelPersona.spec.references[].kind` gains three kinds alongside the existing
`youtube` and `article`:

| Kind | Source | v1 fetch behaviour (D2) |
|------|--------|-------------------------|
| `youtube` | YouTube video | caption track → transcript text *(exists)* |
| `article` | web article / **blog post** | readable article body *(exists)* |
| `github-repo` | GitHub tutorial/docs repo | **markdown files only** (board decision) |
| `teleprompter-script` | DevRel talk/teleprompter script doc | plain-text extraction |
| `talk-recording` | recorded talk | **captioned recording URL** → captions; raw-audio ASR deferred (board decision) |

A **blog** reference uses the existing `article` kind — there is deliberately no
separate `blog` kind.

**Single source of truth:** the `v1alpha1.ReferenceKind*` constants
(`app/api/v1alpha1/devrelpersona_types.go`). Adding a kind is a lock-step change
across exactly these surfaces:

1. `v1alpha1.KnownReferenceKinds` + the `ReferenceKind*` constants.
2. `PersonaReference.Kind` `+kubebuilder:validation:Enum` marker.
3. Scraper dispatch `byKind` (`app/internal/rag/scrape/scrape.go`) — the
   `Kind*` constants there alias the v1alpha1 constants, so they cannot drift.
4. `app/review.KnownSourceKinds` (string-mirrored; see §5).
5. Regenerated CRD manifests: `app/config/crd/bases` **+** `deploy/base/crds`
   **+** `deploy/charts/content-operator/crds` (all three must be copied in sync
   — regenerating fixed a pre-existing drift where the deploy copies were
   missing `spec.retrieval`).

The **validating webhook needs no edit** — it iterates `KnownReferenceKinds`, so
extending the slice extends admission automatically. That is the intended design
and is why the webhook is listed as a lock-step *surface* but requires no code
change.

Two mechanical guards replace tribal knowledge:

- `TestByKindCoversKnownKinds` (scrape) — asserts `byKind`'s key set equals
  `KnownReferenceKinds`, so a kind admitted by the CRD can never dead-end.
- `TestKnownSourceKindsMatchCRD` (review) — asserts `review.KnownSourceKinds`
  equals `v1alpha1.KnownReferenceKinds` as a set.

**D1→D2 boundary.** The three new kinds admit against the CRD *now*, but their
fetchers are D2. Until D2 lands them, the scraper registers them as fail-closed
placeholders (`notImplementedScraper`) that return
`reference kind %q is not yet implemented (tracked by ISI-7357)`. This is a
deliberate, actionable per-reference failure — not the misleading "unsupported
kind" path (which, now that the enum lists these, would name the kind as both
wanted and unsupported). D2 replaces each placeholder with the real scraper; the
lock-step test keeps the key set honest throughout.

## 2. `PrimaryContentKind`

New optional `spec.primaryContentKind` — the dominant **output medium** a DevRel
produces, captured at onboarding (plan O2): `blog | video | talk | tutorial`. It
is distinct from a reference `kind`: references are style *exemplar sources*;
`PrimaryContentKind` is what the persona *targets*. It conditions style synthesis
(D3) and the content test (D4). Unset = "unspecified"; the synthesizer falls back
to a medium-agnostic profile. Enforced by the CRD OpenAPI enum alone (a scalar
enum apiserver rejects with a clear message — no webhook re-check needed, unlike
the list-valued reference kinds).

## 3. Bio / extra-context box → reuse `SelfDescription`

The onboarding bio / extra-context box **reuses the existing
`spec.selfDescription`** field (board decision). No new field is added. The
field doc now records this reuse so a future reader does not add a duplicate.

## 4. Style-profile surface + `Validated` — storage decision

Plan §10 decision 1 = **synthesize an explicit style profile** (board-approved).
The profile is DevRel-reviewable: synthesized **traits** (voice/tone description,
markdown) + a generated **sample**, which the DevRel reviews, content-tests, and
**validates**.

**Decision: the style-profile content lives as a Postgres row
(`app/review.StyleProfile`), NOT in CRD status.** The CRD carries only a
`Validated` status condition.

Rationale (status subresource vs Postgres row):

- The profile is **expensive-to-regenerate synthesized content** (an LLM call)
  and **large free text**. etcd/CRD status is the wrong store for that — status
  should be cheap, reconstructible observed state. This is the same reason the
  scraped corpus and chunks already live in Postgres (`app/review`), not status.
- The DevRel review→content-test→validate flow is a **Booth API / Postgres**
  operation, mirroring the B6 scraper-review queue exactly. Keeping the profile
  next to the corpus it is synthesized from keeps one data domain in one store.
- The **`Validated` signal** is what the operator and the writer-conditioner
  (ISI-7147) must gate on declaratively, without a DB read. That is cheap and
  belongs in CRD status as a **condition** (`PersonaConditionValidated`).

So: `StyleProfile` (Postgres) is the **content source of truth**; the
`Validated` CRD condition is its **cheap declarative mirror**. The Booth validate
action writes both in one operation. `StyleProfile.Validated` ⇔ the
`Validated=True` condition.

`StyleProfile` fields (`app/review/types.go`): `PersonaName`, `OwnerSubject`,
`ReferenceRepo`, `Traits`, `Sample`, `Validated`, `ValidatedAt`, `SourceCount`,
`SynthesizedAt`, timestamps. One row per `(owner, persona)`. The table DDL and
the synthesis/validate write-paths are D3/D4/D7 — D1 freezes only the Go
contract so those build in parallel.

Condition reasons (`PersonaReason*`): `AwaitingSynthesis` (no profile yet),
`ProfileSynthesized` (profile exists, awaiting owner review — `Validated=False`),
`ProfileValidated` (owner accepted — `Validated=True`).

## 5. `app/review.KnownSourceKinds` — why string-duplicated

`app/review` is a **public** package shared across two modules (the `app`
scrape-job binary and the Booth module via `replace`). It stays free of the k8s
API types so Booth can import it without pulling in controller-runtime. Hence
`KnownSourceKinds` is a **string duplicate** of `KnownReferenceKinds`, not an
import of it. The duplication is safe because `TestKnownSourceKindsMatchCRD`
fails the build if the two ever diverge.

## 6. Identity-link contract (frozen here, implemented by D5)

**Problem.** Persona ownership keys on the OIDC subject (`sub`):
`DevRelPersona.spec.owner.subject`, enforced non-empty by the validating webhook.
But Booth `auth.User` (`booth/auth/store.go`) today is `{Username, BcryptHash,
Role}` — it has **no subject**. Self-service onboarding needs a stable
`user ↔ subject ↔ persona` link so a logged-in DevRel can create and own personas
keyed by *their* subject.

**Frozen contract (D5 implements — ISI-7360):**

1. **`auth.User` gains `Subject string`** — the stable identity a persona's
   `spec.owner.subject` is set to. It is the join key for "personas owned by the
   logged-in user": `persona.spec.owner.subject == user.Subject`.
2. **Subject derivation.** With real OIDC, `Subject` = the IdP `sub` claim. Until
   OIDC lands, Booth mints a **stable local subject** per user so self-service
   works today. Recommended form: `booth:local:<username>` (stable for the
   lifetime of the username; deterministic so a persona created in one session is
   still owned after a restart). D5 owns the exact minting + where it is stored
   (the `UserStore` row) and the backfill for existing users.
3. **Invariants D5 must hold:**
   - `Subject` is **non-empty and stable** for every user (required — a persona
     owner must resolve to exactly one user).
   - `Subject` is **unique** across users (two users must not share an owner
     key, or ownership scoping leaks personas between them).
   - Changing a user's `Username` must **not** change their `Subject` (personas
     must survive a rename). This is the main reason `Subject` is a distinct
     field rather than reusing `Username` as the owner key.
   - Deleting a user does **not** delete their personas (CRDs outlive the row);
     the orphan-persona policy is a D5/D7 decision, out of scope for D1.
4. **API scoping (unchanged).** The existing ownership predicate
   `role==Admin OR resource.owner==subject` (design §6) already keys on subject;
   once `auth.User.Subject` exists, the DevRel-scoped persona API (D5) resolves
   the caller's subject from their session and filters by it. No change to the
   predicate — only to how the caller's subject is obtained.

D1 intentionally does **not** modify `auth.User` or any Booth code: that is D5's
deliverable. This section is the contract D5 implements against.

## Lock-step checklist (for the next person who adds a reference kind)

- [ ] `ReferenceKind*` const + `KnownReferenceKinds` (`devrelpersona_types.go`)
- [ ] `PersonaReference.Kind` `+kubebuilder:validation:Enum` marker
- [ ] scraper `byKind` registration (real fetcher, not a placeholder)
- [ ] `review.KnownSourceKinds`
- [ ] `make manifests` → copy to `deploy/base/crds` + `deploy/charts/.../crds`
- [ ] `TestByKindCoversKnownKinds` + `TestKnownSourceKindsMatchCRD` green
