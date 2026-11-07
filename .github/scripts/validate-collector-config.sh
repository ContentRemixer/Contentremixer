#!/usr/bin/env bash
# validate-collector-config.sh — O11y-4 (ISI-7161) "validate-ocb-manifest" check.
#
# The persona/RAG Collector (deploy/overlays/k8squad-test/collector/) runs the
# stock opentelemetry-collector-contrib distro, so no custom OCB build is needed
# (strategy §8). This script PROVES that by running the contrib image's own
# `validate` subcommand against the exact config the overlay ships: `validate`
# parses the config AND instantiates every referenced component, failing with a
# non-zero exit if any one (memory_limiter, batch, k8sattributes, transform,
# redaction, tail_sampling, prometheus, k8s_cluster, otlp, otlphttp, health_check)
# is absent from the build. A green run == all required components present.
#
# Usage: .github/scripts/validate-collector-config.sh
#   Runs from the repo root. Requires docker (CI: ubuntu-latest has it).
#
# The Collector image is read from the Deployment manifest so there is a single
# source of truth for the pinned version — bump it there and this follows.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONFIG="${ROOT}/deploy/overlays/k8squad-test/collector/otel-collector-config.yaml"
DEPLOY="${ROOT}/deploy/overlays/k8squad-test/collector/deployment.yaml"

if [[ ! -f "$CONFIG" ]]; then
  echo "ERROR: collector config not found at $CONFIG" >&2
  exit 1
fi

# Pull the pinned image ref straight out of the Deployment (the `image:` line),
# so validation always runs against the version actually deployed.
IMAGE="$(grep -oE 'ghcr\.io/open-telemetry/[^[:space:]]+' "$DEPLOY" | head -n1)"
if [[ -z "$IMAGE" ]]; then
  echo "ERROR: could not read Collector image from $DEPLOY" >&2
  exit 1
fi

echo "Validating collector config against: $IMAGE"

# The config interpolates ${env:DT_OTLP_ENDPOINT} / ${env:DT_API_TOKEN}; supply
# throwaway values so `validate` can expand them (it never dials anything —
# validation is purely static component instantiation + config-schema checks).
docker run --rm \
  -e DT_OTLP_ENDPOINT="https://validate-only.example.com/api/v2/otlp" \
  -e DT_API_TOKEN="validate-only-dummy" \
  -v "${CONFIG}:/etc/otel/otel-collector-config.yaml:ro" \
  "$IMAGE" validate --config=/etc/otel/otel-collector-config.yaml

echo "OK: all configured Collector components are present in the contrib distro."
