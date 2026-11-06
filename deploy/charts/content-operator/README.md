# content-operator Helm chart

Helm chart for the ContentRemixer Phase-1 operator (design §7). Authored from
the `deploy/base` kustomize manifests as **C4 of the ISI-7093 CI umbrella**
([ISI-7116](https://.../ISI-7116)); it is the packaging prerequisite for the
OCI chart publish (C5).

## What it installs

| Resource | Object(s) |
|----------|-----------|
| CRDs | `modelprofiles.contentremixer.io`, `referencerepos.contentremixer.io`, `devrelpersonas.contentremixer.io` (from `crds/`) |
| Namespace | `contentremixer` (toggle `namespace.create`) |
| Operator | ServiceAccount, ClusterRole + ClusterRoleBinding, Deployment |
| Admission webhook | `operator-admission` Service, `ValidatingWebhookConfiguration`, cert-manager Issuer + Certificate |
| GitHub delivery | `operator-webhook` Service (:8443) |
| LiteLLM gateway | `litellm` Deployment + Service (`litellm.enabled`) |
| Exposure (opt-in) | kgateway `Gateway` + `HTTPRoute` + serving cert (`exposure.enabled`) |

### Cluster prerequisites

- **cert-manager** — for the admission serving cert (`certManager.enabled`, on by default).
- **kgateway + MetalLB** — only if `exposure.enabled=true`.
- **Secrets created out-of-band** — `github-pat`, `github-webhook`, per-model API keys. Never in the repo/chart (design §8). See `../../README.md`.

## Install

```sh
# Secrets first (see deploy/README.md), then:
helm install cr deploy/charts/content-operator --namespace contentremixer --create-namespace

# Pin a specific operator image:
helm install cr deploy/charts/content-operator \
  --set operator.image.repository=ghcr.io/contentremixer/content-operator \
  --set operator.image.tag=sha-7fdf9483ab28
```

> `helm install`/`helm upgrade` install the `crds/` CRDs automatically. Note the
> standard Helm caveat: CRDs in `crds/` are **not upgraded** on `helm upgrade`
> and are not deleted on `helm uninstall` — manage CRD version bumps explicitly.
> `helm template` omits them unless you pass `--include-crds`.

## Key values

| Key | Default | Purpose |
|-----|---------|---------|
| `operator.image.repository` | `ghcr.io/contentremixer/content-operator` | Operator image repo |
| `operator.image.tag` | `""` → `.Chart.AppVersion` (`sha-7fdf9483ab28`) | Operator image tag |
| `operator.replicas` | `1` | Manager replicas (leader-elected) |
| `operator.resources` | 100m/128Mi → 500m/256Mi | Manager requests/limits |
| `namespace.create` | `true` | Emit the Namespace object |
| `rbac.create` / `serviceAccount.create` | `true` | RBAC + SA |
| `certManager.enabled` | `true` | Issuer + admission serving Certificate |
| `litellm.enabled` / `litellm.image.tag` | `true` / `main-v1.82.3` | LiteLLM gateway |
| `exposure.enabled` | `false` | kgateway Gateway + HTTPRoute + cert |
| `exposure.address` | `""` | Pinned MetalLB IP for the Gateway |

Full list with comments: [`values.yaml`](./values.yaml).

## Equivalence with kustomize

`helm template cr deploy/charts/content-operator --include-crds` renders the
same resource set as `kubectl kustomize deploy/base` (15 objects: 3 CRDs +
Namespace + SA + ClusterRole + ClusterRoleBinding + 3 Services + 2 Deployments +
Issuer + Certificate + ValidatingWebhookConfiguration). Intentional differences:

1. **Image repository default** is `ghcr.io/contentremixer/content-operator`
   (ISI-7093 plan rev2, accepted) vs the legacy `.../operator` the base still
   declares. Override `operator.image.repository` to match the base exactly.
2. **Image tag** defaults to the pinned `appVersion` (`sha-7fdf9483ab28`) vs the
   base's `:latest`.
3. Helm adds the conventional `helm.sh/chart` and `app.kubernetes.io/managed-by`
   labels to every object (selectors are unaffected — they key only on
   `app.kubernetes.io/name`).

The `exposure.*` block (off by default) reproduces the `k8squad-test` overlay's
kgateway + MetalLB Gateway/HTTPRoute/cert when enabled.

## Verify locally

```sh
helm lint deploy/charts/content-operator
helm template cr deploy/charts/content-operator --include-crds | kubeconform -strict -ignore-missing-schemas -summary
```
