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
6. **Real github.com delivery:** see "Real github.com delivery" below.

## Real github.com delivery (ISI-7112)

LAN TLS is verified (above). To get **real github.com deliveries** the webhook
needs a public edge, a GitHub-trusted cert, real Secrets, a `ReferenceRepo` CR,
and the repo webhook configured. Each step is tagged **[owner]** (needs an
account/credential/network no agent here holds) or **[agent]** (a manifest/API
call an agent can run once it has a k8Squad-test kubeconfig).

> **`$TARGET_REPO` (ISI-7137):** the one install-time parameter naming the
> content repo the operator watches — `owner/name` on GitHub. Set it once and
> substitute it everywhere below:
> ```sh
> TARGET_REPO="owner/name"   # the DevRel content repo (ISI-7126 Q1 default)
> ```
> It is the only place a content repo is named — `spec.repo: $TARGET_REPO` in
> the `ReferenceRepo` CR (step 4), the `github-pat` scope (write on
> `$TARGET_REPO`), and the webhook `POST /repos/$TARGET_REPO/hooks` (step 5) all
> read from it. (This is distinct from `ContentRemixer/contentremixer`, which is
> the operator/action **source** repo, not a delivery target.)

**Done when:** opening a `recycle:*` issue on `$TARGET_REPO`
shows a `200` in GitHub → Settings → Webhooks → *Recent Deliveries* and the
operator posts the ack comment. This is the infra prerequisite for the E2E gate
([ISI-7085](https://.../ISI-7085)).

> **Edge choice (ISI-7112, board + CEO):** **Option C — Tailscale Funnel** is
> the chosen turnkey path. **Option A (Cloudflare Tunnel)** and **Option B
> (router port-forward)** are kept as documented fallbacks.

#### Irreducible owner-provided inputs (Tailscale path) — the whole bundle

Everything below is a credential/network action **no agent here can self-serve**.
Once these three land, an agent finishes the apply + webhook wiring, leaving
only the `recycle:*` E2E on [ISI-7085](https://.../ISI-7085):

1. **Tailscale tailnet access** — a **Tailscale auth key** (or OAuth client for
   the Kubernetes operator) scoped to the homelab tailnet, **with Funnel enabled
   for the tailnet** (ACL `nodeAttrs` → `funnel`) and **HTTPS + MagicDNS on**.
   The auth key/OAuth client cannot be minted without tailnet-admin access.
2. **Operator `github-pat`** — a PAT with **`contents`, `pull_requests`, and
   `issues` write** on `$TARGET_REPO`, for the operator to ack
   issues and open PRs.
3. **One of** (who runs the apply + webhook wiring):
   - **(a)** a **k8Squad-test kubeconfig for ProxOps** *and* a **GitHub token
     with the Webhooks permission** → an agent runs the `kubectl apply` +
     `POST /repos/.../hooks` end-to-end; **or**
   - **(b)** the **owner runs the Option C runbook** below by hand.

   > The default ProxOps `GITHUB_TOKEN` has repo-admin **but not** the Webhooks
   > permission (confirmed `403` on `/repos/.../hooks`), so (a)'s separate
   > Webhooks-scoped token is genuinely required for the agent to create the hook.

### Option C — Tailscale Funnel (RECOMMENDED)

No domain to own, no router/WAN change, no home-IP exposure, works through
CGNAT, and the homelab already runs Tailscale. Funnel terminates TLS at the
tailnet edge with a **Let's-Encrypt, GitHub-trusted cert** — so **this option
satisfies the "GitHub-trusted cert" requirement for free** (no on-cluster ACME
issuer). Funnel only serves public ports **443 / 8443 / 10000**; the webhook
lands on **443**. `tailscale-funnel.example.yaml` in this overlay is the ready
Tailscale-operator `Ingress` (SHAPE ONLY, not in `kustomization.yaml`).

1. **[owner]** In the Tailscale admin console: enable **Funnel** for the tailnet
   (ACL `nodeAttrs` → `"attr": ["funnel"]`), ensure **HTTPS certificates** and
   **MagicDNS** are on, and mint an **auth key** (or OAuth client) for the
   Kubernetes operator (see the owner-inputs bundle above).
2. **[agent]** Install the Tailscale Kubernetes operator (owns
   `ingressClassName: tailscale`) with that credential — e.g. via the upstream
   `tailscale/tailscale-operator` Helm chart — then apply the Funnel Ingress:
   ```sh
   kubectl apply -f deploy/overlays/k8squad-test/tailscale-funnel.example.yaml
   kubectl -n contentremixer get ingress operator-webhook-funnel \
     -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'   # recycler.<tailnet>.ts.net
   ```
   Public host is now `https://recycler.<tailnet>.ts.net/github/webhook`.

### Option A — Cloudflare Tunnel (fallback)

No router/WAN change, no home-IP exposure, and Cloudflare terminates TLS at its
edge with a trusted cert — so **this option satisfies the "GitHub-trusted cert"
requirement for free** (no on-cluster ACME issuer). `tunnel.example.yaml` in
this overlay is the ready cloudflared Deployment (SHAPE ONLY, not in
`kustomization.yaml`).

1. **[owner]** In Cloudflare Zero Trust (a Cloudflare account + a zone/domain is
   required): create a tunnel, copy its **token**, and add a Public Hostname
   route `recycler.<domain>` → `http://operator-webhook.contentremixer.svc:8443`.
2. **[agent]** Create the token Secret and apply the connector:
   ```sh
   kubectl -n contentremixer create secret generic cloudflared-tunnel-token \
     --from-literal=token="$CF_TUNNEL_TOKEN"
   kubectl apply -f deploy/overlays/k8squad-test/tunnel.example.yaml
   kubectl -n contentremixer rollout status deploy/cloudflared
   ```
   Public host is now `https://recycler.<domain>/github/webhook`.

### Option B — Router port-forward + ACME cert

Forward WAN `:443` → `10.0.0.222` and serve a real cert on the Gateway. Exposes
the home WAN IP and is fragile on a dynamic IP; prefer Option C (or A).

1. **[owner]** Port-forward WAN `:443` → `10.0.0.222:443` on the homelab router,
   and add a public DNS `A` record `recycler.<domain>` → <home WAN IP>.
2. **[owner]** Provide an ACME issuer credential (e.g. a Cloudflare API token for
   DNS-01, or open `:80` for HTTP-01).
3. **[agent]** Replace the self-signed `github-webhook-tls` Certificate in
   `exposure.yaml` with a real issuer, e.g.:
   ```yaml
   # ClusterIssuer (apply once; DNS-01 shown):
   apiVersion: cert-manager.io/v1
   kind: ClusterIssuer
   metadata: { name: letsencrypt-dns01 }
   spec:
     acme:
       server: https://acme-v02.api.letsencrypt.org/directory
       email: <owner-email>
       privateKeySecretRef: { name: letsencrypt-dns01-key }
       solvers:
         - dns01:
             cloudflare:
               apiTokenSecretRef: { name: cloudflare-api-token, key: token }
   ---
   # github-webhook-tls Certificate change: issuerRef -> {name: letsencrypt-dns01,
   # kind: ClusterIssuer}; dnsNames: [recycler.<domain>]; drop ipAddresses.
   ```

### Shared steps (all options)

> Public host: **Option C** → `https://recycler.<tailnet>.ts.net`, **Option A**
> → `https://recycler.<domain>` (Cloudflare), **Option B** → your public DNS
> host. Substitute it for `recycler.<domain>` in the Payload URL below.

3. **Secrets** (out-of-band, never in repo — see "Secrets mechanism" above):
   - **[agent]** `github-webhook` HMAC — generate a fresh random secret; it only
     has to match the value entered in the GitHub webhook:
     ```sh
     WEBHOOK_SECRET="$(openssl rand -hex 32)"
     kubectl -n contentremixer create secret generic github-webhook \
       --from-literal=hmac="$WEBHOOK_SECRET"
     ```
   - **[owner→agent]** `github-pat` — a PAT with `contents`, `pull_requests`, and
     `issues` write on `$TARGET_REPO`, for the operator to ack
     issues and open PRs. An agent can mint/apply this **iff** it is given a token
     with that scope; the default ProxOps `GITHUB_TOKEN` has repo-admin **but not
     the Webhooks permission** (confirmed `403` on `/repos/.../hooks`), so it
     cannot create the repo webhook in step 5 below.
     ```sh
     kubectl -n contentremixer create secret generic github-pat \
       --from-literal=token="$GITHUB_PAT"
     ```
4. **[agent]** Apply the `ReferenceRepo` CR — the per-repo template in
   `deploy/secrets/referencerepo.example.yaml` carries the
   `tokenRef`/`webhookSecretRef`/labelRoutes above; substitute `$TARGET_REPO`
   into its `spec.repo` (and name the CR after the repo) before applying:
   ```sh
   sed "s#\$TARGET_REPO#$TARGET_REPO#" deploy/secrets/referencerepo.example.yaml \
     | kubectl apply -f -
   ```
5. **[owner OR agent-with-Webhooks-token]** Configure the repo webhook — GitHub →
   repo Settings → Webhooks → Add webhook:
   - Payload URL: `https://recycler.<domain>/github/webhook`
   - Content type: `application/json`
   - Secret: the `$WEBHOOK_SECRET` from step 3
   - Events: *Issues* only
   Equivalent API call (needs a token with the **Webhooks** permission):
   ```sh
   curl -X POST -H "Authorization: Bearer $HOOK_TOKEN" \
     -H "Accept: application/vnd.github+json" \
     https://api.github.com/repos/$TARGET_REPO/hooks \
     -d '{"name":"web","active":true,"events":["issues"],
          "config":{"url":"https://recycler.<domain>/github/webhook",
                    "content_type":"json","secret":"'"$WEBHOOK_SECRET"'"}}'
   ```
6. **Verify:** open a `recycle:blog` issue on the repo → GitHub *Recent
   Deliveries* shows `200`, and the operator posts the ack comment.

> **litellm note:** the LiteLLM gateway pod currently can't schedule — k8Squad
> test worker nodes are under **ephemeral-storage pressure** and evict the
> ~1GB image. The ModelProfile controller renders `litellm-config` correctly;
> this is a node-capacity issue (and litellm is subtask C scope), not an
> operator/webhook problem.
