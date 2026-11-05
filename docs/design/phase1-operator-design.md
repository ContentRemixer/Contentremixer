# Phase-1 Technical Design — ContentRemixer Operator

**Issue:** [ISI-7076](/ISI/issues/ISI-7076) (subtask A of [ISI-7061](/ISI/issues/ISI-7061))
**Author:** Winston (Architect) · **Status:** design · **Derived from:** plan rev 6 (board-approved, confirmation `c1b69b86`)
**Audience:** implementers of subtasks B–J. Every contract below is meant to be unambiguous enough to code against without re-deriving intent.

> **Scope guardrail.** This is the Phase-1 (MVP) design. `DevRelPersona` + RAG are Phase 2; The Booth UI + the full role-aware login are Phase 3. We specify the **ownership fields** and the **authz-enforcing Booth API contract** now (per board rev 5) so the later Admin/DevRel split is not a retrofit — but we do **not** build a UI or an IdP integration in Phase 1.

---

## 0. Contents

1. Operator layout (`app/`, kubebuilder) + `deploy/` manifests
2. CRD API types — `ModelProfile`, `ReferenceRepo` (incl. `spec.owner`, `spec.labelRoutes`)
3. Label → format routing contract (webhook · analyzer · writer)
4. Pipeline interfaces — analyzer → writer → checker
5. PR contract + Sample-clearance
6. AuthN/AuthZ + RBAC + ownership/tenancy model + Booth API contract
7. Kubernetes shape on `k8Squad test`
8. Secrets policy
9. Subtask-to-contract map (B–J)
10. Open decisions / assumptions

---

## 1. Operator layout

One Go module, scaffolded with **kubebuilder v4** (controller-runtime). Code lives under `app/` in this repo; rendered manifests under `deploy/`. Content directories (`content/`, `styles/`, `recycled/`) are untouched by the operator image build.

```
app/
  go.mod  go.sum
  PROJECT                      # kubebuilder project file (domain: contentremixer.io)
  Makefile                     # make manifests / generate / docker-build / deploy
  cmd/
    main.go                    # manager entrypoint: registers both controllers + webhook server
  api/
    v1alpha1/
      groupversion_info.go     # +groupName=contentremixer.io ; version v1alpha1
      modelprofile_types.go    # §2.1
      referencerepo_types.go   # §2.2
      common_types.go          # SecretKeyRef, OwnerRef, shared enums
      zz_generated.deepcopy.go # generated
  internal/
    controller/
      modelprofile_controller.go    # renders LiteLLM config (subtask C)
      referencerepo_controller.go    # resolves PAT secret, validates labelRoutes, sets status
    webhook/
      modelprofile_webhook.go        # validating webhook: exactly-one-default (§2.1)
      referencerepo_webhook.go        # validating webhook: label/format sanity (§2.2)
    pipeline/
      analyzer/                       # subtask E — input-format parsers
      writer/                         # subtask F — styles/<outputFormat>.md driven
      checker/                        # subtask G — Mastering: brand voice + scrub
      pr/                             # subtask H — branch + recycled/ + Sample-clearance
    githubapp/                        # subtask D — Probot-equivalent webhook handler (Go)
    litellm/                          # subtask C — config renderer + client
config/                               # kubebuilder-generated kustomize base (CRDs, RBAC, webhook)
deploy/                               # curated overlay actually applied to k8Squad test (§7)
  crds/                               # copied from config/crd/bases (source of truth = api/)
  operator/                           # Deployment, Service, ServiceAccount, RBAC
  webhook/                            # Ingress (TLS) + Service for the GitHub webhook
  litellm/                            # LiteLLM gateway Deployment/Service/ConfigMap
  secrets/                            # *.example.yaml only — never real secret values
```

**Decision — webhook runtime.** The plan text says "Probot". Probot is Node/JS; the operator is Go. To avoid a second language/runtime and a second deployable, the GitHub webhook receiver is implemented **in Go inside the same operator binary** (`internal/githubapp/`, using `google/go-github` + `go-playground/webhooks` or the stdlib HMAC check). "Probot" in the plan should be read as "the GitHub App webhook receiver," not a literal Probot dependency. One image, one Deployment, one Ingress. (If the board specifically wants Probot/Node, that is a separate sidecar — flagged in §10.)

**Controller-runtime wiring (`cmd/main.go`):**
- Manager with leader election (single active replica).
- Two reconcilers registered (`ModelProfile`, `ReferenceRepo`).
- Webhook server (admission) for the two validating webhooks.
- A separate HTTP server (non-admission) on `:8443` behind the TLS Ingress for GitHub webhook deliveries — **not** the admission webhook port. Keep them distinct to avoid cert/path confusion.

---

## 2. CRD API types

Group/version: **`contentremixer.io/v1alpha1`**. Go markers shown are authoritative — `make manifests` must reproduce the stated OpenAPI validation. Namespaced resources (the operator is namespace-scoped to its install namespace in Phase 1; cluster-scope can come later if multi-namespace tenancy is needed).

### 2.0 Shared types (`common_types.go`)

```go
// SecretKeyRef points at a key inside a Kubernetes Secret in the CR's namespace.
// Secrets are NEVER inlined (plan §Config & secrets policy).
type SecretKeyRef struct {
    // +kubebuilder:validation:MinLength=1
    Name string `json:"name"`
    // +kubebuilder:validation:MinLength=1
    Key string `json:"key"`
}

// OwnerRef identifies the subject that owns an owner-scoped CR.
// Phase 1: ModelProfile and ReferenceRepo are Admin-owned (platform-wide),
// so Owner is OPTIONAL on them and, when empty, means "platform/Admin".
// Phase 2: DevRelPersona will REQUIRE Owner. The field is defined now so the
// Booth API ownership filter (§6) is uniform across all CRDs from day one.
type OwnerRef struct {
    // Subject is the IdP subject (OIDC `sub`) of the owning DevRel, or empty
    // for Admin/platform-owned resources.
    // +optional
    Subject string `json:"subject,omitempty"`
}
```

### 2.1 `ModelProfile` — LiteLLM backend (`modelprofile_types.go`)

One CR per model backend. The `ModelProfile` controller (subtask C) renders the LiteLLM `config.yaml` from **all** `ModelProfile`s in the namespace and selects the one with `spec.default: true` as the system default. **Admin-owned (platform-wide).**

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mp
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.provider`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.model`
// +kubebuilder:printcolumn:name="Default",type=boolean,JSONPath=`.spec.default`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type ModelProfile struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   ModelProfileSpec   `json:"spec,omitempty"`
    Status ModelProfileStatus `json:"status,omitempty"`
}

type ModelProfileSpec struct {
    // Provider is the LiteLLM provider prefix (openai, anthropic, azure, vertex_ai,
    // bedrock, ollama, openai-compatible, ...). Free-form string LiteLLM understands.
    // +kubebuilder:validation:MinLength=1
    Provider string `json:"provider"`

    // Model is the provider model id (e.g. "gpt-4o", "claude-opus-4", "llama3.1:70b").
    // Rendered into LiteLLM `model` as "<provider>/<model>" unless APIBase overrides.
    // +kubebuilder:validation:MinLength=1
    Model string `json:"model"`

    // LiteLLMName is the public alias exposed by the gateway (the name agents request,
    // e.g. "default", "fast", "long-context"). Must be unique across ModelProfiles;
    // uniqueness is enforced by the controller (status condition), not the API server.
    // +kubebuilder:validation:MinLength=1
    // +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
    LiteLLMName string `json:"litellmName"`

    // APIBase overrides the endpoint for openai-compatible / self-hosted backends
    // (e.g. the homelab agentgateway/LiteLLM endpoint). Optional.
    // +optional
    APIBase string `json:"apiBase,omitempty"`

    // APIKeyRef references the Secret key holding this backend's API key.
    // Optional for keyless local backends (e.g. ollama).
    // +optional
    APIKeyRef *SecretKeyRef `json:"apiKeyRef,omitempty"`

    // Default marks the system default model. EXACTLY ONE ModelProfile in the
    // namespace may set this true. Enforced by the validating webhook (§2.1a) and
    // re-checked by the controller (defence in depth).
    // +optional
    Default bool `json:"default,omitempty"`

    // Tags are capability hints (e.g. ["long-context","vision","cheap"]) the
    // orchestrator may use to pick a non-default model. Informational in Phase 1.
    // +optional
    Tags []string `json:"tags,omitempty"`

    // Owner — see OwnerRef. Admin/platform-owned when empty (the Phase-1 default).
    // +optional
    Owner OwnerRef `json:"owner,omitempty"`
}

type ModelProfileStatus struct {
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty"`
    // Rendered is true once this profile is present in the generated LiteLLM config.
    // +optional
    Rendered bool `json:"rendered,omitempty"`
}
```

**§2.1a Exactly-one-default validation.** Enforced in a **validating admission webhook** (`modelprofile_webhook.go`), because cross-object uniqueness cannot be expressed in CRD OpenAPI/CEL on a single object:

- On CREATE/UPDATE where `spec.default == true`: list other `ModelProfile`s in the namespace; if any **other** has `default == true`, **reject** with `field.Invalid(spec.default, ...)` — "another ModelProfile (<name>) is already the default; unset it first."
- The webhook does **not** auto-flip the previous default (explicit, predictable; Admin unsets then sets).
- **Zero-default safety net:** the webhook does *not* force a default to exist (you may legitimately have none mid-edit). The LiteLLM renderer (subtask C) treats "no default" as a degraded condition: it still renders all models but sets a `DefaultMissing` status condition and the pipeline fails closed with a clear error rather than guessing. `litellmName` uniqueness is likewise checked by the webhook (reject duplicate alias).

### 2.2 `ReferenceRepo` — The Crate + routing + PR target (`referencerepo_types.go`)

The repo of reference + its GitHub PAT (Secret ref) + PR target settings + the label→format routes. **Admin-owned (platform-wide).**

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=refrepo
// +kubebuilder:printcolumn:name="Repo",type=string,JSONPath=`.spec.repo`
// +kubebuilder:printcolumn:name="Routes",type=integer,JSONPath=`.status.routeCount`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type ReferenceRepo struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   ReferenceRepoSpec   `json:"spec,omitempty"`
    Status ReferenceRepoStatus `json:"status,omitempty"`
}

type ReferenceRepoSpec struct {
    // Repo is "owner/name" on GitHub (e.g. "ContentRemixer/Contentremixer").
    // +kubebuilder:validation:Pattern=`^[^/\s]+/[^/\s]+$`
    Repo string `json:"repo"`

    // TokenRef references the Secret key holding the GitHub PAT used to read the
    // repo, post ack comments, and open PRs. Never inlined.
    TokenRef SecretKeyRef `json:"tokenRef"`

    // WebhookSecretRef references the Secret key holding the GitHub webhook HMAC
    // secret used to verify `X-Hub-Signature-256` on deliveries.
    WebhookSecretRef SecretKeyRef `json:"webhookSecretRef"`

    // PRTarget controls how/where PRs are opened.
    // +optional
    PRTarget PRTargetSpec `json:"prTarget,omitempty"`

    // LabelRoutes maps a GitHub label to an (inputFormat, outputFormat) pair.
    // Routing is fully config-driven: the webhook resolves formats from here,
    // never by parsing the label string. See §3.
    // +kubebuilder:validation:MinItems=1
    LabelRoutes []LabelRoute `json:"labelRoutes"`

    // Owner — Admin/platform-owned when empty (Phase-1 default).
    // +optional
    Owner OwnerRef `json:"owner,omitempty"`
}

type PRTargetSpec struct {
    // BaseBranch is the PR base (default "main").
    // +kubebuilder:default=main
    // +optional
    BaseBranch string `json:"baseBranch,omitempty"`

    // BranchPrefix prefixes generated PR branches (default "recycle/").
    // +kubebuilder:default=recycle/
    // +optional
    BranchPrefix string `json:"branchPrefix,omitempty"`

    // Draft opens PRs as drafts when true (default false).
    // +optional
    Draft bool `json:"draft,omitempty"`
}

type LabelRoute struct {
    // Label is the exact GitHub label, e.g. "recycle:blog".
    // +kubebuilder:validation:MinLength=1
    Label string `json:"label"`

    // InputFormat is the source document type the analyzer parses:
    // teleprompter | livestream | blog | podcast | auto.
    // "auto" => detect from the source meta.yaml (§3).
    // +kubebuilder:validation:Enum=teleprompter;livestream;blog;podcast;auto
    // +kubebuilder:default=auto
    InputFormat string `json:"inputFormat"`

    // OutputFormat selects the writer style guide styles/<outputFormat>.md:
    // blog | short | tutorial | teleprompter.
    // +kubebuilder:validation:Enum=blog;short;tutorial;teleprompter
    OutputFormat string `json:"outputFormat"`
}

type ReferenceRepoStatus struct {
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty"`
    // RouteCount mirrors len(spec.labelRoutes) once validated.
    // +optional
    RouteCount int `json:"routeCount,omitempty"`
    // TokenResolved is true once the PAT Secret ref resolves successfully.
    // +optional
    TokenResolved bool `json:"tokenResolved,omitempty"`
}
```

**§2.2a `ReferenceRepo` validating webhook.** Enum/pattern/`MinItems` are handled by OpenAPI. The webhook adds: (a) **duplicate-label** rejection within `labelRoutes`; (b) reject an `outputFormat` whose `styles/<outputFormat>.md` is not among the known set (the enum already bounds this, but the webhook message names the missing style file to aid Admins). Secret existence is checked by the **controller** (status condition `TokenResolved`), not the webhook, so applying the CR before the Secret doesn't hard-fail admission.

**Enum sourcing.** `InputFormat`/`OutputFormat` enums are the Phase-1 closed set. Adding a format = (1) add `styles/<new>.md`, (2) extend the enum, (3) `make manifests`. Documented so B/F/the enum stay in lock-step.

---

## 3. Label → format routing contract

Single source of truth: `ReferenceRepo.spec.labelRoutes`. Three consumers, one resolution function.

**Resolution function (shared helper, `internal/githubapp/routing.go`):**

```
Resolve(repoCR *ReferenceRepo, issueLabels []string) ([]Route, []string)
  // returns matched Routes (one per recognised recycle:* label) and the list of
  // unmapped recycle:* labels (echoed in the ack as "ignored").
```

Rules (authoritative):
1. Consider only labels with the `recycle:` prefix. Non-`recycle:` labels are irrelevant to routing.
2. For each `recycle:*` label, look it up **by exact string** in `labelRoutes`. **No parsing of the format out of the label name** — a `recycle:blog` label routes to blog only because a route says so.
3. Matched → emit `Route{inputFormat, outputFormat}`. Unmatched `recycle:*` labels → collected into the "unmapped" list.
4. An issue may carry several `recycle:*` labels → several routes → the pipeline runs once per route (each produces its own `recycled/<slug>/` output and may be bundled in one PR or one PR per route — see §5, default: one PR per route).
5. If **no** `recycle:*` label matches any route: the webhook still acks, stating the labels were seen but unmapped; no pipeline run.

**Consumer 1 — webhook (subtask D):** on `issues.labeled` / `issues.opened`, call `Resolve`, post the **ack comment** echoing resolved `(inputFormat → outputFormat)` pairs and naming any unmapped `recycle:*` labels. Only matched routes enqueue pipeline runs.

**Consumer 2 — analyzer (subtask E):** receives `inputFormat` from the route. Picks its parser:
- `teleprompter` → read `files.teleprompter` (from the source `meta.yaml`) as script text.
- `livestream` → YouTube transcript of `youtube_url` (Phase 1 may stub/fixture the transcript fetch; §10).
- `blog` → `files.post` markdown.
- `podcast` → `url`/transcript (Phase 1: treat like article text).
- `auto` → **detect from the source `meta.yaml`**: infer by which `files.*` key / which `content/<type>/` the source path sits in (video→teleprompter, livestream→livestream, blog→blog, podcast→podcast). Detection rule lives in `analyzer`, documented in E.

**Consumer 3 — writer (subtask F):** receives `outputFormat`; loads `styles/<outputFormat>.md` (and, Phase 2, the author/persona voice card) and generates the Mix in that style.

**Phase-1 starter routes** (ship as the default `ReferenceRepo` example in `deploy/secrets`/`*.example.yaml`; board decision #10 recommended "yes"):

```yaml
labelRoutes:
  - { label: "recycle:blog",         inputFormat: auto, outputFormat: blog }
  - { label: "recycle:short",        inputFormat: auto, outputFormat: short }
  - { label: "recycle:tutorial",     inputFormat: auto, outputFormat: tutorial }
  - { label: "recycle:teleprompter", inputFormat: auto, outputFormat: teleprompter }
```

`inputFormat: auto` is the default so Admins only specify output intent unless a source needs an override. This matches the existing issue form (`.github/ISSUE_TEMPLATE/recycle.yml`) whose targets are blog / short / teleprompter / tutorial.

---

## 4. Pipeline interfaces — analyzer → writer → checker

Three stages, Go interfaces, pure data hand-offs (so each subtask is independently testable with fixtures). All stages are **side-effect-free except I/O they own**; the PR assembler (§5) is the only writer to `recycled/`.

```go
// Shared run context, built by the webhook from the issue + resolved route.
type RunContext struct {
    Repo         string     // "owner/name"
    IssueNumber  int
    SourcePath   string     // from the issue form: content/... path OR external URL
    InputFormat  string     // resolved route input (may be "auto")
    OutputFormat string     // resolved route output
    Notes        string     // free-text angle/audience from the issue form
    ModelName    string     // LiteLLM alias to use (default profile unless overridden)
    Owner        OwnerRef   // owner propagated for tenancy tagging (§6)
}

// ---- Analyzer (subtask E) ----
type Sample struct {
    SourcePath   string            // normalised path that was read
    SourceCommit string            // git SHA of the source at read time (for Sample-clearance)
    Meta         map[string]any    // parsed meta.yaml (title, author, tags, files, ...)
    DetectedInput string           // concrete input format after resolving "auto"
    Body         string            // extracted source text (script / transcript / article)
}
type Analyzer interface {
    Analyze(ctx context.Context, rc RunContext) (Sample, error)
}

// ---- Writer (subtask F) ----
type Mix struct {
    OutputFormat string
    Title        string
    Markdown     string            // the generated Mix body
    Meta         map[string]any    // meta.yaml for the recycled/ folder (incl. source refs)
    ModelUsed    string            // concrete model/alias that produced it (for clearance)
}
type Writer interface {
    // Loads styles/<OutputFormat>.md; conditions on Sample + Notes.
    Write(ctx context.Context, rc RunContext, s Sample) (Mix, error)
}

// ---- Checker / Mastering (subtask G) ----
type CheckResult struct {
    Passed     bool
    Violations []Violation   // each: rule id, matched span, severity
}
type Violation struct {
    Rule     string   // "tenant-id" | "token" | "ip" | "internal-hostname" | "brand-voice"
    Match    string   // redacted/truncated evidence
    Severity string   // "block" | "warn"
}
type Checker interface {
    // FAIL-CLOSED: any "block" violation => Passed=false and the pipeline must NOT open a PR.
    Check(ctx context.Context, m Mix) (CheckResult, error)
}
```

**Orchestration (who calls these):** the webhook handler enqueues a `RunContext`; a run worker calls `Analyze → Write → Check`. On `Check.Passed == false` (or any `block` violation), the run **stops**, posts a comment on the issue explaining the blocked violations (redacted), and opens **no** PR. On success it hands `(Sample, Mix)` to the PR assembler (§5).

**Scrub rules (G, hard/fail-closed)** — deterministic regex/matchers, run on the final `Mix.Markdown` *and* `Mix.Meta`:
- **tenant-id** — company/tenant UUIDs and known tenant id patterns.
- **token** — API keys, bearer tokens, PATs, JWT-shaped strings, `sk-...`.
- **ip** — RFC1918 + public IPv4/IPv6 literals.
- **internal-hostname** — `*.local`, `*.cluster.local`, k8Squad/homelab internal domains, bare service names.
- **brand-voice** — soft `warn`-level check against `styles/<format>.md` tone (no marketing voice). `warn` does not block; `block` rules do.

The exact pattern catalogue is G's deliverable; this doc fixes the **interface and the fail-closed semantics**.

---

## 5. PR contract + Sample-clearance

PR assembler (subtask H) is the only component that mutates git / `recycled/`.

**Output layout** (one folder per run, traceable to source — matches README "recycled/ — one folder per request"):
```
recycled/<slug>/
  index.md            # the Mix body (Mix.Markdown)
  meta.yaml           # Mix.Meta + the Sample-clearance fields below
```
`<slug>` = `<issue-number>-<outputFormat>-<source-slug>` (stable, collision-resistant, greppable back to the issue).

**Branch / PR:** branch `= <PRTarget.BranchPrefix><slug>` off `PRTarget.BaseBranch`; one PR per route by default (`Draft` per spec). PR **body MUST contain** a machine-parseable Sample-clearance block:

```markdown
## Sample clearance
- **Source path:** content/videos/2026/2026-11-04-otel-k8s/
- **Source commit:** 3ae0a89b…            <!-- exact SHA the Sample was read at -->
- **Model:** default (anthropic/claude-opus-4 via LiteLLM)
- **Input → Output:** teleprompter → blog
- **Recycle issue:** #42
```

Three fields are **mandatory** per acceptance criteria: **source path, source commit SHA, model**. They come from `Sample.SourcePath`, `Sample.SourceCommit`, `Mix.ModelUsed`. The same block is mirrored into `recycled/<slug>/meta.yaml` under a `sample_clearance:` key so clearance survives outside the PR. PR links back to the issue (`Closes #<n>` / `Recycle issue: #<n>`).

---

## 6. AuthN/AuthZ + RBAC + ownership/tenancy model + Booth API contract

**Phase-1 reality:** no Booth UI, no IdP, no multi-user surface yet. What Phase 1 delivers is (a) the **ownership fields** on the CRD types (§2 `OwnerRef`), and (b) this **frozen contract** that the Booth API (Phase 3) and the Phase-2.5 optional auth gate must implement. Specifying it now is the whole point of pulling ownership into Phase 1 — downstream code never has to retrofit a tenancy column.

### 6.1 Roles
- **Admin** — full platform configuration: CRUD all `ModelProfile`, `ReferenceRepo`, (Phase 2) all `DevRelPersona`; view all runs/drafts/PRs; manage users/roles; approve any PR.
- **DevRel** — scoped to **self**: CRUD only their own `DevRelPersona`; view only runs/drafts/PRs for that persona; **no** access to `ModelProfile`/`ReferenceRepo`; comment/approve only on their own PRs.

### 6.2 Ownership / tenancy model (the enforcement backbone)
- Every owner-scoped CR carries `spec.owner.subject` (the OIDC `sub`). `ModelProfile`/`ReferenceRepo` are Admin/platform-owned → `owner` empty in Phase 1. `DevRelPersona` (Phase 2) **requires** `owner`.
- Every recycle run / draft / PR is **tagged with the owning persona** (and thus owner). In Phase 1 there are no personas, so runs are platform-owned; the `RunContext.Owner` field (§4) is the carrier so Phase 2 only has to populate it.
- Authorization is enforced **server-side in the Booth API**, which is the single front door to the CRs. Kubernetes RBAC authenticates only the Booth's service account (and the operator's); per-user scoping is the Booth API's job, not k8s RBAC.

### 6.3 The authz rule (frozen)
Every Booth API read and write evaluates, for the target resource:

```
authorized  ==  (session.role == "Admin")  OR  (resource.owner.subject == session.subject)
```

- **List/read:** the result set is filtered by this predicate server-side (never client-side hiding). A DevRel listing personas sees only rows where `owner.subject == sub`.
- **Write/approve:** the same predicate gates the mutation; a DevRel writing a resource they don't own → **403**, not a silent no-op.
- **Admin-only resource classes** (`ModelProfile`, `ReferenceRepo`): the predicate's first clause is the only way in; a DevRel is 403 on all of them regardless of ownership (they have no `owner` to match).
- Resources with **empty owner** (platform-owned) are visible/editable by **Admin only** (the second clause can't match an empty subject against a real `sub`).

### 6.4 Booth API contract (shape for Phase 3 / optional 2.5)
- **AuthN:** OIDC/SSO against the homelab IdP (board decision #8, open) → short-lived session → `{ subject, role }`. Fallback minimal built-in user table (bcrypt) only if no IdP exists.
- Every endpoint resolves `{subject, role}` from the session, then applies §6.3 before touching the k8s API.
- Endpoints (Phase 3) are CRUD-over-CRs + run/PR views; each MUST run the predicate. The contract here is the **invariant**, not the route list: *no Booth code path reads or writes a CR without evaluating §6.3.*
- **Audit:** every mutation logs `{subject, role, verb, resourceKind, resourceName, decision}`.

This section is the acceptance item "authz/ownership model + Booth API contract specified in the design doc." Subtask B implements only the `spec.owner` **field**; no enforcement code ships in Phase 1.

---

## 7. Kubernetes shape on `k8Squad test`

Everything runs as a Kubernetes workload on the `k8Squad test` cluster; the GitHub webhook is reachable over a **TLS Ingress**.

```
Namespace: contentremixer
  Deployment  operator            # manager: 2 controllers + admission webhook + github webhook server
    - 1 replica, leader-elected (room to scale replicas later; controllers stay single-active)
    - probes: /healthz (liveness), /readyz (readiness)
    - mounts: webhook TLS cert (from cert-manager Secret), service-account token
  Service     operator-admission  # :443 -> admission webhook (cluster-internal only)
  Service     operator-webhook    # :8443 -> GitHub delivery receiver
  Ingress     github-webhook      # TLS (cert-manager), host e.g. recycler.<homelab-domain>, path /github/webhook
  Deployment  litellm             # LiteLLM gateway (subtask C)
  Service     litellm             # cluster-internal :4000
  ConfigMap   litellm-config      # rendered by ModelProfile controller from all ModelProfiles
  ServiceAccount + Role/RoleBinding  # operator: CRUD own CRDs + read Secrets + write the litellm ConfigMap
  ValidatingWebhookConfiguration     # exactly-one-default + labelRoutes checks (§2)
  CRDs        modelprofiles, referencerepos  (contentremixer.io/v1alpha1)
```

- **`k8Squad test`** is the named test cluster; subtask I (ProxOps) owns the concrete overlay, Ingress host, and cert issuer. This doc fixes the component set and that the **webhook delivery path and the admission webhook path are different Services/ports**.
- **LiteLLM config flow:** `ModelProfile` controller renders `litellm-config` ConfigMap → LiteLLM Deployment mounts it → rolls on change (checksum annotation). No model secrets in the ConfigMap — LiteLLM reads keys from env mounted from the Secrets referenced by each `ModelProfile.apiKeyRef`.
- **Health/probes, resource requests/limits, and the Ingress annotations** are I's to finalise; the operator must expose `/healthz` and `/readyz`.

---

## 8. Secrets policy

Per plan §Config & secrets policy — **no secret value is ever committed or inlined in a CR**:
- GitHub App key, webhook HMAC secret, **GitHub PAT**, model API keys, (Phase 3) OIDC client secret → **k8s `Secret` objects**, referenced by name+key via `SecretKeyRef` (§2.0).
- `deploy/secrets/*.example.yaml` + `models.example.yaml` document the required keys with placeholder values only.
- CI **secret-scan** (gitleaks or trufflehog) over the whole repo (content + `app/`) — gate in subtask I's CI.
- k8Squad test secrets mechanism (plain `Secret` vs sealed-secrets/ExternalSecrets) — **open decision #4**, ProxOps (I) to confirm; the CR contract (`SecretKeyRef`) is identical either way, so this choice does not block B.

---

## 9. Subtask → contract map

| Subtask | What it builds | Binds to this doc |
|---|---|---|
| **B** ISI-7077 — operator scaffold + CRDs | kubebuilder project, both types incl. `spec.owner` + `spec.labelRoutes`, controllers, webhooks | §1, §2 (Go types are the spec), §2.1a/§2.2a webhooks |
| **C** ISI-7078 — LiteLLM integration | render `litellm-config` from all `ModelProfile`s, default selection, keys from Secrets | §2.1, §7 config flow, §8 |
| **D** ISI-7079 — webhook + ack | GitHub delivery receiver, HMAC verify, `Resolve`, ack echoing formats | §2.2, §3 (Resolve rules + ack), §7 webhook Service |
| **E** ISI-7080 — analyzer | parser-by-`inputFormat`, `auto` detection from meta.yaml, emit `Sample` | §3 consumer 2, §4 Analyzer |
| **F** ISI-7081 — writer | `styles/<outputFormat>.md`-driven generation, emit `Mix` | §3 consumer 3, §4 Writer |
| **G** ISI-7082 — checker/Mastering | fail-closed scrub + brand-voice | §4 Checker + scrub rules |
| **H** ISI-7083 — PR assembly | branch, `recycled/<slug>/`, Sample-clearance | §5 |
| **I** ISI-7084 — k8s deploy | Dockerfile, CI (+secret-scan), Deployment/Service/TLS Ingress/Secrets/probes | §7, §8 |
| **J** ISI-7085 — E2E | `recycle:blog` on teleprompter source → styled-blog PR | whole chain; §3 starter routes, §5 PR contract |

Ownership/authz (§6) is spec-only in Phase 1: B adds the `spec.owner` field; no enforcement ships until Phase 3 (or optional 2.5).

---

## 10. Open decisions / assumptions carried from the plan

Non-blocking for B (the CR contracts are stable regardless), but flagged:
1. **#3 default model** — which `ModelProfile` is the Phase-1 default (recommend the existing homelab LiteLLM/agentgateway endpoint, `apiBase` set, `default: true`).
2. **#4 secrets mechanism** — plain `Secret` vs sealed/External (ProxOps/I). `SecretKeyRef` unaffected.
3. **Webhook runtime** — this design implements the GitHub webhook **in Go in the operator binary**, not literal Probot/Node. If the board wants Probot specifically, it becomes a separate sidecar Deployment + Service (D's scope grows); flagged for confirmation.
4. **#10 routing** — starter routes in §3 assume `inputFormat: auto` default and `labelRoutes` on `ReferenceRepo` (board recommended "yes to all").
5. **livestream transcript fetch** — Phase-1 analyzer may use a fixture/stub for YouTube transcript retrieval (the E2E gate uses a teleprompter source, which needs no transcript); real transcript scraping firms up with DevRelPersona scrapers in Phase 2.
6. **#8/#9 auth timing** — §6 is contract-only in Phase 1; OIDC wiring and any 2.5 gate are deferred (board recommended Phase 3 with the UI).

---

*End of Phase-1 design. The Go type blocks in §2 and the interfaces in §4 are the authoritative, implement-against-this contracts; prose elsewhere is rationale.*
