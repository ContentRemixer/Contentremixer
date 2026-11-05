# Deploying the ContentRemixer operator to `k8Squad test`

Subtask I (ISI-7084) of the Phase-1 platform ([ISI-7061](https://.../ISI-7061)).
Implements design [§7 (Kubernetes shape)](../docs/design/phase1-operator-design.md)
and [§8 (secrets policy)](../docs/design/phase1-operator-design.md).

## Layout

```
deploy/
  base/                         # installable base (kustomize root)
    kustomization.yaml
    operator/                   # namespace, SA, RBAC, certs, services, webhook cfg, Deployment
    webhook/                    # GitHub delivery Service (:8443) + TLS Ingress
    litellm/                    # LiteLLM gateway (controller-managed config)
    crds/                       # ModelProfile + ReferenceRepo CRDs
  overlays/k8squad-test/        # image pin + kgateway/MetalLB exposure (exposure.yaml)
  secrets/*.example.yaml        # SHAPE ONLY — placeholder values, never applied as-is
```

## Image & CI

`.github/workflows/operator-ci.yml` (subtask I) gates every push/PR:
`secret-scan` (gitleaks, whole tree) → `go` (build/vet/envtest) → `manifests`
(kustomize render + kubeconform) → `image` (buildx → `ghcr.io/contentremixer/operator`,
pushed as `sha-<12>` on branch/tag, `:latest` on `main`). PRs build-only.

> The full hardened pipeline (cosign, SBOM, trivy, golangci-lint, Helm) is the
> **ISI-7093** umbrella (board-approval pending) and will absorb this workflow.
> The `operator` vs `content-operator` image-name choice is an open ISI-7093
> board decision — this subtask keeps the name the manifests already declare.

## Secrets mechanism — open decision #4 (RESOLVED, ProxOps)

**Decision: plain Kubernetes `Secret` objects, created out-of-band** on
`k8Squad test` for Phase 1. No secret value is committed or inlined in a CR
(design §8); every consumer resolves `name`+`key` via `SecretKeyRef`.

*Why:* it is the single-cluster test tier; the `SecretKeyRef` CR contract is
identical whether the backing store is a plain Secret, sealed-secrets, or
ExternalSecrets (design §8 states this choice does not block other subtasks).
Shipping plain Secrets now unblocks the deploy without a new cluster
dependency.

*Hardening follow-up (NOT Phase-1 test):* promoting to prod should move these
to sealed-secrets or ExternalSecrets so the encrypted material can live in Git.
Tracked as a Phase-2 item — the manifests here need **no change** when it lands
(only the Secret provisioning step below is replaced).

Create them before applying (values from your secret manager, never the repo):

```sh
kubectl create namespace contentremixer

kubectl -n contentremixer create secret generic github-pat \
  --from-literal=token="$GITHUB_PAT"
kubectl -n contentremixer create secret generic github-webhook \
  --from-literal=hmac="$GITHUB_WEBHOOK_SECRET"
# Per-model keys — one per ModelProfile.spec.apiKeyRef (subtask C):
kubectl -n contentremixer create secret generic openai-apikey \
  --from-literal=key="$OPENAI_API_KEY"
```

## Exposure on k8Squad test — kgateway + MetalLB (not Ingress)

The design said "TLS Ingress", but k8Squad test has **no ingress controller**.
It does ingress via the **Gateway API (kgateway, GatewayClass `kgateway`)**
fronted by **MetalLB** LoadBalancer IPs (pool `10.0.0.219-250`). So
`overlays/k8squad-test/exposure.yaml` ships a `Gateway` + `HTTPRoute` instead,
with the Gateway pinned to **`10.0.0.222`** (`spec.addresses`, stable across
rebuilds, matches the cert SAN). TLS terminates at the Gateway; the receiver
stays plain-HTTP on `:8443`.

Cluster prereqs (present on k8Squad test as of first deploy): **cert-manager**,
**kgateway**, **MetalLB**. The TLS cert is **self-signed** (cert-manager
`contentremixer-selfsigned` Issuer) — fine for LAN verification. For real
github.com delivery over the public internet you additionally need a
**public DNS host + edge (tunnel/port-forward) + a GitHub-trusted cert**
(swap the self-signed Certificate for an ACME/real issuer) — see "Real
delivery" below.

## Apply

```sh
# 1. secrets (above)   2. operator + CRDs + litellm + kgateway exposure:
kubectl apply -k deploy/overlays/k8squad-test
kubectl -n contentremixer rollout status deploy/contentremixer-operator
```

## Verify the GitHub webhook over TLS (acceptance — all confirmed on k8Squad test)

1. **Certs issued:** `kubectl -n contentremixer get certificate` → both
   `contentremixer-operator-webhook-tls` (admission) and `github-webhook-tls`
   (Gateway) show `READY=True`.
2. **Gateway programmed:** `kubectl -n contentremixer get gateway contentremixer`
   → `ADDRESS 10.0.0.222 PROGRAMMED True`.
3. **TLS reachable (from the LAN):**
   `curl -sk -X POST https://10.0.0.222/github/webhook -H 'Content-Type: application/json' -d '{"action":"opened","repository":{"full_name":"x/y"}}'`
   → **HTTP 200** (receiver accepts/acks); a malformed body returns `400`. The
   served cert SAN is `recycler.k8squad.test` + `IP:10.0.0.222`.
4. **Admission webhook:** creating a second `default: true` ModelProfile is
   rejected ("another ModelProfile … is already the default") — proves the
   admission path + self-signed CA injection.
5. **Probes:** operator pod `READY 1/1` (liveness `/healthz`, readiness
   `/readyz` on :8081).
6. **Real github.com delivery (remaining, owner-gated):** expose `10.0.0.222`
   publicly (DNS + tunnel/port-forward) with a GitHub-trusted cert, then
   configure the repo webhook (`https://<public-host>/github/webhook`,
   `application/json`, secret = `github-webhook` HMAC, event *Issues*) and
   create a `ReferenceRepo` CR + its Secrets. Open a `recycle:*` issue →
   *Recent Deliveries* `200` and the operator posts the ack.

> **litellm note:** the LiteLLM gateway pod currently can't schedule — k8Squad
> test worker nodes are under **ephemeral-storage pressure** and evict the
> ~1GB image. The ModelProfile controller renders `litellm-config` correctly;
> this is a node-capacity issue (and litellm is subtask C scope), not an
> operator/webhook problem.
