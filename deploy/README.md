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
  overlays/k8squad-test/        # cluster-specific: image tag + Ingress host/issuer/class
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

## Cluster facts to confirm (before first apply)

The overlay carries three `CONFIRM-ME` placeholders
(`overlays/k8squad-test/ingress-patch.yaml`) that are live `k8Squad test`
facts, plus the image tag:

| # | Value | Placeholder | Source |
|---|-------|-------------|--------|
| 1 | Ingress host | `recycler.k8squad.CONFIRM-homelab-domain` | homelab DNS zone |
| 2 | cluster-issuer | `letsencrypt-prod` | `kubectl get clusterissuer` |
| 3 | ingressClassName | `nginx` | `kubectl get ingressclass` |
| — | image tag | `sha-REPLACE_WITH_CI_SHA` | operator-ci run for the deployed SHA |

Prereqs on the cluster: **cert-manager** (for both the self-signed admission
cert and the Ingress cluster-issuer) and an **ingress controller** matching #3.

## Apply

```sh
# 1. secrets (above)   2. CRDs+operator+litellm+webhook+ingress:
kubectl apply -k deploy/overlays/k8squad-test
kubectl -n contentremixer rollout status deploy/contentremixer-operator
```

## Verify the GitHub webhook over TLS (acceptance)

1. **Cert issued:** `kubectl -n contentremixer get certificate` → both
   `contentremixer-operator-webhook-tls` (admission, self-signed) and the
   Ingress `github-webhook-tls` show `READY=True`.
2. **TLS reachable:** from outside the cluster,
   `curl -sv https://<host>/github/webhook -X POST -d '{}'` → TLS handshake
   succeeds against the issued cert; the receiver returns `400` (missing
   `X-Hub-Signature-256`) — a clean reject proves the path is live and HMAC is
   enforced, not that delivery failed.
3. **Real delivery:** configure the GitHub repo webhook
   (`https://<host>/github/webhook`, content-type `application/json`, secret =
   `github-webhook` HMAC, event = *Issues*). Open a `recycle:*` issue →
   GitHub's *Recent Deliveries* shows `200`; the operator posts the ack comment
   echoing the resolved format(s).
4. **Probes:** `kubectl -n contentremixer get pod -l app.kubernetes.io/name=contentremixer-operator`
   → `READY 1/1` (liveness `/healthz`, readiness `/readyz` on :8081).
