#!/usr/bin/env bash
# coverage-gate.sh — ISI-7115 (C1) hard coverage gate for the ContentRemixer operator.
#
# Enforces the board's 80% total line-coverage decision (ISI-7093 plan §2 job 3)
# and prints a per-package breakdown to the GitHub job summary.
#
# Usage: coverage-gate.sh <cover.out> [threshold-percent]
#   COVERAGE_EXCLUDE  regex of cover.out paths to drop before computing the total
#                     (default: generated DeepCopy code + the main() entrypoint,
#                     which carry no hand-written logic and are universally
#                     excluded from Go coverage metrics).
#
# Self-contained: the total is computed from the `mode: set` profile directly
# (statement-weighted, blocks deduped by max count — matches `go tool cover`),
# so no module source tree is required. Exits non-zero below threshold.
set -euo pipefail
export LC_ALL=C  # force '.' decimal separator for awk/printf regardless of runner locale

PROFILE="${1:-cover.out}"
THRESHOLD="${2:-80}"
EXCLUDE_REGEX="${COVERAGE_EXCLUDE:-zz_generated|/cmd/}"

if [[ ! -f "$PROFILE" ]]; then
  echo "coverage-gate: profile '$PROFILE' not found" >&2
  exit 2
fi

# Filter out generated / excluded paths so the gate measures hand-written logic.
FILTERED="$(mktemp)"
trap 'rm -f "$FILTERED"' EXIT
head -1 "$PROFILE" > "$FILTERED"
tail -n +2 "$PROFILE" | { grep -vE "$EXCLUDE_REGEX" || true; } >> "$FILTERED"

# Per-package breakdown + grand total (statement-weighted; blocks deduped by
# taking the max count, which is what `go tool cover` reports for mode:set).
AGG="$(tail -n +2 "$FILTERED" | awk '
  {
    block=$1; ns=$2+0; cnt=$3+0;
    stmt[block]=ns;
    if (cnt > cov[block]) cov[block]=cnt;
  }
  END {
    for (b in stmt) {
      p=b; sub(/:[0-9]+\.[0-9]+,.*/, "", p); sub(/\/[^/]+$/, "", p);
      tot[p]+=stmt[b]; if (cov[b] > 0) covd[p]+=stmt[b];
      gtot+=stmt[b]; if (cov[b] > 0) gcov+=stmt[b];
    }
    for (p in tot) printf "PKG %s|%.1f|%d|%d\n", p, (tot[p] ? 100*covd[p]/tot[p] : 0), covd[p], tot[p];
    printf "TOTAL %.1f\n", (gtot ? 100*gcov/gtot : 0);
  }')"

TOTAL="$(awk '/^TOTAL /{print $2}' <<< "$AGG")"
TABLE="$(awk '/^PKG /{sub(/^PKG /,""); print}' <<< "$AGG" | sort)"
: "${TOTAL:=0}"

{
  echo "### Coverage gate"
  echo ""
  echo "Threshold: **${THRESHOLD}%** total (excluding \`${EXCLUDE_REGEX}\`)"
  echo ""
  echo "| Package | Coverage | Covered/Total stmts |"
  echo "|---|---:|---:|"
  while IFS='|' read -r pkg pct covd tot; do
    [[ -z "$pkg" ]] && continue
    short="${pkg#github.com/*/*/app/}"
    printf '| %s | %s%% | %s/%s |\n' "$short" "$pct" "$covd" "$tot"
  done <<< "$TABLE"
  echo "| **total** | **${TOTAL}%** | |"
} | tee -a "${GITHUB_STEP_SUMMARY:-/dev/stdout}"

# Compare as integers-with-one-decimal via awk to avoid float/locale issues.
if awk -v t="$TOTAL" -v g="$THRESHOLD" 'BEGIN { exit !(t+0 < g+0) }'; then
  echo "::error::coverage ${TOTAL}% is below the ${THRESHOLD}% gate" >&2
  exit 1
fi
echo "coverage ${TOTAL}% meets the ${THRESHOLD}% gate"
