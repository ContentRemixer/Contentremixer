# ContentRemixer Operator

Kubebuilder (controller-runtime, Go) operator for the Content Recycler
platform. Phase-1 subtask B of [ISI-7077] — see the Phase-1 technical design
(issue doc on ISI-7076) for the authoritative CRD contracts.

## Layout

```
cmd/main.go                 manager: 2 controllers + admission webhook server + GitHub receiver
api/v1alpha1/               ModelProfile + ReferenceRepo CRD types (generated deepcopy)
internal/controller/        reconcilers + envtest suite
internal/webhook/           validating webhooks (§2.1a exactly-one-default, §2.2a labelRoutes)
internal/litellm/           LiteLLM config renderer + chat client
internal/githubapp/          GitHub delivery receiver (subtask D) + route run queue
internal/pipeline/           recycle stages: analyzer → writer → checker → pr (E–H)
config/                     controller-gen output (CRD bases, webhook manifests)
../deploy/                  curated overlay applied to the cluster
```

## Recycle pipeline (subtasks D–H)

`recycle:*` issue → HMAC-verified webhook → ack comment + enqueued run →
analyzer (source + parser by inputFormat) → writer (styles/<format>.md via
LiteLLM) → checker (fail-closed Mastering) → PR assembly:
branch `recycle/<issue>-<format>-<source>/` + `recycled/<slug>/{index.md,meta.yaml}`
+ PR with the **Sample clearance** block (source path, source commit SHA,
model) linking back `Closes #<n>`. Runs are enabled by starting the operator
with `--recycle-checkout <path>` and `--litellm-endpoint <url>`
(`LITELLM_API_KEY` env for the gateway key); without both, the receiver is
ack-only.

## Commands

```
make manifests   # regenerate CRD YAML + webhook configs into config/
make generate    # regenerate deepcopy
make build       # fmt + vet + build bin/manager
make test        # envtest suite (downloads envtest binaries on first run)
make docker-build IMG=...
```

envtest runs against local kube-apiserver/etcd binaries (no cluster needed).
CRDs install, controllers reconcile, webhooks + OpenAPI validation are all
covered by `internal/controller` specs.

## CRDs (contentremixer.io/v1alpha1)

- **ModelProfile** (`mp`) — one per LiteLLM backend; `spec.default` allowed on
  exactly one profile per namespace (webhook-enforced, controller re-checked);
  the controller renders the `litellm-config` ConfigMap from all profiles.
- **ReferenceRepo** (`refrepo`) — the crate: GitHub `owner/name`, PAT +
  webhook HMAC via Secret refs, PR target settings, and `spec.labelRoutes`
  (label → inputFormat/outputFormat routing, the single source of truth for
  the webhook/analyzer/writer). Both CRDs carry `spec.owner` (empty =
  platform/Admin in Phase 1; enforcement ships with the Booth API, Phase 3).
