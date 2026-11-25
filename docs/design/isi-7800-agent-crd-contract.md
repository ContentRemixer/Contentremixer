# ISI-7800 — `Agent` + `AgentTeam` CRD contract + skill-kind registry (P0)

Status: **DRAFT — awaiting CEO design sign-off** · Author: Winston (Architect) ·
Parent: [ISI-7778](/ISI/issues/ISI-7778) (plan approved; scope P0–P3, P4 deferred).

This is the **data-model lock** for the admin-portal "manage agents & teams" feature.
It defines two admin-only CRDs, the fixed skill-kind registry (the one code/config
line of §7), the fail-closed webhook validation rules, and the seed set that
reproduces today's hardcoded `orchestrator.DefaultEngine()` DAG as data. **No engine
or UI code is in scope here** — only the contract downstream issues build against:

- ISI-7801 — UX spec (reads this contract)
- ISI-7802 — P1 read-only Admin tabs
- ISI-7803 — P2 CRUD
- ISI-7804 — P3 engine loader (consumes the AgentTeam DAG instead of `DefaultEngine()`)

---

## 1. Design principles (why this shape)

1. **Data vs. code boundary (§7).** The *only* thing that needs new Go is a new
   **skill kind** (a new specialist behaviour). Everything else — which role runs,
   which model, which skill-packs, how specialists hand off — is **data** in these
   two CRDs. A new team topology, a new role label, a model swap, a prompt-pack
   change = edit a CR, no deploy.
2. **Reuse the Phase-1 CRD machinery.** Both CRDs are admin-only `ResourceKind`s
   registered in `booth/internal/store/registry.go`. That one registration buys
   CRUD + §6.3 gating + audit for free (the registry comment already promises this),
   and one line in `server.go:92`'s kind slice wires the HTTP routes. No new authz
   path, no new store code — same pattern `ModelProfile`/`ReferenceRepo` use today.
3. **Fail-closed, twice (defence in depth).** Unknown `agentRef`, a cycle, or an
   unknown edge endpoint is a **hard error at admission** *and* a **hard error in the
   engine loader**. A malformed team can never be persisted; even if one slips in
   (direct etcd write, webhook disabled), the engine refuses it and **no partial
   run ever emits a PR** — mirroring the frozen §4 "Check fails ⇒ no PR" stance and
   the `orchestrator.Orchestrate` fail-closed topo-sort already on `main`.
4. **Reproduce today exactly before changing anything.** The seed set (§6) encodes
   the current `DefaultEngine()` fan-out (architect → {research, coding,
   observability, graphic}) as one `AgentTeam`. P3's loader, fed the seed, must
   produce a byte-identical Mix to today's hardcoded path. Only then do we let
   admins edit it.

---

## 2. `Agent` CRD (`contentremixer.io/v1alpha1`, admin-only)

One `Agent` CR = one configured specialist: a role label, the Go skill kind that
implements its behaviour, its model, its prompt skill-packs, and which actor
template runs it.

### 2.1 Draft Go type (`app/api/v1alpha1/agent_types.go`)

```go
// SkillKind selects the registered Go skill implementation an Agent runs. It is
// a CLOSED set: the one place adding a value requires new Go (§7 registry). Keep
// this enum in lock-step with orchestrator.SkillKindRegistry and the CRD manifests.
type SkillKind string

const (
	SkillKindArchitect     SkillKind = "architect"
	SkillKindResearch      SkillKind = "research"
	SkillKindCoding        SkillKind = "coding"
	SkillKindObservability SkillKind = "observability"
	SkillKindGraphic       SkillKind = "graphic"
	SkillKindWriter        SkillKind = "writer" // the Phase-1 linear content writer
)

// KnownSkillKinds is the closed set for AgentSpec.SkillKind. Enforced by the CRD
// OpenAPI enum (scalar — apiserver rejects with a clear message) AND re-checked by
// the Agent webhook against the live orchestrator.SkillKindRegistry so a manifest
// that drifts ahead of the compiled registry fails closed.
var KnownSkillKinds = []SkillKind{
	SkillKindArchitect, SkillKindResearch, SkillKindCoding,
	SkillKindObservability, SkillKindGraphic, SkillKindWriter,
}

// ActorTemplate selects which actor runtime executes the skill: a content actor
// (prose/code/text, the default) or a media actor (image/video, ISI-7395).
type ActorTemplate string

const (
	ActorTemplateContent ActorTemplate = "content"
	ActorTemplateMedia   ActorTemplate = "media"
)

// AgentSpec defines a configured specialist. Admin-owned (platform-wide); Owner
// is defined for registry uniformity but is empty/"platform" in Phase 1.
type AgentSpec struct {
	// Role is the human-facing label, e.g. "System Architect". Required.
	// +kubebuilder:validation:MinLength=1
	Role string `json:"role"`

	// Description is free-text operator context. Optional.
	// +optional
	Description string `json:"description,omitempty"`

	// SkillKind selects the Go behaviour (closed set — see KnownSkillKinds).
	// +kubebuilder:validation:Enum=architect;research;coding;observability;graphic;writer
	SkillKind SkillKind `json:"skillKind"`

	// SkillPacks are named markdown prompt packs layered on top of the skill's
	// base prompt (the writer already loads a "skill pack" — writer.go:300). Each
	// name must resolve to a pack the actor can load; unknown packs degrade (warn),
	// they do not fail admission. Optional.
	// +optional
	// +listType=atomic
	SkillPacks []string `json:"skillPacks,omitempty"`

	// ModelProfile is the litellmName of the ModelProfile this agent requests.
	// Optional — empty or "default" resolves to the system-default ModelProfile.
	// A non-empty, non-"default" value MUST name an existing ModelProfile (webhook
	// hard error otherwise — fail closed, same stance as the engine).
	// +optional
	ModelProfile string `json:"modelProfile,omitempty"`

	// ActorTemplate selects the actor runtime. Defaults to "content".
	// +kubebuilder:validation:Enum=content;media
	// +kubebuilder:default=content
	// +optional
	ActorTemplate ActorTemplate `json:"actorTemplate,omitempty"`

	// Default marks THE canonical Agent for its SkillKind — the fallback a team
	// node may reference by kind, and the row seeded to reproduce today. AT MOST
	// ONE Agent per SkillKind may set this true (webhook, §4). Zero is allowed.
	// +optional
	Default bool `json:"default,omitempty"`

	// Owner — platform-owned (empty) in Phase 1; present for registry uniformity.
	// +optional
	Owner OwnerRef `json:"owner,omitempty"`
}

type AgentStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" listType:"map"`
}
```

Markers: `+kubebuilder:object:root=true`, `+kubebuilder:subresource:status`,
`+kubebuilder:resource:shortName=agent`, print columns `Role`, `SkillKind`,
`Model` (`.spec.modelProfile`), `Default`.

---

## 3. `AgentTeam` CRD (`contentremixer.io/v1alpha1`, admin-only)

One `AgentTeam` = one orchestration DAG: an entry node, a set of nodes each
bound to an `Agent`, and the hand-off edges. This is the data form of the
hardcoded `DefaultEngine()` wiring on `main`
(`booth/internal/orchestrator/orchestrator.go:139`).

### 3.1 Draft Go type (`app/api/v1alpha1/agentteam_types.go`)

```go
// AgentNode binds a stable node id in the DAG to a specific Agent CR.
type AgentNode struct {
	// ID is the node identifier, unique within the team. Referenced by Entry and
	// by Edges. Also the orchestrator anchor-owner id.
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`

	// AgentRef is the metadata.name of the Agent this node runs. MUST resolve to
	// an existing Agent (webhook + engine hard error otherwise — fail closed).
	// +kubebuilder:validation:MinLength=1
	AgentRef string `json:"agentRef"`
}

// AgentEdge is a directed hand-off: the From node must complete before To runs.
type AgentEdge struct {
	// +kubebuilder:validation:MinLength=1
	From string `json:"from"`
	// +kubebuilder:validation:MinLength=1
	To string `json:"to"`
}

type AgentTeamSpec struct {
	// Entry is the node id that starts the run (the planner/architect). MUST be a
	// declared node id (webhook). The engine runs it first; its outline is adopted
	// by downstream nodes (mirrors orchestrator: architect produces the Outline).
	// +kubebuilder:validation:MinLength=1
	Entry string `json:"entry"`

	// Nodes are the specialists in this team. Node ids must be unique.
	// +kubebuilder:validation:MinItems=1
	// +listType=atomic
	Nodes []AgentNode `json:"nodes"`

	// Edges are the directed hand-offs. Together with Nodes they MUST form a DAG
	// rooted at Entry: no cycles, every endpoint a declared node, every non-entry
	// node reachable from Entry (webhook + engine, §4). Empty edges = a single-node
	// team (the linear-writer case).
	// +optional
	// +listType=atomic
	Edges []AgentEdge `json:"edges,omitempty"`

	// Default marks the team the engine uses when a run names no team. AT MOST ONE
	// AgentTeam may set this true (webhook). Zero is allowed (degraded; engine fails
	// closed if a run arrives with no resolvable team).
	// +optional
	Default bool `json:"default,omitempty"`

	// +optional
	Owner OwnerRef `json:"owner,omitempty"`
}

type AgentTeamStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" listType:"map"`
}
```

Markers: root, status subresource, `shortName=agentteam;team`, print columns
`Entry`, `Nodes` (`.spec.nodes[*].id` count via a status field or JSONPath),
`Default`.

---

## 4. Webhook validation rules (fail-closed)

One `admission.CustomValidator` per CRD in `app/internal/webhook/` (same shape as
`modelprofile_webhook.go` / `referencerepo_webhook.go`). `failurePolicy=fail`,
`verbs=create;update`. OpenAPI enums (`skillKind`, `actorTemplate`) are enforced by
the apiserver before the webhook runs; the webhook covers what OpenAPI cannot.

### 4.1 `Agent` webhook (`agent_webhook.go`)

| # | Rule | Error kind | Cross-object? |
|---|------|-----------|---------------|
| A1 | `skillKind` ∈ live `orchestrator.SkillKindRegistry` (re-check beyond the OpenAPI enum, so a manifest ahead of the compiled binary fails closed) | `Invalid` | no (reads compiled registry) |
| A2 | `modelProfile` empty or `"default"` → OK; else a `ModelProfile` with that `litellmName` must exist in the namespace | `Invalid` | **yes** (List ModelProfiles) |
| A3 | at most one `Agent` with the same `skillKind` may have `default=true` | `Invalid` | **yes** (List Agents) |

`skillPacks` entries are **not** admission-gated (unknown pack degrades at write
time, per the existing writer behaviour) — listed here so the omission is deliberate.

### 4.2 `AgentTeam` webhook (`agentteam_webhook.go`) — the critical one

| # | Rule | Error kind | Cross-object? |
|---|------|-----------|---------------|
| T1 | node ids unique | `Duplicate` | no |
| T2 | `entry` is a declared node id | `Invalid`/`NotFound` | no |
| T3 | every `agentRef` resolves to an existing `Agent` CR | `Invalid` | **yes** (List Agents) |
| T4 | every edge `from`/`to` is a declared node id (no unknown dep) | `Invalid` | no |
| T5 | no self-edge (`from == to`) | `Invalid` | no |
| T6 | the (nodes, edges) graph is **acyclic** (Kahn topo-sort; residual in-degree ⇒ cycle) | `Invalid` | no |
| T7 | every non-entry node is **reachable from `entry`** (BFS/DFS from entry; unreached ⇒ orphan) | `Invalid` | no |
| T8 | at most one `AgentTeam` with `default=true` | `Invalid` | **yes** (List AgentTeams) |

All rejections aggregate into one `apierrors.NewInvalid(...)` with a
`field.ErrorList`, exactly like the ModelProfile webhook, so the Admin UI shows every
problem at once.

### 4.3 Engine-side mirror (defence in depth, implemented in ISI-7804)

The P3 engine loader resolves an `AgentTeam` into `[]orchestrator.Skill` and re-runs
T2–T7 + A1/A2 before executing. `orchestrator.Orchestrate` already hard-errors on
unknown deps and cycles in its `topoSort` (`orchestrator.go`). The loader adds the
`agentRef`→Agent and `skillKind`→registry resolution with the same hard-error
discipline: **any resolution failure returns an error and produces no Mix → no PR.**

---

## 5. Skill-kind registry (§7 — the one code/config line)

A fixed name→constructor map, new file `booth/internal/orchestrator/registry.go`.
This is the *only* place a new skill kind touches Go:

```go
// SkillKindRegistry maps the AgentSpec.skillKind enum value to the Go skill
// constructor. THIS is the §7 code/config line: adding a new *kind* means adding
// a constructor and one entry here (and the v1alpha1.KnownSkillKinds enum +
// manifests). Roles, models, skill-packs, and team topology are data — they never
// touch this map.
var SkillKindRegistry = map[string]func() Skill{
	"architect":     func() Skill { return NewArchitectSkill() },
	"research":      func() Skill { return NewResearchSkill() },
	"coding":        func() Skill { return NewCodingSkill() },
	"observability": func() Skill { return NewObservabilitySkill() },
	"graphic":       func() Skill { return NewGraphicSkill() },
	"writer":        func() Skill { return NewWriterSkill() }, // NEW — see note
}
```

- `architect…graphic` already exist on `main` (`skills.go`). Zero new behaviour.
- **`writer`** is the one new constructor: a thin `Skill` adapter wrapping the
  existing Phase-1 linear writer (`app/internal/pipeline/writer`) so the standalone
  content-writer path is expressible as a single-node team. Its *implementation* is
  P3 work (ISI-7804); the registry **name** `writer` is locked here. If the CEO
  prefers to defer `writer` entirely, drop it from the enum and seed set and the
  contract still closes — flagged as open question Q1 below.

The enum (`v1alpha1.KnownSkillKinds`), this map, and the three CRD manifests are a
lock-step set — same discipline as `KnownReferenceKinds` (devrelpersona_types.go).

---

## 6. Seed-default set

Seeded idempotently on boot (same pattern as the DevRel seed, ISI-7455), only when
absent, so admin edits are never clobbered.

### 6.1 Six default `Agent` CRs

| name | role | skillKind | actorTemplate | modelProfile | default |
|------|------|-----------|---------------|--------------|---------|
| `architect` | System Architect | architect | content | default | true |
| `research` | Researcher | research | content | default | true |
| `coding` | Coding Specialist | coding | content | default | true |
| `observability` | Observability Specialist | observability | content | default | true |
| `graphic` | Graphic Designer | graphic | **media** | default | true |
| `content-writer` | Content Writer | writer | content | default | true |

(`graphic` is the only `media` actor — image generation via the media-actor,
ISI-7395. `default=true` per kind is unambiguous because each seed kind has exactly
one row.)

### 6.2 Default `AgentTeam` — `tutorial-default` (reproduces `DefaultEngine()`)

```yaml
apiVersion: contentremixer.io/v1alpha1
kind: AgentTeam
metadata: { name: tutorial-default }
spec:
  default: true
  entry: architect
  nodes:
    - { id: architect,     agentRef: architect }
    - { id: research,      agentRef: research }
    - { id: coding,        agentRef: coding }
    - { id: observability, agentRef: observability }
    - { id: graphic,       agentRef: graphic }
  edges:
    - { from: architect, to: research }
    - { from: architect, to: coding }
    - { from: architect, to: observability }
    - { from: architect, to: graphic }
```

This is exactly the `main` DAG: `architect` is entry (`DependsOn()==nil`); the other
four each `DependsOn([architect])`. P3's loader fed this seed MUST yield a
byte-identical Mix to today's hardcoded `DefaultEngine()`.

### 6.3 Second seed team — `content-default` (the linear writer)

```yaml
apiVersion: contentremixer.io/v1alpha1
kind: AgentTeam
metadata: { name: content-default }
spec:
  default: false            # tutorial-default owns the one default slot
  entry: writer
  nodes: [ { id: writer, agentRef: content-writer } ]
  edges: []                 # single node — valid DAG
```

Reproduces the non-tutorial linear path as a one-node team. Depends on the `writer`
skill kind (Q1). If `writer` is deferred, this seed is dropped and the linear path
stays on its current hardcoded route until a later issue.

---

## 7. Three-copy manifest lock-step + wiring checklist (for the impl children)

Locked by this contract; executed in ISI-7802/7803/7804, each **branched from
`main`**:

1. `app/api/v1alpha1/agent_types.go` + `agentteam_types.go`; run `make generate`
   (deepcopy into `zz_generated.deepcopy.go`) and `make manifests`.
2. CRD YAML in all **three** copies, lock-step:
   `app/config/crd/bases/`, `deploy/base/crds/`, `deploy/charts/*/crds/`
   (`contentremixer.io_agents.yaml`, `contentremixer.io_agentteams.yaml`).
3. `app/internal/webhook/agent_webhook.go` + `agentteam_webhook.go` (§4) and their
   `SetupWithManager` registration in the operator main.
4. `booth/internal/store/registry.go`: `AgentKind` + `AgentTeamKind`
   (`AdminOnly: true`), and add both to the kind slice in
   `booth/internal/server/server.go:92` → CRUD + §6.3 gating + audit, no new code.
5. `booth/internal/orchestrator/registry.go`: `SkillKindRegistry` (§5) + the new
   `WriterSkill` adapter (Q1).
6. Boot seeder for §6 (idempotent, create-if-absent).

---

## 8. Open questions for CEO sign-off

- **Q1 — `writer` skill kind now or later?** Including it makes the linear
  content path first-class data (one-node `content-default` team) and completes the
  "all orchestration is data" story. Deferring it keeps P0 to the five existing
  scaffold skills and the one `tutorial-default` team. **Recommendation: include it**
  — it is a thin adapter over code that already exists, and it removes the last
  hardcoded routing path.
- **Q2 — `Agent.default` semantics.** Locked here as *at-most-one canonical Agent
  per skillKind*, usable as a by-kind fallback. Alternative: drop `default` from
  `Agent` and require every team node to name an explicit `agentRef` (simpler;
  no by-kind fallback). **Recommendation: keep it** — cheap, matches the ModelProfile
  precedent, and the seeder needs a stable "the default architect" handle.
- **Q3 — reachability (T7) hard error vs. warning.** Orphan nodes are almost always
  a mistake, but a strict hard error could annoy an admin mid-edit. **Recommendation:
  hard error** — consistent with fail-closed; the Admin UI validates before submit.

---

## 9. Acceptance for this issue (P0)

- [x] `Agent` CRD shape drafted (§2)
- [x] `AgentTeam` CRD shape drafted (§3)
- [x] Skill-kind registry name→impl map defined (§5)
- [x] Seed-default set defined — 6 Agents + 2 teams reproducing `DefaultEngine()` (§6)
- [x] Fail-closed webhook validation rules for both CRDs (§4)
- [ ] **CEO design sign-off** (this doc) → then implementation children P1–P3 proceed
```
