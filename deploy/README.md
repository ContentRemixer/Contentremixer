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

# pgvector store credentials (P2-deploy, ISI-7148 — design §7/§9). Used BOTH by
# the Postgres StatefulSet (POSTGRES_USER/PASSWORD/DB) and by the operator
# (DSN, via SecretKeyRef, once the RAG pipeline is wired in — see below). The
# DSN password MUST equal POSTGRES_PASSWORD. See secrets/pgvector-credentials.example.yaml.
# Use a URL-safe password (hex): it is embedded in the DSN URI, and base64
# (+ / =) breaks libpq/pgx URI parsing (the port mis-parses).
PGPASS="$(openssl rand -hex 24)"
kubectl -n contentremixer create secret generic pgvector-credentials \
  --from-literal=POSTGRES_USER=contentremixer \
  --from-literal=POSTGRES_PASSWORD="$PGPASS" \
  --from-literal=POSTGRES_DB=contentremixer \
  --from-literal=DSN="postgres://contentremixer:${PGPASS}@pgvector.contentremixer.svc:5432/contentremixer?sslmode=disable"

# Dynatrace OTLP egress for the telemetry Collector (O11y-4, ISI-7161). The
# OTel Collector exports persona/RAG traces/metrics/logs to Dynatrace via
# OTLP/HTTP using these. Token from the dtctl-managed vault, never the repo.
# See overlays/k8squad-test/dynatrace-otlp.example.yaml for the scopes.
kubectl -n contentremixer create secret generic dynatrace-otlp \
  --from-literal=DT_OTLP_ENDPOINT="https://<env-id>.live.dynatrace.com/api/v2/otlp" \
  --from-literal=DT_API_TOKEN="$DT_API_TOKEN"
```

## Postgres + pgvector store (P2-deploy, ISI-7148)

The base now installs the platform's single relational+vector store (design
§4/§7): a `pgvector` **StatefulSet** (`pgvector/pgvector:pg16` — Postgres 16
with the `vector` extension preinstalled), a **headless Service** (`pgvector`,
cluster-internal `:5432`, never public), and a `PersistentVolumeClaim`
(`ReadWriteOnce`, 5Gi, default StorageClass) for `/var/lib/postgresql/data`.
P2 is **single-replica, no HA** — the corpus (~10³–10⁴ chunks) is re-derivable
by re-indexing each DevRelPersona's `spec.references`, so PVC loss is
recoverable-but-slow (a `pg_dump` CronJob is a documented nice-to-have, §11).

The schema (the `persona_chunks` table + HNSW index) is applied **by the
operator on boot** via an idempotent `CREATE … IF NOT EXISTS` migration
(design §4.2) — there is no init Job. The pgvector image ships the `vector`
extension so the operator's `CREATE EXTENSION IF NOT EXISTS vector` just works.

> **Operator ↔ store wiring (forward contract, not yet in the operator binary).**
> `app/cmd/main.go` does not yet read a DSN or construct `store.Open`, so the
> `pgvector-credentials` `DSN` key is consumed only once the RAG pipeline is
> wired into the DevRelPersona reconciler. That app change is tracked
> separately (see the P2-deploy issue's follow-up); the Secret + StatefulSet
> here are ready for it with **no further deploy change** — the operator just
> adds a `--postgres-dsn` flag / `PGVECTOR_DSN` env `SecretKeyRef` into the
> existing `pgvector-credentials` Secret.

## Telemetry collection plane (O11y-4, ISI-7161)

The k8Squad-test overlay (**not** the base — telemetry egress is cluster-specific)
installs the persona/RAG observability collection plane from the Phase-2
observability strategy §8/§9 (`docs/observability/phase2-personas-rag-observability.md`):

- **OTel Collector** (`collector/`, contrib distro) — receives OTLP from the
  operator, enriches (`k8sattributes`), protects cardinality (`transform` drops
  `source_url`/`db.query.text`/`chunk.id` from **metrics only**), redacts PII
  (`redaction` on **traces + logs only**), tail-samples (keeps 100% of
  errors / degraded / dimension-mismatch / slow-query, §7), and exports to
  Dynatrace. `memory_limiter` is first and `batch` is last in every pipeline
  (the two inviolable ordering rules). Config lives in
  `collector/otel-collector-config.yaml` (mounted via a generated ConfigMap).
- **postgres_exporter** (`postgres-exporter/`) — the §9 database-vantage, scraped
  by the Collector's `prometheus/pgvector` receiver. It connects with the SAME
  `pgvector-credentials` `DSN` the operator uses (one source of truth).
- The **operator** is pointed at the Collector by `patches/operator-otlp-env.yaml`
  (sets `OTEL_EXPORTER_OTLP_ENDPOINT` → `otel-collector.contentremixer.svc:4317`).
  This is what flips `app/cmd/main.go`'s OTLP export from no-op to live. It is a
  no-op on an operator image built before that code landed (the env is ignored),
  so it is safe to apply ahead of the next `operator-ci` image bump.

**No custom OCB build** is needed — every component above ships in
`opentelemetry-collector-contrib`. CI proves it: `collector-validate.yml` runs
`.github/scripts/validate-collector-config.sh`, which runs the contrib image's
`validate` subcommand against the shipped config and fails if any component is
absent (the "validate-ocb-manifest" check).

> **Editing the Collector config does NOT auto-roll the pod** — the generated
> ConfigMap name is stable (`disableNameSuffixHash`), so after editing
> `collector/otel-collector-config.yaml` and re-applying, run:
> `kubectl -n contentremixer rollout restart deploy/otel-collector`.

The three pgvector-availability vantages (§9) are then: operator `rag.store.up`
(authoritative), postgres_exporter `pg_up` (DB view), and `k8s_cluster`
StatefulSet readiness (infra view) — A4 (§10) pages on the first, with the other
two as diagnostic drill-downs.

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

**Done when:** opening a `recycle:*` issue on `ContentRemixer/Contentremixer`
shows a `200` in GitHub → Settings → Webhooks → *Recent Deliveries* and the
operator posts the ack comment. This is the infra prerequisite for the E2E gate
([ISI-7085](https://.../ISI-7085)).

### Option A — Cloudflare Tunnel (RECOMMENDED)

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
the home WAN IP and is fragile on a dynamic IP; prefer Option A.

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

### Shared steps (both options)

3. **Secrets** (out-of-band, never in repo — see "Secrets mechanism" above):
   - **[agent]** `github-webhook` HMAC — generate a fresh random secret; it only
     has to match the value entered in the GitHub webhook:
     ```sh
     WEBHOOK_SECRET="$(openssl rand -hex 32)"
     kubectl -n contentremixer create secret generic github-webhook \
       --from-literal=hmac="$WEBHOOK_SECRET"
     ```
   - **[owner→agent]** `github-pat` — a PAT with `contents`, `pull_requests`, and
     `issues` write on `ContentRemixer/Contentremixer`, for the operator to ack
     issues and open PRs. An agent can mint/apply this **iff** it is given a token
     with that scope; the default ProxOps `GITHUB_TOKEN` has repo-admin **but not
     the Webhooks permission** (confirmed `403` on `/repos/.../hooks`), so it
     cannot create the repo webhook in step 5 below.
     ```sh
     kubectl -n contentremixer create secret generic github-pat \
       --from-literal=token="$GITHUB_PAT"
     ```
4. **[agent]** Apply the `ReferenceRepo` CR (shape in
   `deploy/secrets/referencerepo.example.yaml`; already points at
   `ContentRemixer/Contentremixer` with the `tokenRef`/`webhookSecretRef`/
   labelRoutes above):
   ```sh
   kubectl apply -f deploy/secrets/referencerepo.example.yaml
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
     https://api.github.com/repos/ContentRemixer/Contentremixer/hooks \
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
