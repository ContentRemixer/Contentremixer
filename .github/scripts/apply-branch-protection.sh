#!/usr/bin/env bash
# apply-branch-protection.sh — ISI-7123 (C10) turnkey branch-protection gate.
#
# Makes CI a REQUIRED merge gate so agent-authored PRs cannot merge red
# (ISI-7093 plan §4 board requirement). Pins the single stable `ci-required`
# aggregate job as the required status check and forces all changes through a
# PR with the branch up to date.
#
# Usage:
#   GH_TOKEN=<pat> apply-branch-protection.sh <owner/repo> [branch] [check]
#     owner/repo   target repository (e.g. ContentRemixer/Contentremixer)
#     branch       protected branch           (default: main)
#     check        required status-check name (default: ci-required)
#
#   DRY_RUN=1  print the payload + target and exit without calling the API.
#
# TOKEN SCOPE: the token MUST carry the repository "Administration: Read and
# write" permission (fine-grained PAT) or the `repo` scope (classic PAT). The
# project PAT used for push/PR only has metadata=read and will 403 here — ask
# the repo owner (Alfred/board) for an Administration-scoped token, or have an
# admin run this script once. Verify with: `... <owner/repo> --verify`.
#
# WHY enforce_admins=true: the project automation PATs hold repo `admin`, so
# agents are admins. Without enforce_admins the gate would NOT apply to them and
# a red PR could still be merged — defeating board §4. It is intentionally ON.
set -euo pipefail

API="${GITHUB_API_URL:-https://api.github.com}"
TOKEN="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
REPO="${1:-}"
BRANCH="${2:-main}"
CHECK="${3:-ci-required}"

if [[ -z "$REPO" || "$REPO" == "--verify" ]]; then
  echo "usage: GH_TOKEN=<pat> $0 <owner/repo> [branch] [check]   (or append --verify)" >&2
  exit 2
fi
if [[ -z "$TOKEN" && -z "${DRY_RUN:-}" ]]; then
  echo "error: set GH_TOKEN (or GITHUB_TOKEN) to an Administration-scoped token" >&2
  exit 2
fi

# --verify mode: GET current protection and print it (needs Administration:read).
if [[ "${2:-}" == "--verify" || "${3:-}" == "--verify" ]]; then
  echo "== current protection on ${REPO}@${BRANCH} =="
  curl -fsS -H "Authorization: Bearer $TOKEN" -H "Accept: application/vnd.github+json" \
    "$API/repos/$REPO/branches/$BRANCH/protection"
  exit $?
fi

# Branch-protection payload. `strict:true` = require branch up to date before
# merge. `checks` pins ONLY the aggregate job, so the gate is stable as the
# individual CI jobs evolve. required_pull_request_reviews (0 approvals) forces
# a PR / blocks direct pushes without mandating human review of agent PRs.
read -r -d '' PAYLOAD <<JSON || true
{
  "required_status_checks": {
    "strict": true,
    "checks": [{ "context": "${CHECK}" }]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "required_approving_review_count": 0,
    "dismiss_stale_reviews": false
  },
  "restrictions": null,
  "required_linear_history": false,
  "allow_force_pushes": false,
  "allow_deletions": false,
  "required_conversation_resolution": true
}
JSON

echo "== target: ${REPO}@${BRANCH}  required check: ${CHECK} =="
if [[ -n "${DRY_RUN:-}" ]]; then
  echo "DRY_RUN — payload:"
  echo "$PAYLOAD"
  exit 0
fi

echo "$PAYLOAD" | curl -fsS -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/json" \
  "$API/repos/$REPO/branches/$BRANCH/protection" \
  --data @- \
  | { command -v python3 >/dev/null && python3 -m json.tool || cat; }

echo "branch protection applied to ${REPO}@${BRANCH} (required check: ${CHECK})"
