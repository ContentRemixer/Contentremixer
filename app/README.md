# ContentRemixer Operator

Kubebuilder (controller-runtime, Go) operator for the Content Recycler
platform. Phase-1 subtask B of [ISI-7077] — see the Phase-1 technical design
(issue doc on ISI-7076) for the authoritative CRD contracts.

## Layout

```
cmd/main.go                 manager: 2 controllers + admission webhook server
api/v1alpha1/               ModelProfile + ReferenceRepo CRD types (generated deepcopy)
internal/controller/        reconcilers + envtest suite
internal/webhook/           validating webhooks (§2.1a exactly-one-default, §2.2a labelRoutes)
internal/litellm/           minimal LiteLLM config renderer (subtask C extends)
config/                     controller-gen output (CRD bases, webhook manifests)
../deploy/                  curated overlay applied to the cluster
```

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
