# Substrate control-plane contract & deployment spec (ISI-7507)

> **Status:** architecture artifact. Fixes the endpoint contract that the
> ISI-7397 `substrate-actor` Dispatcher strategy targets, and the deployment
> shape ProxOps applies under ISI-7431. The container **image** this contract
> describes is owner-gated (see §4) — these two artifacts (contract + manifests)
> exist so that whoever builds the image builds the *right* one, and so the
> apply step on ISI-7431 is trivial once an image exists.
>
> Parent chain: ISI-7507 → blocks ISI-7431 (provision) → blocks ISI-7397 (D5
> flip `media` template to a substrate actor) → ISI-7389 (substrate actors ADR).

## 1. Why this document exists

The media-actor flip (ISI-7397) replaces the `media` k8s-Job Strategy with a
`substrate-actor` Strategy behind the `app/internal/dispatch` seam. That
Strategy calls a **substrate control plane** to spawn / suspend / resume media
actors. Today:

- The Dispatcher seam exists (`app/internal/dispatch`, PR #76) with
  `ContentStrategy` (inline) and `MediaStrategy` (k8s-Job). **There is no
  `SubstrateStrategy` yet** — it slots in behind the existing `Strategy`
  interface and must call a concrete endpoint.
- ISI-7417 delivered only an **in-memory Go prototype** (`agentmesh/`,
  K8sAgentMesh#43) — not a deployable service, and that repo is **not reachable**
  from this control plane's GitHub token.
- kgateway **v2.1.3** on `k8squad-test` has native **agentgateway** support, so
  the reachability (data-plane) half is deployable on demand — but there is no
  backend for it to front.

So the single remaining gap is a **deployable substrate control-plane artifact**.
This document pins the contract that artifact must expose (§2–3), the manifests
that deploy it (§5, `deploy/base/substrate/`), and the build the owner must do
(§4).

## 2. Layering (ISI-7387 / ISI-7390 correction — do not collapse)

Three distinct layers; the contract keeps them separate:

| Layer | Concern | Owns |
|-------|---------|------|
| **Substrate** | *existence* | actor lifecycle: spawn, suspend (snapshot execution state), resume, status, terminate |
| **agentgateway** | *reachability* | the front door (Gateway + GatewayParameters) routing callers to the substrate control plane |
| **AgentMesh / style layer** | *identity + memory* | pgvector exemplars, style profiles, DevRelPersona — **external, shared, never snapshotted** |

**Hard split (ISI-7390 challenger correction):** suspend/resume is a *substrate*
op over **execution state only**. The AgentMesh **memory flush** is a **distinct
op** the actor performs against the external style layer — it is **not** part of
the substrate snapshot. ADR-4 ordering the `SubstrateStrategy` enforces:

```
flush-before-suspend:   actor flushes mesh memory → external layer, THEN POST /suspend
resume-reground:        POST /resume, THEN actor re-grounds from memoryRef (external layer)
snapshot ≠ memory:      the snapshot the substrate stores carries NO style/memory bytes
```

`memoryRef` travels through the contract as a **reference** (owner + persona) so a
resumed actor can re-ground itself; the substrate never reads or stores the
memory behind it.

## 3. Endpoint contract (HTTP/JSON)

Base URL = the agentgateway front door (§5). All bodies JSON; all 4xx/5xx carry
`{"error": "<message>"}`. The `SubstrateStrategy` is the only caller.

### 3.1 Spawn — `POST /v1/actors`

Maps 1:1 from `dispatch.WorkOrder`.

```jsonc
// request
{
  "actorTemplate": "media",           // WorkOrder.ActorTemplate
  "image": "ghcr.io/contentremixer/media-actor@sha256:…", // actor workload image
  "skill": "video-designer",          // WorkOrder.Skill (resolved)
  "workOrder": {
    "repo": "ContentRemixer/Contentremixer",
    "issueNumber": 42,
    "inputs": {                       // dispatch.Inputs (minus Token, see note)
      "sourcePath": "content/foo.md",
      "notes": "…",
      "inputFormat": "auto",
      "outputFormat": "demo"
      // PRTarget mirrored as prTarget{owner,repo,baseBranch,…}
    }
  },
  "memoryRef": { "owner": "…", "persona": "henrik-devrel" }, // reference only
  "workerSelector": { "node.kubernetes.io/instance-type": "media" } // optional
}
// response 201
{ "actorId": "act_01J…", "state": "running" }
```

**Token note:** the per-repo PAT (`Inputs.Token`) is a **secret** and MUST NOT
cross this HTTP boundary as a literal. The spawn body carries a
`tokenSecretRef{name,key}` (a Secret in the substrate namespace projected to the
actor pod), never the token value. The `SubstrateStrategy` resolves the Secret
ref the same way the Job launcher does today.

### 3.2 Status / wakeup probe — `GET /v1/actors/{actorId}`

```jsonc
{ "actorId": "act_01J…",
  "state": "running|suspended|succeeded|failed|unknown",
  "phase": "spawning|working|flushing|suspended|regrounding|done",
  "result": { "prUrl": "…" },   // present when state=succeeded
  "error": "…" }                // present when state=failed
```

Idempotent; the Strategy polls this as the wakeup probe (parity with the Job
path's completion watch).

### 3.3 Suspend — `POST /v1/actors/{actorId}/suspend`

Snapshots **execution state only**. Precondition (caller-enforced, §2):
the actor has already flushed mesh memory.

```jsonc
// response 200
{ "actorId": "act_01J…", "state": "suspended", "snapshotId": "snap_…" }
```

### 3.4 Resume — `POST /v1/actors/{actorId}/resume`

```jsonc
// request (re-ground source; substrate passes it to the actor, does not read it)
{ "memoryRef": { "owner": "…", "persona": "henrik-devrel" } }
// response 200
{ "actorId": "act_01J…", "state": "running" }
```

### 3.5 Terminate — `DELETE /v1/actors/{actorId}`

Reaps the actor and its snapshot. `204` on success, idempotent (`204` if already
gone).

### 3.6 Health — `GET /healthz`, `GET /readyz`

Plain `200`/`503` for k8s probes.

### Contract stability

This is fixed **jointly** with ISI-7397: when the `SubstrateStrategy` lands in
`app/internal/dispatch/substrate.go`, its client MUST match §3 verbatim. Any
change is a joint edit of this doc + that client + the substrate server. A
`dispatch`-side contract test (table of request/response fixtures) is the cheap
guard — add it with the Strategy.

## 4. Image build — IN-REPO (board fork option B, implemented)

The board accepted the §6 fork as **option B**: build a substrate control plane
*in this repo*, decoupling the media-actor flip from the unreachable external
K8sAgentMesh repo. It is implemented:

- **Service** — `app/cmd/substrate` (+ `app/internal/substrate`): a Go HTTP
  server exposing the §3 contract on `:8080`, backed by a **durable Postgres
  store** (`substrate_actors`, actor spec+status as JSONB; survives pod
  restarts, contract §4.2), running each actor as a Kubernetes **Job** of its
  template image via a controller-runtime client. Unit-tested
  (`internal/substrate/*_test.go`): spawn/persist, status refresh,
  suspend→resume lifecycle, idempotent terminate, validation.
- **Image** — `app/Dockerfile.substrate` (distroless/static:nonroot, UID 65532,
  matching the hardened Deployment securityContext), published multi-arch
  (amd64+arm64) and **cosign** keyless-signed as
  `ghcr.io/contentremixer/substrate-control-plane` by
  `.github/workflows/substrate-image-publish.yml` (mirrors scrape-job). A push
  to `main` emits the `sha-<12>` tag the overlay pins — reachable by
  k8squad-test over the node harbor mirror ([[isi-7346]]).

**Walking-skeleton scope (honest):** `suspend` tears the actor's Job down while
the store keeps the WorkOrder record; `resume` re-creates the Job (re-grounding
from the carried MemoryRef). This exercises the full §3 lifecycle end-to-end but
does **not** yet do a true CRIU/checkpoint snapshot of live execution state —
that is a v2 optimization (the payoff for fat/bursty media actors per the
ISI-7389 analysis). Memory is never snapshotted here (ADR-4): the actor flushes
and re-grounds itself against the external style layer.

## 5. Deployment manifests

Live in `deploy/base/substrate/` (kustomize base) with a k8squad-test overlay
patch for the image pin + agentgateway LB. They apply cleanly to the
`contentremixer` namespace and deploy: Deployment + Service + ServiceAccount for
the control plane, and the agentgateway `Gateway` + `GatewayParameters` front
door routing to it.

**Validation done:** YAML parses; kustomize build is self-consistent. NOT
dry-run-applied against a live API server from here (no cluster creds in this
run) — ProxOps validates on apply under ISI-7431. Two fields to **verify against
the live kgateway v2.1.3 CRDs** before apply (they are marked in the manifest):
the `GatewayParameters` apiVersion/group and the exact `spec.kube.agentgateway`
casing/shape.

## 6. Disposition & the board fork

Done when (all three met once this PR merges + the image publishes):

1. **Image built+pushed** — §4, done: `app/cmd/substrate` + `app/Dockerfile.substrate`
   + `substrate-image-publish.yml` publish+sign `ghcr.io/contentremixer/substrate-control-plane`
   on merge to `main`.
2. **Manifests apply** — §5, done: `deploy/base/substrate/` (kustomize-validated).
3. **Contract exposed** — §3, done: the `internal/substrate` HTTP server
   implements it (unit-tested).

The board accepted the fork as **option B** (in-repo build) on 2026-11-17
(`confirmation:ISI-7507:image-build-fork:v2`). Option A (external owner builds
from the K8sAgentMesh prototype) is retired.

**Next (ISI-7431):** merge this PR → a `main` build publishes the `sha-<12>`
image → ProxOps applies `deploy/base/substrate` to k8squad-test, pins the image
tag + the agentgateway LB in the overlay, binds the actor-spawn RBAC, and wires
the endpoint to the ISI-7397 SubstrateStrategy.
