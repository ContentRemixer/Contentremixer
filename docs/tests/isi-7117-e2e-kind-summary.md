# ISI-7117 — Regression/E2E job on kind (C6 of ISI-7093)

**Status:** wiring landed. **Parent:** ISI-7093 (CI umbrella), plan §2 job 3.
**Depends on suite:** ISI-7085 (already landed — this ticket is wiring, not authoring).

## What was wired

A new GitHub Actions workflow `.github/workflows/operator-e2e-kind.yml` (job
`recycle-e2e`) runs two layers that must both be green:

| Layer | Proves | Mechanism |
|-------|--------|-----------|
| 1. Behavioral | `recycle:blog` on a teleprompter source → styled-blog PR with Sample-clearance, **and** the fail-closed leak variant (block → no PR, redacted comment) | `go test -run 'TestE2E' ./internal/githubapp/...` — the ISI-7085 suite. GitHub + LiteLLM mocked at their `BaseURL` seams; no network, deterministic. Needs no envtest (fake client). |
| 2. Cluster | The shipped operator image runs on a real cluster: CRDs install, operator serves, admission webhook enforces, a fixture ReferenceRepo reconciles | `kind` cluster + cert-manager + `kubectl apply -k deploy/overlays/kind-e2e` + fixtures. |

### Layer 2 steps (kind)
1. `helm/kind-action` creates cluster `contentremixer-e2e`.
2. `docker build` app/ → `ghcr.io/contentremixer/operator:e2e`, `kind load`ed (IfNotPresent resolves to the loaded image — tag is not `:latest`).
3. cert-manager `v1.16.2` installed (webhook TLS for the admission server).
4. `kustomize build deploy/overlays/kind-e2e | kubectl apply -f -` — CRDs + RBAC + ValidatingWebhookConfiguration + Certificate/Issuer + manager Deployment. No kgateway/MetalLB exposure (kind has none).
5. Waits: CRDs `Established`, Deployment `Available`, Certificate `Ready`.
6. **Negative admission:** `fixtures/referencerepo-invalid.yaml` (`outputFormat: podcast`, not in `KnownOutputFormats`) must be **rejected** with `unknown outputFormat` — proves the validating webhook actually serves over the injected CA, not merely exists.
7. **Positive reconcile:** `fixtures/referencerepo.yaml` (ReferenceRepo + placeholder `github-pat`/`github-webhook` Secrets) must reach `Ready=True` — `resolveSecretKey` checks key existence only, so placeholders suffice.

## Gating (plan §2 requirement)
- `push` to `main` / `v*` tags → **always runs**.
- `pull_request` → the job's `if` runs it **only when the `run-e2e` label is present** (`contains(github.event.pull_request.labels.*.name, 'run-e2e')`); otherwise the job is skipped before kind spins up, keeping normal PRs fast.

## Boundary (deliberate)
The kind layer does **not** deliver a live GitHub webhook into the cluster or
make a real GitHub PR — the recycle→PR assertion is the deterministic mocked
ISI-7085 suite (Layer 1). Live real-GitHub delivery is **ISI-7112**, out of
scope here.

## Artifacts
- `.github/workflows/operator-e2e-kind.yml`
- `deploy/overlays/kind-e2e/kustomization.yaml`
- `deploy/overlays/kind-e2e/fixtures/referencerepo.yaml`
- `deploy/overlays/kind-e2e/fixtures/referencerepo-invalid.yaml`

## Local verification
`go test -run 'TestE2E' ./internal/githubapp/... -v` → both cases PASS
(`TestE2E_RecycleBlogOnTeleprompterSource_OpensStyledBlogPR`,
`TestE2E_RecycleBlog_MasteringBlocksLeak_NoPR`). Overlay renders via
`kubectl kustomize deploy/overlays/kind-e2e` (operator image → `:e2e`, 14
objects). Full kind run executes on CI (no local Docker/kind here).
