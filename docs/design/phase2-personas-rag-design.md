# Phase-2 Technical Design — DevRel Personas + RAG Style-Learning

**Issue:** [ISI-7144](/ISI/issues/ISI-7144) (P2-design subtask of [ISI-7086](/ISI/issues/ISI-7086), Phase 2 of [ISI-7061](/ISI/issues/ISI-7061))
**Author:** Winston (Architect) · **Status:** design · **Derived from:** ISI-7086 plan rev 1 (board-accepted, confirmation `e2973353` → **#6 = pgvector**, **#9 = defer OIDC to P3**)
**Mirrors:** [`phase1-operator-design.md`](./phase1-operator-design.md) (ISI-7076). Read that first — this doc **extends** the Phase-1 operator, it does not replace it.
**Audience:** implementers of P2-A/B/C/D/deploy (ISI-7143, 7145, 7146, 7147, 7148). Every contract below is meant to be unambiguous enough to code against without re-deriving intent.

> **Scope guardrail.** Phase 2 adds (1) the `DevRelPersona` CRD, (2) a scrape→chunk→embed→store→retrieve RAG pipeline that learns each DevRel's voice from their public content, and (3) **owner-scoped** persona conditioning of the Phase-1 writer. Phase 2 ships **no UI and no IdP** — per board decision **#9, OIDC/login is deferred to Phase 3** (The Booth, ISI-7087). Ownership in Phase 2 is carried by `DevRelPersona.spec.owner.subject`, set by an Admin via GitOps, and is the *same* `OwnerRef` the Phase-1 design (§6) froze — so Phase 3 only has to wire a live session subject onto an already-enforced tenancy spine. The Phase-1 pipeline (analyzer → writer → checker → PR) and its CRDs (`ModelProfile`, `ReferenceRepo`) are unchanged except for the **additive** writer-conditioning hook in §5.

---

## 0. Contents

1. What Phase 2 adds to the Phase-1 operator (layout delta)
2. `DevRelPersona` CRD — surface + lifecycle (P2-A)
3. The RAG pipeline — scrape → chunk → embed → store → retrieve (P2-B/C)
4. `VectorStore` Go interface + pgvector schema (decision #6) + owner-scoped retrieval (P2-C)
5. Writer-conditioning contract — persona voice injection (P2-D)
6. Embeddings via LiteLLM + model/dimension contract
7. Postgres/pgvector deploy shape on `k8Squad test` (P2-deploy)
8. Ownership / tenancy in Phase 2 + #9 OIDC deferral
9. Secrets policy delta
10. Subtask → contract map (P2-A/B/C/D/deploy)
11. Open decisions / assumptions

---

## 1. What Phase 2 adds to the Phase-1 operator

Same Go module, same kubebuilder v4 project, same operator image and Deployment. Phase 2 is **additive**:

```
app/
  api/v1alpha1/
    devrelpersona_types.go       # §2 — new CRD (P2-A)
    common_types.go              # EXTENDED: OwnerRef gains a required-on-persona contract (§8); PersonaReference, RetrievalSpec
    zz_generated.deepcopy.go     # regenerated
  internal/
    controller/
      devrelpersona_controller.go  # §2.3 lifecycle: scrape→index→Ready (P2-A, drives P2-B/C)
    webhook/
      devrelpersona_webhook.go     # §2.4 validating webhook: owner required, references sane
    rag/                           # NEW subtree
      scrape/                      # §3 — P2-B: reference fetch (youtube transcript + article) → raw docs
      chunk/                       # §3 — P2-B: raw doc → []Chunk (deterministic splitter)
      embed/                       # §3/§6 — P2-C: []Chunk → embeddings via LiteLLM
      store/                       # §4 — P2-C: VectorStore interface + pgvector adapter
      retrieve/                    # §4/§5 — P2-C: owner-scoped top-k query
    pipeline/
      writer/                      # §5 — P2-D: persona-conditioning hook (additive; Phase-1 path unchanged)
deploy/
  postgres/                        # §7 — P2-deploy: pgvector StatefulSet + Service + Secret example (ProxOps)
```

**Decision — one new deployable (Postgres), not two.** Decision #6 chose **pgvector over a dedicated vector DB** (Qdrant/Weaviate). Rationale (carried from the ISI-7086 plan): the per-persona corpus is tiny (~10³–10⁴ chunks total across all DevRels); one stateful component is cheaper to run and reason about on `k8Squad test` than a second StatefulSet + its own backup story; per-persona isolation is a plain SQL `WHERE owner_subject = …` predicate (the §8 tenancy spine); and retrieval sits behind the `VectorStore` Go interface (§4) so a future Qdrant swap is a contained adapter change, not a pipeline rewrite. **The operator does not embed a vector DB client library beyond `pgx` + `pgvector-go`.**

**Decision — embeddings reuse the existing LiteLLM gateway (§6).** No second model path. The embedding model is just another `ModelProfile` (Phase-1 CRD) tagged `embedding`; the embed stage calls the gateway exactly like the writer calls it for completion. One gateway, one secrets story.

---

## 2. `DevRelPersona` CRD — surface + lifecycle (P2-A, ISI-7143)

Group/version: **`contentremixer.io/v1alpha1`** (same as Phase 1). Namespaced. One CR per DevRel persona. **Owner-REQUIRED** — this is the first owner-scoped CR (contrast `ModelProfile`/`ReferenceRepo`, which are Admin/platform-owned with an optional empty owner, Phase-1 §2.0).

### 2.0 Shared-type additions (`common_types.go`)

`OwnerRef` is reused verbatim from Phase 1 (`{ subject string }`). Phase 2 adds:

```go
// PersonaReference is one piece of a DevRel's public corpus the persona
// learns voice from. The scraper (P2-B) resolves URL by Kind.
type PersonaReference struct {
    // URL is the public source (a YouTube video/channel, an article, a blog
    // index, an RSS feed). Must be http(s).
    // +kubebuilder:validation:Pattern=`^https?://.+`
    URL string `json:"url"`

    // Kind selects the scraper strategy (§3). Closed set, extended the same
    // way as the Phase-1 format enums (add strategy, extend enum, make manifests).
    // +kubebuilder:validation:Enum=youtube;article;blog;rss;transcript
    Kind string `json:"kind"`
}

// RetrievalSpec tunes owner-scoped top-k retrieval for this persona (§4/§5).
type RetrievalSpec struct {
    // TopK is the number of voice exemplars injected into the writer prompt.
    // +kubebuilder:validation:Minimum=1
    // +kubebuilder:validation:Maximum=20
    // +kubebuilder:default=5
    // +optional
    TopK int `json:"topK,omitempty"`

    // MinScore drops exemplars below this cosine similarity (0..1). Default 0
    // keeps all top-k. Conditioning is best-effort: too few matches degrades
    // gracefully to style-only generation, it does not fail the run (§5).
    // +optional
    MinScore string `json:"minScore,omitempty"` // decimal string; parsed to float64
}
```

### 2.1 `DevRelPersona` spec (`devrelpersona_types.go`)

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=persona
// +kubebuilder:printcolumn:name="Owner",type=string,JSONPath=`.spec.owner.subject`
// +kubebuilder:printcolumn:name="Refs",type=integer,JSONPath=`.status.indexedReferences`
// +kubebuilder:printcolumn:name="Chunks",type=integer,JSONPath=`.status.chunkCount`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type DevRelPersona struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   DevRelPersonaSpec   `json:"spec,omitempty"`
    Status DevRelPersonaStatus `json:"status,omitempty"`
}

type DevRelPersonaSpec struct {
    // Owner is REQUIRED for a persona (unlike ModelProfile/ReferenceRepo).
    // owner.subject is the OIDC `sub` of the owning DevRel. In Phase 2 it is
    // set by an Admin via GitOps (no live login yet, #9 → P3); it is the
    // tenancy key for every chunk and every retrieval (§4/§8).
    // Required-and-non-empty is enforced by the validating webhook (§2.4),
    // since CRD OpenAPI cannot require a nested string.
    Owner OwnerRef `json:"owner"`

    // DisplayName is the human label for the persona (e.g. "Henrik - OTel/k8s").
    // +kubebuilder:validation:MinLength=1
    DisplayName string `json:"displayName"`

    // SelfDescription is the persona's own positioning/voice prose, authored by
    // the DevRel (or Admin). It is injected verbatim into the writer system
    // prompt as the "agent self-description" (§5) — the deterministic half of
    // conditioning that does not depend on retrieval.
    // +kubebuilder:validation:MinLength=1
    SelfDescription string `json:"selfDescription"`

    // References is the public corpus the persona learns voice from. The
    // controller drives scrape→chunk→embed→store over these (§2.3, §3).
    // +kubebuilder:validation:MinItems=1
    References []PersonaReference `json:"references"`

    // ModelName optionally pins the LiteLLM completion alias for this persona's
    // Mixes (else the run/default model wins, Phase-1 §4). Informational to the
    // writer; does not affect embedding (§6).
    // +optional
    ModelName string `json:"modelName,omitempty"`

    // Retrieval tunes top-k owner-scoped exemplar retrieval (§2.0, §5).
    // +optional
    Retrieval RetrievalSpec `json:"retrieval,omitempty"`
}
```

### 2.2 `DevRelPersona` status

```go
type DevRelPersonaStatus struct {
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty"`

    // Phase is the coarse lifecycle state (§2.3): Pending | Scraping |
    // Indexing | Ready | Degraded.
    // +optional
    Phase string `json:"phase,omitempty"`

    // IndexedReferences is how many spec.references have been successfully
    // scraped+indexed (<= len(spec.references)).
    // +optional
    IndexedReferences int `json:"indexedReferences,omitempty"`

    // ChunkCount is the number of chunks currently stored for this persona in
    // the VectorStore (owner-scoped count, §4).
    // +optional
    ChunkCount int `json:"chunkCount,omitempty"`

    // ObservedGeneration is the spec generation the status reflects, so a
    // spec edit re-triggers indexing (standard controller pattern).
    // +optional
    ObservedGeneration int64 `json:"observedGeneration,omitempty"`

    // LastIndexedTime is when the corpus was last (re)indexed.
    // +optional
    LastIndexedTime *metav1.Time `json:"lastIndexedTime,omitempty"`
}
```

### 2.3 Lifecycle (reconciler, P2-A drives P2-B/C)

The `DevRelPersona` controller is the orchestrator of the RAG pipeline (§3). On every reconcile of a persona CR:

1. **Validate** owner.subject non-empty (defence-in-depth; webhook is primary, §2.4). Empty → `Degraded`, condition `OwnerMissing`, no indexing.
2. **Short-circuit** if `status.observedGeneration == metadata.generation` and `Ready` — nothing changed, requeue on the periodic resync only.
3. **Phase = Scraping.** For each `spec.references[i]`, call the scraper (§3, P2-B) by `Kind`. Per-reference failure is isolated: it increments nothing, records a `ReferenceFailed` condition naming the URL, and the reconcile continues (best-effort; one dead link does not sink the persona).
4. **Phase = Indexing.** Chunk → embed → upsert into the `VectorStore` under `(owner.subject, persona-name)` (§4). The upsert is **replace-by-source**: all chunks for a given `source_url` are deleted then re-inserted, so re-indexing is idempotent and a removed reference's chunks are cleaned up (controller diffs stored `source_url`s against `spec.references`).
5. **Phase = Ready** once ≥1 reference indexed and `chunkCount > 0`; set `status.indexedReferences`, `chunkCount`, `lastIndexedTime`, `observedGeneration`, and condition `Ready=True`. Zero references indexed (all failed) → `Degraded`, `Ready=False`; the writer then falls back to style-only generation for this persona (§5) — it never blocks a run.
6. **Deletion** (finalizer `contentremixer.io/persona-chunks`): on delete, `VectorStore.DeletePersona(owner.subject, name)` purges all chunks before the finalizer is removed, so a deleted persona leaves no orphaned vectors (and no cross-tenant residue).

**Re-index cadence.** Reconcile on spec change (generation bump) + a periodic resync (default 24h, configurable) so new videos/articles behind the same reference URLs (e.g. a channel or RSS feed) are picked up. Heavy scraping is rate-limited per the scraper's own budget (§3); the controller does not hammer sources every resync if nothing changed (ETag/last-modified honoured where the source supports it — P2-B detail).

### 2.4 Validating webhook (`devrelpersona_webhook.go`)

OpenAPI covers enum/pattern/`MinItems`/`MinLength`. The webhook adds what OpenAPI cannot:

- **owner.subject required and non-empty** — reject CREATE/UPDATE with an empty `spec.owner.subject` (`field.Required(spec.owner.subject, …)`). This is the line that makes the persona owner-scoped from day one.
- **duplicate reference URLs** within `spec.references` → reject (naming the dup), so the corpus has no double-weighted source.
- **persona uniqueness per owner** is advisory: the controller sets a `DuplicateDisplayName` condition if two personas share `(owner.subject, displayName)`; not a hard admission reject (two CR names, one display label, is legal but flagged).

---

## 3. The RAG pipeline — scrape → chunk → embed → store → retrieve

Four Go stages under `internal/rag/`, pure data hand-offs, each independently testable with fixtures (same discipline as the Phase-1 pipeline, §4 of P1). The `DevRelPersona` controller (§2.3) is the only orchestrator; stages never import each other.

```
References[]  ──scrape──▶  RawDoc[]  ──chunk──▶  Chunk[](no embedding)  ──embed──▶  Chunk[](+embedding)  ──store──▶  pgvector
                (P2-B)                 (P2-B)                              (P2-C/§6)                        (P2-C/§4)

                                                       retrieve (query time, §4/§5): owner-scoped top-k  ──▶  writer (§5)
```

### 3.1 Scrape (P2-B, ISI-7145)

```go
// RawDoc is one fetched source document before chunking.
type RawDoc struct {
    SourceURL  string            // the resolved concrete URL (a channel ref may expand to many)
    SourceKind string            // echoes PersonaReference.Kind
    Title      string            // best-effort
    Text       string            // extracted plain text (transcript body / article body)
    FetchedAt  time.Time
    Meta       map[string]any    // kind-specific (e.g. video id, publish date)
}

type Scraper interface {
    // Scrape resolves one PersonaReference to zero-or-more RawDocs. A channel
    // or feed reference fans out to many RawDocs (one per video/post); a single
    // article/video resolves to one. Network/parse failure returns an error the
    // controller isolates per-reference (§2.3 step 3).
    Scrape(ctx context.Context, ref PersonaReference) ([]RawDoc, error)
}
```

Strategy by `Kind`:
- **youtube** — fetch the transcript (captions) for a video, or enumerate a channel's recent videos then transcript each. Phase-1 already anticipated a YouTube transcript fetch (P1 §10 note) which was stubbed; P2-B makes it real and reuses it. No video is transcribed by ASR in P2 — captions only; caption-less videos are skipped with a `ReferenceFailed` note.
- **article** / **blog** — fetch the page, extract the main content (readability-style extraction), strip nav/boilerplate. `blog` fans out over an index/listing page to its posts; `article` is a single page.
- **rss** — parse the feed, fan out to item links, then treat each as `article`.
- **transcript** — the URL is already plain-text/VTT; fetch and normalize.

**ponytail:** readability extraction uses one existing Go library (e.g. `go-readability`) rather than a bespoke DOM parser. Scraping is bounded: a per-reference cap on fanned-out docs (default 50, configurable) with a `log` of what was dropped — no silent unbounded crawl.

### 3.2 Chunk (P2-B, ISI-7145)

```go
// Chunk is one retrievable/embeddable unit. PersonaOwner + PersonaName are the
// tenancy keys (§4/§8); Embedding is filled by the embed stage (§6).
type Chunk struct {
    ID           string    // deterministic: sha256(owner|persona|source_url|ordinal) — idempotent upsert
    PersonaOwner string    // owner.subject (tenancy key)
    PersonaName  string    // the CR metadata.name
    SourceURL    string
    SourceKind   string
    Ordinal      int       // 0-based position within the source doc
    Text         string
    Embedding    []float32 // nil until the embed stage fills it
    Metadata     map[string]any
}

type Chunker interface {
    // Chunk splits one RawDoc into ordered chunks. Deterministic given the same
    // input (so re-indexing yields stable IDs). Target ~512 tokens with ~64-token
    // overlap, split on sentence/paragraph boundaries; never mid-sentence.
    Chunk(doc RawDoc, owner, persona string) []Chunk
}
```

Chunking is deterministic and side-effect-free. The ~512/64 target is a starting heuristic (`ponytail:` tunable, not load-bearing); the contract is *stable ordered chunks with deterministic IDs* so the §2.3 replace-by-source upsert stays idempotent.

### 3.3 Embed (P2-C, ISI-7146) — see §6 for the model/dimension contract.

### 3.4 Store + retrieve (P2-C, ISI-7146) — see §4.

---

## 4. `VectorStore` interface + pgvector schema + owner-scoped retrieval (P2-C, ISI-7146)

The single seam decision #6 lives behind. **All** storage/retrieval goes through this Go interface; the pgvector adapter is the only Phase-2 implementation, and a future Qdrant swap replaces only the adapter.

```go
// Query is an owner-scoped nearest-neighbour search. OwnerSubject is MANDATORY
// and non-empty — a VectorStore implementation MUST reject an empty OwnerSubject
// rather than scan across tenants (§8 tenancy invariant). PersonaName scopes
// further to one persona.
type Query struct {
    OwnerSubject string    // tenancy scope — REQUIRED, non-empty (fail closed if empty)
    PersonaName  string    // persona scope — REQUIRED
    Embedding    []float32 // the query vector (the writer's retrieval seed, §5)
    TopK         int       // max matches
    MinScore     float64   // drop matches below this cosine similarity (0 = keep all top-k)
}

// Match is one retrieved chunk with its similarity score (cosine, 0..1).
type Match struct {
    Chunk
    Score float64
}

// VectorStore is the storage seam for persona voice chunks (decision #6:
// pgvector). Owner scoping is enforced in EVERY method — there is no unscoped
// read or delete.
type VectorStore interface {
    // Upsert replaces-by-source: for each distinct (OwnerSubject, PersonaName,
    // SourceURL) in chunks, existing rows are deleted then the new chunks
    // inserted (idempotent re-index, §2.3 step 4). All chunks carry a non-empty
    // PersonaOwner; an empty one is a programming error and is rejected.
    Upsert(ctx context.Context, chunks []Chunk) error

    // Query returns the TopK nearest chunks WITHIN (OwnerSubject, PersonaName)
    // only. The owner predicate is applied in SQL, never in Go after a broad
    // scan. Empty OwnerSubject => error (fail closed), never a cross-tenant read.
    Query(ctx context.Context, q Query) ([]Match, error)

    // DeletePersona purges all chunks for one persona (finalizer path, §2.3
    // step 6). Scoped to (ownerSubject, personaName).
    DeletePersona(ctx context.Context, ownerSubject, personaName string) error

    // CountPersona returns the stored chunk count for status.chunkCount (§2.2).
    CountPersona(ctx context.Context, ownerSubject, personaName string) (int, error)
}
```

### 4.1 pgvector schema

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS persona_chunks (
    id             TEXT         PRIMARY KEY,          -- Chunk.ID (deterministic sha256)
    owner_subject  TEXT         NOT NULL,             -- tenancy key (§8)
    persona_name   TEXT         NOT NULL,
    source_url     TEXT         NOT NULL,
    source_kind    TEXT         NOT NULL,
    ordinal        INT          NOT NULL,
    text           TEXT         NOT NULL,
    embedding      vector(1536) NOT NULL,             -- dim = embedding model's dim (§6); 1536 = text-embedding-3-small default
    metadata       JSONB        NOT NULL DEFAULT '{}',
    indexed_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Tenancy + replace-by-source lookups (the hot path for Upsert/Delete/Count).
CREATE INDEX IF NOT EXISTS persona_chunks_owner_persona_idx
    ON persona_chunks (owner_subject, persona_name);
CREATE INDEX IF NOT EXISTS persona_chunks_owner_persona_source_idx
    ON persona_chunks (owner_subject, persona_name, source_url);

-- ANN index for cosine similarity retrieval.
CREATE INDEX IF NOT EXISTS persona_chunks_embedding_idx
    ON persona_chunks USING hnsw (embedding vector_cosine_ops);
```

**The owner-scoped retrieval query (frozen):**

```sql
SELECT id, owner_subject, persona_name, source_url, source_kind, ordinal, text, metadata,
       1 - (embedding <=> $3) AS score            -- cosine similarity
FROM persona_chunks
WHERE owner_subject = $1                            -- tenancy predicate — ALWAYS present
  AND persona_name  = $2
  AND 1 - (embedding <=> $3) >= $5                  -- MinScore
ORDER BY embedding <=> $3                            -- cosine distance ASC = most similar first
LIMIT $4;                                            -- TopK
```

`$1 = OwnerSubject`, `$2 = PersonaName`, `$3 = query embedding`, `$4 = TopK`, `$5 = MinScore`. **The `owner_subject = $1` clause is non-negotiable and appears in every read.** This is the whole reason #6 chose pgvector: isolation is a WHERE clause, not a separate collection-management story. The adapter rejects an empty `$1` before building SQL (fail closed).

### 4.2 Connection + migrations

- Driver: `pgx/v5` + `pgvector-go` for `vector` codec. One connection pool in the operator, DSN from a Secret (`SecretKeyRef`, §9).
- The schema above ships as an **idempotent migration** the operator applies on startup (`CREATE … IF NOT EXISTS`, `CREATE EXTENSION IF NOT EXISTS`). No separate migration tool/deployable. `ponytail:` single forward-only migration file for P2; a versioned migration tool is a P3 concern if the schema churns.
- **HNSW `m`/`ef_construction` and the `embedding` dimension are the only two knobs that need a rebuild to change** — both are deploy-time constants (§6, §7), flagged in §11.

---

## 5. Writer-conditioning contract (P2-D, ISI-7147)

**Additive to the Phase-1 writer (`internal/pipeline/writer`), not a rewrite.** The Phase-1 `Writer.Write(ctx, rc, s)` already builds `systemPrompt(format, style)` + `userPrompt(rc, s)` and is unchanged for platform-owned (persona-less) runs. Phase 2 injects persona voice when, and only when, a run is owned by a persona.

### 5.1 The conditioning seam

A new optional collaborator on the writer, injected like `Generate` and `FS` (nil = Phase-1 behaviour):

```go
// PersonaConditioner supplies the persona voice overlay for a run. Nil on the
// Writer => Phase-1 behaviour (style-only). Implemented by internal/rag/retrieve
// backed by a VectorStore; tests inject stubs.
type PersonaConditioner interface {
    // Condition returns the persona overlay for a run, or an empty overlay
    // (not an error) when the run has no persona or retrieval finds nothing.
    // BEST-EFFORT: a retrieval/store error degrades to style-only (logged,
    // overlay.Degraded=true) and MUST NOT fail the writer — a recycle run never
    // blocks on RAG (§2.3 step 5).
    Condition(ctx context.Context, rc pipeline.RunContext, s pipeline.Sample) (PersonaOverlay, error)
}

type PersonaOverlay struct {
    SelfDescription string      // DevRelPersona.spec.selfDescription (deterministic half)
    Exemplars       []Exemplar  // top-k owner-scoped voice chunks (retrieval half)
    Degraded        bool        // true when retrieval was unavailable/empty
}

type Exemplar struct {
    Text      string
    SourceURL string
    Score     float64
}
```

### 5.2 How the run carries the persona

`pipeline.RunContext` already carries `Owner OwnerRef` (Phase-1 §4, present in code today). Phase 2 adds **one field** — the persona CR name — so the conditioner can scope retrieval to exactly one persona:

```go
// added to pipeline.RunContext:
//   // PersonaName is the DevRelPersona CR scoping this run's voice
//   // conditioning (§5). Empty => platform-owned, no persona (Phase-1 path).
//   PersonaName string
```

`Owner.Subject` + `PersonaName` are exactly the `VectorStore.Query` tenancy keys (§4). The webhook/orchestrator (Phase-1 §3 consumer 1) sets both when a recycle run is attributed to a persona; how a run gets attributed to a persona (a label, an issue field, a default persona on the `ReferenceRepo`) is a **wiring decision deferred to the P2-D/P3 integration** — this doc fixes only that the writer consumes `(Owner.Subject, PersonaName)` and that empty = Phase-1.

### 5.3 Retrieval → prompt injection (frozen shape)

When `PersonaConditioner != nil` and `rc.PersonaName != ""`:

1. The conditioner embeds a **retrieval seed** (the `Sample` body + `rc.Notes`, truncated) via the same embedding model as indexing (§6) and runs the §4.1 owner-scoped query with the persona's `RetrievalSpec` (TopK/MinScore).
2. The overlay is woven into the prompt **in addition to** the style guide, never replacing it:
   - **System prompt** gains a `--- persona self-description ---` block (`overlay.SelfDescription`) after the style guide: *"Write as this DevRel. Their voice: …"*. This is the deterministic anchor and applies even when retrieval is empty/degraded.
   - **User prompt** gains a `--- voice exemplars (do not copy facts, match tone/register) ---` block listing `overlay.Exemplars[].Text`. Explicit instruction: exemplars are **tone references only** — the no-invented-facts and reuse-the-source rules from the Phase-1 system prompt still bind; exemplars must not leak as content.
3. **Order of precedence** (unambiguous for implementers): source substance (from `Sample`) > style guide (`styles/<format>.md`) > persona self-description > voice exemplars. The checker/Mastering gate (Phase-1 §4, fail-closed) runs **unchanged** on the result — persona conditioning never bypasses the scrub.

### 5.4 Degradation (never block a run)

- No persona (`PersonaName == ""`): Phase-1 output, byte-for-byte.
- Persona Ready but retrieval returns `< 1` exemplar (sparse corpus, high MinScore): inject self-description only; `Degraded=true`; run proceeds.
- VectorStore/embedding unavailable: `Degraded=true`, style-only generation, logged; run proceeds and the PR is still opened. **RAG is an enhancement, not a gate.**

---

## 6. Embeddings via LiteLLM + model/dimension contract

- **One gateway.** The embed stage (§3.3) and the retrieval seed (§5.3) call the **same LiteLLM gateway** the writer uses for completion (Phase-1 subtask C). Embeddings are a `ModelProfile` with `tags: ["embedding"]` and a distinct `litellmName` (e.g. `embed`). The embed stage requests that alias; keys come from the profile's `apiKeyRef` Secret (Phase-1 §8) — no new secret path.
- **Dimension is a hard contract.** The pgvector `embedding vector(N)` column (§4.1) `N` **must equal** the embedding model's output dimension. Default `N = 1536` (OpenAI `text-embedding-3-small`). Changing the embedding model to a different dimension is a **schema migration + full re-index**, not a hot swap — called out in §11. The embed stage asserts `len(embedding) == N` on every vector and fails the index (not the run) on mismatch, so a misconfigured model is caught at index time with a clear error, not as silent garbage retrieval.
- **Query/index symmetry.** The retrieval seed MUST be embedded with the *same* model/alias as the stored chunks, else cosine similarity is meaningless. The conditioner reads the alias from the same config the indexer used (a single `embedding` alias constant resolved from the embedding `ModelProfile`), guaranteeing symmetry.

---

## 7. Postgres/pgvector deploy shape on `k8Squad test` (P2-deploy, ISI-7148 — ProxOps)

One new stateful workload in the `contentremixer` namespace. ProxOps (I) owns the concrete overlay; this doc fixes the component set and the operator's contract with it.

```
Namespace: contentremixer   (same as Phase 1)
  StatefulSet  pgvector            # image: pgvector/pgvector:pg16 (Postgres 16 + vector ext preinstalled)
    - 1 replica (P2: no HA — ponytail: single-instance, tiny corpus; HA is a P3 concern)
    - PersistentVolumeClaim (RWO) for /var/lib/postgresql/data — survives pod restarts
    - resources: modest (corpus is ~10³–10⁴ chunks); requests/limits are I's to finalise
    - readiness: pg_isready; liveness: pg_isready
  Service      pgvector            # cluster-internal :5432 (headless or ClusterIP; no Ingress — never public)
  Secret       pgvector-credentials# POSTGRES_PASSWORD + the operator's DSN key (SecretKeyRef, §9)
  (schema)                         # applied by the operator on startup (§4.2), not by an init Job
```

- **Not public.** Postgres is cluster-internal only — no Ingress, no NodePort. The only clients are the operator pods.
- **Operator ↔ Postgres.** The operator reads its DSN from `pgvector-credentials` via `SecretKeyRef` (identical mechanism to the Phase-1 GitHub PAT / model keys). It opens one `pgx` pool and applies the idempotent migration (§4.2) on boot.
- **Backup/restore** of the PVC is ProxOps's call (the corpus is fully re-derivable by re-indexing from `spec.references`, so loss is recoverable but slow — a periodic `pg_dump` is nice-to-have, not required for P2). Flagged in §11.
- **Secrets mechanism** (plain `Secret` vs sealed/External) is the same open decision #4 from Phase-1 §8 — unaffected by this doc; `SecretKeyRef` is identical either way.

---

## 8. Ownership / tenancy in Phase 2 + #9 OIDC deferral

Phase 2 is where the Phase-1 §6 ownership *spec* gets its first real teeth — on the data plane, not yet a login plane.

### 8.1 The tenancy invariant (frozen)
Every persona chunk is stamped with `owner_subject` (= `DevRelPersona.spec.owner.subject`). **Every** `VectorStore` read/delete is scoped by `owner_subject` in SQL (§4). There is no API path that retrieves or counts chunks across owners. A persona's voice can never leak into another persona's Mix, because retrieval physically cannot cross the `owner_subject` predicate. This is the data-plane realization of the Phase-1 §6.3 rule (`resource.owner.subject == session.subject`), with the subject sourced from the CR spec rather than a session (because there is no session yet).

### 8.2 #9 — OIDC/login deferred to Phase 3 (board-accepted)
Per board decision #9 (`e2973353`): **no OIDC gate, no login, no Booth UI in Phase 2.** Rationale (from the ISI-7086 plan, upheld): Phase 2 has no interactive surface to protect — personas are created by Admins via GitOps, and k8s RBAC + the CRD `spec.owner` already govern who can write a persona. A standalone P2 auth gate would protect nothing (YAGNI). The `owner.subject` is set by the Admin authoring the CR; Phase 3 (The Booth, ISI-7087) is where a **live OIDC session subject** replaces "the subject the Admin typed," and where the Phase-1 §6.3/§6.4 Booth API contract (read/write predicate, audit log) is finally *enforced* against end users. Phase 2 changes nothing about that frozen contract — it just populates the column it will one day filter on.

**Net:** Phase 2 enforces tenancy on the **data plane** (retrieval isolation, §4/§8.1) and defers tenancy on the **access plane** (who may create/edit a persona as an authenticated human) to Phase 3. That split is deliberate and board-approved.

---

## 9. Secrets policy delta

Unchanged Phase-1 policy (P1 §8): no secret value committed or inlined; everything is a k8s `Secret` referenced by `SecretKeyRef`. Phase-2 additions:
- **Postgres DSN / password** → `pgvector-credentials` Secret, referenced by the operator via `SecretKeyRef`. `deploy/postgres/*.example.yaml` documents the keys with placeholders only.
- **Embedding model API key** → just another `ModelProfile.apiKeyRef` (Phase-1 mechanism), no new surface.
- CI secret-scan (gitleaks) already covers the whole repo including `deploy/postgres/` and `internal/rag/`; the DSN example must use placeholder credentials so it does not trip the scanner.

---

## 10. Subtask → contract map

| Subtask | What it builds | Binds to this doc |
|---|---|---|
| **P2-A** ISI-7143 — DevRelPersona CRD + reconciler | `devrelpersona_types.go`, controller lifecycle, validating webhook, `common_types.go` additions | §2 (Go types are the spec), §2.3 lifecycle, §2.4 webhook; orchestrates §3 |
| **P2-B** ISI-7145 — reference scraper + chunker | `internal/rag/scrape` + `internal/rag/chunk`: `Scraper`/`Chunker`, youtube/article/blog/rss/transcript strategies | §3.1 Scraper, §3.2 Chunker |
| **P2-C** ISI-7146 — embedding + pgvector store/retrieve | `internal/rag/embed` + `internal/rag/store` + `internal/rag/retrieve`: `VectorStore` + pgvector adapter + owner-scoped query | §4 (interface + schema + frozen query), §6 embeddings |
| **P2-D** ISI-7147 — writer persona conditioning | `PersonaConditioner`, `PersonaOverlay`, writer hook, `RunContext.PersonaName` | §5 (seam + injection shape + degradation) |
| **P2-deploy** ISI-7148 — Postgres/pgvector on `k8Squad test` (ProxOps) | `deploy/postgres/` StatefulSet + Service + Secret example | §7, §4.2 migration contract, §9 |

Access-plane authz (OIDC/login/Booth) is **not** a Phase-2 subtask (decision #9 → Phase 3, §8.2).

---

## 11. Open decisions / assumptions

Non-blocking for P2-A (the CR + interface contracts are stable regardless), but flagged:

1. **Embedding model + dimension (§6)** — default `text-embedding-3-small` / `vector(1536)`. If the board/ProxOps prefers a self-hosted embedding model on the homelab LiteLLM endpoint, the dimension changes → the §4.1 column + HNSW index change → full re-index. Pick once before P2-C ships; it is the one choice that is expensive to reverse.
2. **Run → persona attribution (§5.2)** — how a recycle run is tagged to a persona (issue label, issue-form field, or a `defaultPersona` on `ReferenceRepo`) is deferred to P2-D/P3 wiring. The writer contract (`Owner.Subject` + `PersonaName`, empty = Phase-1) is stable regardless.
3. **Chunking parameters (§3.2)** — ~512-token chunks / ~64 overlap is a starting heuristic, tunable without contract change.
4. **HNSW index params (§4.1)** — `m`/`ef_construction` defaults; only matter at corpus scale we are well under. Changing them needs a REINDEX (deploy-time, flagged).
5. **Postgres HA + backup (§7)** — P2 is single-replica + PVC; corpus is re-derivable by re-indexing, so a `pg_dump` cron is nice-to-have, not required. HA → P3 if persona retrieval becomes load-bearing on the write path.
6. **Re-index cadence (§2.3)** — default 24h resync for channel/feed references; tune per source politeness limits in P2-B.
7. **YouTube captions only (§3.1)** — no ASR fallback in P2; caption-less videos are skipped (logged). ASR is a later enhancement if corpora prove too sparse.

---

*End of Phase-2 design. The Go type blocks in §2/§3/§4/§5 and the pgvector schema + frozen owner-scoped query in §4.1 are the authoritative, implement-against-this contracts; prose elsewhere is rationale. This doc extends `phase1-operator-design.md`; where the two overlap, Phase-1 remains the base and Phase-2 is strictly additive.*
