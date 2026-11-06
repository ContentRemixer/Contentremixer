#!/usr/bin/env bash
# Regenerate the type-safe Go metric stubs from the Weaver semantic-convention registry.
# O11y-1 / ISI-7158. Requires the OpenTelemetry Weaver CLI (https://github.com/open-telemetry/weaver).
#
#   weaver >= 0.28.0
#
# Usage:  docs/observability/weaver/generate.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/../../.." && pwd)"
REGISTRY="${HERE}/registry"
TEMPLATES="${HERE}/templates"
OUT="${REPO_ROOT}/app/internal/telemetry/metricconv"

WEAVER="${WEAVER:-weaver}"

echo ">> weaver registry check"
"${WEAVER}" registry check -r "${REGISTRY}" --future

echo ">> weaver registry generate go -> ${OUT}"
mkdir -p "${OUT}"
"${WEAVER}" registry generate go "${OUT}" -r "${REGISTRY}" -t "${TEMPLATES}" --future

if command -v gofmt >/dev/null 2>&1; then
  echo ">> gofmt"
  gofmt -w "${OUT}"
fi

echo ">> done"
