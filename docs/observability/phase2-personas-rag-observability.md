# Phase-2 Observability Strategy — DevRel Personas + RAG

**Issue:** [ISI-7156](/ISI/issues/ISI-7156) (Plan observability for Phase-2 personas+RAG)
**Owner:** O11y Engineer (Observability Agent) · **Status:** design
**Derives from:** [`phase2-personas-rag-design.md`](../design/phase2-personas-rag-design.md) (ISI-7144, frozen contract) and [`phase1-operator-design.md`](../design/phase1-operator-design.md) (ISI-7076)
**Audience:** implementers of P2-A/B/C/D/deploy (ISI-7143/7145/7146/7147/7148) and the Developer (Amelia) who wires the instrumentation stories spawned from this doc.

> **What this doc fixes.** The *observability contract* for everything Phase-2 adds: the metric instruments (names, types, units, attributes), the structured log events, the trace span tree, the OTel Collector pipeline shape, and the Dynatrace alerting/SLO definitions. It surfaces the three failure modes the issue calls out explicitly — **indexing failures**, **degraded conditioning**, and **Postgres availability** — as first-class, alertable signals. The Go type blocks and metric/attribute tables here are implement-against-this; prose is rationale.

---

## 0. Contents

1. Principles & scope
2. Semantic conventions this phase respects (and the one custom namespace it adds)
3. Cardinality policy — the single most important constraint
4. Metrics contract (per component)
5. Logging contract (structured events + PII policy)
6. Tracing contract (span tree + context propagation into the recycle run)
7. Sampling strategy
8. OTel Collector pipeline shape (k8Squad test)
9. Postgres / pgvector availability — how it is observed
10. Alerting & SLOs (Dynatrace, test-consumable KPIs)
11. Dynatrace artifacts (dashboard / notebook / SLOs) — downstream build
12. Instrumentation → owner map (handoff)
13. Open decisions / assumptions

---

## 1. Principles & scope

Three pillars in harmony: every RAG operation emits a **metric** (is it healthy, how fast, how often), a **structured log** (what exactly happened on this one occurrence, for forensics), and lives on a **trace span** (where in the reconcile/recycle flow it sat, and what it was correlated with). The win condition is that a DevRel's PR that silently fell back to style-only generation is explainable end-to-end: the recycle-run trace shows a `persona.condition` span with `degraded=true`, the span links to the reconcile trace that left the persona `Degraded`, and the `ReferenceFailed` log on that reconcile names the dead URL — all without ever logging chunk text.

**In scope** (the Phase-2 delta only; Phase-1 operator telemetry is unchanged and assumed present):

| Component | Source (design ref) |
|---|---|
| `DevRelPersona` reconciler — scrape/index phases, per-reference failures, re-index cadence | §2.3, §2.4 |
| `internal/rag/` pipeline — scrape → chunk → embed → store → retrieve | §3, §4 |
| Embedding calls through the LiteLLM gateway | §6 |
| `VectorStore` / pgvector — pool health, query latency, chunk counts per persona | §4, §7 |
| Owner-scoped retrieval — top-k hit rates, degradation / fallback-to-style-only | §4.1, §5.4 |
| Writer persona-conditioning hook — degraded conditioning events | §5 |
| pgvector StatefulSet availability on `k8Squad test` | §7 |

**Out of scope:** Phase-1 pipeline telemetry (analyzer/writer/checker/PR), access-plane/OIDC observability (deferred with #9 to Phase-3/Booth), and any UI telemetry (no UI in P2).

---

## 2. Semantic conventions respected

Semantic conventions are contracts — we do not invent attribute keys where OTel already has one.

- **GenAI (embeddings)** — the embed stage (§3.3) and retrieval seed (§5.3) are GenAI embedding operations through LiteLLM (OpenAI-compatible). Use OTel GenAI semconv:
  - `gen_ai.operation.name = "embeddings"`
  - `gen_ai.system = "openai"` (LiteLLM presents an OpenAI-compatible surface; `gen_ai.system` records the wire protocol, not the backing model host)
  - `gen_ai.request.model` / `gen_ai.response.model` = the LiteLLM alias (e.g. `embed`)
  - `gen_ai.usage.input_tokens` (embeddings have no output tokens)
  - metric `gen_ai.client.operation.duration` (histogram, s), `gen_ai.client.token.usage` (histogram)
  - `error.type` on failure (e.g. `429`, `timeout`, `dimension_mismatch`)
- **Database (pgvector)** — the `VectorStore` adapter is a Postgres client. Use OTel DB semconv:
  - `db.system.name = "postgresql"`, `db.namespace = "<dbname>"`, `db.collection.name = "persona_chunks"`
  - `db.operation.name` ∈ `{ query, upsert, delete, count }` (our logical ops, §4)
  - metric `db.client.operation.duration` (histogram, s), `db.client.connection.count` / `.pending_requests` / `.max` (pool, §9)
  - **`db.query.text` is span-only and parameterized** — never a metric attribute, never with literal owner subjects interpolated.
- **Kubernetes / controller-runtime** — the reconciler already runs under controller-runtime, which emits `controller_runtime_reconcile_*` (Prometheus). We keep those and add the domain metrics in §4. k8s resource attributes (`k8s.namespace.name`, `k8s.pod.name`, …) come from the `k8sattributesprocessor` (§8) — **k8s only; never `resourcedetectionprocessor` here** (we are in-cluster, not on a VM).

**Custom namespace.** Everything domain-specific that OTel has no convention for lives under a single prefix: **`contentremixer.persona.*`** and **`contentremixer.rag.*`**. This mirrors the Phase-1 custom metric prefix. Candidate upstream contribution of a `persona`/`rag` convention is tracked as an open item (§13); until then these are a documented custom convention (Weaver schema in §4.6).

---

## 3. Cardinality policy — read this first

Cardinality is the enemy. The tenancy keys `owner_subject` and `persona_name` are seductive metric labels and a dimensional-explosion trap the moment the DevRel count grows.

**Rules, enforced in the Collector (§8) as defence-in-depth, not just by convention:**

1. **`persona_name` and `owner_subject` MAY be metric attributes in Phase-2 only** because the DevRel count is tiny (single digits → low tens). They are capped: the Collector drops the per-persona dimension (aggregates to a catch-all `__other__`) above a configured series budget, and logs what it dropped (no silent truncation). At Phase-3 scale this flips to exemplars-only (below).
2. **`owner_subject` is an OIDC `sub`** — a pseudonymous identifier. It is acceptable as a low-cardinality label at P2 scale, but it is **never** placed in a log message body or a free-text field, and it is **hashed** (`sha256`, first 12 hex) anywhere it would otherwise be user-visible in a shared dashboard. The raw subject stays on spans/resource attrs scoped to the tenant.
3. **`source_url` is UNBOUNDED and is NEVER a metric attribute.** It lives on spans (`contentremixer.rag.source.url`), on structured logs, and as metric **exemplars** only. Same for `chunk.id`, `video_id`, and any per-document identifier.
4. **`db.query.text` / chunk text / embedding vectors are NEVER metric attributes** and never logged (text is also a PII surface, §5.4).
5. Any new attribute added later must declare its cardinality class (`bounded` | `bounded-at-p2` | `unbounded`) in the Weaver schema (§4.6); `unbounded` attributes are rejected from metric pipelines by a Collector `transform` rule.

Smell test for implementers: *"could this attribute's value set grow with traffic or corpus size?"* → if yes, it is span/log/exemplar-only.

---

## 4. Metrics contract

All instruments are OTel metrics exported via OTLP. Units follow UCUM (`s`, `1`, `By`). Histograms use explicit-bucket boundaries tuned per signal (below). Attribute values in `{…}` are the closed sets.

### 4.1 Reconciler (`DevRelPersona` controller) — P2-A

| Instrument | Type | Unit | Attributes | Surfaces |
|---|---|---|---|---|
| `contentremixer.persona.reconcile.duration` | histogram | s | `result{success,degraded,error}` , `persona` | reconcile cost / stalls |
| `contentremixer.persona.reconcile.total` | counter | 1 | `result{success,degraded,error}`, `persona` | reconcile outcomes |
| `contentremixer.persona.phase` | up-down counter (gauge-like) | 1 | `phase{Pending,Scraping,Indexing,Ready,Degraded}`, `persona` | **1 for current phase, 0 otherwise** → phase occupancy for alerting on "stuck in Degraded" |
| `contentremixer.persona.indexed_references` | observable gauge | 1 | `persona` | from `status.indexedReferences` (§2.2) |
| `contentremixer.persona.chunk_count` | observable gauge | 1 | `persona` | from `status.chunkCount` (§2.2) |
| `contentremixer.persona.reference.failed.total` | counter | 1 | `kind{youtube,article,blog,rss,transcript}`, `reason{network,parse,no_captions,empty,cap_exceeded}`, `persona` | **indexing failures** (§2.3 step 3). `source_url` is an exemplar, not a label |
| `contentremixer.persona.reindex.total` | counter | 1 | `trigger{spec_change,periodic_resync}`, `persona` | re-index cadence (§2.3) |
| `contentremixer.persona.last_indexed.age` | observable gauge | s | `persona` | now − `status.lastIndexedTime`; **staleness alert source** |

`persona.phase` as a per-phase 0/1 series is the cleanest way to alert on "persona X has been in `Degraded` for > N minutes" without scraping conditions — Dynatrace can `max()` the `Degraded` series over a window.

### 4.2 Scrape stage — P2-B

| Instrument | Type | Unit | Attributes | Surfaces |
|---|---|---|---|---|
| `contentremixer.rag.scrape.duration` | histogram | s | `kind`, `result{ok,error}` | per-reference fetch cost |
| `contentremixer.rag.scrape.docs` | histogram | 1 | `kind` | RawDocs produced per reference (fan-out) |
| `contentremixer.rag.scrape.dropped.total` | counter | 1 | `kind`, `reason{cap_exceeded}` | **honours the §3.1 per-reference cap (default 50) — no silent truncation** |
| `contentremixer.rag.scrape.errors.total` | counter | 1 | `kind`, `reason{network,parse,no_captions}` | feeds `reference.failed` |

Buckets (s): `[0.1, 0.5, 1, 2, 5, 10, 30, 60]` — scraping is network-bound and slow-tailed.

### 4.3 Chunk stage — P2-B

| Instrument | Type | Unit | Attributes | Surfaces |
|---|---|---|---|---|
| `contentremixer.rag.chunk.count` | histogram | 1 | `kind` | chunks per RawDoc (chunker health; a doc yielding 0 chunks is a bug signal) |
| `contentremixer.rag.chunk.size_tokens` | histogram | 1 | — | chunk size distribution vs the ~512 target (§3.2) |

### 4.4 Embed stage + LiteLLM — P2-C / §6

GenAI semconv (§2) carries the gateway call; we add the pipeline-level and the hard-contract signals:

| Instrument | Type | Unit | Attributes | Surfaces |
|---|---|---|---|---|
| `gen_ai.client.operation.duration` | histogram | s | `gen_ai.operation.name=embeddings`, `gen_ai.request.model`, `error.type` | embedding latency via LiteLLM |
| `gen_ai.client.token.usage` | histogram | 1 | `gen_ai.request.model`, `gen_ai.token.type=input` | embedding token spend |
| `contentremixer.rag.embed.batch.duration` | histogram | s | `result{ok,error}` | batch embed cost (many chunks/call) |
| `contentremixer.rag.embed.dimension_mismatch.total` | counter | 1 | `gen_ai.request.model`, `expected_dim` | **§6 hard contract: `len(embedding) != N` → page.** A misconfigured embedding model breaks all retrieval silently; this makes it loud |
| `contentremixer.rag.embed.gateway.errors.total` | counter | 1 | `status_class{4xx,429,5xx,timeout}`, `gen_ai.request.model` | LiteLLM gateway health from the embed path |

Buckets (s): `[0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10]`.

### 4.5 Store + retrieve (pgvector) — P2-C / §4

| Instrument | Type | Unit | Attributes | Surfaces |
|---|---|---|---|---|
| `db.client.operation.duration` | histogram | s | `db.operation.name{query,upsert,delete,count}`, `db.system.name=postgresql` | **retrieval query latency (the hot ANN path) + upsert cost** |
| `db.client.connection.count` | observable gauge | 1 | `state{used,idle}` | pgx pool occupancy (§9) |
| `db.client.connection.pending_requests` | observable gauge | 1 | — | pool back-pressure (acquire waiters) |
| `db.client.connection.max` | observable gauge | 1 | — | pool ceiling |
| `contentremixer.rag.store.up` | observable gauge | 1 | — | **1 if the operator's last pgvector health ping succeeded, else 0** — the authoritative "operator can reach the store" signal (§9) |
| `contentremixer.rag.store.errors.total` | counter | 1 | `db.operation.name`, `reason{conn,timeout,constraint,other}` | store failures feeding degradation |
| `contentremixer.rag.retrieve.matches` | histogram | 1 | `persona` | **top-k hit count returned vs requested** (TopK) — retrieval effectiveness |
| `contentremixer.rag.retrieve.top_score` | histogram | 1 | `persona` | top match cosine score distribution — **conditioning-quality signal** (scores trending low = corpus drift / poor seed) |
| `contentremixer.rag.retrieve.empty.total` | counter | 1 | `persona`, `reason{no_rows,below_minscore}` | **retrieval returned < 1 exemplar → degradation (§5.4)** |

Latency buckets for `db.client.operation.duration` (s): `[0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1]` — ANN retrieval should sit in low tens of ms at this corpus size; the long buckets catch HNSW pathologies / pool starvation.
Score buckets for `retrieve.top_score` (1): `[0, 0.3, 0.5, 0.6, 0.7, 0.8, 0.85, 0.9, 0.95, 1.0]`.

### 4.6 Writer conditioning — P2-D / §5

| Instrument | Type | Unit | Attributes | Surfaces |
|---|---|---|---|---|
| `contentremixer.persona.condition.total` | counter | 1 | `outcome{full,self_description_only,degraded,no_persona}`, `persona` | **the fallback ladder of §5.4 as a counter** — the single best "is conditioning healthy" signal |
| `contentremixer.persona.condition.duration` | histogram | s | `outcome` | conditioning overhead on the writer path (must stay small — RAG is additive, §5) |
| `contentremixer.persona.condition.degraded.total` | counter | 1 | `reason{store_unavailable,embed_failed,empty_retrieval,persona_not_ready}`, `persona` | **degraded conditioning, by root cause** — the issue's second headline failure mode |

`outcome` ladder maps 1:1 to §5.4: `full` = self-description + ≥1 exemplar; `self_description_only` = persona Ready but 0 exemplars passed MinScore; `degraded` = store/embed unavailable (style-only); `no_persona` = Phase-1 path (`PersonaName==""`). The ratio `degraded / (full+self_description_only+degraded)` over runs with a persona is the **conditioning-availability SLI** (§10).

### 4.7 Weaver schema

The custom `contentremixer.persona.*` / `contentremixer.rag.*` metrics + attributes (with cardinality class per §3) ship as a Weaver semantic-convention registry at `observability/weaver/registry/contentremixer-rag.yaml`, so the attribute contract is machine-validated (`weaver registry check`) and type-safe instrumentation stubs can be generated for the Go code. Building that registry is instrumentation story **O11y-1** (§12).

---

## 5. Logging contract

Structured JSON logs via controller-runtime's logr (zap backend), one event per occurrence, every event carrying `trace_id`/`span_id` (OTel log-trace correlation) and the low-cardinality scope keys.

### 5.1 Common fields on every RAG/persona log

`persona` (CR name), `owner_hash` (sha256/12 of owner.subject — **never the raw subject in the body**), `trace_id`, `span_id`, `reconcile_id` (or `run_id` on the write path).

### 5.2 Events (level, key fields)

| Event | Level | Key fields | Why |
|---|---|---|---|
| `persona.reconcile.start` / `.end` | INFO | `phase`, `generation`, `duration_ms`, `result` | reconcile envelope |
| `persona.reference.failed` | **WARN** | `kind`, `source_url`, `reason`, `error` | **per-reference indexing failure (§2.3-3) — the forensic record behind the `reference.failed` metric** |
| `persona.index.complete` | INFO | `indexed_references`, `chunk_count`, `duration_ms` | index outcome |
| `persona.scrape.cap_exceeded` | WARN | `kind`, `ref_url`, `fetched`, `cap`, `dropped` | no silent truncation (§3.1) |
| `rag.embed.dimension_mismatch` | **ERROR** | `model`, `expected_dim`, `got_dim` | **§6 hard-contract violation — indexing poisoned** |
| `rag.embed.gateway_error` | WARN/ERROR | `model`, `status`, `attempt`, `error` | LiteLLM failures |
| `rag.store.unavailable` | **ERROR** | `db.operation.name`, `error` | pgvector unreachable (§9) |
| `persona.condition.degraded` | **WARN** | `reason`, `run_id` | **fallback-to-style-only on the write path (§5.4)** |

### 5.3 PII policy (CRITICAL)

Scraped references are public DevRel content, but public ≠ PII-free (names, emails, handles appear in transcripts/articles).

- **Chunk text, embedding vectors, and `db.query.text` with literals are NEVER logged** — logs carry counts, URLs, scores, and IDs only. This also bounds log volume.
- A Collector-side `transform`/`redaction` processor scans log bodies and span attributes for PII patterns (email, phone, obvious token/secret shapes) as defence-in-depth and redacts before export. This is the standard PII detection & cleanup gate.
- `owner_subject` is pseudonymous but treated as sensitive: hashed in anything cross-tenant-visible (§3-2).

---

## 6. Tracing contract

Two traces, linked. Correlation is the whole point — a reader must be able to pivot from "this PR used style-only voice" to "because this reference 404'd three reconciles ago."

### 6.1 Index-time trace (reconcile)

```
span  DevRelPersona.Reconcile                      {persona, owner_hash, generation, phase.final}
 ├─ span rag.scrape            (per reference)      {kind, source.url, docs}              ← source.url is span attr, never metric
 │   └─ span http.get          (readability/yt)     {http.url, http.status_code}
 ├─ span rag.chunk                                  {docs, chunks}
 ├─ span rag.embed             (GenAI embeddings)   {gen_ai.*, chunks, dim}
 │   └─ span http.post litellm                      {http.status_code, gen_ai.request.model}
 └─ span rag.store.upsert      (DB)                 {db.operation.name=upsert, db.collection.name, chunks}
```

Per-reference failure sets `span.status=ERROR` + an exception event on the `rag.scrape` span only; the parent reconcile stays `OK` if ≥1 reference indexed (best-effort semantics of §2.3 must be legible in the trace, not flattened to a single error).

### 6.2 Query-time trace (writer conditioning) — linked into the recycle run

```
span  persona.condition                            {persona, owner_hash, outcome, degraded}
 ├─ span rag.retrieve.embed    (GenAI embeddings)   {gen_ai.*}   ← the retrieval seed (§5.3)
 └─ span rag.store.query       (DB)                 {db.operation.name=query, matches, top_score}
```

**`persona.condition` is a child of the existing Phase-1 recycle-run span** (the writer step). Context propagates via the `ctx` already threaded through `Writer.Write` → `PersonaConditioner.Condition`. Result: the recycle-run trace that produces a PR shows, inline, whether persona conditioning fired `full`, fell back to `self_description_only`, or `degraded` — closing the loop between the RAG subsystem and the Phase-1 content pipeline (three pillars in harmony across phase boundaries). No new propagation mechanism; it reuses the Go `context.Context` chain the design already mandates.

---

## 7. Sampling strategy

Sampling is a strategy, not a failure — but the failure modes must never be sampled away.

- **Tail-based sampling** at the Collector (`tail_sampling` processor), policies evaluated in order:
  1. `status_code == ERROR` → **keep 100%** (any reconcile/scrape/store/embed error).
  2. span attribute `degraded == true` OR `outcome == degraded` → **keep 100%** (every degraded conditioning event is kept).
  3. `rag.embed.dimension_mismatch` present → **keep 100%**.
  4. `db.client.operation.duration`-bearing span over latency threshold (e.g. query > 250 ms) → **keep 100%** (latency tail).
  5. everything else (happy path) → **probabilistic 10%** (tunable).
- Metrics are **never** sampled (they are aggregates); exemplars on histograms carry a trace_id into the kept-trace set so a p99 latency bar links to a real retained trace.
- At P2 traffic (a handful of personas, 24h resync, low recycle volume) the happy-path ratio can start at 100% and only drop if volume grows — documented, not silently capped.

---

## 8. OTel Collector pipeline shape (k8Squad test)

The operator exports OTLP (gRPC) to the in-cluster Collector; the Collector enriches, protects cardinality, and exports to Dynatrace. Critical ordering rules are non-negotiable.

```yaml
# collector pipeline (shape only; ProxOps owns the concrete overlay — ISI-7148 sibling)
processors:
  memory_limiter:            # MUST be FIRST — back-pressure before OOM
    check_interval: 1s
    limit_percentage: 80
    spike_limit_percentage: 25
  k8sattributes:             # k8s ONLY (we are in-cluster) — NEVER resourcedetection here
    extract:
      metadata: [k8s.namespace.name, k8s.pod.name, k8s.deployment.name, k8s.node.name]
  transform/cardinality:     # enforce §3 — drop unbounded attrs from METRIC pipeline
    metric_statements:
      - context: datapoint
        statements:
          - delete_key(attributes, "source_url")
          - delete_key(attributes, "db.query.text")
          - delete_key(attributes, "chunk.id")
  redaction/pii:             # §5.4 PII cleanup on logs + span attrs (defence-in-depth)
    allow_all_keys: true
    blocked_values: ["[\\w.%+-]+@[\\w.-]+\\.[A-Za-z]{2,}", "\\+?\\d[\\d ()-]{7,}\\d"]
  tail_sampling:             # §7 — traces pipeline only
    policies: [ {errors:100%}, {degraded:100%}, {dim_mismatch:100%}, {slow_query:100%}, {probabilistic:10%} ]
  batch:                     # MUST be LAST — amortise export
    timeout: 5s

receivers:
  otlp: { protocols: { grpc: {}, http: {} } }     # from the operator
  prometheus/pgvector: {}                          # scrapes postgres_exporter (§9)
  kubeletstats / k8s_cluster: {}                   # StatefulSet pod readiness (§9)

exporters:
  otlphttp/dynatrace: {}                           # OTLP → Dynatrace (dtctl-managed token)

service:
  pipelines:
    traces:  { receivers:[otlp], processors:[memory_limiter,k8sattributes,redaction/pii,tail_sampling,batch], exporters:[otlphttp/dynatrace] }
    metrics: { receivers:[otlp,prometheus/pgvector,k8s_cluster], processors:[memory_limiter,k8sattributes,transform/cardinality,batch], exporters:[otlphttp/dynatrace] }
    logs:    { receivers:[otlp], processors:[memory_limiter,k8sattributes,redaction/pii,batch], exporters:[otlphttp/dynatrace] }
```

- **`memory_limiter` first, `batch` last** in every pipeline — the two inviolable ordering rules.
- `transform/cardinality` sits in the **metrics** pipeline only (it must not strip `source_url` from traces, where it is wanted).
- `redaction/pii` sits in **traces + logs** (where free text can appear), not metrics.
- OCB note: the base contrib collector already carries `tail_sampling`, `transform`, `redaction`, `k8sattributes`, `prometheus`. **No custom OCB build is required for Phase-2** — if ProxOps already runs a custom distro, confirm these five components are in its manifest (a `validate-ocb-manifest` check); otherwise the contrib image suffices.

---

## 9. Postgres / pgvector availability — how it is observed

The issue names Postgres availability as a headline failure mode. It is observed from **three independent vantage points** so a single blind spot can't hide an outage:

1. **Operator vantage (authoritative for "can we actually use it").** The `VectorStore` pgx pool exposes `pgxpool.Stat()`; the adapter publishes it as the `db.client.connection.*` observable gauges (§4.5) and runs a lightweight `SELECT 1` health ping on a ticker, publishing `contentremixer.rag.store.up` (1/0). **This is the signal that matters** — Postgres can be `Running` in k8s yet unreachable by the operator (pool exhausted, auth expired, network policy). `rag.store.errors.total{reason=conn}` corroborates.
2. **Database vantage.** A `postgres_exporter` sidecar/Deployment scraped by the Collector `prometheus/pgvector` receiver provides `pg_up`, connection counts, `pg_stat_database_*`, and slow-query stats — the DB's own view (disk, locks, backends). ProxOps owns deploying the exporter (sibling to ISI-7148).
3. **Kubernetes vantage.** `k8s_cluster`/kube-state-metrics give StatefulSet `kube_statefulset_status_replicas_ready`, pod restart counts, and PVC bound state — the infra view (pod crashloop, PVC unbound, node pressure).

Alert (§10) fires on **1** (operator can't reach it) with **2/3** as diagnostic drill-downs. Chunk-count-per-persona (`contentremixer.persona.chunk_count`, §4.1) is the data-plane health check layered on top: a persona that was `Ready` with N chunks dropping to 0 is a corruption/wipe signal even while `pg_up=1`.

---

## 10. Alerting & SLOs

### 10.1 Alerts (Dynatrace Davis / metric-event)

| # | Alert | Condition (DQL/metric-event) | Severity | Maps to |
|---|---|---|---|---|
| A1 | **Persona indexing degraded** | `persona.phase{phase=Degraded} == 1` sustained > 15 min, OR `rate(persona.reference.failed.total) ` spike per persona | WARN→ERROR | indexing failures |
| A2 | **Persona corpus stale** | `persona.last_indexed.age > 2 × resyncInterval` (default 48h) | WARN | re-index cadence stall |
| A3 | **Degraded conditioning** | `rate(persona.condition.degraded.total) / rate(persona.condition.total{outcome!=no_persona}) > 5%` over 1h | WARN | fallback-to-style-only |
| A4 | **pgvector unavailable** | `rag.store.up == 0` for > 2 min (primary) ; corroborate `pg_up==0` / StatefulSet not ready | **CRITICAL (page)** | Postgres availability |
| A5 | **Embedding gateway failing** | `rate(rag.embed.gateway.errors.total) > threshold` OR `429` class sustained | ERROR | LiteLLM embed path |
| A6 | **Embedding dimension mismatch** | `rag.embed.dimension_mismatch.total > 0` (any) | **CRITICAL (page)** | §6 hard contract — retrieval poisoned |
| A7 | **Retrieval latency high** | `db.client.operation.duration{op=query} p95 > 250 ms` over 15 min | WARN | pool starvation / HNSW pathology |

### 10.2 SLOs (test-consumable KPIs — `define-slos` output)

Written so a test / synthetic can assert against them (consumed by the Testing Architect handoff).

```yaml
slos:
  - id: persona-indexing-success
    objective: 0.95        # ≥95% of reconciles of a Ready-eligible persona reach Ready (not Degraded)
    sli: sum(persona.reconcile.total{result=success}) / sum(persona.reconcile.total{result!=error_transient})
    window: 7d
  - id: conditioning-availability
    objective: 0.99        # ≥99% of persona-attributed runs condition without store/embed degradation
    sli: 1 - ( sum(persona.condition.degraded.total) / sum(persona.condition.total{outcome!=no_persona}) )
    window: 7d
  - id: retrieval-latency
    objective: 0.99        # ≥99% of owner-scoped queries < 200 ms
    sli: histogram_fraction(db.client.operation.duration{op=query}, lt=0.2)
    window: 1d
  - id: pgvector-availability
    objective: 0.999       # operator-observed store reachability
    sli: avg(rag.store.up)
    window: 30d
```

`conditioning-availability` deliberately excludes `self_description_only` from the error budget — degrading to self-description-only when the corpus is genuinely sparse is **correct behaviour** (§5.4), not an incident; only store/embed *unavailability* burns budget.

---

## 11. Dynatrace artifacts — downstream build

These are specified here but **built in a follow-up story with a live Dynatrace environment**, because dashboard creation MUST follow the `dt-app-dashboards` skill (7-step workflow, DQL validated against real sample output, deployed via `scripts/deploy_dashboard.sh`) and all DQL must be validated against the real data model before embedding in tiles — which requires the telemetry to actually be flowing (i.e. after O11y-2/O11y-3 land). Building them now would mean inventing field names blind, which the skill forbids.

- **Dashboard** (`build-project-dashboard` / `dt-app-dashboards`, 24-col grid): row 1 persona fleet health (phase occupancy, chunk counts, last-indexed age) · row 2 RAG pipeline (scrape/embed/store latency + error rates) · row 3 retrieval quality (top_score histogram, matches, degraded ratio) · row 4 pgvector (store.up, pool occupancy, query p95).
- **Diagnostic notebook** (`build-diagnostic-notebook`): "why did persona X degrade?" runbook — walk reference.failed → last reconcile trace → store.up.
- **SLOs**: the four in §10.2 as Dynatrace SLO entities.

Owner: O11y Engineer, as story **O11y-4**, blocked on telemetry flowing.

---

## 12. Instrumentation → owner map (handoff)

The *strategy* (this doc) is complete. Implementation is decomposed into stories that attach to the P2 code they instrument — they **cannot** land before the code exists, so each is blocked on its P2 sibling.

| Story | Work | Owner | Blocked on |
|---|---|---|---|
| **O11y-1** | Weaver registry for `contentremixer.persona.*`/`rag.*` (§4.7) + generated Go metric stubs; cardinality classes (§3) | Developer (Amelia) | — (can start now; pure schema) |
| **O11y-2** | Reconciler + RAG-stage instrumentation: metrics §4.1–4.6, logs §5, spans §6.1 | Developer (Amelia) | P2-A/B/C (ISI-7143/7145/7146) |
| **O11y-3** | Writer-conditioning instrumentation (§4.6, §6.2) incl. span linkage into the recycle run | Developer (Amelia) | P2-D (ISI-7147) |
| **O11y-4** | Collector pipeline overlay (§8) + postgres_exporter + Dynatrace dashboard/notebook/SLOs (§11) | ProxOps + O11y Engineer | P2-deploy (ISI-7148), O11y-2/3 |

Validation handoff to the **Testing Architect**: the §10.2 SLO KPIs are the test-consumable contract; `validate-traces` / `validate-telemetry-data` run against them once O11y-2..4 land on `k8Squad test`.

---

## 13. Open decisions / assumptions

1. **Per-persona metric dimension at scale (§3-1).** Phase-2 keeps `persona` as a metric label (tiny DevRel count). The flip point to exemplars-only is a Phase-3 decision when the DevRel count grows — the Collector series-budget cap makes the transition safe (drops to `__other__` + logs) but the exact budget is a deploy tunable.
2. **GenAI semconv version.** OTel GenAI conventions are still maturing; pin a specific semconv version in the Weaver registry and re-check on upgrade (`schema-diff`).
3. **postgres_exporter vs native `postgresql` receiver (§9).** The Collector has a native `postgresql` receiver that covers most `pg_stat_*`; exporter chosen for richer custom queries, but ProxOps may prefer the native receiver — either satisfies vantage-2. Non-blocking.
4. **Score-drift baseline (§4.5 `retrieve.top_score`).** "Scores trending low" needs a per-persona baseline to alert on; Phase-2 ships the histogram and a static-threshold alert (A-candidate), adaptive baselining deferred.
5. **Custom semconv upstreaming (§2).** Whether to propose a `persona`/`rag` convention upstream vs keep it vendored — vendored for P2.

---

*End of Phase-2 observability strategy. §4 (metrics), §5 (logs), §6 (spans), §8 (collector ordering), and §10 (alerts/SLOs) are the implement-against-this contracts; §12 is the handoff. This doc derives from the frozen `phase2-personas-rag-design.md` and does not change any Phase-2 code contract — it only observes it.*
