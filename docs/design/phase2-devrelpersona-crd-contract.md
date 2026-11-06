# DevRelPersona CRD surface + ingestion hand-off contract (P2-A, ISI-7143)

Status: implemented in the operator (ISI-7143). This is the concrete,
code-backed contract the Phase-2 siblings build on. It is scoped to the CRD
surface and the status/condition state machine P2-A owns; the broader Phase-2
architecture (pgvector store, scraper internals, writer conditioning) lives in
`phase2-personas-rag-design.md` (Architect, authored in parallel). Where the
two overlap, the field/condition names here are authoritative because they are
what the shipped CRD and reconciler actually use.

Relationship to Phase 1: `DevRelPersona` is the first **owner-scoped** CRD.
It reuses the shared `OwnerRef` (`api/v1alpha1/common_types.go`) so the Booth
API ownership filter (`role==Admin OR resource.owner==subject`, design §6) is
uniform across every CRD. It follows the P1 `ModelProfile`/`ReferenceRepo`
operator pattern: field-level validation via the CRD OpenAPI schema, value
defaulting via `+kubebuilder:default`, and a **validating** admission webhook
for the cross-field invariants OpenAPI cannot express. There is no separate
mutating webhook, matching P1.

## CRD surface (`contentremixer.io/v1alpha1`, kind `DevRelPersona`)

```yaml
apiVersion: contentremixer.io/v1alpha1
kind: DevRelPersona
metadata:
  name: alice
  namespace: contentremixer
spec:
  owner:
    subject: "oidc|alice"          # REQUIRED (webhook-enforced non-empty)
  selfDescription: "pragmatic, example-first, dry humour"   # optional; P2-D may refine
  references:                       # optional; order-significant (listType=atomic)
    - url: "https://youtu.be/<id>"
      kind: youtube                 # enum: youtube | article
    - url: "https://example.com/post"
      kind: article
status:
  phase: Pending                    # Idle|Pending|Scraping|Embedding|Ready|Error
  exemplarCount: 0                  # owned by P2-C
  lastEmbeddedAt: null              # owned by P2-C
  observedReferenceHash: "<sha256>" # owned by P2-A (edge-trigger fingerprint)
  conditions: [...]
```

- `spec.owner.subject` **required** — the webhook rejects an empty owner
  (`field.Required`). Admin-ownership (empty subject) is NOT allowed for
  personas, unlike the Phase-1 CRDs.
- `spec.references[].kind` is the closed set `KnownReferenceKinds`
  (`youtube`, `article`). The CRD enum rejects unknown kinds; the webhook
  re-checks with a message naming the allowed kinds (defence in depth). Adding
  a kind = teach P2-B, extend the enum + `KnownReferenceKinds`, `make manifests`.
- `shortNames`: `persona`, `personas`.

## Status / condition state machine (the hand-off)

P2-A is an **edge-trigger only**. It accepts the spec and converts a change to
`spec.references` into enqueued work; it never scrapes or embeds.

| Phase       | Owner | Meaning |
|-------------|-------|---------|
| `Idle`      | P2-A  | owned persona with zero references; nothing to ingest |
| `Pending`   | P2-A  | references changed; scrape+embed enqueued |
| `Scraping`  | P2-B  | fetching exemplars |
| `Embedding` | P2-C  | chunking / embedding / storing |
| `Ready`     | P2-C  | style index built and queryable |
| `Error`     | P2-B/C| a downstream step failed |

Conditions:

- **`Accepted`** (P2-A) — `True` once the spec is admitted (owner present).
  `False`/`OwnerMissing` only if an owner-less object slips past admission.
- **`IngestionPending`** (raised by P2-A, cleared by P2-C) — `True`/`ReferencesChanged`
  when `spec.references` differs from `status.observedReferenceHash`; P2-C sets
  it `False`/`Embedded` once the new set is embedded. `False`/`NoReferences`
  when the list is empty.
- **`Ready`** (True owned by P2-C) — P2-A only ever forces it `False`/`IngestionPending`
  while a re-ingest is outstanding, so the Booth API never serves a stale index
  as current.

### Edge-trigger mechanics (why siblings won't fight P2-A)

P2-A writes `status.observedReferenceHash` = a SHA-256 of the (order-sensitive,
length-prefixed) reference list **in the same status update** that sets
`Pending`. On any later reconcile where the live `spec.references` hashes to the
same value, P2-A touches only the `Accepted` condition (idempotent) and leaves
`phase`, `IngestionPending`, `Ready`, `exemplarCount`, and `lastEmbeddedAt`
untouched. So once P2-B/P2-C advance the phase, a steady-state resync does not
revert their work. A genuine `spec.references` edit flips the hash and P2-A
re-enters `Pending`.

### Contract for siblings

- **P2-B (scraper, watch `DevRelPersona`):** act when `IngestionPending==True`.
  Move `phase` `Pending`→`Scraping`, fetch each `spec.references[]` per its
  `kind`, hand the raw exemplars to P2-C. Set `phase=Error` + an `Error`
  condition on failure; do not clear `IngestionPending`.
- **P2-C (embed/store):** `phase` `Scraping`→`Embedding`→`Ready`. On success set
  `status.exemplarCount`, `status.lastEmbeddedAt`, `Ready=True`, and
  `IngestionPending=False`/`Embedded`. These four fields are P2-C's exclusively;
  P2-A never writes them.
- **P2-D (writer):** read-only consumer of `spec.selfDescription` + the embedded
  index keyed by this CR; may PATCH `spec.selfDescription` (the refine loop),
  which does NOT change `observedReferenceHash` and therefore does not re-trigger
  ingestion.

## Implementation pointers

- Types: `app/api/v1alpha1/devrelpersona_types.go` (phase + condition constants,
  `KnownReferenceKinds`).
- Webhook: `app/internal/webhook/devrelpersona_webhook.go`.
- Reconciler: `app/internal/controller/devrelpersona_controller.go`
  (`referenceHash` is the edge-trigger fingerprint).
- CRD manifest: `app/config/crd/bases/contentremixer.io_devrelpersonas.yaml`
  (mirrored into `deploy/base/crds/` and `deploy/charts/content-operator/crds/`).
- RBAC + webhook registration: `deploy/base/operator/{rbac,webhook-configuration}.yaml`
  and the chart templates.
- Tests: `devrelpersona_controller_test.go` (envtest: accept/enqueue, inert
  resync, re-enqueue, idle, webhook reject) + `devrelpersona_webhook_test.go`.
