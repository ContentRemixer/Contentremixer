#!/usr/bin/env bash
# onboard-content-repo.sh — repeatable "Onboard a content repo" install step
# (ISI-7139, parent ISI-7126). ONE operator + ONE public Funnel edge serves N
# target repos: each is its own ReferenceRepo CR + its own GitHub webhook, all
# pointing at the same public host; the operator routes by repository.full_name.
#
# This script is the thin imperative half of onboarding (the GitHub webhook can
# only be created via the API, not declared in Helm). The declarative half is a
# `referenceRepos:` entry in deploy/charts/content-operator/values.yaml, which
# Helm reconciles into the ReferenceRepo CR. Run order:
#
#   1. add the repo to .Values.referenceRepos and `helm upgrade`      (declarative)
#   2. ./hack/onboard-content-repo.sh <owner/name>                    (this script)
#
# Actions (each independently idempotent):
#   - ensure the per-repo Secrets exist (github PAT + webhook HMAC);
#   - create OR update the GitHub webhook on the target repo (never duplicates);
#   - optionally render+apply the ReferenceRepo CR directly (--apply-cr), for
#     non-Helm installs;
#   - optionally push the content-CI bundle into the repo (--with-ci-bundle).
#
# Re-running is safe: Secrets are server-side applied, the webhook is matched by
# its payload URL and PATCHed in place, and CR apply is declarative.
set -euo pipefail

# --------------------------------------------------------------------------
usage() {
  cat >&2 <<'USAGE'
Usage: onboard-content-repo.sh <owner/name> [options]

Required (positional OR env):
  <owner/name>            TARGET_REPO — the GitHub content repo to onboard.

Webhook step (needs a Webhooks-scoped GitHub token):
  env HOOK_TOKEN          GitHub token with repo "Webhooks" write on the repo.
                          (GITHUB_WEBHOOKS_TOKEN is accepted as an alias.)
  env PUBLIC_HOST         Public Funnel host, e.g. recycler.<tailnet>.ts.net.
  env WEBHOOK_SECRET      HMAC shared secret. If unset, a fresh one is generated
                          (openssl rand -hex 32) and used for BOTH the GitHub
                          webhook and the k8s Secret so they match.

Operator PAT Secret step (optional):
  env GITHUB_PAT          PAT the operator uses to ack issues + open PRs
                          (contents, pull_requests, issues write on the repo).

Options:
  --namespace NS          k8s namespace (default: contentremixer).
  --name NAME             CR + Secret basename (default: derived from the repo
                          name part, lower-cased, non [a-z0-9-] -> "-").
  --public-host HOST      Same as PUBLIC_HOST env.
  --webhook-path PATH     Receiver path (default: /github/webhook).
  --events "a,b"          GitHub events to subscribe (default: issues).
  --shared-creds          Use the shared Secrets github-pat / github-webhook
                          instead of per-repo <name>-github-pat / <name>-webhook-hmac.
  --no-secrets            Skip the k8s Secret step (manage Secrets out-of-band).
  --no-webhook            Skip the GitHub webhook step.
  --apply-cr              Render the ReferenceRepo CR from the chart and kubectl
                          apply it (for non-Helm installs). Requires helm.
  --with-ci-bundle        Push the content-CI bundle (content-ci.yml + PR
                          templates) into the repo's default branch.
  --bundle-dir DIR        Bundle source (default: deploy/reference-repo-bundle).
  --dry-run               Print what would happen; make no changes.
  -h, --help              This help.

Examples:
  # Per-repo creds (default); generate HMAC; create webhook:
  PUBLIC_HOST=recycler.tail1234.ts.net HOOK_TOKEN=ghp_xxx GITHUB_PAT=ghp_yyy \
    ./hack/onboard-content-repo.sh ContentRemixer/contentmixer-data

  # Shared single PAT/HMAC across all repos:
  PUBLIC_HOST=... HOOK_TOKEN=... WEBHOOK_SECRET=$EXISTING_HMAC \
    ./hack/onboard-content-repo.sh Acme/blog --shared-creds --no-secrets
USAGE
  exit "${1:-2}"
}

# --------------------------------------------------------------------------
NAMESPACE="contentremixer"
NAME=""
WEBHOOK_PATH="/github/webhook"
EVENTS="issues"
SHARED_CREDS=0
DO_SECRETS=1
DO_WEBHOOK=1
APPLY_CR=0
WITH_BUNDLE=0
BUNDLE_DIR="deploy/reference-repo-bundle"
DRY_RUN=0
TARGET_REPO="${TARGET_REPO:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --namespace) NAMESPACE="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --public-host) PUBLIC_HOST="$2"; shift 2 ;;
    --webhook-path) WEBHOOK_PATH="$2"; shift 2 ;;
    --events) EVENTS="$2"; shift 2 ;;
    --shared-creds) SHARED_CREDS=1; shift ;;
    --no-secrets) DO_SECRETS=0; shift ;;
    --no-webhook) DO_WEBHOOK=0; shift ;;
    --apply-cr) APPLY_CR=1; shift ;;
    --with-ci-bundle) WITH_BUNDLE=1; shift ;;
    --bundle-dir) BUNDLE_DIR="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage 0 ;;
    -*) echo "unknown option: $1" >&2; usage ;;
    *) if [ -z "$TARGET_REPO" ]; then TARGET_REPO="$1"; else echo "unexpected arg: $1" >&2; usage; fi; shift ;;
  esac
done

HOOK_TOKEN="${HOOK_TOKEN:-${GITHUB_WEBHOOKS_TOKEN:-}}"
PUBLIC_HOST="${PUBLIC_HOST:-}"
GITHUB_PAT="${GITHUB_PAT:-}"

die() { echo "error: $*" >&2; exit 1; }
log() { echo ">> $*" >&2; }
run() { if [ "$DRY_RUN" = 1 ]; then echo "+ $*" >&2; else "$@"; fi; }

[ -n "$TARGET_REPO" ] || { echo "error: TARGET_REPO (owner/name) is required" >&2; usage; }
echo "$TARGET_REPO" | grep -qE '^[^/[:space:]]+/[^/[:space:]]+$' \
  || die "TARGET_REPO '$TARGET_REPO' must be \"owner/name\""

# CR/Secret basename — MUST match content-operator.referenceRepoName in the chart.
if [ -z "$NAME" ]; then
  leaf="${TARGET_REPO#*/}"
  NAME="$(echo "$leaf" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9-]/-/g; s/^-+//; s/-+$//')"
fi
[ -n "$NAME" ] || die "could not derive a DNS-1123 name from '$TARGET_REPO'; pass --name"

if [ "$SHARED_CREDS" = 1 ]; then
  PAT_SECRET="github-pat"; HMAC_SECRET="github-webhook"
else
  PAT_SECRET="${NAME}-github-pat"; HMAC_SECRET="${NAME}-webhook-hmac"
fi

log "repo=$TARGET_REPO name=$NAME ns=$NAMESPACE pat-secret=$PAT_SECRET hmac-secret=$HMAC_SECRET"

# --------------------------------------------------------------------------
# HMAC: reuse the provided one, else generate a fresh secret used for both sides.
WEBHOOK_SECRET="${WEBHOOK_SECRET:-}"
if [ -z "$WEBHOOK_SECRET" ]; then
  if [ "$DO_WEBHOOK" = 1 ] || { [ "$DO_SECRETS" = 1 ] && [ "$SHARED_CREDS" = 0 ]; }; then
    command -v openssl >/dev/null || die "openssl needed to generate WEBHOOK_SECRET (or set WEBHOOK_SECRET)"
    WEBHOOK_SECRET="$(openssl rand -hex 32)"
    log "generated a fresh WEBHOOK_SECRET (HMAC) — stored in Secret $HMAC_SECRET and the GitHub webhook"
  fi
fi

# --------------------------------------------------------------------------
# 1. k8s Secrets (idempotent via create --dry-run | apply).
apply_secret() {  # name key value
  local sname="$1" key="$2" val="$3"
  if [ "$DRY_RUN" = 1 ]; then
    echo "+ kubectl -n $NAMESPACE apply secret/$sname ($key=***)" >&2; return
  fi
  kubectl -n "$NAMESPACE" create secret generic "$sname" \
    --from-literal="$key=$val" --dry-run=client -o yaml | kubectl apply -f -
}
if [ "$DO_SECRETS" = 1 ]; then
  if [ -n "$GITHUB_PAT" ]; then
    log "ensuring Secret $PAT_SECRET (key token)"
    apply_secret "$PAT_SECRET" token "$GITHUB_PAT"
  else
    log "skip PAT Secret ($PAT_SECRET): set GITHUB_PAT to create it (or manage out-of-band)"
  fi
  if [ -n "$WEBHOOK_SECRET" ]; then
    log "ensuring Secret $HMAC_SECRET (key hmac)"
    apply_secret "$HMAC_SECRET" hmac "$WEBHOOK_SECRET"
  fi
else
  log "skip k8s Secrets (--no-secrets)"
fi

# --------------------------------------------------------------------------
# 2. GitHub webhook — idempotent: match by payload URL, PATCH if present else POST.
if [ "$DO_WEBHOOK" = 1 ]; then
  command -v jq >/dev/null || die "jq is required for the webhook step"
  [ -n "$HOOK_TOKEN" ] || die "HOOK_TOKEN (Webhooks-scoped) required for the webhook step (or pass --no-webhook)"
  [ -n "$PUBLIC_HOST" ] || die "PUBLIC_HOST required for the webhook step (or pass --no-webhook)"
  [ -n "$WEBHOOK_SECRET" ] || die "WEBHOOK_SECRET required for the webhook step"

  URL="https://${PUBLIC_HOST}${WEBHOOK_PATH}"
  API="https://api.github.com/repos/${TARGET_REPO}/hooks"
  events_json="$(printf '%s' "$EVENTS" | jq -Rc 'split(",") | map(gsub("^\\s+|\\s+$";""))')"
  cfg="$(jq -nc --arg url "$URL" --arg secret "$WEBHOOK_SECRET" \
    '{url:$url, content_type:"json", secret:$secret, insecure_ssl:"0"}')"

  gh_api() {  # method path [body]
    curl -sS --fail-with-body -X "$1" \
      -H "Authorization: Bearer $HOOK_TOKEN" \
      -H "Accept: application/vnd.github+json" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "$2" ${3:+-d "$3"}
  }

  log "resolving existing webhook for $URL on $TARGET_REPO"
  existing_id=""
  if [ "$DRY_RUN" = 1 ]; then
    log "(dry-run) would GET $API and match .config.url == $URL"
  else
    existing_id="$(gh_api GET "$API" | jq -r --arg url "$URL" \
      'map(select(.config.url == $url)) | (.[0].id // "")')"
  fi

  body="$(jq -nc --argjson events "$events_json" --argjson config "$cfg" \
    '{name:"web", active:true, events:$events, config:$config}')"

  if [ -n "$existing_id" ]; then
    log "webhook exists (id=$existing_id) — PATCH (idempotent update of events/secret)"
    run gh_api PATCH "${API}/${existing_id}" "$body" >/dev/null
  else
    log "no matching webhook — POST create"
    run gh_api POST "$API" "$body" >/dev/null
  fi
  log "webhook ready: $URL events=[$EVENTS]"
else
  log "skip GitHub webhook (--no-webhook)"
fi

# --------------------------------------------------------------------------
# 3. ReferenceRepo CR — declarative via Helm by default; --apply-cr for non-Helm.
CHART="deploy/charts/content-operator"
if [ "$APPLY_CR" = 1 ]; then
  command -v helm >/dev/null || die "helm is required for --apply-cr"
  [ -d "$CHART" ] || die "chart not found at $CHART (run from the repo root)"
  log "rendering + applying ReferenceRepo CR '$NAME' from the chart"
  vals="$(cat <<EOF
referenceRepos:
  - repo: ${TARGET_REPO}
    name: ${NAME}
    labelRoutes:
      - { label: "recycle:blog", outputFormat: blog }
      - { label: "recycle:short", outputFormat: short }
      - { label: "recycle:tutorial", outputFormat: tutorial }
      - { label: "recycle:teleprompter", outputFormat: teleprompter }
    prTarget: { baseBranch: main, branchPrefix: recycle/, draft: true }
$( [ "$SHARED_CREDS" = 1 ] && printf '    tokenRef: { name: %s, key: token }\n    webhookSecretRef: { name: %s, key: hmac }\n' "$PAT_SECRET" "$HMAC_SECRET" )
EOF
)"
  if [ "$DRY_RUN" = 1 ]; then
    echo "+ helm template ... --show-only templates/referencerepos.yaml | kubectl apply -f -" >&2
    echo "$vals" >&2
  else
    helm template onboard "$CHART" -n "$NAMESPACE" \
      --show-only templates/referencerepos.yaml -f <(printf '%s' "$vals") \
      | kubectl apply -f -
  fi
fi

# --------------------------------------------------------------------------
# 4. content-CI bundle (optional). Pushes content-ci.yml + PR templates to the
#    repo default branch. Best-effort; skipped if the bundle isn't present.
if [ "$WITH_BUNDLE" = 1 ]; then
  if [ ! -d "$BUNDLE_DIR" ]; then
    log "skip content-CI bundle: $BUNDLE_DIR not found (shipped by ISI-7122/C8; pass --bundle-dir)"
  else
    [ -n "$HOOK_TOKEN" ] || die "a contents-scoped token (HOOK_TOKEN) is needed to push the bundle"
    tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
    log "pushing content-CI bundle from $BUNDLE_DIR into $TARGET_REPO"
    if [ "$DRY_RUN" = 1 ]; then
      echo "+ clone $TARGET_REPO, copy $BUNDLE_DIR/*, commit+push onboarding branch" >&2
    else
      git -c "http.extraHeader=Authorization: Bearer $HOOK_TOKEN" \
        clone --depth 1 "https://github.com/${TARGET_REPO}.git" "$tmp/repo"
      cp -r "$BUNDLE_DIR/." "$tmp/repo/"
      ( cd "$tmp/repo"
        git checkout -b chore/content-ci-bundle
        git add -A
        if git diff --cached --quiet; then
          log "content-CI bundle already present — nothing to push"
        else
          git -c user.name="ProxOps" -c user.email="henrik@perfbytes.com" \
            commit -m "chore: install content-CI bundle (ISI-7139 onboarding)"
          git -c "http.extraHeader=Authorization: Bearer $HOOK_TOKEN" push -u origin chore/content-ci-bundle
          log "pushed branch chore/content-ci-bundle — open a PR on $TARGET_REPO to merge"
        fi
      )
    fi
  fi
fi

# --------------------------------------------------------------------------
cat >&2 <<EOF

Done. To make this onboarding declarative (so \`helm upgrade\` reconciles the CR),
add to deploy/charts/content-operator/values.yaml under referenceRepos: and
re-run \`helm upgrade\`:

  - repo: ${TARGET_REPO}
    name: ${NAME}
    labelRoutes:
      - { label: "recycle:blog", outputFormat: blog }
      - { label: "recycle:short", outputFormat: short }
      - { label: "recycle:tutorial", outputFormat: tutorial }
      - { label: "recycle:teleprompter", outputFormat: teleprompter }
    prTarget: { baseBranch: main, branchPrefix: recycle/, draft: true }$( [ "$SHARED_CREDS" = 1 ] && printf '\n    tokenRef: { name: %s, key: token }\n    webhookSecretRef: { name: %s, key: hmac }' "$PAT_SECRET" "$HMAC_SECRET" )
EOF
