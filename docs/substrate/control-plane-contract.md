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

## 4. Image build — OWNER-GATED (the actual blocker)

A DevRel/DevOps/Architect agent on **this** control plane **cannot** build the
substrate image: the K8sAgentMesh repo holding the ISI-7417 prototype is not
reachable from this control plane's GitHub token, and the prototype is
in-memory (not a deployable service). The build is owned by the **Substrate /
K8sAgentMesh project owner + board/ProxOps**.

Whichever path the board picks (§6), the image MUST:

1. Expose the §3 HTTP contract (`:8080` by default — see manifests).
2. Replace the prototype's in-memory `SkeletonStore` with a **durable** store
   for actor records + execution snapshots (reuse the live Postgres in
   `contentremixer`, a dedicated table/schema; snapshot blobs as `bytea` or an
   object store). Snapshots survive control-plane pod restarts.
3. Honor ADR-4: snapshot = execution state only; **no** style/memory bytes.
4. Be multi-arch (linux/amd64 + arm64 — k8squad-test is mixed), **cosign**-signed,
   and pushed to a registry reachable by k8squad-test (same lane as the other
   images: `ghcr.io/contentremixer/…`, mirrored through the node harbor cache —
   see [[isi-7346]]/[[isi-7436]]).

The manifests (§5) reference a **placeholder** image tag; the k8squad-test
overlay pins the real digest once the owner publishes it.

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

Done when: image built+pushed (§4, owner), manifests apply (§5, done — pending
image), contract exposed (§3, done — pending server implementing it).

The only non-agent-executable item is the **image**. Two unblock paths, for the
board to choose (raised as a request_confirmation on ISI-7507):

- **(A) Owner path** — Substrate/K8sAgentMesh owner builds+pushes the image from
  the ISI-7417 prototype (or makes the K8sAgentMesh repo reachable from this
  control plane's token so a DevOps agent here can build it).
- **(B) In-repo path** — board authorizes building a **minimal** substrate
  control plane *in this repo* that satisfies §3 (durable store + the five
  lifecycle endpoints), decoupling the flip from the unreachable external repo.
  Larger build, but fully agent-executable here and under our CI/cosign lane.

Both paths deliver the **same** contract (§3) and deploy the **same** manifests
(§5), so these artifacts are robust to the decision.
